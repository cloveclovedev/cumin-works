package workflow

// This file starts the Reviewer and handles the end of its run: I3 (the
// required checks passed, so the Reviewer reviews the head commit), the
// check of the review on GitHub after done, and I10 (the Reviewer returned
// blocked). docs/ja/designs/poll.md, the topic on the Reviewer request.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/agent"
	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/notify"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
)

// reviewerRequest is one request to the Reviewer: the pull request, the
// head commit that the work directory holds, the round, and the session
// that it resumes.
type reviewerRequest struct {
	review ReviewRequest
	// reviewer is the login of the Reviewer App, "<slug>[bot]". The check
	// after the run reads its latest review.
	reviewer string
	// readyAt is the last cumin/status/ready of the issue: the start of the
	// count of the rounds, which the end of the run counts again.
	readyAt time.Time
	// sessionID resumes that session. Empty starts a new session.
	sessionID string
	// ownerLogin is the login of the Owner for the facts of the request,
	// read before the label changed. Empty says that there is none. The
	// requests that follow in the same run (the review fix of I5, the
	// explanation of the cause of I8) carry the same login.
	ownerLogin string
}

// startReview applies I3: every required check passed on the head commit of
// the pull request. cumin reads the round from GitHub first, then changes
// the label to cumin/status/reviewing, then starts the Reviewer; a read
// that fails changes nothing, and the next poll tries again (principle 3).
//
// Round 1 starts a new session. Round 2 and later resume the session of
// the last Reviewer run of the issue, and name the commit of the last
// review, so that the Reviewer checks the fixes since then
// (issue-states.md, the sessions of an agent; agents/reviewer.md, the scope
// of each round).
func (s *Service) startReview(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, a StartReview) error {
	owner, repo := target.Repository.Owner, target.Repository.Name
	repository := target.Repository.String()
	if s.Agents == nil {
		return errors.New("I3: no agent service is configured")
	}
	sub, ok := snapshot.SubIssue(a.Number)
	if !ok {
		return fmt.Errorf("I3: issue #%d is not in the snapshot", a.Number)
	}
	pr, ok := sub.LatestPullRequest()
	if !ok || pr.Number != a.PullRequest {
		return fmt.Errorf("I3: pull request #%d of issue #%d is not in the snapshot", a.PullRequest, a.Number)
	}
	reviewer, err := s.Agents.BotLogin(ctx, owner, config.RoleReviewer)
	if err != nil {
		return fmt.Errorf("I3: read the login of the Reviewer App: %w", err)
	}
	// The last cumin/status/ready of the issue starts the count of the
	// rounds. The query of the label times reads one issue when it gets
	// the number of an implementation issue.
	times, rate, err := s.GitHub.ReadLabelTimes(ctx, token, owner, repo, a.Number)
	if err != nil {
		return fmt.Errorf("I3: read the label times of issue #%d: %w", a.Number, err)
	}
	s.logger().Debug("I3: read the label times", "repository", repository, "issue", a.Number,
		"rate_limit_cost", rate.Cost, "rate_limit_remaining", rate.Remaining)
	readyAt := times[a.Number][LabelReady]
	ownerLogin, err := s.readOwnerLogin(ctx, token, target, a.Number)
	if err != nil {
		return fmt.Errorf("I3: read the login of the Owner of issue #%d: %w", a.Number, err)
	}
	round := ReviewRounds(pr.Reviews, reviewer, readyAt) + 1
	// An approval of the head commit leaves no diff to name.
	approved := LastApprovedCommit(pr.Reviews, reviewer)
	if approved == pr.HeadCommit {
		approved = ""
	}
	req := reviewerRequest{
		review: ReviewRequest{
			Repository:   repository,
			Issue:        a.Number,
			PullRequest:  pr.Number,
			HeadCommit:   pr.HeadCommit,
			Round:        round,
			Limit:        settings.Settings.MaxReviewRounds,
			Approved:     approved,
			LastReviewed: LastReviewedCommit(pr.Reviews, reviewer, readyAt),
		},
		reviewer:   reviewer,
		readyAt:    readyAt,
		ownerLogin: ownerLogin,
	}
	if round > 1 {
		req.sessionID = s.State.Issue(repository, a.Number).ReviewerSessionID
	}

	labels := LabelsAfterReview(sub.Labels)
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, a.Number, labels); err != nil {
		return fmt.Errorf("I3: move issue #%d to the review: %w", a.Number, err)
	}
	s.logger().Info("I3: the pull request is ready for review",
		"repository", repository, "issue", a.Number, "pull_request", a.PullRequest,
		"round", round, "labels", labels)
	done := s.markInProgress(ctx, repository, a.Number)
	s.running.Add(1)
	go func() {
		defer s.running.Done()
		defer done()
		s.runReviewer(ctx, target, settings, a.Number, req)
	}()
	return nil
}

