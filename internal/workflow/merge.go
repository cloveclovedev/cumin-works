package workflow

// This file handles an approved pull request: I6 (risk/low, cumin-core
// merges), I7 (risk/medium or risk/high, the Owner decides), and the merge
// step that I6 shares with I12. It also handles the review of the Owner on
// a pull request that waits for the merge decision: I12 (an approval,
// cumin-core merges) and I13 (a request for changes, the Implementer fixes).
// docs/ja/designs/poll.md, the topic on the merge step.

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
// of the pull request. It reads the issue again for the labels of the
// issue now and for the default branch, whose rules name the required
// checks. A read that fails is logged, and the issue keeps its label.
// ownerLogin is the login of the Owner that the Reviewer run holds; a
// conflict resolution carries it, because no later poll acts on an issue
// in cumin/status/reviewing after a read that failed here.
func (s *Service) afterApproval(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, number int, pr PullRequest, ownerLogin string) {
	owner, repo := target.Repository.Owner, target.Repository.Name
	token, err := target.Token(ctx)
	if err != nil {
		log.Error("I6: no token; the issue keeps its label", "error", err.Error())
		return
	}
	read, err := s.GitHub.ReadSubIssue(ctx, token, owner, repo, number)
	if err != nil {
		log.Error("I6: the issue was not read again; the issue keeps its label", "error", err.Error())
		return
	}
	log.Debug("read the issue again", "issue", number, "rate_limit_cost", read.RateLimit.Cost, "rate_limit_remaining", read.RateLimit.Remaining)
	sub := toSubIssue(read.Issue)
	required, err := s.GitHub.RequiredChecks(ctx, token, owner, repo, read.DefaultBranch)
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
		s.mergeStep(ctx, log, target, settings, RowI6, sub, pr, read.DefaultBranch,
			func(string) (string, error) { return ownerLogin, nil })
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
// pushed after the approval is never merged. A conflict goes back to the
// Implementer (resolveConflict). Every other failure stops the issue for
// the Owner with one sentence: a head that moved, any other answer of
// GitHub, and a close that failed. ownerLogin gives the login of the Owner
// for the conflict resolution request; it is called only on a conflict.
func (s *Service) mergeStep(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, row string, sub SubIssue, pr PullRequest, defaultBranch string, ownerLogin func(token string) (string, error)) {
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
			s.resolveConflict(ctx, log, target, settings, row, sub, pr, defaultBranch, ownerLogin)
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

// resolveConflict handles a merge that conflicts with the default branch
// (the failure column of I6): the label becomes cumin/status/implementing
// first (principle 3), then the Implementer resolves the conflict in the
// session of its last run, on the branch of the pull request. The end of
// that run is the end of any Implementer run: I2 verifies it, then the
// checks and the review follow. The review counts its rounds again from
// the last APPROVE (issue-states.md, the rounds), and the new head needs a
// new approval.
//
// The login of the Owner comes from ownerLogin, before the label changes.
// After the approval of the Reviewer (I6) it is the login that the
// Reviewer run holds, so nothing can fail. After the approval of the Owner
// (I12) it is a read; when the read fails, the issue keeps
// cumin/status/awaiting-owner-review, and I12 applies again at the next
// poll.
func (s *Service) resolveConflict(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, row string, sub SubIssue, pr PullRequest, defaultBranch string, ownerLogin func(token string) (string, error)) {
	owner, repo := target.Repository.Owner, target.Repository.Name
	token, err := target.Token(ctx)
	if err != nil {
		log.Error(row+": no token; the issue keeps its label", "error", err.Error())
		return
	}
	login, err := ownerLogin(token)
	if err != nil {
		log.Error(row+": the login of the Owner was not read; the issue keeps its label", "error", err.Error())
		return
	}
	labels := ReplaceStatusLabel(sub.Labels, LabelImplementing)
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, sub.Number, labels); err != nil {
		log.Error(row+": the label was not changed; nothing is requested", "error", err.Error())
		return
	}
	log.Info(row+": the merge conflicts; the issue goes back to the Implementer", "pull_request", pr.Number, "labels", labels)
	repository := target.Repository.String()
	branch := pr.HeadBranch
	s.runImplementer(ctx, target, settings, sub.Number, implementerRequest{
		row: row, kind: "conflict resolution", branch: branch, pullRequest: pr.Number,
		sessionID:    s.State.Issue(repository, sub.Number).SessionID,
		ownerLogin:   login,
		conflictHead: pr.HeadCommit,
		text: func(workDir string) string {
			return ConflictResolutionRequestText(repository, sub.Number, pr.Number, branch, workDir, defaultBranch)
		},
	})
}

// resolveConflictBeforeChecks applies I14: the pull request of an issue in
// cumin/status/awaiting-checks conflicts with the default branch. The
// label becomes cumin/status/implementing first (principle 3), then the
// Implementer resolves the conflict in the session of its last run, on the
// branch of the pull request: the same request as after a merge that
// conflicts (resolveConflict). The end of that run is the end of any
// Implementer run: I2 verifies it, and a head that did not change stops
// the issue for the Owner with the row I14.
//
// The request does not count toward the limit of check fix requests: a
// conflict comes from the merge of another pull request, not from a
// mistake of the Implementer (issue-states.md, the rows while an issue
// waits for the checks). A login of the Owner that cannot be read and a
// label that does not change are errors of the poll: nothing is requested,
// the issue keeps cumin/status/awaiting-checks, and the next poll tries
// again.
func (s *Service) resolveConflictBeforeChecks(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, a ResolveConflict) error {
	owner, repo := target.Repository.Owner, target.Repository.Name
	repository := target.Repository.String()
	log := s.logger().With("repository", repository, "issue", a.Number, "pull_request", a.PullRequest)
	sub, ok := snapshot.SubIssue(a.Number)
	if !ok {
		return fmt.Errorf("I14: issue #%d is not in the snapshot", a.Number)
	}
	pr, ok := sub.LatestPullRequest()
	if !ok || pr.Number != a.PullRequest {
		return fmt.Errorf("I14: pull request #%d of issue #%d is not in the snapshot", a.PullRequest, a.Number)
	}
	ownerLogin, err := s.readOwnerLogin(ctx, token, target, a.Number)
	if err != nil {
		return fmt.Errorf("I14: read the login of the Owner of issue #%d: %w", a.Number, err)
	}
	labels := ReplaceStatusLabel(sub.Labels, LabelImplementing)
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, a.Number, labels); err != nil {
		return fmt.Errorf("I14: move issue #%d back to the Implementer: %w", a.Number, err)
	}
	log.Info("I14: the pull request conflicts with the default branch; the issue goes back to the Implementer", "labels", labels)
	branch := pr.HeadBranch
	if branch == "" {
		branch = BranchName(sub.Number, sub.Title)
	}
	defaultBranch := snapshot.DefaultBranch
	err = s.goImplementer(ctx, target, settings, a.Number, implementerRequest{
		row: RowI14, kind: "conflict resolution", branch: branch, pullRequest: pr.Number,
		sessionID:    s.State.Issue(repository, a.Number).SessionID,
		ownerLogin:   ownerLogin,
		conflictHead: pr.HeadCommit,
		text: func(workDir string) string {
			return ConflictResolutionRequestText(repository, a.Number, pr.Number, branch, workDir, defaultBranch)
		},
	})
	if err != nil {
		return fmt.Errorf("I14: request the conflict resolution for issue #%d: %w", a.Number, err)
	}
	return nil
}

