package workflow

// This file handles an approved pull request: "ask for the merge decision"
// (risk/medium or risk/high, a Maintainer decides), and the steps in
// cumin/status/merging, which "start the merge" starts (after the review of
// the Reviewer with risk/low, or after the approval of a Maintainer). Each
// step is decided at a poll from the pull request on GitHub. It also
// handles the review of a Maintainer on a pull request that waits for the
// merge decision: "start the merge" (an approval, cumin-core merges) and
// "send back for changes" (a request for changes, the Implementer fixes).
// docs/ja/designs/poll.md, the topic on the merge step.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/notify"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
)

// DefaultMergeWait is how long cumin waits between two merges of one poll
// of a repository, so that GitHub updates the default branch in between.
const DefaultMergeWait = 5 * time.Second

// closeTimeLimit bounds the read and the close after a merge, which run
// even while cumin stops. It is shorter than the time that the stop waits
// for the requests that run (DefaultStopGrace), so that both end before
// the process does.
const closeTimeLimit = 10 * time.Second

func (s *Service) mergeWait() time.Duration {
	if s.MergeWait > 0 {
		return s.MergeWait
	}
	return DefaultMergeWait
}

// askMaintainerToMerge applies "ask for the merge decision": the
// label cumin/status/awaiting-merge-decision, then the request of the review
// of the Issue Owner, then one notification that links the pull request.
// issueOwnerLogin gives the login of the Issue Owner; it is read before the
// label changes, and a failed read is returned with nothing changed. A
// label change that fails is returned, without the review request and the
// notification: the issue keeps cumin/status/reviewing, and the next poll
// decides the same and notifies then. The review request changes no
// decision: without an Issue Owner login none is sent, and one that fails is
// only logged. The notification goes out in both cases.
func (s *Service) askMaintainerToMerge(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, token string, sub SubIssue, pr PullRequest, issueOwnerLogin func(ctx context.Context, token string) (string, error)) error {
	owner, repo := target.Repository.Owner, target.Repository.Name
	login, err := issueOwnerLogin(ctx, token)
	if err != nil {
		return fmt.Errorf(string(ActionAskForTheMergeDecision)+": read the Issue Owner login of issue #%d: %w", sub.Number, err)
	}
	labels := ReplaceStatusLabel(sub.Labels, LabelAwaitingMergeDecision)
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, sub.Number, labels); err != nil {
		return fmt.Errorf(string(ActionAskForTheMergeDecision)+": move issue #%d to awaiting-merge-decision: %w", sub.Number, err)
	}
	log.Info(string(ActionAskForTheMergeDecision)+": the merge waits for a Maintainer", "labels", labels, "pull_request", pr.Number)
	if login == "" {
		log.Info(string(ActionAskForTheMergeDecision)+": there is no Issue Owner login; the review of the Issue Owner is not requested", "pull_request", pr.Number)
	} else if err := s.GitHub.RequestReview(ctx, token, owner, repo, pr.Number, login); err != nil {
		log.Error(string(ActionAskForTheMergeDecision)+": the review of the Issue Owner was not requested; the notification still goes out", "pull_request", pr.Number, "error", err.Error())
	} else {
		log.Info(string(ActionAskForTheMergeDecision)+": requested the review of the Issue Owner", "pull_request", pr.Number, "reviewer", login)
	}
	s.notify(ctx, log.With("action", ActionAskForTheMergeDecision), settings != nil && settings.Settings.Notify.DiscordEnabled, notify.Notification{
		Action:     string(ActionAskForTheMergeDecision),
		Reason:     "the merge needs a decision",
		Repository: target.Repository.String(),
		Subject:    fmt.Sprintf("issue #%d", sub.Number),
		Link:       github.PullRequestURL(owner, repo, pr.Number),
	})
	return nil
}

// readMergingFacts adds the facts of the steps in cumin/status/merging to
// each sub-issue of the snapshot that needs them (MergeNeedsFacts): it reads
// that issue again, so that the decision judges on the reviews, the checks,
// and the labels of this moment. A failed read leaves the facts out, so
// nothing is decided for that issue in this poll.
func (s *Service) readMergingFacts(ctx context.Context, log *slog.Logger, token string, target Target, settings *RepositorySettings, snapshot *Snapshot) {
	for i := range snapshot.RequirementIssues {
		for j := range snapshot.RequirementIssues[i].SubIssues {
			sub := &snapshot.RequirementIssues[i].SubIssues[j]
			if !MergeNeedsFacts(*sub, snapshot.Running[sub.Number]) {
				continue
			}
			if read, ok, err := s.mergingNow(ctx, log.With("issue", sub.Number), token, target, settings, sub.Number); ok && err == nil {
				*sub = read
			}
		}
	}
}

