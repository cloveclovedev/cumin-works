package agent

// This file is the adapter of Claude Code. Everything that is specific
// to Claude Code (the options, the events of the stream) stays here. The
// rest of cumin sees only Request, Run, and AbnormalEnd.
//
// Options verified in the official documentation: "Run Claude Code
// programmatically" (headless) and "CLI reference". The events come from
// measured-constraints.md rows 1 and 26.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// defaultGrace is the time between SIGTERM and SIGKILL at the time limit.
const defaultGrace = 10 * time.Second

// ClaudeCode starts Claude Code for one request.
type ClaudeCode struct {
	// Path is the executable, from the setting roles.<role>.cli_path.
	Path string
	// Logger may be nil. Then the default logger is used.
	Logger *slog.Logger
	// Grace is the time that the CLI gets to end after SIGTERM, before
	// SIGKILL. Zero means defaultGrace. Tests shorten it.
	Grace time.Duration
}

func (c ClaudeCode) grace() time.Duration {
	if c.Grace > 0 {
		return c.Grace
	}
	return defaultGrace
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
//
// The run ends at the time limit of the request: SIGTERM goes to the
// process group of the CLI, and SIGKILL follows after the grace period.
// The CLI is in its own process group, so the signals reach the commands
// that the agent started. A cancelled context ends the run the same way.
func (c ClaudeCode) Run(ctx context.Context, req Request) (*Run, error) {
	log := c.logger().With("role", req.Role, "work_dir", req.WorkDir)
	if req.SessionID == "" {
		log.Info("agent start", "session", "new", "time_limit", req.TimeLimit)
	} else {
		log.Info("agent start", "session", "resumed", "session_id", req.SessionID, "time_limit", req.TimeLimit)
	}

	runCtx := ctx
	if req.TimeLimit > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, req.TimeLimit)
		defer cancel()
	}

	cmd := exec.CommandContext(runCtx, c.Path, c.args(req)...)
	cmd.Dir = req.WorkDir
	// Stdin is nil: the process reads from the null device (os/exec).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// When the context is done, Cancel sends SIGTERM to the group. After
	// WaitDelay, os/exec kills the CLI and closes the pipes (os/exec:
	// Cmd.Cancel, Cmd.WaitDelay).
	cmd.Cancel = func() error { return signalGroup(cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = c.grace()
	reader := &streamReader{c: c, log: log}
	cmd.Stdout = reader
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, limit: 64 << 10}

	if err := cmd.Start(); err != nil {
		if runCtx.Err() != nil {
			return nil, c.fail(log, &AbnormalEnd{Kind: EndTimeLimit, Detail: "the run was stopped before the start", Err: runCtx.Err()})
		}
		return nil, c.fail(log, &AbnormalEnd{Kind: EndProcessFailed, Detail: "start " + c.Path, Err: err})
	}
	pid := cmd.Process.Pid

	waitErr := cmd.Wait()
	reader.flush()
	if runCtx.Err() != nil {
		// The grace period is over. Nothing of the group may stay alive.
		_ = signalGroup(pid, syscall.SIGKILL)
	}
	if stderr.Len() > 0 {
		log.Debug("agent stderr", "text", stderr.String())
	}

	s := reader.s
	end := &AbnormalEnd{SessionID: s.sessionID, PID: pid}
	var exitErr *exec.ExitError
	switch {
	case runCtx.Err() != nil && ctx.Err() == nil:
		end.Kind, end.Detail, end.Err = EndTimeLimit, fmt.Sprintf("the time limit of %s passed", req.TimeLimit), runCtx.Err()
	case runCtx.Err() != nil:
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

// signalGroup sends the signal to the process group of pid. A group that
// is gone counts as done, so that Wait returns the exit status.
func signalGroup(pid int, sig syscall.Signal) error {
	err := syscall.Kill(-pid, sig)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

// streamReader is the stdout of the CLI. It reads one event for each
// line as the lines arrive, so that a line that is still in the pipe when
// the process exits is not lost.
type streamReader struct {
	c   ClaudeCode
	log *slog.Logger
	s   stream
	buf []byte
}

func (r *streamReader) Write(p []byte) (int, error) {
	r.buf = append(r.buf, p...)
	for {
		i := bytes.IndexByte(r.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		r.line(r.buf[:i])
		r.buf = r.buf[i+1:]
	}
}

// flush reads a last line without a newline.
func (r *streamReader) flush() {
	r.line(r.buf)
	r.buf = nil
}

func (r *streamReader) line(line []byte) {
	if len(bytes.TrimSpace(line)) == 0 {
		return
	}
	// A copy, because buf is reused.
	r.c.readLine(r.log, &r.s, append([]byte(nil), line...))
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