// mergeOwnerApproval applies I12 to a candidate: it reads the permission of
// each person whose review decides, keeps the Owners (IsOwner), and checks
// that the latest review of an Owner is APPROVED on the head commit
// (OwnerApproved). Then the risk label and the required checks decide as
// for I6 (DecideMerge); the risk does not choose between the Owner and
// cumin here, because the Owner already decided. The merge step runs in its
// own goroutine, marked as running, so that the next poll does not take the
// same issue again while the step waits for GitHub to close it.
//
// The first value says whether I12 acted (a merge or a stop), for Q4. A
// permission that cannot be read is an error of the poll; the next poll
// tries again. Checks that do not pass leave the issue as it is: I12
// applies again when they pass.
func (s *Service) mergeOwnerApproval(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, required []RequiredCheck, a MergeOwnerApproval) (bool, error) {
	log := s.logger().With("repository", target.Repository.String(), "issue", a.Number)
	sub, ok := snapshot.SubIssue(a.Number)
	if !ok {
		return false, fmt.Errorf("I12: issue #%d is not in the snapshot", a.Number)
	}
	pr, ok := sub.LatestPullRequest()
	if !ok || pr.Number != a.PullRequest {
		return false, fmt.Errorf("I12: pull request #%d of issue #%d is not in the snapshot", a.PullRequest, a.Number)
	}
	owners, err := s.readOwners(ctx, token, target, a.Reviewers)
	if err != nil {
		return false, fmt.Errorf("I12: issue #%d: %w", a.Number, err)
	}
	if !OwnerApproved(pr.Reviews, pr.HeadCommit, owners) {
		log.Debug("I12: no approval of an Owner on the head commit", "pull_request", pr.Number)
		return false, nil
	}
	decision := DecideMerge(sub.Labels, required, pr.Checks)
	switch decision {
	case MergeChecksNotPassed:
		log.Info("I12: the Owner approved; the merge waits for the required checks", "pull_request", pr.Number)
		return false, nil
	case MergeNoRiskLabel, MergeTwoRiskLabels:
		reason := RiskLabelReason(decision)
		s.stopForOwner(ctx, log, target, settings, stop{
			row: RowI12, issue: a.Number, labels: sub.Labels, reason: reason,
			comment: StopNote(RowI12, reason, pr.Number, false),
		})
		return true, nil
	}
	log.Info("I12: the Owner approved the head commit", "pull_request", pr.Number, "head_commit", pr.HeadCommit)
	done := s.markInProgress(ctx, target.Repository.String(), a.Number)
	s.running.Add(1)
	go func() {
		defer s.running.Done()
		defer done()
		s.mergeStep(ctx, log, target, settings, RowI12, sub, pr, snapshot.DefaultBranch,
			func(token string) (string, error) { return s.readOwnerLogin(ctx, token, target, a.Number) })
	}()
	return true, nil
}