// mergingNow reads one implementation issue again, and only that issue,
// with the facts that MergeEnd decides from: the account of the newest
// cumin/status/merging, the merged pull request when no open one is left,
// the login of the Reviewer App, the Maintainers among the people whose reviews
// decide, and the required checks.
//
// The error is the one of a read that failed. The bool is false when the
// issue is not an open issue in cumin/status/merging any more. Nothing is
// decided in both cases. A label that does not count ends the read, and
// one notification goes out.
func (s *Service) mergingNow(ctx context.Context, log *slog.Logger, token string, target Target, settings *RepositorySettings, number int) (SubIssue, bool, error) {
	owner, repo := target.Repository.Owner, target.Repository.Name
	failed := func(what string, err error) (SubIssue, bool, error) {
		log.Error("the merge: "+what+" was not read; the next poll decides", "error", err.Error())
		return SubIssue{}, false, err
	}
	read, err := s.GitHub.ReadSubIssue(ctx, token, owner, repo, number)
	if err != nil {
		return failed("the issue", err)
	}
	log.Debug("read the issue again", "issue", number, "rate_limit_cost", read.RateLimit.Cost, "rate_limit_remaining", read.RateLimit.Remaining)
	sub := toSubIssue(read.Issue)
	if !MergeNeedsFacts(sub, false) {
		log.Info("the issue is not in cumin/status/merging; nothing changes", "labels", sub.Labels)
		return sub, false, nil
	}
	actor, counts, err := s.readStatusActor(ctx, token, target, number, LabelMerging, false)
	if err != nil {
		return failed("the actor of the newest "+LabelMerging, err)
	}
	facts := &MergingFacts{StatusCounts: counts}
	sub.Merging = facts
	if !counts {
		s.tellStatusOfAnother(ctx, log, target, settings, number, LabelMerging, actor)
		return sub, true, nil
	}
	pr, ok := sub.LatestPullRequest()
	if !ok {
		// A merged pull request is no longer open. The links of the issue
		// tell whether GitHub merged it, also when the answer of the merge
		// got lost.
		linked, rate, err := s.GitHub.ReadLinkedPullRequests(ctx, token, owner, repo, number)
		if err != nil {
			return failed("the linked pull requests", err)
		}
		log.Debug("read the linked pull requests", "rate_limit_cost", rate.Cost, "rate_limit_remaining", rate.Remaining)
		// Only the newest linked pull request is the one of this merge. A
		// merged pull request of an earlier stay does not close the issue.
		var newest github.LinkedPullRequest
		for _, l := range linked {
			if l.Number > newest.Number {
				newest = l
			}
		}
		if newest.Merged {
			facts.Merged = newest.Number
		}
		return sub, true, nil
	}
	if s.Agents == nil {
		return failed("the login of the Reviewer App", errors.New("no agent service is configured"))
	}
	if facts.Reviewer, err = s.Agents.BotLogin(ctx, owner, config.RoleReviewer); err != nil {
		return failed("the login of the Reviewer App", err)
	}
	if facts.Maintainers, err = s.readMaintainers(ctx, token, target, DecidingReviewers(pr.Reviews)); err != nil {
		return failed("the permissions of the reviewers", err)
	}
	required, err := s.GitHub.RequiredChecks(ctx, token, owner, repo, read.DefaultBranch)
	if err != nil {
		return failed("the required checks", err)
	}
	facts.Required = toRequiredChecks(required)
	return sub, true, nil
}