// runReviewer prepares the Reviewer worktree at the head commit and runs
// one review request to its end.
//
// An abnormal end starts the same request once more, in the same work
// directory and in a new session, as for the Implementer; after the second
// one, the issue goes to the Owner with the row I3. A blocked result is
// I10: the issue goes to the Owner at once, without a retry. After done,
// cumin reads the latest review of the Reviewer on GitHub; when it is not
// on the head commit with APPROVE or REQUEST_CHANGES, cumin asks once more
// in the same session, and the second miss stops the issue with the row
// I5 (the Reviewer requirement, completion).
//
// The step after the run (the check of the review, and the stop after
// blocked) is kept after a temporary failure of GitHub, and a later poll
// runs it again from its read (keptstep.go).
func (s *Service) runReviewer(ctx context.Context, target Target, settings *RepositorySettings, number int, req reviewerRequest) {
	log := s.logger().With("repository", target.Repository.String(), "issue", number,
		"role", config.RoleReviewer, "pull_request", req.review.PullRequest)
	role := settings.Settings.Roles[config.RoleReviewer]
	checkout := agent.Checkout{
		Owner:  target.Repository.Owner,
		Repo:   target.Repository.Name,
		Issue:  number,
		Role:   config.RoleReviewer,
		Commit: req.review.HeadCommit,
	}
	// Each round reviews a new head commit, and Prepare reuses a worktree as
	// it is; so the worktree of an earlier round goes. The Reviewer writes
	// nothing there, so nothing is lost (agent-run.md, the work directory).
	if err := s.Workspace.Remove(ctx, checkout); err != nil {
		log.Error("I3: the worktree of an earlier round was not removed", "error", err.Error())
		return
	}
	workDir, err := s.Workspace.Prepare(ctx, target.RemoteURL, checkout)
	if err != nil {
		log.Error("I3: the work directory was not prepared", "error", err.Error())
		return
	}
	req.review.WorkDir = workDir
	log.Info("I3: requested the review", "round", req.review.Round, "limit", req.review.Limit,
		"head_commit", req.review.HeadCommit, "resumed", req.sessionID != "")
	request := agent.StartRequest{
		Owner:        target.Repository.Owner,
		Repo:         target.Repository.Name,
		Role:         config.RoleReviewer,
		RiskCriteria: settings.RiskCriteria,
		Facts:        agent.Facts{IssueNumber: number, IssueKind: agent.IssueKindImplementation, OwnerLogin: req.ownerLogin, ProtectedPaths: settings.ProtectedPaths},
		Text:         ReviewRequestText(req.review),
		WorkDir:      workDir,
		Settings:     &role,
		SessionID:    req.sessionID,
	}
	s.reviewRuns(ctx, log, target, settings, number, req, request, false)
}

