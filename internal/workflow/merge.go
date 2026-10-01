package workflow

// This file handles an approved pull request: I6 (risk/low, cumin-core
// merges), I7 (risk/medium or risk/high, the Owner decides), and the merge
// step that I6 shares with I12. docs/ja/designs/poll.md, the topic on the
// merge step.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/notify"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
)

// DefaultCloseWait is how long the merge step waits for GitHub to close
// the implementation issue through the closing link, before cumin reads
// it. GitHub closed a linked sub-issue within seconds (measured row 60);
// since 2026-09-30 it does not always close it (#276, C5).
const DefaultCloseWait = 10 * time.Second

// closeTimeLimit bounds the read and the close after a merge, which run
// even while cumin stops. It is shorter than the time that the stop waits
// for the requests that run (DefaultStopGrace), so that both end before
// the process does.
const closeTimeLimit = 10 * time.Second

func (s *Service) closeWait() time.Duration {
	if s.CloseWait > 0 {
		return s.CloseWait
	}
	return DefaultCloseWait
}

// afterApproval applies I6 or I7 when the Reviewer approved the head commit
// of the pull request. It reads the snapshot again for the labels of the
// issue now and for the default branch, whose rules name the required
// checks. A read that fails is logged, and the issue keeps its label.
func (s *Service) afterApproval(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, number int, pr PullRequest) {
	owner, repo := target.Repository.Owner, target.Repository.Name
	token, err := target.Token(ctx)
	if err != nil {
		log.Error("I6: no token; the issue keeps its label", "error", err.Error())
		return
	}
	read, err := s.GitHub.ReadSnapshot(ctx, token, owner, repo)
	if err != nil {
		log.Error("I6: the snapshot was not read again; the issue keeps its label", "error", err.Error())
		return
	}
	snapshot := toSnapshot(read)
	sub, ok := snapshot.SubIssue(number)
	if !ok {
		log.Error("I6: the issue is not in the snapshot")
		return
	}
	required, err := s.GitHub.RequiredChecks(ctx, token, owner, repo, snapshot.DefaultBranch)
	if err != nil {
		log.Error("I6: the required checks were not read; the issue keeps its label", "error", err.Error())
		return
	}
	// The checks come from this read, not from the read that found the
	// review: a check can run again in between. A pull request that is gone
	// or whose head moved is treated as checks that do not pass.
	approved := pr
	found := false
	for _, now := range sub.PullRequests {
		if now.Number == approved.Number {
			pr, found = now, true
		}
	}
	decision := MergeChecksNotPassed
	if found && pr.HeadCommit == approved.HeadCommit {
		decision = DecideMerge(sub.Labels, toRequiredChecks(required), pr.Checks)
	}
	log.Info("I6: decided on the approved pull request", "decision", decision.String(), "pull_request", pr.Number)
	switch decision {
	case MergeNow:
		s.mergeStep(ctx, log, target, settings, RowI6, sub, pr)
	case MergeAskOwner:
		s.askOwnerToMerge(ctx, log, target, settings, token, sub, pr)
	case MergeChecksNotPassed:
		labels := ReplaceStatusLabel(sub.Labels, LabelAwaitingChecks)
		if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, number, labels); err != nil {
			log.Error("I6: the label was not changed", "error", err.Error())
			return
		}
		log.Info("I6: a required check does not pass on the approved commit; the issue waits for the checks again", "labels", labels)
	default:
		reason := RiskLabelReason(decision)
		s.stopForOwner(ctx, log, target, settings, stop{
			row: RowI6, issue: number, labels: sub.Labels, reason: reason,
			comment: StopNote(RowI6, reason, pr.Number, false),
		})
	}
}