// mergeEndAtPoll applies one step in cumin/status/merging that a poll
// decided (MergeEnd). sent counts the merges that this poll sent before
// for the repository.
func (s *Service) mergeEndAtPoll(ctx context.Context, log *slog.Logger, token string, target Target, snapshot Snapshot, settings *RepositorySettings, action Action, sent *int) error {
	owner, repo := target.Repository.Owner, target.Repository.Name
	switch a := action.(type) {
	case LeaveMerge:
		sub, _ := snapshot.SubIssue(a.Number)
		labels := ReplaceStatusLabel(sub.Labels, LabelChecking)
		if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, a.Number, labels); err != nil {
			return fmt.Errorf("go back to the checks: move issue #%d to %s: %w", a.Number, LabelChecking, err)
		}
		log.Info("go back to the checks: the conditions of the merge do not hold; no merge is sent", "issue", a.Number, "labels", labels)
		return nil
	case CloseMergedIssue:
		sub, _ := snapshot.SubIssue(a.Number)
		return s.closeMergedIssue(ctx, log.With("issue", a.Number), token, target, settings, sub, a.PullRequest)
	case SendMerge:
		sub, _ := snapshot.SubIssue(a.Number)
		return s.sendMerge(ctx, log.With("issue", a.Number), token, target, settings, sub, snapshot.DefaultBranch, a, sent)
	case ResolveMergeConflict:
		sub, _ := snapshot.SubIssue(a.Number)
		pr, ok := sub.LatestPullRequest()
		if !ok || pr.Number != a.PullRequest {
			return fmt.Errorf("request a conflict resolution: pull request #%d of issue #%d is not in the snapshot", a.PullRequest, a.Number)
		}
		return s.resolveConflict(ctx, log.With("issue", a.Number), token, target, settings, sub, pr, snapshot.DefaultBranch)
	}
	return fmt.Errorf("unknown step of the merge %T", action)
}

// sendMerge sends the merge of the approved head commit with merge_method
// of the repository settings. The facts of this poll decided it
// (MergeEnd), so the conditions of the merge hold at this merge.
//
// The issue keeps cumin/status/merging after the merge: the next poll reads
// the merged pull request and closes the issue when GitHub did not. The
// same holds for an answer that got lost, so no merge is sent twice. A
// temporary failure is an error of the poll, and the next poll decides
// again. GitHub refuses the merge for three kinds of reasons:
//
//   - The base branch was modified: the issue keeps its label with no
//     comment, and the next poll sends the merge again.
//   - A conflict: "request a conflict resolution" (resolveConflict). While
//     cumin stops after its runs, nothing changes instead: the issue keeps
//     cumin/status/merging, and the next start of cumin decides again.
//   - Any other lasting reason: "stop the merge".
//
// Before the second and each later merge of one poll, cumin waits
// (mergeWait), so that GitHub updates the default branch in between.
func (s *Service) sendMerge(ctx context.Context, log *slog.Logger, token string, target Target, settings *RepositorySettings, sub SubIssue, defaultBranch string, a SendMerge, sent *int) error {
	owner, repo := target.Repository.Owner, target.Repository.Name
	pr, ok := sub.LatestPullRequest()
	if !ok || pr.Number != a.PullRequest || pr.HeadCommit != a.HeadCommit {
		return fmt.Errorf("the merge: pull request #%d of issue #%d is not in the snapshot", a.PullRequest, a.Number)
	}
	if *sent > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.mergeWait()):
		}
	}
	*sent++
	method := string(settings.Settings.MergeMethod)
	err := s.GitHub.MergePullRequest(ctx, token, owner, repo, pr.Number, pr.HeadCommit, method)
	if err == nil {
		log.Info("merged the pull request", "pull_request", pr.Number, "merge_method", method, "head_commit", pr.HeadCommit)
		return nil
	}
	log.Warn("the pull request was not merged", "pull_request", pr.Number, "error", err.Error())
	stopIssue := func(reason string) error {
		labels := ReplaceStatusLabel(sub.Labels, LabelAwaitingDecision)
		if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, a.Number, labels); err != nil {
			return fmt.Errorf("stop the merge for a Maintainer: move issue #%d to %s: %w", a.Number, LabelAwaitingDecision, err)
		}
		s.stopForMaintainer(ctx, log, target, settings, stop{
			action: ActionStopTheMerge, issue: a.Number, labelDone: true, reason: reason,
			comment: StopNote(ActionStopTheMerge, reason, pr.Number, false),
		})
		return nil
	}
	switch {
	case temporary(err) != nil:
		return err
	case errors.Is(err, github.ErrBaseModified):
		log.Info("the base branch was modified; the next poll sends the merge again", "pull_request", pr.Number)
		return nil
	case errors.Is(err, github.ErrConflict):
		return s.resolveConflict(ctx, log, token, target, settings, sub, pr, defaultBranch)
	case errors.Is(err, github.ErrHeadMoved):
		return stopIssue(MergeHeadMovedReason(pr.Number))
	}
	// The answer of an earlier merge can be lost while the pull request
	// still read as open. Then GitHub refuses a merged pull request, and
	// the next poll closes the issue.
	if merged, readErr := s.GitHub.PullRequestIsMerged(ctx, token, owner, repo, pr.Number); readErr == nil && merged {
		log.Info("the pull request is merged already; the next poll closes the issue", "pull_request", pr.Number)
		return nil
	}
	return stopIssue(MergeFailedReason(pr.Number, statusAnswer(err)))
}