// reviewRuns runs one request to the Reviewer to its end, and then the step
// after the run. missed says that the request asks once more, after a run
// that left no review on the head commit. See runReviewer.
func (s *Service) reviewRuns(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, number int, req reviewerRequest, request agent.StartRequest, missed bool) {
	key := inProgressKey{repository: target.Repository.String(), issue: number}
	var firstKind agent.EndKind
	attempt := 1
	for {
		run, err := s.Agents.Start(ctx, request)
		var abnormal *agent.AbnormalEnd
		switch {
		case errors.As(err, &abnormal):
			log.Info("the agent run ended abnormally", "kind", abnormal.Kind.String(),
				"session_id", abnormal.SessionID, "detail", abnormal.Detail, "attempt", attempt)
			if ctx.Err() != nil {
				// cumin is stopping. The label stays, and the Owner
				// restarts the issue with cumin/status/ready.
				return
			}
			if attempt < agentAttempts {
				attempt++
				firstKind = abnormal.Kind
				request.SessionID = ""
				log.Info("I3: the same request runs again in the same work directory", "attempt", attempt)
				continue
			}
			s.stopAfterAbnormalEnd(ctx, log, target, settings, RowI3, "Reviewer", number, firstKind, abnormal.Kind)
			return
		case err != nil:
			log.Error("the agent was not started", "error", err.Error())
			return
		}

		log.Info("the agent run ended", "result", run.Result.Result, "session_id", run.SessionID)
		s.quotaAfterRun(ctx, log, target, number, run)
		s.keepSession(log, target, config.RoleReviewer, number, run.SessionID)
		// The first try runs here. A try of the kept step runs in a poll.
		try := &reviewTry{}
		if run.Result.Result != agent.ResultDone {
			step := &keptStep{name: "the stop after a blocked review", log: log}
			step.run = func(ctx context.Context) error {
				again := try.again
				try.again = true
				return s.stopBlockedReview(ctx, log, target, settings, number, run.Result.BlockedReason, again)
			}
			s.tryStep(ctx, key, step)
			return
		}
		step := &keptStep{name: "the check of the review", log: log}
		step.run = func(ctx context.Context) error {
			rest, err := s.afterReview(ctx, log, target, settings, number, req, request, run.SessionID, missed, try)
			try.again = true
			step.rest = rest
			return err
		}
		s.tryStep(ctx, key, step)
		return
	}
}

// reviewTry is what the step after a Reviewer run keeps from one try to the
// next.
type reviewTry struct {
	// again says that the step ran before: this try is one of the kept step.
	again bool
	// lost is the status label of a write that ended with a temporary
	// failure. Its answer did not come, so the issue can have the label.
	lost string
}

// stopBlockedReview applies I10: it reads the issue for its labels, and
// hands it to the Owner with the blocked_reason of the Reviewer. A
// temporary failure of the read is returned, before anything is written,
// and the caller keeps the step. A try of the kept step that finds the
// issue out of cumin/status/reviewing changes nothing.
func (s *Service) stopBlockedReview(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, number int, reason string, again bool) error {
	sub, err := s.readSubIssueNow(ctx, log, target, number)
	if temporary(err) != nil {
		return err
	}
	if err == nil && again && !slices.Contains(sub.Labels, LabelReviewing) {
		log.Info("I10: the issue left cumin/status/reviewing while the stop was kept; nothing changes", "labels", sub.Labels)
		return nil
	}
	s.stopBlocked(ctx, log, target, settings, RowI10, "Reviewer", number, reason, labelsNow(sub, err == nil))
	return nil
}

// afterReview is the step after a done result of the Reviewer: it reads the
// issue again, checks the review, and applies the path of that review up to
// the label change. It returns the rest of the step, which can take long
// (the fix request of I5, the explanation of I8, the merge of I6, the same
// request once more), or nil when the step ended.
//
// A temporary failure of a call to GitHub (the token, a read, the required
// checks, a label change) is returned, and the caller keeps the step. The
// step then runs again from the read. The rest starts only after a try
// without a failure, so no request is sent twice. Every other failure is
// logged and ends the step, as before.
func (s *Service) afterReview(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, number int, req reviewerRequest, request agent.StartRequest, sessionID string, missed bool, try *reviewTry) (func(context.Context), error) {
	result, sub, pr, ok, err := s.checkReview(ctx, log, target, number, req)
	if !ok {
		return nil, temporary(err)
	}
	// The issue left cumin/status/reviewing while the step was kept. Only a
	// label that an earlier try wrote lets the step go on: its answer was
	// lost, and what follows the label is still to do.
	if try.again && !slices.Contains(sub.Labels, LabelReviewing) && (try.lost == "" || !slices.Contains(sub.Labels, try.lost)) {
		log.Info("I3: the issue left cumin/status/reviewing while the check of the review was kept; nothing changes", "labels", sub.Labels)
		return nil, nil
	}
	if pr.HeadCommit != req.review.HeadCommit {
		return nil, s.headMoved(ctx, log, target, sub, pr, try)
	}
	switch result {
	case ReviewApprovedOnHead:
		log.Info("I3: the Reviewer approved the head commit", "round", req.review.Round)
		return s.afterApproval(ctx, log, target, settings, number, pr, req.ownerLogin, try)
	case ReviewChangesRequestedOnHead:
		return s.afterChangesRequested(ctx, log, target, settings, number, req, sub, pr, sessionID, try)
	}
	if !missed {
		log.Warn("I3: no review on the head commit; the Reviewer is asked once more", "round", req.review.Round)
		request.SessionID = sessionID
		request.Text = ReviewAgainRequestText(req.review)
		return func(ctx context.Context) {
			s.reviewRuns(ctx, log, target, settings, number, req, request, true)
		}, nil
	}
	s.stopForOwner(ctx, log, target, settings, stop{
		row:     RowI5,
		issue:   number,
		labels:  sub.Labels,
		reason:  MissingReviewReason,
		comment: StopNote(RowI5, MissingReviewReason, req.review.PullRequest, true),
	})
	return nil, nil
}

