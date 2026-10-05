package workflow

// This file starts the Reviewer (I3: the required checks passed, so the
// Reviewer reviews the head commit) and applies the way out of
// cumin/status/reviewing, which the poll and the end of a Reviewer run
// decide with the same pure function (ReviewEnd).
// docs/ja/designs/poll.md, the topic on the Reviewer request.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	// again says that the request is "request the review again": the
	// second request of this stay in cumin/status/reviewing.
	again bool
	// resumes says that the second request follows a run of this stay that
	// ended and left its session: it resumes that session with the short
	// text. Without it, the second request is the whole review request.
	resumes bool
	// cause is the review that the request "request the cause from the
	// Reviewer" (I8) is about, or nil for a review request.
	cause *Review
}

// startReview applies I3: every required check passed on the head commit of
// the pull request. cumin reads the round from GitHub first, writes the
// start of the stay in the state file, then changes the label to
// cumin/status/reviewing, then starts the Reviewer; a read that fails
// changes nothing, and the next poll tries again (principle 3).
//
// Round 1 starts a new session. Round 2 and later resume the session of
// the last Reviewer run of the issue, and name the commit of the last
// review, so that the Reviewer checks the fixes since then
// (issue-states.md, the sessions of an agent; agents/reviewer.md, the scope
// of each round).
func (s *Service) startReview(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, a StartReview) error {
	owner, repo := target.Repository.Owner, target.Repository.Name
	repository := target.Repository.String()
	sub, ok := snapshot.SubIssue(a.Number)
	if !ok {
		return fmt.Errorf("I3: issue #%d is not in the snapshot", a.Number)
	}
	pr, ok := sub.LatestPullRequest()
	if !ok || pr.Number != a.PullRequest {
		return fmt.Errorf("I3: pull request #%d of issue #%d is not in the snapshot", a.PullRequest, a.Number)
	}
	req, err := s.reviewRequestOf(ctx, token, target, settings, a.Number, pr)
	if err != nil {
		return err
	}
	// The stay starts before the label changes, as the stay in
	// cumin/status/implementing does (startStay): a restart of cumin right
	// after the label change then finds the head commit of the request, and
	// not the count of an earlier stay.
	stored := s.State.Issue(repository, a.Number)
	stored.ReviewRequests, stored.ReviewHead = 0, pr.HeadCommit
	if err := s.State.Set(repository, a.Number, stored); err != nil {
		return fmt.Errorf("I3: keep the start of the review of issue #%d: %w", a.Number, err)
	}
	labels := LabelsAfterReview(sub.Labels)
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, a.Number, labels); err != nil {
		return fmt.Errorf("I3: move issue #%d to the review: %w", a.Number, err)
	}
	s.logger().Info("I3: the pull request is ready for review",
		"repository", repository, "issue", a.Number, "pull_request", a.PullRequest,
		"round", req.review.Round, "labels", labels)
	s.goReviewer(ctx, target, settings, a.Number, req)
	return nil
}

// reviewRequestOf reads what a request to the Reviewer needs, for the pull
// request as cumin read it: the login of the Reviewer App, the last
// cumin/status/ready of the issue, which starts the count of the rounds,
// and the login of the Owner. It changes nothing.
func (s *Service) reviewRequestOf(ctx context.Context, token string, target Target, settings *RepositorySettings, number int, pr PullRequest) (reviewerRequest, error) {
	owner, repo := target.Repository.Owner, target.Repository.Name
	repository := target.Repository.String()
	if s.Agents == nil {
		return reviewerRequest{}, errors.New("I3: no agent service is configured")
	}
	reviewer, err := s.Agents.BotLogin(ctx, owner, config.RoleReviewer)
	if err != nil {
		return reviewerRequest{}, fmt.Errorf("I3: read the login of the Reviewer App: %w", err)
	}
	// The query of the label times reads one issue when it gets the number
	// of an implementation issue.
	times, rate, err := s.GitHub.ReadLabelTimes(ctx, token, owner, repo, number)
	if err != nil {
		return reviewerRequest{}, fmt.Errorf("I3: read the label times of issue #%d: %w", number, err)
	}
	s.logger().Debug("I3: read the label times", "repository", repository, "issue", number,
		"rate_limit_cost", rate.Cost, "rate_limit_remaining", rate.Remaining)
	readyAt := times[number][LabelReady]
	ownerLogin, err := s.readOwnerLogin(ctx, token, target, number)
	if err != nil {
		return reviewerRequest{}, fmt.Errorf("I3: read the login of the Owner of issue #%d: %w", number, err)
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
			Issue:        number,
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
		req.sessionID = s.State.Issue(repository, number).ReviewerSessionID
	}
	return req, nil
}