// closeMergedIssue applies "close the merged issue": it reads the issue,
// and closes it as completed when it is open. cumin closes an issue only
// here, inside cumin/status/merging, so an issue that a Maintainer reopens
// later stays open.
//
// The merge cannot be undone, so the read and the close still run while
// cumin stops, with their own time limit. A temporary failure, and a call
// that the time limit ended, are errors of the poll: the next poll reads
// the merged pull request again. Any other failure is "stop the merge".
func (s *Service) closeMergedIssue(ctx context.Context, log *slog.Logger, token string, target Target, settings *RepositorySettings, sub SubIssue, pullRequest int) error {
	owner, repo := target.Repository.Owner, target.Repository.Name
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), closeTimeLimit)
	defer cancel()
	// failed leaves the issue for the next poll, or stops it for a Maintainer.
	failed := func(err error) error {
		if github.IsTemporary(err) || errors.Is(closeCtx.Err(), context.DeadlineExceeded) {
			return err
		}
		reason := CloseFailedReason(pullRequest, statusAnswer(err))
		s.stopForMaintainer(ctx, log, target, settings, stop{
			action: ActionStopTheMerge, issue: sub.Number, labels: sub.Labels, reason: reason,
			comment: StopNote(ActionStopTheMerge, reason, pullRequest, false),
		})
		return nil
	}
	open, err := s.GitHub.IssueIsOpen(closeCtx, token, owner, repo, sub.Number)
	if err != nil {
		log.Warn("close the merged issue: the issue was not read", "error", err.Error())
		return failed(err)
	}
	if !open {
		log.Info("close the merged issue: GitHub closed the issue", "pull_request", pullRequest)
		return nil
	}
	if err := s.GitHub.CloseIssueAsCompleted(closeCtx, token, owner, repo, sub.Number); err != nil {
		log.Warn("close the merged issue: the issue was not closed", "error", err.Error())
		return failed(err)
	}
	log.Info("close the merged issue: closed the issue that GitHub left open after the merge", "pull_request", pullRequest)
	return nil
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

// resolveConflict applies "request a conflict resolution" from
// cumin/status/merging: GitHub reports a conflict with the default branch
// at the poll (ResolveMergeConflict, no merge is sent), or it refused the
// merge for one. The label becomes cumin/status/implementing first
// (principle 3), then the Implementer resolves the conflict in the session
// of its last run, on the branch of the pull request. The end of that run
// is the end of any Implementer run: the checks and the review follow, and
// the new head needs a new approval.
//
// A login of the Issue Owner that cannot be read and a label that does not
// change are errors of the poll: nothing is requested, the issue keeps
// cumin/status/merging, and the next poll decides again. The same holds
// while the start waits for its permit (permitStart).
func (s *Service) resolveConflict(ctx context.Context, log *slog.Logger, token string, target Target, settings *RepositorySettings, sub SubIssue, pr PullRequest, defaultBranch string) error {
	owner, repo := target.Repository.Owner, target.Repository.Name
	repository := target.Repository.String()
	permit, ok := s.permitStart(ctx, log, "conflict resolution", config.RoleImplementer, target, sub.Number)
	if !ok {
		return nil
	}
	login, err := s.readIssueOwnerLogin(ctx, token, target, sub.Number)
	if err != nil {
		return fmt.Errorf("request a conflict resolution: read the Issue Owner login of issue #%d: %w", sub.Number, err)
	}
	if err := s.startStay(repository, sub.Number, true); err != nil {
		return fmt.Errorf("request a conflict resolution: keep the start of the stay of issue #%d in cumin/status/implementing: %w", sub.Number, err)
	}
	labels := ReplaceStatusLabel(sub.Labels, LabelImplementing)
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, sub.Number, labels); err != nil {
		return fmt.Errorf("request a conflict resolution: move issue #%d back to the Implementer: %w", sub.Number, err)
	}
	log.Info("the merge conflicts; the issue goes back to the Implementer", "pull_request", pr.Number, "labels", labels)
	branch := pr.HeadBranch
	err = s.goImplementer(ctx, target, settings, sub.Number, implementerRequest{
		action: ActionRequestAConflictResolution, kind: "conflict resolution", branch: branch, pullRequest: pr.Number,
		sessionID:       s.State.Issue(repository, sub.Number).SessionID,
		issueOwnerLogin: login, permit: permit,
		text: func(workDir string) string {
			return ConflictResolutionRequestText(repository, sub.Number, pr.Number, branch, workDir, defaultBranch)
		},
	})
	if err != nil {
		return fmt.Errorf("request a conflict resolution for issue #%d: %w", sub.Number, err)
	}
	return nil
}