// replaceStatus replaces the status label of the issue, for a step that can
// run again. A label that the issue already has is not written again: an
// earlier try wrote it. A write that ends with a temporary failure is noted
// in try, because GitHub can hold it. It returns the new labels.
func (s *Service) replaceStatus(ctx context.Context, token string, target Target, sub SubIssue, label string, try *reviewTry) ([]string, error) {
	labels := ReplaceStatusLabel(sub.Labels, label)
	if slices.Contains(sub.Labels, label) {
		return labels, nil
	}
	err := s.GitHub.SetIssueLabels(ctx, token, target.Repository.Owner, target.Repository.Name, sub.Number, labels)
	if github.IsTemporary(err) {
		try.lost = label
	}
	return labels, err
}

// MissingReviewReason is the sentence of the stop after the second run
// without a review on the head commit, for the comment and the
// notification alike.
const MissingReviewReason = "The Reviewer reported done twice, but its latest review is not on the head commit of the pull request with APPROVE or REQUEST_CHANGES."

// checkReview reads the issue again and checks the latest review of the
// Reviewer on the pull request of the request. The bool is false when the
// issue or the pull request could not be read; that is logged, and the
// issue keeps its label. The error is the one of a read that failed.
func (s *Service) checkReview(ctx context.Context, log *slog.Logger, target Target, number int, req reviewerRequest) (ReviewResult, SubIssue, PullRequest, bool, error) {
	sub, err := s.readSubIssueNow(ctx, log, target, number)
	if err != nil {
		return ReviewMissing, SubIssue{}, PullRequest{}, false, err
	}
	pr, ok := sub.LatestPullRequest()
	if !ok || pr.Number != req.review.PullRequest {
		log.Error("I3: the pull request of the review is no longer open")
		return ReviewMissing, sub, PullRequest{}, false, nil
	}
	result := CheckReview(pr, req.reviewer)
	log.Info("I3: checked the review", "result", result.String(), "head_commit", pr.HeadCommit)
	return result, sub, pr, true, nil
}

// headMoved handles a head commit that moved while the Reviewer worked, for
// example when the Owner pushed. Only the old head passed the required
// checks, so the issue goes back to cumin/status/awaiting-checks: the checks
// run on the new head, and I3 (or I4) decides again. A review that the
// Reviewer gave on the old head stays on GitHub and counts as it is. It
// returns a temporary failure, for the kept step.
func (s *Service) headMoved(ctx context.Context, log *slog.Logger, target Target, sub SubIssue, pr PullRequest, try *reviewTry) error {
	token, err := target.Token(ctx)
	if err != nil {
		log.Error("I3: no token; the issue keeps its label", "error", err.Error())
		return temporary(err)
	}
	labels, err := s.replaceStatus(ctx, token, target, sub, LabelAwaitingChecks, try)
	if err != nil {
		log.Error("I3: the label was not changed", "error", err.Error())
		return temporary(err)
	}
	log.Info("I3: the head commit moved during the review; the issue waits for the checks again",
		"head_commit", pr.HeadCommit, "labels", labels)
	return nil
}

