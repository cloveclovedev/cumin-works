package workflow

// This file runs the tool checks of a repository at its poll and keeps the
// failed ones for the one check before a start (permitStart): while a
// check of a repository fails, no agent starts for that repository
// (issue-states.md, "stop agent starts" and "resume agent starts";
// requirements/cumin-core.md, the topic on the tool checks).

import (
	"context"
	"log/slog"
	"strings"

	"github.com/cloveclovedev/cumin-works/internal/agent"
)

// runToolChecks runs every tool check of the repository in the order of
// its settings file, and keeps the failed ones until the next poll of the
// repository. It runs before the poll decides a start of an agent. The
// result lives in memory: after a restart the first poll runs the checks
// before any start.
func (s *Service) runToolChecks(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings) {
	var failed []string
	for _, check := range settings.Settings.ToolChecks {
		name := strings.Join(check.Command, " ")
		if err := agent.RunToolCheck(ctx, s.Workspace.Root, check.Command, check.TimeLimit); err != nil {
			log.Info(string(ActionStopAgentStarts)+": a tool check of the repository fails", "check", name, "reason", err.Error())
			failed = append(failed, name)
		}
	}
	repository := target.Repository.String()
	s.toolCheckMu.Lock()
	stopped := len(s.failedChecks[repository]) > 0
	if s.failedChecks == nil {
		s.failedChecks = map[string][]string{}
	}
	s.failedChecks[repository] = failed
	s.toolCheckMu.Unlock()
	if stopped && len(failed) == 0 {
		log.Info(string(ActionResumeAgentStarts) + ": every tool check of the repository passes")
	}
}

// failedToolChecks returns the tool checks of the repository that failed
// at its last poll, each as its command.
func (s *Service) failedToolChecks(repository string) []string {
	s.toolCheckMu.Lock()
	defer s.toolCheckMu.Unlock()
	return s.failedChecks[repository]
}