// resolveConflictAtPoll applies "request a conflict resolution": the pull
// request of an issue in
// cumin/status/checking or in cumin/status/awaiting-merge-decision
// conflicts with the default branch. The
// label becomes cumin/status/implementing first (principle 3), then the
// Implementer resolves the conflict in the session of its last run, on the
// branch of the pull request: the same request as after a merge that
// conflicts (resolveConflict). The end of that run is the end of any
// Implementer run: the check of the pull request after the Implementer ends
// verifies it, and a head that did not change stops the issue for a
// Maintainer with the action "stop the implementation".
//
// The request does not count toward the limit of check fix requests: a
// conflict comes from the merge of another pull request, not from a
// mistake of the Implementer (issue-states.md, the rows while an issue
// waits for the checks). A login of the Issue Owner that cannot be read and a
// label that does not change are errors of the poll: nothing is requested,
// the issue keeps its label, and the next poll tries again.
func (s *Service) resolveConflictAtPoll(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, a ResolveConflict) error {
	owner, repo := target.Repository.Owner, target.Repository.Name
	repository := target.Repository.String()
	log := s.logger().With("repository", repository, "issue", a.Number, "pull_request", a.PullRequest)
	sub, ok := snapshot.SubIssue(a.Number)
	if !ok {
		return fmt.Errorf(string(ActionRequestAConflictResolution)+": issue #%d is not in the snapshot", a.Number)
	}
	pr, ok := sub.LatestPullRequest()
	if !ok || pr.Number != a.PullRequest {
		return fmt.Errorf(string(ActionRequestAConflictResolution)+": pull request #%d of issue #%d is not in the snapshot", a.PullRequest, a.Number)
	}
	permit, ok := s.permitStart(ctx, log, "conflict resolution", config.RoleImplementer, target, a.Number)
	if !ok {
		return nil
	}
	issueOwnerLogin, err := s.readIssueOwnerLogin(ctx, token, target, a.Number)
	if err != nil {
		return fmt.Errorf(string(ActionRequestAConflictResolution)+": read the Issue Owner login of issue #%d: %w", a.Number, err)
	}
	if err := s.startStay(repository, a.Number, true); err != nil {
		return fmt.Errorf(string(ActionRequestAConflictResolution)+": keep the start of the stay of issue #%d in cumin/status/implementing: %w", a.Number, err)
	}
	labels := ReplaceStatusLabel(sub.Labels, LabelImplementing)
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, a.Number, labels); err != nil {
		return fmt.Errorf(string(ActionRequestAConflictResolution)+": move issue #%d back to the Implementer: %w", a.Number, err)
	}
	log.Info(string(ActionRequestAConflictResolution)+": the pull request conflicts with the default branch; the issue goes back to the Implementer", "labels", labels)
	branch := pr.HeadBranch
	if branch == "" {
		branch = BranchName(sub.Number, sub.Title)
	}
	defaultBranch := snapshot.DefaultBranch
	err = s.goImplementer(ctx, target, settings, a.Number, implementerRequest{
		action: ActionRequestAConflictResolution, kind: "conflict resolution", branch: branch, pullRequest: pr.Number,
		sessionID:       s.State.Issue(repository, a.Number).SessionID,
		issueOwnerLogin: issueOwnerLogin, permit: permit,
		text: func(workDir string) string {
			return ConflictResolutionRequestText(repository, a.Number, pr.Number, branch, workDir, defaultBranch)
		},
	})
	if err != nil {
		return fmt.Errorf(string(ActionRequestAConflictResolution)+": request the conflict resolution for issue #%d: %w", a.Number, err)
	}
	return nil
}