// afterChangesRequested decides between I5 and I8 on a review that asked
// for changes on the head commit. The round is counted again from the
// reviews that cumin just read, so it is the round of that review.
//
// Below max_review_rounds, I5 moves the issue to cumin/status/implementing
// first (principle 3), then asks the Implementer to fix the comments in the
// session of its last run, on the branch of the pull request. The end of
// that run is the end of any Implementer run: I2 verifies it, and the
// checks and I3 follow. At the limit, I8 asks the Reviewer to explain the
// cause, in the session of the run that just ended.
//
// The request is the rest of the step: the function returns it, and the
// caller runs it. A temporary failure of the token or of the label change
// is returned, for the kept step, and nothing is requested.
func (s *Service) afterChangesRequested(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, number int, req reviewerRequest, sub SubIssue, pr PullRequest, sessionID string, try *reviewTry) (func(context.Context), error) {
	round := ReviewRounds(pr.Reviews, req.reviewer, req.readyAt)
	limit := settings.Settings.MaxReviewRounds
	latest, _ := LatestReview(pr.Reviews, req.reviewer)
	if !ReviewFixAllowed(round, limit) {
		log.Info("I8: blocking comments remain at the limit of rounds", "round", round, "limit", limit)
		return func(ctx context.Context) {
			s.explainCause(ctx, log, target, settings, number, req, latest, sessionID)
		}, nil
	}
	token, err := target.Token(ctx)
	if err != nil {
		log.Error("I5: no token; the issue keeps its label", "error", err.Error())
		return nil, temporary(err)
	}
	labels, err := s.replaceStatus(ctx, token, target, sub, LabelImplementing, try)
	if err != nil {
		log.Error("I5: the label was not changed; nothing is requested", "error", err.Error())
		return nil, temporary(err)
	}
	log.Info("I5: the Reviewer requested changes; the issue goes back to the Implementer",
		"round", round, "limit", limit, "review", latest.URL, "labels", labels)
	repository := target.Repository.String()
	branch := pr.HeadBranch
	return func(ctx context.Context) {
		s.runImplementer(ctx, target, settings, number, implementerRequest{
			row: "I5", kind: "review fix", branch: branch, pullRequest: pr.Number,
			sessionID:  s.State.Issue(repository, number).SessionID,
			ownerLogin: req.ownerLogin,
			text: func(workDir string) string {
				return ReviewFixRequestText(repository, number, pr.Number, branch, workDir, latest.URL)
			},
		})
	}, nil
}

// explainCause applies I8: blocking comments remain at the limit of rounds.
// The Reviewer writes one decision request on the pull request, in the
// session that holds the rounds. cumin then looks for a comment of the
// Reviewer that starts with the heading of a decision request and is not
// older than the last review; with it, the issue goes to
// cumin/status/awaiting-owner-decision and the Owner gets one notification
// that links the comment. The Reviewer wrote the reason, so cumin writes
// no comment of its own.
//
// Without that comment, with a blocked result, or after a second abnormal
// end, the stop step hands the issue to the Owner with the row I8: the
// Owner must decide either way. The time of the last review comes from
// GitHub, as the time of the comment does, so the clock of the Host plays
// no part.
func (s *Service) explainCause(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, number int, req reviewerRequest, latest Review, sessionID string) {
	role := settings.Settings.Roles[config.RoleReviewer]
	limit := req.review.Limit
	log.Info("I8: requested the explanation of the cause", "resumed", sessionID != "")
	request := agent.StartRequest{
		Owner:        target.Repository.Owner,
		Repo:         target.Repository.Name,
		Role:         config.RoleReviewer,
		RiskCriteria: settings.RiskCriteria,
		Facts:        agent.Facts{IssueNumber: number, IssueKind: agent.IssueKindImplementation, OwnerLogin: req.ownerLogin, ProtectedPaths: settings.ProtectedPaths},
		Text:         ExplainCauseRequestText(req.review.Repository, number, req.review.PullRequest, limit, req.review.WorkDir),
		WorkDir:      req.review.WorkDir,
		Settings:     &role,
		SessionID:    sessionID,
	}
	var firstKind agent.EndKind
	for attempt := 1; attempt <= agentAttempts; attempt++ {
		run, err := s.Agents.Start(ctx, request)
		var abnormal *agent.AbnormalEnd
		switch {
		case errors.As(err, &abnormal):
			log.Info("the agent run ended abnormally", "kind", abnormal.Kind.String(),
				"session_id", abnormal.SessionID, "detail", abnormal.Detail, "attempt", attempt)
			if ctx.Err() != nil {
				return
			}
			if attempt < agentAttempts {
				firstKind = abnormal.Kind
				request.SessionID = ""
				log.Info("I8: the same request runs again in the same work directory", "attempt", attempt+1)
				continue
			}
			s.stopAfterAbnormalEnd(ctx, log, target, settings, RowI8, "Reviewer", number, firstKind, abnormal.Kind)
			return
		case err != nil:
			log.Error("the agent was not started", "error", err.Error())
			return
		}
		log.Info("the agent run ended", "result", run.Result.Result, "session_id", run.SessionID)
		s.keepSession(log, target, config.RoleReviewer, number, run.SessionID)
		if run.Result.Result != agent.ResultDone {
			s.stopAfterBlocked(ctx, log, target, settings, RowI8, "Reviewer", number, run.Result.BlockedReason)
			return
		}
		s.handOverExplanation(ctx, log, target, settings, number, req, latest)
		return
	}
}