// askOwnerToMerge applies I7: the label cumin/status/awaiting-owner-review,
// then one notification that links the pull request. A label that fails
// does not hold back the notification, because the end of the Reviewer run
// happens once, and the Owner must still learn that the pull request
// waits.
func (s *Service) askOwnerToMerge(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, token string, sub SubIssue, pr PullRequest) {
	owner, repo := target.Repository.Owner, target.Repository.Name
	labels := ReplaceStatusLabel(sub.Labels, LabelAwaitingOwnerReview)
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, sub.Number, labels); err != nil {
		log.Error("I7: the label was not changed", "error", err.Error())
	} else {
		log.Info("I7: the merge waits for the Owner", "labels", labels, "pull_request", pr.Number)
	}
	s.notifyOwner(ctx, log.With("row", RowI7), settings != nil && settings.Settings.Notify.DiscordEnabled, notify.Notification{
		Row:        RowI7,
		Reason:     "the merge needs a decision",
		Repository: target.Repository.String(),
		Subject:    fmt.Sprintf("issue #%d", sub.Number),
		Link:       github.PullRequestURL(owner, repo, pr.Number),
	})
}

// mergeStep merges the pull request at its head commit with merge_method of
// the repository settings, then closes the implementation issue once when
// GitHub did not: it waits a short time, reads the issue, and closes it as
// completed when it is open. It never closes the issue at a later poll, so
// an issue that the Owner reopens stays open. row is I6 or I12, for the
// log and the stop step.
//
// The sha of the merge is the approved head commit, so a commit that was
// pushed after the approval is never merged. Every failure stops the issue
// for the Owner with one sentence: a conflict, a head that moved, any other
// answer of GitHub, and a close that failed.
func (s *Service) mergeStep(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, row string, sub SubIssue, pr PullRequest) {
	owner, repo := target.Repository.Owner, target.Repository.Name
	stopIssue := func(reason string) {
		s.stopForOwner(ctx, log, target, settings, stop{
			row: row, issue: sub.Number, labels: sub.Labels, reason: reason,
			comment: StopNote(row, reason, pr.Number, false),
		})
	}
	token, err := target.Token(ctx)
	if err != nil {
		log.Error(row+": no token; the issue keeps its label", "error", err.Error())
		return
	}
	method := string(settings.Settings.MergeMethod)
	if err := s.GitHub.MergePullRequest(ctx, token, owner, repo, pr.Number, pr.HeadCommit, method); err != nil {
		log.Warn(row+": the pull request was not merged", "pull_request", pr.Number, "error", err.Error())
		switch {
		case errors.Is(err, github.ErrConflict):
			stopIssue(MergeConflictReason(pr.Number))
		case errors.Is(err, github.ErrHeadMoved):
			stopIssue(MergeHeadMovedReason(pr.Number))
		default:
			stopIssue(MergeFailedReason(pr.Number, statusAnswer(err)))
		}
		return
	}
	log.Info(row+": merged the pull request", "pull_request", pr.Number, "merge_method", method, "head_commit", pr.HeadCommit)

	// The merge cannot be undone, and no later poll comes back to this
	// issue: its pull request is no longer open. So a stop of cumin ends
	// the wait early, and the close still runs, with its own time limit.
	select {
	case <-ctx.Done():
		log.Info(row + ": cumin is stopping; the issue is checked without the wait")
	case <-time.After(s.closeWait()):
	}
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeLimit)
	defer cancel()
	open, err := s.GitHub.IssueIsOpen(closeCtx, token, owner, repo, sub.Number)
	if err != nil {
		log.Warn(row+": the issue was not read after the merge", "error", err.Error())
		stopIssue(CloseFailedReason(pr.Number, statusAnswer(err)))
		return
	}
	if !open {
		log.Info(row + ": GitHub closed the issue")
		return
	}
	if err := s.GitHub.CloseIssueAsCompleted(closeCtx, token, owner, repo, sub.Number); err != nil {
		log.Warn(row+": the issue was not closed after the merge", "error", err.Error())
		stopIssue(CloseFailedReason(pr.Number, statusAnswer(err)))
		return
	}
	log.Info(row + ": closed the issue that GitHub left open after the merge")
}

// statusAnswer is the answer of GitHub in an error of the client: the
// status and the message, or the whole error when there is no status.
func statusAnswer(err error) string {
	var status *github.StatusError
	if errors.As(err, &status) {
		return fmt.Sprintf("status %d: %s", status.Status, status.Message)
	}
	return err.Error()
}
