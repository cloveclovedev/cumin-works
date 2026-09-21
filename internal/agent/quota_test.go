package agent

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The tests cover the quota read before a start (#67) with the fake CLI.

func quotaNotRead(t *testing.T, err error) *QuotaNotRead {
	t.Helper()
	var q *QuotaNotRead
	if !errors.As(err, &q) {
		t.Fatalf("error is %T (%v), want *QuotaNotRead", err, err)
	}
	return q
}

func TestReadQuota_ReadsTheLastEvent(t *testing.T) {
	path, record := fakeCLI(t, "quota-run.jsonl", 0)
	logger, logs := newTestLogger()
	c := ClaudeCode{Path: path, Logger: logger}

	usage, err := c.ReadQuota(context.Background())
	if err != nil {
		t.Fatalf("ReadQuota: %v", err)
	}
	if usage.FiveHour.Utilization != 0.31 || usage.Weekly.Utilization != 0.61 {
		t.Errorf("usage = %+v, want the last event", usage)
	}
	if !usage.FiveHour.ResetsAt.Equal(time.Unix(1900000000, 0)) || !usage.Weekly.ResetsAt.Equal(time.Unix(1900300000, 0)) {
		t.Errorf("reset times = %v, %v", usage.FiveHour.ResetsAt, usage.Weekly.ResetsAt)
	}
	if usage.ReadAt.IsZero() {
		t.Error("ReadAt is zero")
	}

	args := recordedArgs(t, record)
	for flag, want := range map[string]string{
		"-p":                quotaPrompt,
		"--system-prompt":   quotaSystemPrompt,
		"--model":           "haiku",
		"--tools":           "",
		"--setting-sources": "project",
		"--output-format":   "stream-json",
	} {
		i := slices.Index(args, flag)
		if i < 0 || i+1 >= len(args) {
			t.Errorf("args have no %s: %q", flag, args)
			continue
		}
		if args[i+1] != want {
			t.Errorf("%s = %q, want %q", flag, args[i+1], want)
		}
	}
	for _, present := range []string{"--verbose", "--no-session-persistence"} {
		if !slices.Contains(args, present) {
			t.Errorf("args have no %s: %q", present, args)
		}
	}
	for _, absent := range []string{"--json-schema", "--permission-mode", "--append-system-prompt", "--resume", "--bare"} {
		if slices.Contains(args, absent) {
			t.Errorf("args have %s: %q", absent, args)
		}
	}

	env := recordedEnv(t, record)
	for _, absent := range []string{"GH_TOKEN", "GIT_CONFIG_COUNT", "GIT_CONFIG_VALUE_0", "GIT_AUTHOR_NAME", "GH_CONFIG_DIR"} {
		if _, ok := env[absent]; ok {
			t.Errorf("the environment of the minimal run has %s", absent)
		}
	}
	if env["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] != "1" || env["ENABLE_CLAUDEAI_MCP_SERVERS"] != "false" {
		t.Error("the environment of the minimal run does not turn auto memory and the claude.ai MCP servers off")
	}

	// The utilization is not logged at info level. The check reads the
	// attributes of the records, not the text of the lines: the time can
	// hold the same digits.
	logs.requireNoText(t, "0.31", "0.61", "utilization", "fake stderr")
	if !logs.hasMessage("quota usage read") {
		t.Errorf("info logs do not say that the usage was read: %+v", logs.records)
	}
}

func TestReadQuota_RunsInAnEmptyDirectoryThatIsRemoved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-claude")
	listing := filepath.Join(dir, "listing")
	fixture, err := filepath.Abs(filepath.Join("testdata", "quota-run.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nls -A . > " + listing + " && pwd >> " + listing + "\ncat " + fixture + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := quiet(path).ReadQuota(context.Background()); err != nil {
		t.Fatalf("ReadQuota: %v", err)
	}
	data, err := os.ReadFile(listing)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 {
		t.Fatalf("the working directory was not empty: %q", lines)
	}
	if _, err := os.Stat(lines[0]); !os.IsNotExist(err) {
		t.Errorf("the working directory %q still exists after the run (err %v)", lines[0], err)
	}
}

func TestReadQuota_NotRead(t *testing.T) {
	tests := []struct {
		name       string
		fixture    string
		exitCode   int
		wantReason string
	}{
		{"no rate_limit_event", "no-quota.jsonl", 0, "no usage"},
		{"incomplete window", "incomplete-quota.jsonl", 0, "no usage"},
		{"a complete event, then an unreadable one", "quota-last-unreadable.jsonl", 0, "no usage"},
		{"a complete event, then one with a changed type", "quota-last-wrong-type.jsonl", 0, "no usage"},
		{"exit code 1", "quota-run.jsonl", 1, "exit code 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, _ := fakeCLI(t, tt.fixture, tt.exitCode)
			var logs bytes.Buffer
			c := ClaudeCode{Path: path, Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))}
			usage, err := c.ReadQuota(context.Background())
			if err == nil {
				t.Fatalf("ReadQuota = %+v, want an error", usage)
			}
			if q := quotaNotRead(t, err); !strings.Contains(q.Reason, tt.wantReason) {
				t.Errorf("Reason = %q, want it to contain %q", q.Reason, tt.wantReason)
			}
			if usage != (QuotaUsage{}) {
				t.Errorf("usage = %+v, want empty", usage)
			}
			if !strings.Contains(logs.String(), "quota usage not read") || strings.Contains(logs.String(), "utilization") {
				t.Errorf("info logs:\n%s", logs.String())
			}
		})
	}
}

func TestReadQuota_MissingExecutable(t *testing.T) {
	c := quiet(filepath.Join(t.TempDir(), "no-such-cli"))
	_, err := c.ReadQuota(context.Background())
	if q := quotaNotRead(t, err); !strings.Contains(q.Reason, "start") || q.Err == nil {
		t.Errorf("QuotaNotRead = %+v", q)
	}
}

func TestReadQuota_TimeLimitLeavesNoChild(t *testing.T) {
	path, childPID := neverEndingCLI(t, "")
	c := quiet(path)
	c.Grace = time.Second
	c.QuotaTimeLimit = time.Second

	start := time.Now()
	_, err := c.ReadQuota(context.Background())
	elapsed := time.Since(start)

	if q := quotaNotRead(t, err); !strings.Contains(q.Reason, "time limit") {
		t.Errorf("Reason = %q, want the time limit", q.Reason)
	}
	if elapsed > c.QuotaTimeLimit+c.Grace+2*time.Second {
		t.Errorf("ReadQuota took %v, want about the limit plus the grace period", elapsed)
	}
	if !processGone(t, childPID) {
		t.Error("the child of the fake CLI is still alive")
	}
}

func TestReadQuota_CancelIsReported(t *testing.T) {
	path, _ := fakeCLI(t, "quota-run.jsonl", 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := quiet(path).ReadQuota(ctx)
	if q := quotaNotRead(t, err); !strings.Contains(q.Reason, "stopped") && !strings.Contains(q.Reason, "start") {
		t.Errorf("Reason = %q", q.Reason)
	}
}
