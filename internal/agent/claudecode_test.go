package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
)

// The tests cover the second to fifth requirements of #7 with a fake
// CLI: a shell script that records its arguments and prints a fixture.

// fakeCLI writes the script and returns its path and the record path.
// The record holds the arguments, NUL-separated; the record's sibling
// file ".cwd" holds the working directory, and ".env" the environment.
func fakeCLI(t *testing.T, fixture string, exitCode int) (path, record string) {
	t.Helper()
	dir := t.TempDir()
	path = filepath.Join(dir, "fake-claude")
	record = filepath.Join(dir, "record")
	abs, err := filepath.Abs(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\nfor a in \"$@\"; do printf '%%s\\0' \"$a\"; done > %q\npwd > %q\nenv > %q\ncat %q\necho 'fake stderr' >&2\nexit %d\n", record, record+".cwd", record+".env", abs, exitCode)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, record
}

func recordedArgs(t *testing.T, record string) []string {
	t.Helper()
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
}

func request(t *testing.T) Request {
	t.Helper()
	return Request{
		Role:            config.RoleImplementer,
		RoleInstruction: "# Implementer\n\nYou implement one issue.",
		Text:            "Implement issue 12 on branch cumin/12-example.",
		WorkDir:         t.TempDir(),
		TimeLimit:       time.Minute,
		Credentials:     testCredentials,
	}
}

// testCredentials are made up. The token is a marker that the tests
// search for in logs and errors.
var testCredentials = Credentials{
	Token:       "ghs_fake_token_marker_0123456789",
	AuthorName:  "example-implementer[bot]",
	AuthorEmail: "12345+example-implementer[bot]@users.noreply.github.com",
}