// mergeMaintainerApproval applies "start the merge" after the approval of a
// Maintainer to a candidate: it reads the permission of each person whose
// review decides, keeps the Maintainers (IsMaintainer), and checks that the
// latest review of a Maintainer is APPROVED on the head commit
// (MaintainerApproved). Then the risk label and the
// required checks decide as for "start the merge" after the review of the
// Reviewer (DecideMerge); the risk does not choose between a Maintainer and
// cumin here, because a Maintainer already decided. Only
// the label changes, to cumin/status/merging: the merge is sent inside that
// state (MergeEnd).
//
// The first value says whether "start the merge" acted (the label change or
// a stop), for "tell that cumin waits". A
// permission that cannot be read is an error of the poll; the next poll
// tries again. Checks that do not pass leave the issue as it is: "start the
// merge" applies again when they pass.
func (s *Service) mergeMaintainerApproval(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, required []RequiredCheck, a MergeMaintainerApproval) (bool, error) {
	log := s.logger().With("repository", target.Repository.String(), "issue", a.Number)
	sub, ok := snapshot.SubIssue(a.Number)
	if !ok {
		return false, fmt.Errorf(string(ActionStartTheMerge)+": issue #%d is not in the snapshot", a.Number)
	}
	pr, ok := sub.LatestPullRequest()
	if !ok || pr.Number != a.PullRequest {
		return false, fmt.Errorf(string(ActionStartTheMerge)+": pull request #%d of issue #%d is not in the snapshot", a.PullRequest, a.Number)
	}
	maintainers, err := s.readMaintainers(ctx, token, target, a.Reviewers)
	if err != nil {
		return false, fmt.Errorf(string(ActionStartTheMerge)+": issue #%d: %w", a.Number, err)
	}
	if !MaintainerApproved(pr.Reviews, pr.HeadCommit, maintainers) {
		log.Debug(string(ActionStartTheMerge)+": no approval of a Maintainer on the head commit", "pull_request", pr.Number)
		return false, nil
	}
	decision := DecideMerge(sub.Labels, required, pr.Checks)
	switch decision {
	case MergeChecksNotPassed:
		log.Info(string(ActionStartTheMerge)+": a Maintainer approved; the merge waits for the required checks", "pull_request", pr.Number)
		return false, nil
	case MergeNoRiskLabel, MergeTwoRiskLabels:
		reason := RiskLabelReason(decision)
		s.stopForMaintainer(ctx, log, target, settings, stop{
			action: ActionStartTheMerge, issue: a.Number, labels: sub.Labels, reason: reason,
			comment: StopNote(ActionStartTheMerge, reason, pr.Number, false),
		})
		return true, nil
	}
	labels := ReplaceStatusLabel(sub.Labels, LabelMerging)
	if err := s.GitHub.SetIssueLabels(ctx, token, target.Repository.Owner, target.Repository.Name, a.Number, labels); err != nil {
		return false, fmt.Errorf(string(ActionStartTheMerge)+": move issue #%d to %s: %w", a.Number, LabelMerging, err)
	}
	log.Info(string(ActionStartTheMerge)+": a Maintainer approved the head commit", "pull_request", pr.Number, "head_commit", pr.HeadCommit, "labels", labels)
	return true, nil
}

// readMaintainers reads the permission of each person whose review decides, and
// returns who of them is a Maintainer (IsMaintainer).
func (s *Service) readMaintainers(ctx context.Context, token string, target Target, reviewers []string) (map[string]bool, error) {
	maintainers := map[string]bool{}
	for _, login := range reviewers {
		permission, userType, err := s.GitHub.RepositoryPermission(ctx, token, target.Repository.Owner, target.Repository.Name, login)
		if err != nil {
			return nil, err
		}
		maintainers[login] = IsMaintainer(permission, userType)
	}
	return maintainers, nil
}