// handOverExplanation checks the comment of I8 and hands the issue to the
// Owner. See explainCause.
func (s *Service) handOverExplanation(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, number int, req reviewerRequest, latest Review) {
	owner, repo := target.Repository.Owner, target.Repository.Name
	pullRequest := req.review.PullRequest
	sub, ok := s.subIssueNow(ctx, log, target, number)
	if !ok {
		return
	}
	token, err := target.Token(ctx)
	if err != nil {
		log.Error("I8: no token; the issue keeps its label", "error", err.Error())
		return
	}
	read, rate, err := s.GitHub.ReadIssueComments(ctx, token, owner, repo, pullRequest, latest.SubmittedAt)
	if err != nil {
		log.Error("I8: the comments of the pull request were not read; the issue keeps its label", "error", err.Error())
		return
	}
	log.Debug("I8: read the comments of the pull request", "rate_limit_cost", rate.Cost, "rate_limit_remaining", rate.Remaining)
	comments := make([]Comment, 0, len(read))
	for _, c := range read {
		comments = append(comments, Comment{Author: c.Author, CreatedAt: c.CreatedAt, Body: c.Body, URL: c.URL})
	}
	explanation, found := ExplanationOf(comments, req.reviewer, latest.SubmittedAt)
	if !found {
		s.stopForOwner(ctx, log, target, settings, stop{
			row:     RowI8,
			issue:   number,
			labels:  sub.Labels,
			reason:  MissingExplanationReason,
			comment: StopNote(RowI8, MissingExplanationReason, pullRequest, false),
		})
		return
	}
	labels := ReplaceStatusLabel(sub.Labels, LabelAwaitingOwnerDecision)
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, number, labels); err != nil {
		// The notification still goes: the Owner must learn that the
		// review did not end, as in the stop step.
		log.Error("I8: the label was not changed", "error", err.Error())
	} else {
		log.Info("I8: the issue waits for the Owner", "labels", labels, "comment", explanation.URL)
	}
	s.notifyOwner(ctx, log.With("row", RowI8), settings != nil && settings.Settings.Notify.DiscordEnabled, notify.Notification{
		Row:        RowI8,
		Reason:     fmt.Sprintf("blocking comments remain after %d review rounds: %s", req.review.Limit, firstBodyLine(explanation.Body)),
		Repository: target.Repository.String(),
		Subject:    fmt.Sprintf("issue #%d", number),
		Link:       explanation.URL,
	})
}

// MissingExplanationReason is the sentence of the stop of I8 when the
// Reviewer wrote no decision request, for the comment and the
// notification alike.
const MissingExplanationReason = "Blocking comments remain at the limit of review rounds, and the Reviewer reported done, but it wrote no decision request on the pull request after its last review."
