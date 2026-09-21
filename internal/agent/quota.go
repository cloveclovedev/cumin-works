package agent

// This file reads the quota usage before a start (R1, I1). Claude Code
// reports the usage only in a run that calls the model
// (measured-constraints.md rows 29, 30), so cumin runs the smallest
// possible request and reads its rate_limit_event. docs/ja/designs/
// cumin-core.md ("起動前の使用率の確認") records the decision. The rest
// of cumin gets only QuotaUsage or an error.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"
)

// The minimal run. These are constants, not settings: the settings table
// of the requirement does not list them, and they change only when Claude
// Code changes.
const (
	// quotaModel is the smallest model. The alias comes from the official
	// list of model aliases ("Model configuration").
	quotaModel = "haiku"
	// quotaSystemPrompt replaces the whole system prompt, so that the
	// request is as small as possible (CLI reference: --system-prompt).
	quotaSystemPrompt = "Reply with one word."
	quotaPrompt       = "Reply with the one word OK."
	// defaultQuotaTimeLimit bounds the run. It ends in one or two
	// seconds (row 30); a much longer run means that something is wrong.
	defaultQuotaTimeLimit = 60 * time.Second
)

// quotaArgs is the command line of the minimal run. No tools, no result
// schema, no permission mode: the model only answers.
func (c ClaudeCode) quotaArgs() []string {
	return []string{
		"-p", quotaPrompt,
		"--system-prompt", quotaSystemPrompt,
		"--model", quotaModel,
		// No tool at all (CLI reference: --tools "").
		"--tools", "",
		// No user-level settings, as in every run (row 6e).
		"--setting-sources", "project",
		"--output-format", "stream-json",
		"--verbose",
	}
}

func (c ClaudeCode) quotaTimeLimit() time.Duration {
	if c.QuotaTimeLimit > 0 {
		return c.QuotaTimeLimit
	}
	return defaultQuotaTimeLimit
}

// ReadQuota reads the quota usage with one minimal run of Claude Code.
// The run happens in an empty temporary directory, so that no CLAUDE.md
// is read, with the base environment and no credentials. An error is a
// *QuotaNotRead that says why; the caller then does not start an agent.
func (c ClaudeCode) ReadQuota(ctx context.Context) (QuotaUsage, error) {
	log := c.logger().With("run", "quota")
	dir, err := os.MkdirTemp("", "cumin-quota-")
	if err != nil {
		return QuotaUsage{}, c.quotaFail(log, "create the working directory", err)
	}
	defer os.RemoveAll(dir)

	ex := c.execute(ctx, log, execution{
		dir:   dir,
		env:   baseEnvironment(),
		args:  c.quotaArgs(),
		limit: c.quotaTimeLimit(),
	})
	var exitErr *exec.ExitError
	switch {
	case ex.startErr != nil:
		return QuotaUsage{}, c.quotaFail(log, "start "+c.Path, ex.startErr)
	case ex.limitErr != nil && ctx.Err() == nil:
		return QuotaUsage{}, c.quotaFail(log, fmt.Sprintf("the time limit of %s passed", c.quotaTimeLimit()), ex.limitErr)
	case ex.limitErr != nil:
		return QuotaUsage{}, c.quotaFail(log, "the run was stopped", ctx.Err())
	case ex.waitErr != nil && errors.As(ex.waitErr, &exitErr):
		return QuotaUsage{}, c.quotaFail(log, fmt.Sprintf("exit code %d", exitErr.ExitCode()), nil)
	case ex.waitErr != nil:
		return QuotaUsage{}, c.quotaFail(log, "wait", ex.waitErr)
	case ex.s.quota == nil:
		// No rate_limit_event, or one whose shape cumin does not know.
		return QuotaUsage{}, c.quotaFail(log, "the run reported no usage in a rate_limit_event that cumin can read", nil)
	}
	log.Info("quota usage read")
	return *ex.s.quota, nil
}

func (c ClaudeCode) quotaFail(log *slog.Logger, reason string, err error) error {
	log.Info("quota usage not read", "reason", reason)
	return &QuotaNotRead{Reason: reason, Err: err}
}
