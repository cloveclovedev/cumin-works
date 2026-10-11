package workflow

// This file notifies once when the tool checks of a repository begin to
// fail ("stop agent starts" of issue-states.md for the tool checks;
// requirements/cumin-core.md, the table of notifications). A Maintainer
// learns from it that cumin starts no agent for the repository, and does
// not get the same message at every poll.
//
// docs/ja/designs/cumin-core.md, the topic on the notification of the tool
// checks.

import (
	"context"
	"log/slog"
	"strings"

	"github.com/cloveclovedev/cumin-works/internal/notify"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
)

// tellFailedToolChecks notifies once when the failed tool checks of the
// repository change from none to some, and names each failed check. A poll
// where every check passes forgets the mark, so the next failure notifies
// again. The mark lives in memory: a restart may send one more. It is
// taken back when the channel fails, so that the next poll tries again.
// The setting notify.discord.enabled of the repository decides (enabled):
// the checks and the stop belong to that repository only.
func (s *Service) tellFailedToolChecks(ctx context.Context, log *slog.Logger, target Target, enabled bool, failed []string) {
	repository := target.Repository.String()
	s.toolCheckMu.Lock()
	if len(failed) == 0 {
		delete(s.toolChecksTold, repository)
		s.toolCheckMu.Unlock()
		return
	}
	tell := !s.toolChecksTold[repository]
	if s.toolChecksTold == nil {
		s.toolChecksTold = map[string]bool{}
	}
	s.toolChecksTold[repository] = true
	s.toolCheckMu.Unlock()
	if !tell {
		return
	}
	sent := s.notify(ctx, log, enabled, notify.Notification{
		Action:     string(ActionStopAgentStarts),
		Reason:     failedToolChecksReason(failed),
		Repository: repository,
		Link:       github.RepositoryURL(target.Repository.Owner, target.Repository.Name),
	})
	if !sent {
		s.toolCheckMu.Lock()
		delete(s.toolChecksTold, repository)
		s.toolCheckMu.Unlock()
	}
}

// failedToolChecksReason is the reason of the notification: each failed
// check as its command, in the order of the settings file. The checks come
// last, so that the limit of a reason (internal/notify) cuts only the end
// of their list.
func failedToolChecksReason(failed []string) string {
	return "A tool check of this repository fails. cumin starts no agent for this repository until every check passes. " +
		"The agent runs that are going on end as usual. Failed checks: " + strings.Join(failed, "; ")
}
