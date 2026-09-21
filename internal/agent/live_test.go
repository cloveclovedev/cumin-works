package agent

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/roles"
)

// TestLive_AgentRun is the live check of the last requirement of #7. It
// runs real Claude Code three times, without GitHub, in a throwaway
// repository. It runs only with CUMIN_LIVE=1, because it uses quota.
// docs/ja/development/agent-live-check.md says how to run it.
//
// The log of this test holds no quota number and no session ID.
func TestLive_AgentRun(t *testing.T) {
	if os.Getenv("CUMIN_LIVE") != "1" {
		t.Skip("set CUMIN_LIVE=1 to run the live check; it uses quota")
	}
	path := os.Getenv("CUMIN_CLAUDE_PATH")
	if path == "" {
		var err error
		if path, err = exec.LookPath("claude"); err != nil {
			t.Fatalf("claude is not on PATH: %v (set CUMIN_CLAUDE_PATH)", err)
		}
	}
	r := newRemote(t)
	w := Workspace{Root: filepath.Join(t.TempDir(), "work"), Logger: testLogger(t)}
	c := Checkout{Owner: "example-org", Repo: "example-repo", Issue: 1, Role: config.RoleImplementer, Branch: "cumin/1-live-check"}
	dir, err := w.Prepare(context.Background(), r.path, c)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	instruction, err := roles.Instruction(config.RoleImplementer)
	if err != nil {
		t.Fatal(err)
	}
	cli := ClaudeCode{Path: path, Logger: testLogger(t)}
	// The agent does not reach GitHub in this check, so the credentials
	// are placeholders.
	base := Request{Role: config.RoleImplementer, RoleInstruction: instruction, WorkDir: dir, TimeLimit: 5 * time.Minute, Credentials: testCredentials}

	// Run 1: a new session that reads a file and returns done.
	req := base
	req.Text = "Read README.md in the current directory. Do not change any file. " +
		"Then finish with the result done, and a summary of one sentence that names the file that you read."
	run1, err := cli.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}
	if run1.Result.Result != ResultDone || run1.Result.Summary == "" {
		t.Errorf("run 1: result = %+v, want done with a summary", run1.Result)
	}
	if run1.SessionID == "" {
		t.Error("run 1: no session ID")
	}
	if !run1.QuotaRead {
		t.Error("run 1: no quota usage was read")
	} else if now := time.Now(); !run1.Quota.FiveHour.ResetsAt.After(now) || !run1.Quota.Weekly.ResetsAt.After(now) {
		t.Error("run 1: a reset time is not in the future")
	}
	t.Logf("run 1: result %s, session ID read: %v, quota read: %v, reset times in the future: %v",
		run1.Result.Result, run1.SessionID != "", run1.QuotaRead,
		run1.QuotaRead && run1.Quota.FiveHour.ResetsAt.After(time.Now()) && run1.Quota.Weekly.ResetsAt.After(time.Now()))

	// Run 2: the same session. The agent must remember run 1.
	req = base
	req.SessionID = run1.SessionID
	req.Text = "Which file did you read in your earlier turn? " +
		"Finish with the result done, and a summary of one sentence that names that file."
	run2, err := cli.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if run2.SessionID != run1.SessionID {
		t.Error("run 2: the session ID differs from run 1")
	}
	if run2.Result.Result != ResultDone || !strings.Contains(run2.Result.Summary, "README") {
		t.Errorf("run 2: result = %+v, want done with a summary that names README.md", run2.Result)
	}
	t.Logf("run 2: result %s, same session as run 1: %v, summary names README.md: %v",
		run2.Result.Result, run2.SessionID == run1.SessionID, strings.Contains(run2.Result.Summary, "README"))

	// Run 3: a run over the time limit. No process of its group may stay.
	// The command creates a marker file first, so that the test knows
	// that the child ran before the stop.
	req = base
	req.Text = "Run exactly this shell command with the Bash tool, and wait for it to finish: " +
		"`touch started && sleep 600`. Then finish with the result done."
	req.TimeLimit = 20 * time.Second
	start := time.Now()
	_, err = cli.Run(context.Background(), req)
	elapsed := time.Since(start)
	var end *AbnormalEnd
	if !errors.As(err, &end) || end.Kind != EndTimeLimit {
		t.Fatalf("run 3: err = %v, want an abnormal end of kind %s", err, EndTimeLimit)
	}
	if end.PID == 0 {
		t.Fatal("run 3: no process ID")
	}
	if _, err := os.Stat(filepath.Join(dir, "started")); err != nil {
		t.Errorf("run 3: the agent did not run the command before the stop: %v", err)
	}
	if _, err := syscall.Getpgid(end.PID); err == nil {
		t.Errorf("run 3: the process group leader is still alive")
	}
	// pgrep exits with 1 when no process matches. Any other failure
	// means that the check did not run.
	out, pgrepErr := exec.Command("pgrep", "-g", strconv.Itoa(end.PID)).Output()
	var pgrepExit *exec.ExitError
	switch {
	case pgrepErr == nil:
		t.Errorf("run 3: processes are still in the group of the CLI:\n%s", out)
	case errors.As(pgrepErr, &pgrepExit) && pgrepExit.ExitCode() == 1:
		// No process in the group.
	default:
		t.Fatalf("run 3: pgrep did not run: %v", pgrepErr)
	}
	t.Logf("run 3: kind %s after %s, command started: %v, group empty: %v",
		end.Kind, elapsed.Round(time.Second), fileExists(filepath.Join(dir, "started")), pgrepErr != nil)
}

