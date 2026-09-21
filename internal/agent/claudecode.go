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
	"sort"
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
	// QuotaTimeLimit bounds the minimal run of ReadQuota. Zero means
	// defaultQuotaTimeLimit. Tests shorten it.
	QuotaTimeLimit time.Duration
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
// the end, and returns the run. An error is an *AbnormalEnd, except for a
// request without credentials, which is refused before the start.
//
// The environment of the CLI is built from a fixed list (env.go). It
// holds the token of the request for git and gh, and nothing of the Host
// user's credentials.
//
// The run ends at the time limit of the request: SIGTERM goes to the
// process group of the CLI, and SIGKILL follows after the grace period.
// The CLI is in its own process group, so the signals reach the commands
// that the agent started. A cancelled context ends the run the same way.
func (c ClaudeCode) Run(ctx context.Context, req Request) (*Run, error) {
	if err := req.Credentials.validate(); err != nil {
		return nil, fmt.Errorf("agent run: %w", err)
	}
	log := c.logger().With("role", req.Role, "work_dir", req.WorkDir)
	if req.SessionID == "" {
		log.Info("agent start", "session", "new", "time_limit", req.TimeLimit)
	} else {
		log.Info("agent start", "session", "resumed", "session_id", req.SessionID, "time_limit", req.TimeLimit)
	}

	// gh keeps its configuration and state in this directory during the
	// run. It starts empty, so gh uses its defaults.
	ghConfigDir, err := os.MkdirTemp("", "cumin-gh-")
	if err != nil {
		return nil, c.fail(log, &AbnormalEnd{Kind: EndProcessFailed, Detail: "create the gh configuration directory", Err: err})
	}
	defer os.RemoveAll(ghConfigDir)

	// The output of the CLI may hold the token (a tool that prints its
	// environment, an error with the authorization header). It is
	// redacted before any log.
	ex := c.execute(ctx, log, execution{
		dir:     req.WorkDir,
		env:     environment(req.Credentials, ghConfigDir),
		args:    c.args(req),
		limit:   req.TimeLimit,
		secrets: req.Credentials.secrets(),
	})
	if ex.startErr != nil {
		if ex.limitErr != nil {
			return nil, c.fail(log, &AbnormalEnd{Kind: EndTimeLimit, Detail: "the run was stopped before the start", Err: ex.limitErr})
		}
		return nil, c.fail(log, &AbnormalEnd{Kind: EndProcessFailed, Detail: "start " + c.Path, Err: ex.startErr})
	}

	s := ex.s
	end := &AbnormalEnd{SessionID: s.sessionID, PID: ex.pid}
	var exitErr *exec.ExitError
	switch {
	case ex.limitErr != nil && ctx.Err() == nil:
		end.Kind, end.Detail, end.Err = EndTimeLimit, fmt.Sprintf("the time limit of %s passed", req.TimeLimit), ex.limitErr
	case ex.limitErr != nil:
		end.Kind, end.Detail, end.Err = EndTimeLimit, "the run was stopped", ctx.Err()
	case ex.waitErr != nil && errors.As(ex.waitErr, &exitErr):
		end.Kind, end.Detail = EndProcessFailed, fmt.Sprintf("exit code %d", exitErr.ExitCode())
	case ex.waitErr != nil:
		end.Kind, end.Detail, end.Err = EndProcessFailed, "wait", ex.waitErr
	case s.result == nil:
		end.Kind, end.Detail = EndNoResult, "the run ended without a result event"
	case s.result.IsError:
		end.Kind, end.Detail = EndError, "the result event has is_error true, subtype "+s.result.Subtype
	case len(s.result.StructuredOutput) == 0:
		end.Kind, end.Detail = EndInvalidResult, "the result event has no structured_output"
	case s.sessionID == "":
		// A later request could not continue this session.
		end.Kind, end.Detail = EndInvalidResult, "the run reported no session ID"
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

// execution is one process of the CLI: what to start, and what it left
// behind. Run and ReadQuota share it.
type execution struct {
	dir     string
	env     []string
	args    []string
	limit   time.Duration
	secrets []string

	// pid is the process of the CLI. 0 when it did not start.
	pid int
	// startErr is set when the CLI did not start.
	startErr error
	// limitErr is set when the time limit passed or the context ended.
	limitErr error
	// waitErr is the error of Wait: a non-zero exit code, or a failure.
	waitErr error
	s       stream
}

// execute starts the CLI, reads its stream until the end, and kills what
// stays in its process group. See Run for the stop at the time limit.
func (c ClaudeCode) execute(ctx context.Context, log *slog.Logger, ex execution) execution {
	runCtx := ctx
	if ex.limit > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, ex.limit)
		defer cancel()
	}

	cmd := exec.CommandContext(runCtx, c.Path, ex.args...)
	cmd.Dir = ex.dir
	cmd.Env = ex.env
	// Stdin is nil: the process reads from the null device (os/exec).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// When the context is done, Cancel sends SIGTERM to the group. After
	// WaitDelay, os/exec kills the CLI and closes the pipes (os/exec:
	// Cmd.Cancel, Cmd.WaitDelay).
	cmd.Cancel = func() error { return signalGroup(cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = c.grace()
	reader := &streamReader{c: c, log: log, secrets: ex.secrets}
	cmd.Stdout = reader
	var stderr bytes.Buffer
	cmd.Stderr = &limitedWriter{w: &stderr, limit: 64 << 10}

	if err := cmd.Start(); err != nil {
		ex.startErr, ex.limitErr = err, runCtx.Err()
		return ex
	}
	ex.pid = cmd.Process.Pid

	ex.waitErr = cmd.Wait()
	reader.flush()
	ex.limitErr = runCtx.Err()
	// Nothing of the group may stay alive after the run, whatever its
	// end: a command that the agent left in the background would keep
	// the token in its environment. After a time limit, the grace period
	// is over at this point.
	_ = signalGroup(ex.pid, syscall.SIGKILL)
	if stderr.Len() > 0 {
		log.Debug("agent stderr", "text", redact(stderr.String(), ex.secrets))
	}
	ex.s = reader.s
	return ex
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
	c       ClaudeCode
	log     *slog.Logger
	secrets []string
	s       stream
	buf     []byte
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
	r.c.readLine(r.log, &r.s, append([]byte(nil), line...), r.secrets)
}

func (c ClaudeCode) readLine(log *slog.Logger, s *stream, line []byte, secrets []string) {
	var e event
	if err := json.Unmarshal(line, &e); err != nil {
		log.Debug("agent output is not JSON", "text", truncate(redact(string(line), secrets), 200))
		return
	}
	if e.SessionID != "" {
		s.sessionID = e.SessionID
	}
	switch e.Type {
	case "system":
		if e.Subtype == "init" {
			// The names of the fields, not the values: the record of a
			// live run uses them, and a changed shape shows here first.
			log.Debug("agent init event", "fields", fieldNames(line, ""))
		}
	case "rate_limit_event":
		log.Debug("agent rate limit event", "fields", fieldNames(line, "rate_limit_info"), "status", statusOf(e.RateLimitInfo))
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

// fieldNames returns the sorted names of the fields of the JSON object in
// line, or of its nested object at key when key is not empty.
func fieldNames(line []byte, key string) []string {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(line, &object); err != nil {
		return nil
	}
	if key != "" {
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(object[key], &nested); err != nil {
			return nil
		}
		object = nested
	}
	names := make([]string, 0, len(object))
	for name := range object {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func statusOf(info *rateLimitInfo) string {
	if info == nil {
		return ""
	}
	return info.Status
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
