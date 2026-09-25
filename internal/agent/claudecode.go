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
	"path/filepath"
	"slices"
	"sort"
	"strings"
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
	if req.SkillsDir != "" {
		// The skills of cumin: Claude Code loads the skills under
		// .claude/skills/ of a directory passed with --add-dir (official:
		// Skills, "Where skills are discovered"). The directory holds only
		// the skills, so the file access that the flag grants is harmless.
		args = append(args, "--add-dir", req.SkillsDir)
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
	// Fields of the init event that show user-level context
	// (measured-constraints.md rows 27, 28; the live record of #67).
	Plugins     json.RawMessage `json:"plugins"`
	MCPServers  json.RawMessage `json:"mcp_servers"`
	MemoryPaths json.RawMessage `json:"memory_paths"`
	// Field of the init event that lists the skills of the run
	// (measured-constraints.md row 86).
	Skills json.RawMessage `json:"skills"`
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
	// init is true after the init event.
	init bool
	// userContext is the reason when the init event showed user-level
	// context. The run is then stopped at once.
	userContext string
	initEvent   event
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
	// The role is not added here: the caller gives a logger that names it
	// (Service.Start), and adding it again gave one line two "role"
	// fields in the live scenario Impl-1. The work directory is an
	// absolute path of the Host, so it goes to one debug line, as
	// Workspace does with the path of a worktree.
	log := c.logger()
	log.Debug("agent work directory", "work_dir", req.WorkDir)
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
		dir:        req.WorkDir,
		env:        environment(req.Credentials, ghConfigDir),
		args:       c.args(req),
		limit:      req.TimeLimit,
		secrets:    req.Credentials.secrets(),
		checkInit:  true,
		wantSkills: wantSkills(req),
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
	case s.userContext != "":
		// The run was stopped by the reader, before the agent worked.
		end.Kind, end.Detail = EndUserContext, s.userContext
	case ex.limitErr != nil && ctx.Err() == nil:
		end.Kind, end.Detail, end.Err = EndTimeLimit, fmt.Sprintf("the time limit of %s passed", req.TimeLimit), ex.limitErr
	case ex.limitErr != nil:
		end.Kind, end.Detail, end.Err = EndTimeLimit, "the run was stopped", ctx.Err()
	case ex.waitErr != nil && errors.As(ex.waitErr, &exitErr):
		end.Kind, end.Detail = EndProcessFailed, fmt.Sprintf("exit code %d", exitErr.ExitCode())
	case ex.waitErr != nil:
		end.Kind, end.Detail, end.Err = EndProcessFailed, "wait", ex.waitErr
	case !s.init:
		// Without the record of the start, cumin cannot know what the
		// agent read.
		end.Kind, end.Detail = EndUserContext, noInitEvent
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
	// checkInit stops the run when the init event shows user-level
	// context: plugins, MCP servers, or memory outside dir.
	checkInit bool
	// wantSkills are the skills that cumin wrote for the role of this
	// request. The init event must list every one of them.
	wantSkills []string

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
	// The reader stops the run through this cancel, with the same
	// signals as the time limit.
	runCtx, stop := context.WithCancel(runCtx)
	defer stop()

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
	if ex.checkInit {
		reader.workDir, reader.stop, reader.wantSkills = ex.dir, stop, ex.wantSkills
	}
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
	// workDir, wantSkills and stop are set when the init event is checked.
	// stop ends the run when the event shows user-level context, or when a
	// skill of cumin did not arrive.
	workDir    string
	wantSkills []string
	stop       context.CancelFunc
	s          stream
	buf        []byte
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
	if r.stop == nil {
		return
	}
	// The check runs once: at the init event, or at a result that came
	// without one. Either way the run is stopped at once when it fails.
	var reason string
	switch {
	case r.s.init:
		reason = userContext(r.s.initEvent, r.workDir, r.wantSkills)
	case r.s.result != nil:
		reason = noInitEvent
	default:
		return
	}
	stop := r.stop
	r.stop = nil
	if reason != "" {
		r.s.userContext = reason
		r.log.Info("agent start record shows user-level context; the run is stopped", "reason", reason)
		stop()
	}
}

// noInitEvent is the reason of a run whose record of the start is missing.
const noInitEvent = "the run had no init event"