// TestLive_ReadQuota is the live check of #67: one real minimal run reads
// the quota usage. It runs only with CUMIN_LIVE=1, because it uses a
// small amount of quota. The log holds the field names of the init event
// and of rate_limit_info, and the value of status, but no number.
func TestLive_ReadQuota(t *testing.T) {
	if os.Getenv("CUMIN_LIVE") != "1" {
		t.Skip("set CUMIN_LIVE=1 to run the live check; it uses quota")
	}
	path := os.Getenv("CUMIN_CLAUDE_PATH")
	if path == "" {
		var err error
		if path, err = exec.LookPath("claude"); err != nil {
			t.Fatalf("claude is not on PATH: %v (set CUMIN_CLAUDE_PATH)", err)
		}
	}
	cli := ClaudeCode{Path: path, Logger: recordLogger(t)}
	start := time.Now()
	usage, err := cli.ReadQuota(context.Background())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ReadQuota: %v", err)
	}
	now := time.Now()
	if !usage.FiveHour.ResetsAt.After(now) || !usage.Weekly.ResetsAt.After(now) {
		t.Error("a reset time is not in the future")
	}
	if usage.FiveHour.Utilization < 0 || usage.FiveHour.Utilization > 1 || usage.Weekly.Utilization < 0 || usage.Weekly.Utilization > 1 {
		t.Error("a utilization is outside 0 to 1")
	}
	t.Logf("quota read: %v, reset times in the future: %v, utilizations in 0 to 1: %v, took %s",
		err == nil, usage.FiveHour.ResetsAt.After(now) && usage.Weekly.ResetsAt.After(now),
		usage.FiveHour.Utilization >= 0 && usage.FiveHour.Utilization <= 1 && usage.Weekly.Utilization >= 0 && usage.Weekly.Utilization <= 1,
		elapsed.Round(time.Second))
}

// recordLogger logs at debug level into the test log, so that the field
// names of the events appear, but replaces every value that is a number
// or an output of the CLI, so that the log can be copied into a record.
func recordLogger(t *testing.T) *slog.Logger {
	omitted := map[string]bool{"five_hour": true, "weekly": true, "five_hour_resets_at": true, "weekly_resets_at": true, "text": true, "session_id": true, "work_dir": true}
	return slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if omitted[a.Key] {
				return slog.String(a.Key, "<omitted>")
			}
			return a
		},
	}))
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// testLogger logs at info level into the test log, without the debug
// lines that hold quota numbers. The session ID and the work directory
// are redacted, so that the log can be copied into a record.
func testLogger(t *testing.T) *slog.Logger {
	return slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{
		Level: slog.LevelInfo,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == "session_id" || a.Key == "work_dir" {
				return slog.String(a.Key, "<redacted>")
			}
			return a
		},
	}))
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