// goReviewer runs one Reviewer request in its own goroutine, so that the
// poll goes on while the agent works.
func (s *Service) goReviewer(ctx context.Context, target Target, settings *RepositorySettings, number int, req reviewerRequest) {
	s.goInWork(ctx, target, number, func(ctx context.Context) {
		s.runReviewer(ctx, target, settings, number, req)
	})
}

// goInWork runs a step of an issue in its own goroutine, and counts the
// issue as in work until the step ends, so that no poll decides for it.
func (s *Service) goInWork(ctx context.Context, target Target, number int, step func(context.Context)) {
	done := s.markInProgress(ctx, target.Repository.String(), number)
	s.running.Add(1)
	go func() {
		defer s.running.Done()
		defer done()
		step(ctx)
	}()
}

// runReviewer prepares the Reviewer worktree at the head commit and runs
// one request to the Reviewer to its end: the review, the review again, or
// the cause at the round limit (I8).
//
// An abnormal end starts the same request once more, in the same work
// directory and in a new session; after the second one, the issue goes to
// the Owner with the row of the request. A blocked result is I10: cumin
// posts the blocked_reason and stops the issue for the Owner at once,
// without a retry. After done, the end of the run decides as the poll does
// (endReview): it reads the facts of the way out of cumin/status/reviewing,
// and ReviewEnd decides. A failed read changes nothing: the issue keeps
// cumin/status/reviewing, and the next poll decides from the same facts.
func (s *Service) runReviewer(ctx context.Context, target Target, settings *RepositorySettings, number int, req reviewerRequest) {
	repository := target.Repository.String()
	log := s.logger().With("repository", repository, "issue", number,
		"role", config.RoleReviewer, "pull_request", req.review.PullRequest)
	role := settings.Settings.Roles[config.RoleReviewer]
	row := RowI3
	if req.cause != nil {
		row = RowI8
	}
	// uncount takes back the count of a second request that did not start.
	uncount := func() {
		if !req.again {
			return
		}
		if err := s.countReviewRequest(repository, number, -1); err != nil {
			log.Error(row+": the count of the request that did not start was not taken back", "error", err.Error())
		}
	}
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
		log.Error(row+": the worktree of an earlier round was not removed", "error", err.Error())
		uncount()
		return
	}
	workDir, err := s.Workspace.Prepare(ctx, target.RemoteURL, checkout)
	if err != nil {
		log.Error(row+": the work directory was not prepared", "error", err.Error())
		uncount()
		return
	}
	req.review.WorkDir = workDir
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
	switch {
	case req.cause != nil:
		request.Text = ExplainCauseRequestText(req.review.Repository, number, req.review.PullRequest, req.review.Limit, workDir)
		log.Info("I8: requested the explanation of the cause", "resumed", req.sessionID != "")
	case req.again:
		// Only a session that got the whole request gets the short text.
		if req.resumes {
			request.Text = ReviewAgainRequestText(req.review)
		}
		log.Warn("I3: no review on the head commit; the Reviewer is asked once more", "round", req.review.Round,
			"resumed", req.sessionID != "", "whole_request", !req.resumes)
	default:
		log.Info("I3: requested the review", "round", req.review.Round, "limit", req.review.Limit,
			"head_commit", req.review.HeadCommit, "resumed", req.sessionID != "")
	}

	var firstKind agent.EndKind
	for attempt := 1; ; attempt++ {
		run, err := s.Agents.Start(ctx, request)
		var abnormal *agent.AbnormalEnd
		switch {
		case errors.As(err, &abnormal):
			log.Info("the agent run ended abnormally", "kind", abnormal.Kind.String(),
				"session_id", abnormal.SessionID, "detail", abnormal.Detail, "attempt", attempt)
			if ctx.Err() != nil {
				// cumin is stopping. The label stays, and the next start of
				// cumin decides from the facts on GitHub.
				return
			}
			if attempt < agentAttempts {
				firstKind = abnormal.Kind
				request.SessionID = ""
				log.Info(row+": the same request runs again in the same work directory", "attempt", attempt+1)
				continue
			}
			s.stopAfterAbnormalEnd(ctx, log, target, settings, row, "Reviewer", number, firstKind, abnormal.Kind)
			return
		case err != nil:
			log.Error("the agent was not started", "error", err.Error())
			uncount()
			return
		}

		log.Info("the agent run ended", "result", run.Result.Result, "session_id", run.SessionID)
		s.quotaAfterRun(ctx, log, target, number, run)
		s.keepSession(log, target, config.RoleReviewer, number, run.SessionID)
		if run.Result.Result != agent.ResultDone {
			if req.cause == nil {
				row = RowI10
			}
			s.stopAfterBlocked(ctx, log, target, settings, row, "Reviewer", number, run.Result.BlockedReason)
			return
		}
		s.endReview(ctx, log, target, settings, number, req)
		return
	}
}

