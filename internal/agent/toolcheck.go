package agent

// This file runs one tool check of a repository: a command that proves
// that a tool of the agents works on the Host (requirements/cumin-core.md,
// the topic on the tool checks). The caller decides what a failed check
// stops. docs/ja/designs/agent-run.md (the topic on the tool checks)
// records the decisions.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// RunToolCheck runs the command of one tool check in dir, and waits for
// its end for at most limit. The check passes, and the result is nil, only
// when the exit code is 0 inside the limit. A command that is not found, a
// time limit that passed, and any other exit code give an error that says
// why.
//
// The process gets the Host variables of an agent (baseEnvironment), so it
// finds a tool on the same PATH as an agent does, and no token of a GitHub
// App. No shell reads the command. dir is created when it is missing: the
// first poll of a new Host comes before the first worktree.
func RunToolCheck(ctx context.Context, dir string, command []string, limit time.Duration) error {
	if len(command) == 0 || command[0] == "" {
		return errors.New("the check has no command")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create the working directory: %w", err)
	}
	runCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()

	// os/exec looks for the program on the PATH of cumin, which is the
	// PATH of baseEnvironment (os/exec: Command, LookPath).
	cmd := exec.CommandContext(runCtx, command[0], command[1:]...)
	cmd.Dir = dir
	cmd.Env = baseEnvironment()
	// Stdin, Stdout, and Stderr are nil: the process reads from and writes
	// to the null device (os/exec), so no output of a check reaches a log.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// A check has nothing to save, so the time limit kills its process
	// group at once (os/exec: Cmd.Cancel).
	cmd.Cancel = func() error { return signalGroup(cmd.Process.Pid, syscall.SIGKILL) }
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", command[0], err)
	}
	err := cmd.Wait()
	// Nothing of the check stays behind in its process group.
	_ = signalGroup(cmd.Process.Pid, syscall.SIGKILL)

	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return nil
	case ctx.Err() != nil:
		return fmt.Errorf("the check was stopped: %w", ctx.Err())
	case runCtx.Err() != nil:
		return fmt.Errorf("the time limit of %s passed", limit)
	case errors.As(err, &exitErr):
		return fmt.Errorf("exit code %d", exitErr.ExitCode())
	}
	return fmt.Errorf("wait: %w", err)
}