// readOwners reads the permission of each person whose review decides, and
// returns who of them is an Owner (IsOwner).
func (s *Service) readOwners(ctx context.Context, token string, target Target, reviewers []string) (map[string]bool, error) {
	owners := map[string]bool{}
	for _, login := range reviewers {
		permission, userType, err := s.GitHub.RepositoryPermission(ctx, token, target.Repository.Owner, target.Repository.Name, login)
		if err != nil {
			return nil, err
		}
		owners[login] = IsOwner(permission, userType)
	}
	return owners, nil
}

// fixOwnerReview applies I13 to a candidate: it reads the permission of each
// person whose review decides, keeps the Owners (IsOwner), and checks that
// the latest review of an Owner is CHANGES_REQUESTED on the head commit
// (OwnerRequestedChanges). Then the label becomes cumin/status/implementing
// first (principle 3), and the Implementer addresses that review in the
// session of its last run, on the branch of the pull request. The end of
// that run is the end of any Implementer run: I2 verifies it, then the
// checks and the review follow, and I7 asks the Owner again. The review
// counts its rounds again from the last APPROVE of the Reviewer
// (issue-states.md, the rounds).
//
// The first value says whether I13 acted (the send-back), for Q4. A
// permission or a login of the Owner that cannot be read, and a label that
// does not change, are errors of the poll: nothing is requested, the issue
// keeps cumin/status/awaiting-owner-review, and the next poll tries again.
func (s *Service) fixOwnerReview(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, a FixOwnerReview) (bool, error) {
	owner, repo := target.Repository.Owner, target.Repository.Name
	repository := target.Repository.String()
	log := s.logger().With("repository", repository, "issue", a.Number)
	sub, ok := snapshot.SubIssue(a.Number)
	if !ok {
		return false, fmt.Errorf("I13: issue #%d is not in the snapshot", a.Number)
	}
	pr, ok := sub.LatestPullRequest()
	if !ok || pr.Number != a.PullRequest {
		return false, fmt.Errorf("I13: pull request #%d of issue #%d is not in the snapshot", a.PullRequest, a.Number)
	}
	owners, err := s.readOwners(ctx, token, target, a.Reviewers)
	if err != nil {
		return false, fmt.Errorf("I13: issue #%d: %w", a.Number, err)
	}
	review, ok := OwnerRequestedChanges(pr.Reviews, pr.HeadCommit, owners)
	if !ok {
		log.Debug("I13: no request for changes of an Owner on the head commit", "pull_request", pr.Number)
		return false, nil
	}
	ownerLogin, err := s.readOwnerLogin(ctx, token, target, a.Number)
	if err != nil {
		return false, fmt.Errorf("I13: read the login of the Owner of issue #%d: %w", a.Number, err)
	}
	labels := ReplaceStatusLabel(sub.Labels, LabelImplementing)
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, a.Number, labels); err != nil {
		return false, fmt.Errorf("I13: move issue #%d back to the Implementer: %w", a.Number, err)
	}
	log.Info("I13: the Owner requested changes; the issue goes back to the Implementer",
		"pull_request", pr.Number, "review", review.URL, "labels", labels)
	branch := pr.HeadBranch
	err = s.goImplementer(ctx, target, settings, a.Number, implementerRequest{
		row: "I13", kind: "owner review fix", branch: branch, pullRequest: pr.Number,
		sessionID:  s.State.Issue(repository, a.Number).SessionID,
		ownerLogin: ownerLogin,
		text: func(workDir string) string {
			return OwnerReviewFixRequestText(repository, a.Number, pr.Number, branch, workDir, review.URL)
		},
	})
	if err != nil {
		return true, fmt.Errorf("I13: request the fix for issue #%d: %w", a.Number, err)
	}
	return true, nil
}