// endReview decides the end of a Reviewer run that returned done, as the
// poll does: it reads the issue again with the facts of the way out of
// cumin/status/reviewing, and applies what ReviewEnd decides. A step that
// starts an agent (the review fix, the review again, the cause) or the
// merge runs here, in the goroutine of the run, so the issue stays in work.
func (s *Service) endReview(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, number int, req reviewerRequest) {
	token, err := target.Token(ctx)
	if err != nil {
		log.Error("I3: no token; the next poll decides the end of the review", "error", err.Error())
		return
	}
	sub, defaultBranch, ok, err := s.reviewingNow(ctx, log, token, target, settings, number)
	if err != nil || !ok {
		return
	}
	// The run knows its own request, also when the state file lost it.
	sub.Reviewing.CauseRequested = req.cause != nil
	sub.Reviewing.RequestedAgain = sub.Reviewing.RequestedAgain || req.again
	sub.Reviewing.RequestedHead = req.review.HeadCommit
	action := ReviewEnd(sub, false)
	if action == nil {
		log.Info("I3: the end of the review was not decided; the next poll decides", "labels", sub.Labels)
		return
	}
	ownerLogin := func(context.Context, string) (string, error) { return req.ownerLogin, nil }
	rest, err := s.applyReviewEnd(ctx, log, token, target, settings, sub, defaultBranch, ownerLogin, true, action)
	if err != nil {
		log.Error("the issue was not moved; the next poll decides again", "error", err.Error())
		return
	}
	if rest != nil {
		rest(ctx)
	}
}

// readReviewingFacts adds the facts of the way out of cumin/status/reviewing
// to each sub-issue of the snapshot that needs them (ReviewNeedsFacts): it
// reads that issue again, so that the decision judges on the reviews and
// the labels of this moment. A failed read leaves the facts out, so nothing
// is decided for that issue in this poll.
func (s *Service) readReviewingFacts(ctx context.Context, log *slog.Logger, token string, target Target, settings *RepositorySettings, snapshot *Snapshot) {
	for i := range snapshot.RequirementIssues {
		for j := range snapshot.RequirementIssues[i].SubIssues {
			sub := &snapshot.RequirementIssues[i].SubIssues[j]
			if !ReviewNeedsFacts(*sub, snapshot.Running[sub.Number]) {
				continue
			}
			if read, _, ok, err := s.reviewingNow(ctx, log.With("issue", sub.Number), token, target, settings, sub.Number); ok && err == nil {
				*sub = read
			}
		}
	}
}

