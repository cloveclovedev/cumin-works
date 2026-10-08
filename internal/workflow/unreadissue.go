package workflow

// An issue over a limit of a read is left out of the poll, with its
// requirement issue (readSnapshot). Nothing moves for that requirement
// issue until a person makes the issue smaller, so a notification has to
// say so.
//
// This file remembers which issues cumin already told about, and notifies
// once for each issue and limit. docs/ja/designs/poll.md, the topic on the
// notification of an issue that cumin cannot read in full.

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cloveclovedev/cumin-works/internal/notify"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
)

// toldUnreadIssue names one issue over one limit that cumin already told
// about. Another limit of the same issue is another thing to tell.
type toldUnreadIssue struct {
	issue int
	limit string
}

// notifyUnreadIssues notifies about each issue of this poll that cumin
// cannot read in full, once for each issue and limit (cumin-core.md, "Issues
// that cumin cannot read in full"). An issue that this poll read in full is
// forgotten, so a new pass of a limit is told again.
//
// What was told lives in memory only, as the count of the failed polls does
// (pollfailure.go). A restart of cumin loses it, which can only bring one
// more notification for each issue.
func (s *Service) notifyUnreadIssues(ctx context.Context, log *slog.Logger, target Target, enabled bool, unread []github.UnreadIssue) {
	key := repositoryKey(target.Repository)
	s.failureMu.Lock()
	told := s.unreadTold[key]
	// Only the issues that are still over the same limit stay told.
	still := map[toldUnreadIssue]bool{}
	var tell []github.UnreadIssue
	for _, issue := range unread {
		k := toldUnreadIssue{issue: issue.Issue, limit: issue.Limit}
		if told[k] {
			still[k] = true
		} else if !still[k] {
			still[k] = true
			tell = append(tell, issue)
		}
	}
	if s.unreadTold == nil {
		s.unreadTold = map[string]map[toldUnreadIssue]bool{}
	}
	if len(still) == 0 {
		delete(s.unreadTold, key)
	} else {
		s.unreadTold[key] = still
	}
	s.failureMu.Unlock()

	owner, repo := target.Repository.Owner, target.Repository.Name
	for _, issue := range tell {
		log := log.With("issue", issue.Issue, "requirement_issue", issue.Requirement, "limit", issue.Limit)
		log.Warn("cumin tells once about the issue that it cannot read in full")
		// The row of the table of notifications of cumin-core.md gives
		// this notification no name, so Action stays empty.
		s.notify(ctx, log, enabled, notify.Notification{
			Reason: fmt.Sprintf("cumin cannot read issue #%d in full: it has %s. cumin decides nothing for the requirement issue #%d until a poll reads the issue in full.",
				issue.Issue, issue.Limit, issue.Requirement),
			Repository: target.Repository.String(),
			Subject:    fmt.Sprintf("issue #%d", issue.Issue),
			Link:       github.IssueURL(owner, repo, issue.Issue),
		})
	}
}
