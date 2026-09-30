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
	"time"

	"github.com/cloveclovedev/cumin-works/internal/agent"
	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/notify"
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
	round := ReviewRounds(pr.Reviews, reviewer, readyAt) + 1
	req := reviewerRequest{
		review: ReviewRequest{
			Repository:   repository,
			Issue:        a.Number,
			PullRequest:  pr.Number,
			HeadCommit:   pr.HeadCommit,
			Round:        round,
			Limit:        settings.Settings.MaxReviewRounds,
			LastReviewed: LastReviewedCommit(pr.Reviews, reviewer, readyAt),
		},
		reviewer: reviewer,
		readyAt:  readyAt,
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
		Text:         ReviewRequestText(req.review),
		WorkDir:      workDir,
		Settings:     &role,
		SessionID:    req.sessionID,
	}

	var firstKind agent.EndKind
	attempt, missed := 1, false
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
		if run.Result.Result != agent.ResultDone {
			s.stopAfterBlocked(ctx, log, target, settings, RowI10, "Reviewer", number, run.Result.BlockedReason)
			return
		}
		result, sub, pr, ok := s.checkReview(ctx, log, target, number, req)
		if !ok {
			return
		}
		if pr.HeadCommit != req.review.HeadCommit {
			s.headMoved(ctx, log, target, number, sub, pr)
			return
		}
		switch result {
		case ReviewApprovedOnHead:
			// The merge (I6, I7) is the next requirement. Until then the
			// issue keeps cumin/status/reviewing.
			log.Info("I3: the Reviewer approved the head commit", "round", req.review.Round)
			return
		case ReviewChangesRequestedOnHead:
			s.afterChangesRequested(ctx, log, target, settings, number, req, sub, pr, run.SessionID)
			return
		}
		if !missed {
			missed = true
			log.Warn("I3: no review on the head commit; the Reviewer is asked once more", "round", req.review.Round)
			request.SessionID = run.SessionID
			request.Text = ReviewAgainRequestText(req.review)
			attempt = 1
			continue
		}
		s.stopForOwner(ctx, log, target, settings, stop{
			row:     RowI5,
			issue:   number,
			labels:  sub.Labels,
			reason:  MissingReviewReason,
			comment: StopNote(RowI5, MissingReviewReason, req.review.PullRequest, true),
		})
		return
	}
}

// MissingReviewReason is the sentence of the stop after the second run
// without a review on the head commit, for the comment and the
// notification alike.
const MissingReviewReason = "The Reviewer reported done twice, but its latest review is not on the head commit of the pull request with APPROVE or REQUEST_CHANGES."

// checkReview reads the snapshot again and checks the latest review of the
// Reviewer on the pull request of the request. The last value is false
// when the snapshot or the pull request could not be read; that is logged,
// and the issue keeps its label.
func (s *Service) checkReview(ctx context.Context, log *slog.Logger, target Target, number int, req reviewerRequest) (ReviewResult, SubIssue, PullRequest, bool) {
	sub, ok := s.subIssueNow(ctx, log, target, number)
	if !ok {
		return ReviewMissing, SubIssue{}, PullRequest{}, false
	}
	pr, ok := sub.LatestPullRequest()
	if !ok || pr.Number != req.review.PullRequest {
		log.Error("I3: the pull request of the review is no longer open")
		return ReviewMissing, sub, PullRequest{}, false
	}
	result := CheckReview(pr, req.reviewer)
	log.Info("I3: checked the review", "result", result.String(), "head_commit", pr.HeadCommit)
	return result, sub, pr, true
}

// headMoved handles a head commit that moved while the Reviewer worked, for
// example when the Owner pushed. Only the old head passed the required
// checks, so the issue goes back to cumin/status/awaiting-checks: the checks
// run on the new head, and I3 (or I4) decides again. A review that the
// Reviewer gave on the old head stays on GitHub and counts as it is.
func (s *Service) headMoved(ctx context.Context, log *slog.Logger, target Target, number int, sub SubIssue, pr PullRequest) {
	token, err := target.Token(ctx)
	if err != nil {
		log.Error("I3: no token; the issue keeps its label", "error", err.Error())
		return
	}
	labels := ReplaceStatusLabel(sub.Labels, LabelAwaitingChecks)
	if err := s.GitHub.SetIssueLabels(ctx, token, target.Repository.Owner, target.Repository.Name, number, labels); err != nil {
		log.Error("I3: the label was not changed", "error", err.Error())
		return
	}
	log.Info("I3: the head commit moved during the review; the issue waits for the checks again",
		"head_commit", pr.HeadCommit, "labels", labels)
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
func (s *Service) afterChangesRequested(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, number int, req reviewerRequest, sub SubIssue, pr PullRequest, sessionID string) {
	round := ReviewRounds(pr.Reviews, req.reviewer, req.readyAt)
	limit := settings.Settings.MaxReviewRounds
	latest, _ := LatestReview(pr.Reviews, req.reviewer)
	if !ReviewFixAllowed(round, limit) {
		log.Info("I8: blocking comments remain at the limit of rounds", "round", round, "limit", limit)
		s.explainCause(ctx, log, target, settings, number, req, latest, sessionID)
		return
	}
	owner, repo := target.Repository.Owner, target.Repository.Name
	token, err := target.Token(ctx)
	if err != nil {
		log.Error("I5: no token; the issue keeps its label", "error", err.Error())
		return
	}
	labels := ReplaceStatusLabel(sub.Labels, LabelImplementing)
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, number, labels); err != nil {
		log.Error("I5: the label was not changed; nothing is requested", "error", err.Error())
		return
	}
	log.Info("I5: the Reviewer requested changes; the issue goes back to the Implementer",
		"round", round, "limit", limit, "review", latest.URL, "labels", labels)
	repository := target.Repository.String()
	branch := pr.HeadBranch
	s.runImplementer(ctx, target, settings, number, implementerRequest{
		row: "I5", kind: "review fix", branch: branch, pullRequest: pr.Number,
		sessionID: s.State.Issue(repository, number).SessionID,
		text: func(workDir string) string {
			return ReviewFixRequestText(repository, number, pr.Number, branch, workDir, latest.URL)
		},
	})
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