// reviewingNow reads one implementation issue again, and only that issue,
// with the facts that ReviewEnd decides from: the account and the time of
// the newest cumin/status/reviewing, the decision requests on the issue
// after it, the last cumin/status/ready, the required checks after an
// approval, the decision request of the Reviewer on the pull request at the
// round limit, and what the state file holds for this stay. The string is
// the default branch.
//
// The error is the one of a read that failed. The bool is false when the
// issue is not an open issue in cumin/status/reviewing any more. Nothing is
// decided in both cases. A label that does not count ends the read, and
// the Owner is told once.
func (s *Service) reviewingNow(ctx context.Context, log *slog.Logger, token string, target Target, settings *RepositorySettings, number int) (SubIssue, string, bool, error) {
	owner, repo := target.Repository.Owner, target.Repository.Name
	failed := func(what string, err error) (SubIssue, string, bool, error) {
		log.Error("the review: "+what+" was not read; the next poll decides", "error", err.Error())
		return SubIssue{}, "", false, err
	}
	read, err := s.GitHub.ReadSubIssue(ctx, token, owner, repo, number)
	if err != nil {
		return failed("the issue", err)
	}
	log.Debug("read the issue again", "issue", number, "rate_limit_cost", read.RateLimit.Cost, "rate_limit_remaining", read.RateLimit.Remaining)
	sub := toSubIssue(read.Issue)
	if !ReviewNeedsFacts(sub, false) {
		log.Info("the issue is not in cumin/status/reviewing; nothing changes", "labels", sub.Labels)
		return sub, read.DefaultBranch, false, nil
	}
	actor, counts, err := s.readStatusActor(ctx, token, target, number, LabelReviewing, false)
	if err != nil {
		return failed("the actor of the newest "+LabelReviewing, err)
	}
	facts := &ReviewingFacts{StatusCounts: counts, ReviewingAt: actor.At, Limit: settings.Settings.MaxReviewRounds}
	sub.Reviewing = facts
	if !counts {
		s.tellStatusOfAnother(ctx, log, target, settings, number, LabelReviewing, actor)
		return sub, read.DefaultBranch, true, nil
	}
	if s.Agents == nil {
		return failed("the login of the Reviewer App", errors.New("no agent service is configured"))
	}
	if facts.Reviewer, err = s.Agents.BotLogin(ctx, owner, config.RoleReviewer); err != nil {
		return failed("the login of the Reviewer App", err)
	}
	// cumin-core posts the blocked_reason of the Reviewer, so its decision
	// request is a question too.
	askers := []string{facts.Reviewer}
	if target.Login != nil {
		core, err := target.Login(ctx)
		if err != nil {
			return failed("the login of cumin-core", err)
		}
		askers = append(askers, core)
	}
	asked, err := s.readComments(ctx, log, token, target, number, facts.ReviewingAt)
	if err != nil {
		return failed("the comments of the issue", err)
	}
	facts.QuestionAt = QuestionAt(asked, askers...)
	times, rate, err := s.GitHub.ReadLabelTimes(ctx, token, owner, repo, number)
	if err != nil {
		return failed("the label times", err)
	}
	log.Debug("read the label times", "rate_limit_cost", rate.Cost, "rate_limit_remaining", rate.Remaining)
	facts.ReadyAt = times[number][LabelReady]
	stored := s.State.Issue(target.Repository.String(), number)
	facts.RequestedAgain, facts.RequestedHead = stored.ReviewRequests > 0, stored.ReviewHead
	pr, ok := sub.LatestPullRequest()
	if !ok {
		log.Error("I3: the pull request of the review is no longer open")
		return sub, read.DefaultBranch, true, nil
	}
	if CheckReview(pr, facts.Reviewer) == ReviewApprovedOnHead {
		// The required checks are a REST call of their own, so only an
		// approval reads them.
		required, err := s.GitHub.RequiredChecks(ctx, token, owner, repo, read.DefaultBranch)
		if err != nil {
			return failed("the required checks", err)
		}
		facts.Required = toRequiredChecks(required)
	}
	if latest, ok := ReviewNeedsExplanation(pr, facts.Reviewer, facts.ReadyAt, facts.Limit); ok {
		comments, err := s.readComments(ctx, log, token, target, pr.Number, latest.SubmittedAt)
		if err != nil {
			return failed("the comments of the pull request", err)
		}
		facts.Explanation, facts.Explained = ExplanationOf(comments, facts.Reviewer, latest.SubmittedAt)
	}
	return sub, read.DefaultBranch, true, nil
}

// readComments reads the comments of an issue or of a pull request since a
// time.
func (s *Service) readComments(ctx context.Context, log *slog.Logger, token string, target Target, number int, since time.Time) ([]Comment, error) {
	read, rate, err := s.GitHub.ReadIssueComments(ctx, token, target.Repository.Owner, target.Repository.Name, number, since)
	if err != nil {
		return nil, err
	}
	log.Debug("read the comments", "number", number, "rate_limit_cost", rate.Cost, "rate_limit_remaining", rate.Remaining)
	comments := make([]Comment, 0, len(read))
	for _, c := range read {
		comments = append(comments, Comment{Author: c.Author, CreatedAt: c.CreatedAt, Body: c.Body, URL: c.URL})
	}
	return comments, nil
}

// countReviewRequest changes, in the state file, how many times the review
// was requested again during this stay in cumin/status/reviewing. delta is
// 1 before the second request starts, and -1 when that run did not start.
func (s *Service) countReviewRequest(repository string, number, delta int) error {
	stored := s.State.Issue(repository, number)
	stored.ReviewRequests = max(0, stored.ReviewRequests+delta)
	return s.State.Set(repository, number, stored)
}