// quiet is a ClaudeCode whose logs go nowhere.
func quiet(path string) ClaudeCode {
	return ClaudeCode{Path: path, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func abnormalEnd(t *testing.T, err error) *AbnormalEnd {
	t.Helper()
	var end *AbnormalEnd
	if !errors.As(err, &end) {
		t.Fatalf("error is %T (%v), want *AbnormalEnd", err, err)
	}
	return end
}

const fixtureSessionID = "11111111-2222-4333-8444-555555555555"

func TestRun_ValidDone(t *testing.T) {
	path, _ := fakeCLI(t, "done.jsonl", 0)
	logger, logs := newTestLogger()
	c := ClaudeCode{Path: path, Logger: logger}

	run, err := c.Run(context.Background(), request(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.SessionID != fixtureSessionID {
		t.Errorf("SessionID = %q", run.SessionID)
	}
	if want := (Result{Result: "done", Summary: "I read the file."}); run.Result != want {
		t.Errorf("Result = %+v, want %+v", run.Result, want)
	}
	if !run.QuotaRead {
		t.Fatal("QuotaRead = false, want true")
	}
	// The last rate_limit_event of the run is the quota usage.
	if run.Quota.FiveHour.Utilization != 0.26 || run.Quota.Weekly.Utilization != 0.51 {
		t.Errorf("Quota = %+v, want the last event", run.Quota)
	}
	if !run.Quota.FiveHour.ResetsAt.Equal(time.Unix(1900000000, 0)) || !run.Quota.Weekly.ResetsAt.Equal(time.Unix(1900300000, 0)) {
		t.Errorf("reset times = %v, %v", run.Quota.FiveHour.ResetsAt, run.Quota.Weekly.ResetsAt)
	}
	if run.Quota.ReadAt.IsZero() {
		t.Error("ReadAt is zero")
	}
	// The utilization is not logged at info level. The check reads the
	// attributes of the records, not the text of the lines: the time can
	// hold the same digits.
	logs.requireNoText(t, "0.26", "0.51", "utilization", "fake stderr")
	if !logs.hasAttr("session_id", fixtureSessionID) || !logs.hasAttr("result", "done") {
		t.Errorf("info logs do not name the session and the result: %+v", logs.records)
	}
}

func TestRun_ValidBlocked(t *testing.T) {
	path, _ := fakeCLI(t, "blocked.jsonl", 0)
	run, err := quiet(path).Run(context.Background(), request(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Result.Result != ResultBlocked || !strings.HasPrefix(run.Result.BlockedReason, "## Decision needed:") {
		t.Errorf("Result = %+v", run.Result)
	}
	if !run.QuotaRead || run.Quota.FiveHour.Utilization != 0.25 {
		t.Errorf("Quota = %+v, read %v", run.Quota, run.QuotaRead)
	}
}

func TestRun_NoQuotaIsNotAnAbnormalEnd(t *testing.T) {
	path, _ := fakeCLI(t, "no-quota.jsonl", 0)
	run, err := quiet(path).Run(context.Background(), request(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.QuotaRead || run.Quota != (QuotaUsage{}) {
		t.Errorf("Quota = %+v, read %v; want empty", run.Quota, run.QuotaRead)
	}
}

// A rate_limit_event whose window lacks a field is read as "no usage",
// not as zero usage.
func TestRun_IncompleteQuotaWindowIsNotRead(t *testing.T) {
	path, _ := fakeCLI(t, "incomplete-quota.jsonl", 0)
	run, err := quiet(path).Run(context.Background(), request(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.QuotaRead {
		t.Errorf("QuotaRead = true with %+v, want false", run.Quota)
	}
}

func TestRun_AbnormalEnds(t *testing.T) {
	tests := []struct {
		name          string
		fixture       string
		exitCode      int
		wantKind      EndKind
		wantDetail    string
		wantSessionID string
	}{
		{"invalid result", "invalid-result.jsonl", 0, EndInvalidResult, `"result" is "finished"`, fixtureSessionID},
		{"blocked without a reason", "blocked-empty-reason.jsonl", 0, EndInvalidResult, `empty "blocked_reason"`, fixtureSessionID},
		{"is_error", "is-error.jsonl", 0, EndError, "error_during_execution", fixtureSessionID},
		{"no result event", "no-result.jsonl", 0, EndNoResult, "without a result event", fixtureSessionID},
		{"exit code 1", "done.jsonl", 1, EndProcessFailed, "exit code 1", fixtureSessionID},
		{"no session ID", "no-session-id.jsonl", 0, EndInvalidResult, "no session ID", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, _ := fakeCLI(t, tt.fixture, tt.exitCode)
			run, err := quiet(path).Run(context.Background(), request(t))
			if err == nil {
				t.Fatalf("Run = %+v, want an abnormal end", run)
			}
			end := abnormalEnd(t, err)
			if end.Kind != tt.wantKind {
				t.Errorf("Kind = %s, want %s", end.Kind, tt.wantKind)
			}
			if !strings.Contains(end.Detail, tt.wantDetail) {
				t.Errorf("Detail = %q, want it to contain %q", end.Detail, tt.wantDetail)
			}
			if end.SessionID != tt.wantSessionID {
				t.Errorf("SessionID = %q, want %q", end.SessionID, tt.wantSessionID)
			}
			if end.PID == 0 {
				t.Error("PID = 0, want the process of the CLI")
			}
		})
	}
}

// A cancelled context ends the run with the kind of the time limit, not
// as a failure of the CLI.
func TestRun_CancelIsTimeLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-claude")
	started := filepath.Join(dir, "started")
	// The script prints the init event, then marks that it started. The
	// child keeps no pipe open, so the read ends when sh is killed.
	// Children of the real CLI are the subject of #41.
	script := "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"system\",\"subtype\":\"init\",\"session_id\":\"" + fixtureSessionID + "\",\"plugins\":[],\"mcp_servers\":[]}'\n: > " + started + "\nsleep 60 >/dev/null 2>&1\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(started); err == nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		cancel()
	}()

	_, err := quiet(path).Run(ctx, request(t))
	end := abnormalEnd(t, err)
	if end.Kind != EndTimeLimit {
		t.Errorf("Kind = %s, want %s", end.Kind, EndTimeLimit)
	}
	if end.SessionID != fixtureSessionID {
		t.Errorf("SessionID = %q, want the one from the init event", end.SessionID)
	}
}

func TestRun_CancelBeforeStartIsTimeLimit(t *testing.T) {
	path, _ := fakeCLI(t, "done.jsonl", 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := quiet(path).Run(ctx, request(t))
	if end := abnormalEnd(t, err); end.Kind != EndTimeLimit || end.PID != 0 {
		t.Errorf("AbnormalEnd = %+v, want the kind %s without a process", end, EndTimeLimit)
	}
}

func TestRun_MissingExecutable(t *testing.T) {
	c := quiet(filepath.Join(t.TempDir(), "no-such-cli"))
	_, err := c.Run(context.Background(), request(t))
	end := abnormalEnd(t, err)
	if end.Kind != EndProcessFailed || end.PID != 0 || end.Err == nil {
		t.Errorf("AbnormalEnd = %+v", end)
	}
}

func TestRun_CommandLine(t *testing.T) {
	path, record := fakeCLI(t, "done.jsonl", 0)
	req := request(t)
	if _, err := quiet(path).Run(context.Background(), req); err != nil {
		t.Fatalf("Run: %v", err)
	}
	args := recordedArgs(t, record)

	pairs := map[string]string{
		"-p":                     req.Text,
		"--append-system-prompt": req.RoleInstruction,
		"--setting-sources":      "project",
		"--permission-mode":      "bypassPermissions",
		"--json-schema":          ResultSchema,
		"--output-format":        "stream-json",
	}
	for flag, want := range pairs {
		i := slices.Index(args, flag)
		if i < 0 || i+1 >= len(args) {
			t.Errorf("args have no %s: %q", flag, args)
			continue
		}
		if args[i+1] != want {
			t.Errorf("%s = %q, want %q", flag, args[i+1], want)
		}
	}
	if !slices.Contains(args, "--verbose") {
		t.Errorf("args have no --verbose: %q", args)
	}
	for _, absent := range []string{"--resume", "--model", "--add-dir", "--bare", "--allowedTools", "--dangerously-skip-permissions"} {
		if slices.Contains(args, absent) {
			t.Errorf("args have %s: %q", absent, args)
		}
	}
	cwd, err := os.ReadFile(record + ".cwd")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := realPath(t, strings.TrimSpace(string(cwd))), realPath(t, req.WorkDir); got != want {
		t.Errorf("working directory = %q, want %q", got, want)
	}
}

func TestRun_ResumeAndModel(t *testing.T) {
	path, record := fakeCLI(t, "done.jsonl", 0)
	req := request(t)
	req.SessionID = fixtureSessionID
	req.Model = "example-model"
	if _, err := quiet(path).Run(context.Background(), req); err != nil {
		t.Fatalf("Run: %v", err)
	}
	args := recordedArgs(t, record)
	for flag, want := range map[string]string{"--resume": fixtureSessionID, "--model": "example-model", "--append-system-prompt": req.RoleInstruction} {
		i := slices.Index(args, flag)
		if i < 0 || i+1 >= len(args) || args[i+1] != want {
			t.Errorf("args have no %s %q: %q", flag, want, args)
		}
	}
}

// The tests below cover the sixth requirement of #7: a run over the time
// limit is stopped, is an abnormal end, and leaves no child process.

// neverEndingCLI writes a fake CLI that prints the init event, starts a
// child, records the child's process ID in the returned file, and waits.
// prologue is shell text that runs first (for example a trap).
func neverEndingCLI(t *testing.T, prologue string) (path, childPID string) {
	t.Helper()
	return neverEndingCLIWithInit(t, prologue, `{"type":"system","subtype":"init","session_id":"`+fixtureSessionID+`","plugins":[],"mcp_servers":[]}`)
}

// neverEndingCLIWithInit is neverEndingCLI with the given init line.
func neverEndingCLIWithInit(t *testing.T, prologue, initLine string) (path, childPID string) {
	t.Helper()
	dir := t.TempDir()
	path = filepath.Join(dir, "fake-claude")
	childPID = filepath.Join(dir, "child.pid")
	// The child starts before the init line, so that a run that is
	// stopped at the init event has a recorded child to check.
	script := "#!/bin/sh\n" + prologue + "\n" +
		"sleep 300 &\n" +
		"echo $! > " + childPID + "\n" +
		"printf '%s\\n' '" + initLine + "'\n" +
		"wait\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, childPID
}

// processGone reports whether the process in the file is gone, waiting
// a short time for the kernel to reap it.
func processGone(t *testing.T, pidFile string) bool {
	t.Helper()
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the fake CLI did not record its child: %v", err)
	}
	var pid int
	if _, err := fmt.Sscan(string(data), &pid); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	syscall.Kill(pid, syscall.SIGKILL) // do not leave it behind
	return false
}

// The tests below cover the check of the init event (#68): user-level
// context ends the run at once, with the kind "user-level context".

func TestRun_UserContext(t *testing.T) {
	tests := []struct {
		name       string
		fixture    string
		wantDetail string
	}{
		{"a user-level plugin", "init-plugin.jsonl", "plugins"},
		{"a user-level MCP server", "init-mcp.jsonl", "MCP servers"},
		{"an auto memory path", "init-memory.jsonl", "memory_paths"},
		{"no init event", "no-init.jsonl", "no init event"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, _ := fakeCLI(t, tt.fixture, 0)
			var logs bytes.Buffer
			c := ClaudeCode{Path: path, Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))}
			run, err := c.Run(context.Background(), request(t))
			if err == nil {
				t.Fatalf("Run = %+v, want an abnormal end", run)
			}
			end := abnormalEnd(t, err)
			if end.Kind != EndUserContext {
				t.Errorf("Kind = %s, want %s", end.Kind, EndUserContext)
			}
			if !strings.Contains(end.Detail, tt.wantDetail) {
				t.Errorf("Detail = %q, want it to contain %q", end.Detail, tt.wantDetail)
			}
			if strings.Contains(end.Detail, "/example/home") || strings.Contains(logs.String(), "/example/home") {
				t.Errorf("the detail or the log names a path:\n%s", logs.String())
			}
		})
	}
}

// memory_paths inside the work directory is not user-level context;
// outside it is. The rule is checked directly, because a fixture cannot
// name a work directory that exists on every machine.
func TestUserContext_MemoryPathsAgainstTheWorkDirectory(t *testing.T) {
	work := t.TempDir()
	// clean is an init event with empty plugins and MCP servers, as a
	// real one with --setting-sources project.
	clean := func(memoryPaths string) event {
		return event{Plugins: []byte(`[]`), MCPServers: []byte(`[]`), MemoryPaths: []byte(memoryPaths)}
	}
	inside := clean(`{"auto":"` + filepath.Join(work, ".claude", "memory") + `"}`)
	if reason := userContext(inside, work); reason != "" {
		t.Errorf("userContext(inside) = %q, want none", reason)
	}
	outside := clean(`{"auto":"` + filepath.Join(t.TempDir(), "memory") + `"}`)
	if reason := userContext(outside, work); !strings.Contains(reason, "memory_paths") {
		t.Errorf("userContext(outside) = %q, want memory_paths", reason)
	}
	none := event{Plugins: []byte(`[]`), MCPServers: []byte(`[]`), MemoryPaths: []byte(`null`)}
	if reason := userContext(none, work); reason != "" {
		t.Errorf("userContext(none) = %q, want none", reason)
	}
	// A record without the fields cannot confirm that nothing was loaded.
	if reason := userContext(event{MCPServers: []byte(`[]`)}, work); !strings.Contains(reason, "no plugins field") {
		t.Errorf("userContext(no plugins field) = %q", reason)
	}
	if reason := userContext(event{Plugins: []byte(`[]`)}, work); !strings.Contains(reason, "no mcp_servers field") {
		t.Errorf("userContext(no mcp_servers field) = %q", reason)
	}
	// A present value without a path cannot be checked, so it counts.
	for _, raw := range []string{`true`, `{"auto":1}`, `[1, 2]`} {
		unknown := clean(raw)
		if reason := userContext(unknown, work); !strings.Contains(reason, "unknown shape") {
			t.Errorf("userContext(memory_paths %s) = %q, want unknown shape", raw, reason)
		}
	}
}

// A CLI that prints a result without an init event and then keeps
// running is stopped at the result, not at the time limit.
func TestRun_ResultWithoutInitStopsTheRunAtOnce(t *testing.T) {
	result := `{"type":"result","subtype":"success","is_error":false,"session_id":"` + fixtureSessionID + `","structured_output":{"result":"done","summary":"x","blocked_reason":""}}`
	path, childPID := neverEndingCLIWithInit(t, "", result)
	c := quiet(path)
	c.Grace = time.Second
	req := request(t)
	req.TimeLimit = 30 * time.Second

	start := time.Now()
	_, err := c.Run(context.Background(), req)
	elapsed := time.Since(start)

	end := abnormalEnd(t, err)
	if end.Kind != EndUserContext || !strings.Contains(end.Detail, "no init event") {
		t.Errorf("AbnormalEnd = %+v, want %s without an init event", end, EndUserContext)
	}
	if elapsed > c.Grace+3*time.Second {
		t.Errorf("Run took %v, want a stop well before the time limit", elapsed)
	}
	if !processGone(t, childPID) {
		t.Error("the child of the fake CLI is still alive")
	}
}

// A CLI that shows a plugin in its init event and then keeps running is
// stopped at once, and its child is gone.
func TestRun_UserContextStopsTheRunAtOnce(t *testing.T) {
	init := `{"type":"system","subtype":"init","session_id":"` + fixtureSessionID + `","plugins":[{"name":"example-plugin"}],"mcp_servers":[]}`
	path, childPID := neverEndingCLIWithInit(t, "", init)
	c := quiet(path)
	c.Grace = time.Second
	req := request(t)
	req.TimeLimit = 30 * time.Second

	start := time.Now()
	_, err := c.Run(context.Background(), req)
	elapsed := time.Since(start)

	end := abnormalEnd(t, err)
	if end.Kind != EndUserContext || !strings.Contains(end.Detail, "plugins") {
		t.Errorf("AbnormalEnd = %+v, want %s about plugins", end, EndUserContext)
	}
	if end.SessionID != fixtureSessionID {
		t.Errorf("SessionID = %q, want the one from the init event", end.SessionID)
	}
	if elapsed > c.Grace+3*time.Second {
		t.Errorf("Run took %v, want a stop well before the time limit", elapsed)
	}
	if !processGone(t, childPID) {
		t.Error("the child of the fake CLI is still alive")
	}
}

// A command that the agent left in the background, with its stdio
// redirected, does not survive a normal end. It holds the token.
func TestRun_NormalEndLeavesNoChild(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-claude")
	childPID := filepath.Join(dir, "child.pid")
	fixture, err := filepath.Abs(filepath.Join("testdata", "done.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nsleep 300 >/dev/null 2>&1 &\necho $! > " + childPID + "\ncat " + fixture + "\nexit 0\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	run, err := quiet(path).Run(context.Background(), request(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Result.Result != ResultDone {
		t.Errorf("Result = %+v", run.Result)
	}
	if !processGone(t, childPID) {
		t.Error("the background child of the fake CLI is still alive after a normal end")
	}
}

// The two tests of the time limit measure a run that is stopped. They
// flapped in both directions on a busy machine with one second each, so
// the limit, the grace, and the slack are wider here.
const (
	timeLimitTestLimit = 3 * time.Second
	timeLimitTestGrace = 3 * time.Second
	timeLimitTestSlack = 5 * time.Second
)

func TestRun_TimeLimitStopsTheRunAndItsChild(t *testing.T) {
	path, childPID := neverEndingCLI(t, "")
	c := quiet(path)
	// The limit and the grace are longer than the test needs, and the
	// slack below is wider, so that a busy machine does not fail the test:
	// what is measured is that the run ends around the limit, not how fast
	// the Host is.
	c.Grace = timeLimitTestGrace
	req := request(t)
	req.TimeLimit = timeLimitTestLimit

	start := time.Now()
	_, err := c.Run(context.Background(), req)
	elapsed := time.Since(start)

	end := abnormalEnd(t, err)
	if end.Kind != EndTimeLimit {
		t.Errorf("Kind = %s, want %s", end.Kind, EndTimeLimit)
	}
	if !strings.Contains(end.Detail, "time limit") || end.PID == 0 || end.SessionID != fixtureSessionID {
		t.Errorf("AbnormalEnd = %+v", end)
	}
	if elapsed > req.TimeLimit+c.Grace+timeLimitTestSlack {
		t.Errorf("Run took %v, want about the limit plus the grace period", elapsed)
	}
	if !processGone(t, childPID) {
		t.Error("the child of the fake CLI is still alive")
	}
}

func TestRun_TimeLimitKillsAfterGraceWhenTermIsIgnored(t *testing.T) {
	// The child inherits the ignored SIGTERM, so only SIGKILL ends it.
	path, childPID := neverEndingCLI(t, "trap '' TERM")
	c := quiet(path)
	c.Grace = timeLimitTestGrace
	req := request(t)
	req.TimeLimit = timeLimitTestLimit

	start := time.Now()
	_, err := c.Run(context.Background(), req)
	elapsed := time.Since(start)

	if end := abnormalEnd(t, err); end.Kind != EndTimeLimit {
		t.Errorf("Kind = %s, want %s", end.Kind, EndTimeLimit)
	}
	if elapsed < req.TimeLimit+c.Grace {
		t.Errorf("Run took %v, want at least the limit plus the grace period", elapsed)
	}
	if elapsed > req.TimeLimit+c.Grace+timeLimitTestSlack {
		t.Errorf("Run took %v, want about the limit plus the grace period", elapsed)
	}
	if !processGone(t, childPID) {
		t.Error("the child of the fake CLI is still alive after SIGKILL")
	}
}

func TestRun_ExitOnTermEndsBeforeTheGracePeriod(t *testing.T) {
	// The CLI ends its child and exits on SIGTERM, as Claude Code does.
	path, childPID := neverEndingCLI(t, "trap 'kill $child; exit 143' TERM")
	script, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	script = bytes.Replace(script, []byte("echo $! > "), []byte("child=$!\necho $child > "), 1)
	if err := os.WriteFile(path, script, 0o755); err != nil {
		t.Fatal(err)
	}
	c := quiet(path)
	c.Grace = 5 * time.Second
	req := request(t)
	req.TimeLimit = time.Second

	start := time.Now()
	_, err = c.Run(context.Background(), req)
	elapsed := time.Since(start)

	if end := abnormalEnd(t, err); end.Kind != EndTimeLimit {
		t.Errorf("Kind = %s, want %s", end.Kind, EndTimeLimit)
	}
	if elapsed > req.TimeLimit+c.Grace/2 {
		t.Errorf("Run took %v, want well under the limit plus the grace period", elapsed)
	}
	if !processGone(t, childPID) {
		t.Error("the child of the fake CLI is still alive")
	}
}

// A request with a skills directory passes it with --add-dir.
func TestRun_SkillsDirIsPassedWithAddDir(t *testing.T) {
	path, record := fakeCLI(t, "done.jsonl", 0)
	req := request(t)
	req.SkillsDir = t.TempDir()
	if _, err := quiet(path).Run(context.Background(), req); err != nil {
		t.Fatalf("Run: %v", err)
	}
	args := recordedArgs(t, record)
	i := slices.Index(args, "--add-dir")
	if i < 0 || i+1 >= len(args) || args[i+1] != req.SkillsDir {
		t.Errorf("args have no --add-dir %s: %q", req.SkillsDir, args)
	}
}
