package agent

// This file is the adapter of Claude Code. Everything that is specific
// to Claude Code (the options, the events of the stream) stays here. The
// rest of cumin sees only Request, Run, and AbnormalEnd.
//
// Options verified in the official documentation: "Run Claude Code
// programmatically" (headless) and "CLI reference". The events come from
// measured-constraints.md rows 1 and 26.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"time"
)

// ClaudeCode starts Claude Code for one request.
type ClaudeCode struct {
	// Path is the executable, from the setting roles.<role>.cli_path.
	Path string
	// Logger may be nil. Then the default logger is used.
	Logger *slog.Logger
}

// args builds the command line of one request.
func (c ClaudeCode) args(req Request) []string {
	args := []string{
		"-p", req.Text,
		"--append-system-prompt", req.RoleInstruction,
		// No user-level instructions (measured-constraints.md row 6e).
		"--setting-sources", "project",
		// All tools allowed: nobody answers a permission prompt in a
		// headless run.
		"--permission-mode", "bypassPermissions",
		"--json-schema", ResultSchema,
		// stream-json needs --verbose (headless documentation).
		"--output-format", "stream-json",
		"--verbose",
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if req.SessionID != "" {
		args = append(args, "--resume", req.SessionID)
	}
	return args
}

// event is the part of one stream-json line that cumin reads.
type event struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	// Fields of the result event.
	IsError          bool            `json:"is_error"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	// Field of the rate_limit_event.
	RateLimitInfo *rateLimitInfo `json:"rate_limit_info"`
}

type rateLimitInfo struct {
	Status         string `json:"status"`
	UnifiedWindows struct {
		FiveHour *rateLimitWindow `json:"five_hour"`
		SevenDay *rateLimitWindow `json:"seven_day"`
	} `json:"unifiedWindows"`
}

// rateLimitWindow uses pointers, so that a missing or renamed field is
// told from a zero value.
type rateLimitWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    *int64   `json:"resetsAt"` // Unix seconds
}

func (w *rateLimitWindow) complete() bool {
	return w != nil && w.Utilization != nil && w.ResetsAt != nil
}

// stream is what the reader collected from stdout.
type stream struct {
	sessionID string
	result    *event
	quota     *QuotaUsage
}

// Run starts Claude Code in the work directory of the request, waits for
// the end, and returns the run. An error is always an *AbnormalEnd.
func (c ClaudeCode) Run(ctx context.Context, req Request) (*Run, error) {
	log := c.logger().With("role", req.Role, "work_dir", req.WorkDir)
	if req.SessionID == "" {
		log.Info("agent start", "session", "new")
	} else {
		log.Info("agent start", "session", "resumed", "session_id", req.SessionID)
	}

	cmd := exec.CommandContext(ctx, c.Path, c.args(req)...)
	cmd.Dir = req.WorkDir
	// Stdin is nil: the process reads from the null device (os/exec).
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, limit: 64 << 10}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, c.fail(log, &AbnormalEnd{Kind: EndProcessFailed, Detail: "open stdout", Err: err})
	}
	if err := cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return nil, c.fail(log, &AbnormalEnd{Kind: EndTimeLimit, Detail: "the run was stopped before the start", Err: ctx.Err()})
		}
		return nil, c.fail(log, &AbnormalEnd{Kind: EndProcessFailed, Detail: "start " + c.Path, Err: err})
	}
	pid := cmd.Process.Pid

	s := c.read(log, stdout)
	waitErr := cmd.Wait()
	if stderr.Len() > 0 {
		log.Debug("agent stderr", "text", stderr.String())
	}

	end := &AbnormalEnd{SessionID: s.sessionID, PID: pid}
	var exitErr *exec.ExitError
	switch {
	case ctx.Err() != nil:
		// The caller stopped the run. The time limit of the role (#41)
		// uses the same kind.
		end.Kind, end.Detail, end.Err = EndTimeLimit, "the run was stopped", ctx.Err()
	case waitErr != nil && errors.As(waitErr, &exitErr):
		end.Kind, end.Detail = EndProcessFailed, fmt.Sprintf("exit code %d", exitErr.ExitCode())
	case waitErr != nil:
		end.Kind, end.Detail, end.Err = EndProcessFailed, "wait", waitErr
	case s.result == nil:
		end.Kind, end.Detail = EndNoResult, "the run ended without a result event"
	case s.result.IsError:
		end.Kind, end.Detail = EndError, "the result event has is_error true, subtype "+s.result.Subtype
	case len(s.result.StructuredOutput) == 0:
		end.Kind, end.Detail = EndInvalidResult, "the result event has no structured_output"
	default:
		result, err := ValidateResult(s.result.StructuredOutput)
		if err != nil {
			end.Kind, end.Detail = EndInvalidResult, err.Error()
			break
		}
		run := &Run{SessionID: s.sessionID, Result: result}
		if s.quota != nil {
			run.Quota, run.QuotaRead = *s.quota, true
		}
		log.Info("agent end", "session_id", run.SessionID, "result", result.Result, "quota_read", run.QuotaRead)
		return run, nil
	}
	return nil, c.fail(log, end)
}

// read collects the session ID, the last quota usage, and the result
// event from stdout. Other lines are skipped.
func (c ClaudeCode) read(log *slog.Logger, r io.Reader) stream {
	var s stream
	reader := bufio.NewReader(r)
	for {
		line, err := reader.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			c.readLine(log, &s, line)
		}
		if err != nil {
			return s
		}
	}
}

func (c ClaudeCode) readLine(log *slog.Logger, s *stream, line []byte) {
	var e event
	if err := json.Unmarshal(line, &e); err != nil {
		log.Debug("agent output is not JSON", "text", truncate(string(line), 200))
		return
	}
	if e.SessionID != "" {
		s.sessionID = e.SessionID
	}
	switch e.Type {
	case "rate_limit_event":
		if q, ok := quotaOf(e.RateLimitInfo); ok {
			s.quota = &q
			log.Debug("agent quota usage",
				"five_hour", q.FiveHour.Utilization, "five_hour_resets_at", q.FiveHour.ResetsAt,
				"weekly", q.Weekly.Utilization, "weekly_resets_at", q.Weekly.ResetsAt)
		}
	case "result":
		s.result = &e
	}
}

// quotaOf converts the rate limit event. It reports false when a window
// or one of its fields is missing, so that a changed event format is
// read as "no usage" and not as zero usage.
func quotaOf(info *rateLimitInfo) (QuotaUsage, bool) {
	if info == nil || !info.UnifiedWindows.FiveHour.complete() || !info.UnifiedWindows.SevenDay.complete() {
		return QuotaUsage{}, false
	}
	window := func(w *rateLimitWindow) QuotaWindow {
		return QuotaWindow{Utilization: *w.Utilization, ResetsAt: time.Unix(*w.ResetsAt, 0)}
	}
	return QuotaUsage{
		FiveHour: window(info.UnifiedWindows.FiveHour),
		Weekly:   window(info.UnifiedWindows.SevenDay),
		ReadAt:   time.Now(),
	}, true
}

func (c ClaudeCode) fail(log *slog.Logger, end *AbnormalEnd) error {
	log.Info("agent abnormal end", "kind", end.Kind.String(), "session_id", end.SessionID, "detail", end.Detail)
	return end
}

func (c ClaudeCode) logger() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// limitedWriter keeps the first limit bytes and drops the rest.
type limitedWriter struct {
	w     *bytes.Buffer
	limit int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if room := l.limit - l.w.Len(); room > 0 {
		l.w.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}