func (c ClaudeCode) readLine(log *slog.Logger, s *stream, line []byte, secrets []string) {
	// The type first, so that an event that does not decode is still
	// known by its type.
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil {
		log.Debug("agent output is not JSON", "text", truncate(redact(string(line), secrets), 200))
		return
	}
	var e event
	if err := json.Unmarshal(line, &e); err != nil {
		if envelope.Type == "rate_limit_event" {
			// A rate limit event of a changed shape: no usage, not the
			// earlier value.
			s.quota = nil
		}
		log.Debug("agent event does not decode", "type", envelope.Type, "err", err.Error())
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
			// The skills of a run are checked, so their shape is named
			// too, which a live run records without any value.
			log.Debug("agent init event", "fields", fieldNames(line, ""), "skills_shape", jsonShape(e.Skills))
			s.init, s.initEvent = true, e
		}
	case "rate_limit_event":
		log.Debug("agent rate limit event", "fields", fieldNames(line, "rate_limit_info"), "status", statusOf(e.RateLimitInfo))
		// The last event is the usage. An event that cumin cannot read
		// replaces an earlier one with "no usage", so that a changed
		// shape is not hidden by an older value.
		s.quota = nil
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

// userContext reports why the init event shows context from outside the
// work directory, or "" when it shows none. Checked: plugins and MCP
// servers (empty with --setting-sources project, row 27), and
// memory_paths (absent when auto memory is off, row 28; the live record
// of #67). plugins and mcp_servers must be present: a record without
// them cannot confirm that nothing was loaded. The init event lists no
// instruction files, so instructions cannot be checked here. The reason
// names the field, not the paths.
func userContext(e event, workDir string, wantSkills []string) string {
	// A missing field cannot confirm that nothing was loaded. Safe side.
	if e.Plugins == nil {
		return "the init event has no plugins field"
	}
	if e.MCPServers == nil {
		return "the init event has no mcp_servers field"
	}
	if jsonPresent(e.Plugins) {
		return "the init event lists plugins"
	}
	if jsonPresent(e.MCPServers) {
		return "the init event lists MCP servers"
	}
	if jsonPresent(e.MemoryPaths) {
		paths := jsonStrings(e.MemoryPaths)
		if len(paths) == 0 {
			// A shape without paths cannot be checked. Safe side.
			return "the init event has memory_paths of an unknown shape"
		}
		for _, path := range paths {
			if !underDir(path, workDir) {
				return "the init event has memory_paths outside the work directory"
			}
		}
	}
	return missingSkill(e, wantSkills)
}

// missingSkill reports which skill of cumin the init event does not list,
// or "" when it lists them all. cumin writes the skills of a role into one
// directory and passes it with --add-dir, so a skill that is not offered
// means that the agent would write a text of GitHub from memory instead of
// from the template.
//
// The field must be present, as plugins and mcp_servers must: a record
// that cumin cannot read confirms nothing. The field is a list of the
// names of the skills, measured on 2026-09-25 with Claude Code 2.1.273
// (the record on #166); the official documentation does not describe it.
// Any other shape is unreadable and ends the run, as a missing plugins
// field does.
func missingSkill(e event, want []string) string {
	if e.Skills == nil {
		return "the init event has no skills field"
	}
	if len(want) == 0 {
		return ""
	}
	got, ok := jsonStringList(e.Skills)
	if !ok {
		// A shape that cumin cannot read cannot confirm anything.
		return "the init event has skills of an unknown shape"
	}
	for _, name := range want {
		if !slices.Contains(got, name) {
			return "the init event does not list the skill " + name
		}
	}
	return ""
}

// wantSkills are the skills that the start record of this request must
// list: those of its role, when cumin passed a directory of skills.
func wantSkills(req Request) []string {
	if req.SkillsDir == "" {
		return nil
	}
	return SkillNamesOf(req.Role)
}

// jsonPresent reports whether raw is a JSON value other than null, an
// empty array, or an empty object. A value of an unknown shape counts as
// present, on the safe side.
func jsonPresent(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return false
	}
	var array []json.RawMessage
	if err := json.Unmarshal(trimmed, &array); err == nil {
		return len(array) > 0
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err == nil {
		return len(object) > 0
	}
	return true
}

// jsonShape names the shape of raw, and never a value of it: the type,
// and for a list the type of its items with the keys of an object. A live
// run records the shape of a field that cumin reads, so that a changed
// shape is told from a changed value.
func jsonShape(raw json.RawMessage) string {
	if raw == nil {
		return "absent"
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "not JSON"
	}
	list, ok := value.([]any)
	if !ok {
		if value == nil {
			return "null"
		}
		if object, ok := value.(map[string]any); ok {
			return "object with " + strings.Join(sortedKeys(object), ",")
		}
		return fmt.Sprintf("%T", value)
	}
	if len(list) == 0 {
		return "empty list"
	}
	switch item := list[0].(type) {
	case string:
		return "list of strings"
	case map[string]any:
		return "list of objects with " + strings.Join(sortedKeys(item), ",")
	default:
		return fmt.Sprintf("list of %T", item)
	}
}

// sortedKeys returns the keys of an object in a fixed order.
func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// jsonStringList returns the strings of a JSON list of strings. The
// second value is false for any other shape, so that the caller can tell
// "cumin cannot read this" from "the list is empty".
func jsonStringList(raw json.RawMessage) ([]string, bool) {
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, false
	}
	return list, true
}

// jsonStrings collects every string in raw, at any depth.
func jsonStrings(raw json.RawMessage) []string {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case string:
			out = append(out, v)
		case []any:
			for _, item := range v {
				walk(item)
			}
		case map[string]any:
			for _, item := range v {
				walk(item)
			}
		}
	}
	walk(value)
	return out
}

// underDir reports whether path is dir or inside dir, after symbolic
// links are resolved.
func underDir(path, dir string) bool {
	path, dir = resolvePath(path), resolvePath(dir)
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

// resolvePath makes p absolute and resolves the symbolic links of its
// longest existing ancestor. A path that does not exist yet (a memory
// directory that is not created) is resolved through its parents, so that
// it compares with an existing directory on a system where a temporary
// directory is behind a link.
func resolvePath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	p = filepath.Clean(p)
	rest := ""
	for cur := p; ; {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
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
