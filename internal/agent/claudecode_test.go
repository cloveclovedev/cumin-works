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
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
)

// The tests cover the second to fifth requirements of #7 with a fake
// CLI: a shell script that records its arguments and prints a fixture.

// fakeCLI writes the script and returns its path and the record path.
// The record holds the arguments, NUL-separated, and the last line of
// the record's sibling file ".cwd" is the working directory.
func fakeCLI(t *testing.T, fixture string, exitCode int) (path, record string) {
	t.Helper()
	dir := t.TempDir()
	path = filepath.Join(dir, "fake-claude")
	record = filepath.Join(dir, "record")
	abs, err := filepath.Abs(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\nfor a in \"$@\"; do printf '%%s\\0' \"$a\"; done > %q\npwd > %q\ncat %q\necho 'fake stderr' >&2\nexit %d\n", record, record+".cwd", abs, exitCode)
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
	}
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
	var logs bytes.Buffer
	c := ClaudeCode{Path: path, Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))}

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
	// The utilization is not logged at info level.
	for _, forbidden := range []string{"0.26", "0.51", "utilization", "fake stderr"} {
		if strings.Contains(logs.String(), forbidden) {
			t.Errorf("info logs hold %q:\n%s", forbidden, logs.String())
		}
	}
	if !strings.Contains(logs.String(), "session_id="+fixtureSessionID) || !strings.Contains(logs.String(), "result=done") {
		t.Errorf("info logs do not name the session and the result:\n%s", logs.String())
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
	script := "#!/bin/sh\nprintf '%s\\n' '{\"type\":\"system\",\"subtype\":\"init\",\"session_id\":\"" + fixtureSessionID + "\"}'\n: > " + started + "\nsleep 60 >/dev/null 2>&1\n"
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
	for _, absent := range []string{"--resume", "--model", "--bare", "--allowedTools", "--dangerously-skip-permissions"} {
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