// applyReviewEnd applies one way out of cumin/status/reviewing that
// ReviewEnd decided, for the poll and for the end of a Reviewer run alike.
// It changes the label first where the action has one. It returns the rest
// of the step, which can take long (an agent run, or the merge), or nil
// when the step ended: the poll runs the rest in a goroutine of its own,
// and the end of a run runs it in its goroutine.
//
// An error says that nothing more happened: the issue keeps
// cumin/status/reviewing, and the next poll decides again from the same
// facts. No request, comment, or notification goes out before the label
// changed, so none goes out twice. ownerLogin gives the login of the Owner
// for a review fix. afterRun says that a Reviewer run of this stay just
// ended and left its session; a poll passes false, because a restart of
// cumin can have cut the run before its session was kept.
func (s *Service) applyReviewEnd(ctx context.Context, log *slog.Logger, token string, target Target, settings *RepositorySettings, sub SubIssue, defaultBranch string, ownerLogin func(ctx context.Context, token string) (string, error), afterRun bool, action Action) (func(context.Context), error) {
	owner, repo := target.Repository.Owner, target.Repository.Name
	repository := target.Repository.String()
	number := sub.Number
	pr, _ := sub.LatestPullRequest()
	move := func(row, label string) ([]string, error) {
		labels := ReplaceStatusLabel(sub.Labels, label)
		if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, number, labels); err != nil {
			return nil, fmt.Errorf("%s: move issue #%d to %s: %w", row, number, label, err)
		}
		return labels, nil
	}
	switch a := action.(type) {
	case StopReview:
		labels, err := move(a.Row, LabelAwaitingDecision)
		if err != nil {
			return nil, err
		}
		if !a.Question {
			log.Warn(a.Row+": the review stops for the Owner", "reason", a.Reason, "retried", a.Retried, "labels", labels)
			s.stopForOwner(ctx, log, target, settings, stop{
				row: a.Row, issue: number, labelDone: true, reason: a.Reason,
				comment: StopNote(a.Row, a.Reason, a.PullRequest, a.Retried),
			})
			return nil, nil
		}
		log.Info("I10: the Reviewer asked a question; the issue waits for the Owner", "labels", labels)
		s.notifyOwner(ctx, log.With("row", RowI10), settings.Settings.Notify.DiscordEnabled, notify.Notification{
			Row:        RowI10,
			Reason:     "The Reviewer asked a question during the review.",
			Repository: repository,
			Subject:    fmt.Sprintf("issue #%d", number),
			Link:       github.IssueURL(owner, repo, number),
		})
	case BackToChecks:
		labels, err := move(RowI3, LabelChecking)
		if err != nil {
			return nil, err
		}
		if a.HeadMoved {
			log.Info("I3: the head commit moved during the review; the issue waits for the checks again",
				"head_commit", pr.HeadCommit, "labels", labels)
		} else {
			log.Info("I6: a required check does not pass on the approved commit; the issue waits for the checks again", "labels", labels)
		}
	case AskOwnerToMerge:
		log.Info("I3: the Reviewer approved the head commit")
		return nil, s.askOwnerToMerge(ctx, log, target, settings, token, sub, pr)
	case StopAtRoundLimit:
		labels, err := move(RowI8, LabelAwaitingDecision)
		if err != nil {
			return nil, err
		}
		log.Info("I8: the issue waits for the Owner", "labels", labels, "comment", a.Explanation.URL)
		s.notifyOwner(ctx, log.With("row", RowI8), settings.Settings.Notify.DiscordEnabled, notify.Notification{
			Row:        RowI8,
			Reason:     fmt.Sprintf("blocking comments remain after %d review rounds: %s", sub.Reviewing.Limit, firstBodyLine(a.Explanation.Body)),
			Repository: repository,
			Subject:    fmt.Sprintf("issue #%d", number),
			Link:       a.Explanation.URL,
		})
	case RequestReviewFix:
		login, err := ownerLogin(ctx, token)
		if err != nil {
			return nil, fmt.Errorf("I5: read the login of the Owner of issue #%d: %w", number, err)
		}
		if err := s.startStay(repository, number, false); err != nil {
			return nil, fmt.Errorf("I5: keep the start of the stay of issue #%d in implementing: %w", number, err)
		}
		labels, err := move(RowI5, LabelImplementing)
		if err != nil {
			return nil, err
		}
		log.Info("I5: the Reviewer requested changes; the issue goes back to the Implementer",
			"round", a.Round, "limit", sub.Reviewing.Limit, "review", a.Review.URL, "labels", labels)
		branch := pr.HeadBranch
		return func(ctx context.Context) {
			s.runImplementer(ctx, target, settings, number, implementerRequest{
				row: "I5", kind: "review fix", branch: branch, pullRequest: pr.Number,
				sessionID:  s.State.Issue(repository, number).SessionID,
				ownerLogin: login,
				text: func(workDir string) string {
					return ReviewFixRequestText(repository, number, pr.Number, branch, workDir, a.Review.URL)
				},
			})
		}, nil
	case RequestReviewAgain, RequestCause:
		req, err := s.reviewRequestOf(ctx, token, target, settings, number, pr)
		if err != nil {
			return nil, err
		}
		if cause, ok := a.(RequestCause); ok {
			// The cause goes on in the session of the last Reviewer run,
			// which holds the rounds.
			log.Info("I8: blocking comments remain at the limit of rounds", "limit", req.review.Limit)
			req.cause = &cause.Review
			req.sessionID = s.State.Issue(repository, number).ReviewerSessionID
		} else {
			// The second request has the round of the first one: no review
			// of this round is on GitHub. After a run of this stay, it goes
			// on in the session of that run, with the short text. At a
			// poll, the state file can hold no session of this stay, so the
			// request is the whole review request, in the session that a
			// first request of this round takes (reviewRequestOf).
			req.again = true
			if req.resumes = afterRun; afterRun {
				req.sessionID = s.State.Issue(repository, number).ReviewerSessionID
			}
			if err := s.countReviewRequest(repository, number, 1); err != nil {
				return nil, fmt.Errorf("I3: count the second request of the review of issue #%d: %w", number, err)
			}
		}
		return func(ctx context.Context) { s.runReviewer(ctx, target, settings, number, req) }, nil
	case MergeApproved:
		log.Info("I3: the Reviewer approved the head commit")
		return func(ctx context.Context) {
			s.mergeStep(ctx, log, target, settings, RowI6, sub, pr, defaultBranch,
				func(token string) (string, error) { return ownerLogin(ctx, token) },
				// A try of the kept merge decides again from the facts, and
				// merges only when the decision is still the merge of this
				// head commit. Every other decision is left to the next
				// poll.
				func(ctx context.Context) (bool, error) {
					// The try can run long after the decision, so it takes a
					// token of its own.
					token, err := target.Token(ctx)
					if err != nil {
						return false, temporary(err)
					}
					now, _, ok, err := s.reviewingNow(ctx, log, token, target, settings, number)
					if err != nil || !ok {
						return false, temporary(err)
					}
					head, _ := now.LatestPullRequest()
					_, merge := ReviewEnd(now, false).(MergeApproved)
					return merge && head.Number == pr.Number && head.HeadCommit == pr.HeadCommit, nil
				})
		}, nil
	default:
		return nil, fmt.Errorf("unknown way out of the review %T", action)
	}
	return nil, nil
}

