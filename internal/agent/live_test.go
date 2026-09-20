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
	// #8 turns auto memory off in the adapter. Until then, by hand.
	t.Setenv("CLAUDE_CODE_DISABLE_AUTO_MEMORY", "1")

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
	base := Request{Role: config.RoleImplementer, RoleInstruction: instruction, WorkDir: dir, TimeLimit: 5 * time.Minute}

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
	req = base
	req.Text = "Run the shell command `sleep 600` with the Bash tool, and wait for it to finish. " +
		"Then finish with the result done."
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
	if _, err := syscall.Getpgid(end.PID); err == nil {
		t.Errorf("run 3: the process group leader is still alive")
	}
	out, pgrepErr := exec.Command("pgrep", "-g", itoa(end.PID)).Output()
	if len(strings.TrimSpace(string(out))) > 0 {
		t.Errorf("run 3: processes are still in the group of the CLI:\n%s", out)
	}
	t.Logf("run 3: kind %s after %s, group empty: %v (pgrep exit: %v)", end.Kind, elapsed.Round(time.Second), len(strings.TrimSpace(string(out))) == 0, pgrepErr)
}

func itoa(n int) string { return strconv.Itoa(n) }

// testLogger logs at info level into the test log, without the debug
// lines that hold quota numbers.
func testLogger(t *testing.T) *slog.Logger {
	return slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