// fixMaintainerReview applies "send back for changes" to a candidate: it
// reads the permission of each person whose review decides, keeps the
// Maintainers (IsMaintainer), and checks that
// the latest review of a Maintainer is CHANGES_REQUESTED on the head commit,
// newer than the last cumin/status/awaiting-merge-decision of the issue
// (MaintainerRequestedChanges). Then the label becomes cumin/status/implementing
// first (principle 3), and the Implementer addresses that review in the
// session of its last run, on the branch of the pull request. The end of
// that run is the end of any Implementer run: the check of the pull request
// after the Implementer ends verifies it, then the checks and the review
// follow, and "ask for the merge decision" asks a Maintainer again. The
// review counts its rounds again from the last APPROVE of the Reviewer
// (issue-states.md, the rounds).
//
// The first value says whether "send back for changes" acted, for "tell
// that cumin waits". The
// second value says that a Maintainer requested changes, and that the request
// waits for the permit of its start (permitStart): the poll then keeps the
// issue, so that no conflict resolution moves the head commit away from the
// review of the Maintainer. A permission or a login of the Issue Owner that
// cannot be read, and a label that
// does not change, are errors of the poll: nothing is requested, the issue
// keeps cumin/status/awaiting-merge-decision, and the next poll tries again.
func (s *Service) fixMaintainerReview(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, a FixMaintainerReview) (acted, waits bool, err error) {
	owner, repo := target.Repository.Owner, target.Repository.Name
	repository := target.Repository.String()
	log := s.logger().With("repository", repository, "issue", a.Number)
	sub, ok := snapshot.SubIssue(a.Number)
	if !ok {
		return false, false, fmt.Errorf(string(ActionSendBackForChanges)+": issue #%d is not in the snapshot", a.Number)
	}
	pr, ok := sub.LatestPullRequest()
	if !ok || pr.Number != a.PullRequest {
		return false, false, fmt.Errorf(string(ActionSendBackForChanges)+": pull request #%d of issue #%d is not in the snapshot", a.PullRequest, a.Number)
	}
	maintainers, err := s.readMaintainers(ctx, token, target, a.Reviewers)
	if err != nil {
		return false, false, fmt.Errorf(string(ActionSendBackForChanges)+": issue #%d: %w", a.Number, err)
	}
	review, ok := MaintainerRequestedChanges(pr.Reviews, pr.HeadCommit, maintainers, sub.AwaitingMergeDecisionAt)
	if !ok {
		log.Debug(string(ActionSendBackForChanges)+": no new request for changes of a Maintainer on the head commit", "pull_request", pr.Number)
		return false, false, nil
	}
	permit, ok := s.permitStart(ctx, log, "owner review fix", config.RoleImplementer, target, a.Number)
	if !ok {
		return false, true, nil
	}
	issueOwnerLogin, err := s.readIssueOwnerLogin(ctx, token, target, a.Number)
	if err != nil {
		return false, false, fmt.Errorf(string(ActionSendBackForChanges)+": read the Issue Owner login of issue #%d: %w", a.Number, err)
	}
	if err := s.startStay(repository, a.Number, false); err != nil {
		return false, false, fmt.Errorf(string(ActionSendBackForChanges)+": keep the start of the stay of issue #%d in cumin/status/implementing: %w", a.Number, err)
	}
	labels := ReplaceStatusLabel(sub.Labels, LabelImplementing)
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, a.Number, labels); err != nil {
		return false, false, fmt.Errorf(string(ActionSendBackForChanges)+": move issue #%d back to the Implementer: %w", a.Number, err)
	}
	log.Info(string(ActionSendBackForChanges)+": a Maintainer requested changes; the issue goes back to the Implementer",
		"pull_request", pr.Number, "review", review.URL, "labels", labels)
	branch := pr.HeadBranch
	err = s.goImplementer(ctx, target, settings, a.Number, implementerRequest{
		action: ActionSendBackForChanges, kind: "owner review fix", branch: branch, pullRequest: pr.Number,
		sessionID:       s.State.Issue(repository, a.Number).SessionID,
		issueOwnerLogin: issueOwnerLogin, permit: permit,
		text: func(workDir string) string {
			return MaintainerReviewFixRequestText(repository, a.Number, pr.Number, branch, workDir, review.URL)
		},
	})
	if err != nil {
		return true, false, fmt.Errorf(string(ActionSendBackForChanges)+": request the fix for issue #%d: %w", a.Number, err)
	}
	return true, false, nil
}