// reviewEndAtPoll applies one way out of cumin/status/reviewing that a poll
// decided. The rest of the step runs in its own goroutine, and the issue
// counts as in work until it ends.
func (s *Service) reviewEndAtPoll(ctx context.Context, log *slog.Logger, token string, target Target, snapshot Snapshot, settings *RepositorySettings, action Action) error {
	number := ReviewEndIssue(action)
	sub, _ := snapshot.SubIssue(number)
	ownerLogin := func(ctx context.Context, token string) (string, error) {
		return s.readOwnerLogin(ctx, token, target, number)
	}
	rest, err := s.applyReviewEnd(ctx, log.With("issue", number), token, target, settings, sub, snapshot.DefaultBranch, ownerLogin, false, action)
	if err != nil || rest == nil {
		return err
	}
	s.goInWork(ctx, target, number, rest)
	return nil
}

// MissingReviewReason is the sentence of the stop after the second run
// without a review on the head commit, for the comment and the
// notification alike.
const MissingReviewReason = "cumin requested the review twice, but the latest review of the Reviewer is not on the head commit of the pull request with APPROVE or REQUEST_CHANGES."

// MissingExplanationReason is the sentence of the stop of I8 when the
// Reviewer wrote no decision request, for the comment and the
// notification alike.
const MissingExplanationReason = "Blocking comments remain at the limit of review rounds, and the Reviewer reported done, but it wrote no decision request on the pull request after its last review."
