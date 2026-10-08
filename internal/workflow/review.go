package workflow

// This file starts the Reviewer ("request the review": the required checks
// passed, so the Reviewer reviews the head commit) and applies the way out of
// cumin/status/reviewing, which the poll and the end of a Reviewer run
// decide with the same pure function (ReviewEnd).
// docs/ja/designs/poll.md, the topic on the Reviewer request.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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
	// title is the title of the issue, for the monitor file.
	title string
	// reviewer is the login of the Reviewer App, "<slug>[bot]". The check
	// after the run reads its latest review.
	reviewer string
	// readyAt is the last cumin/status/ready of the issue: the start of the
	// count of the rounds, which the end of the run counts again.
	readyAt time.Time
	// sessionID resumes that session. Empty starts a new session.
	sessionID string
	// issueOwnerLogin is the login of the Issue Owner for the facts of the
	// request, read before the label changed. Empty says that there is none.
	// The requests that follow in the same run ("request a review fix",
	// "request the cause") carry the same login.
	issueOwnerLogin string
	// again says that the request is the second one of its kind during this
	// stay in cumin/status/reviewing: "request the review again", or the
	// second request of the cause.
	again bool
	// resumes says that the second request follows a run of this stay that
	// ended and left its session: it resumes that session with the short
	// text. Without it, the second request is the whole review request.
	resumes bool
	// cause is the review that the request "request the cause" is about, or
	// nil for a review request.
	cause *Review
	// permit is the permit of the start of this request (permitStart).
	permit StartPermit
}

// startReview applies "request the review": every required check passed on
// the head commit of the pull request. cumin reads the round from GitHub
// first, writes the start of the stay in the state file, then changes the
// label to cumin/status/reviewing, then starts the Reviewer; a read that
// fails changes nothing, and the next poll tries again (principle 3).
//
// Round 1 starts a new session. Round 2 and later resume the session of
// the last Reviewer run of the issue, and name the commit of the last
// review, so that the Reviewer checks the fixes since then
// (issue-states.md, the sessions of an agent; agents/reviewer.md, the scope
// of each round).
func (s *Service) startReview(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, a StartReview) error {
	repository := target.Repository.String()
	sub, ok := snapshot.SubIssue(a.Number)
	if !ok {
		return fmt.Errorf(string(ActionRequestTheReview)+": issue #%d is not in the snapshot", a.Number)
	}
	pr, ok := sub.LatestPullRequest()
	if !ok || pr.Number != a.PullRequest {
		return fmt.Errorf(string(ActionRequestTheReview)+": pull request #%d of issue #%d is not in the snapshot", a.PullRequest, a.Number)
	}
	permit, ok := s.permitStart(ctx, s.logger().With("repository", repository, "issue", a.Number), "review", config.RoleReviewer, target, a.Number)
	if !ok {
		return nil
	}
	req, err := s.reviewRequestOf(ctx, token, target, settings, a.Number, pr)
	if err != nil {
		return err
	}
	req.permit, req.title = permit, sub.Title
	// The stay starts before the label changes, as the stay in
	// cumin/status/implementing does (startStay): a restart of cumin right
	// after the label change then finds the head commit of the request, and
	// not the count of an earlier stay.
	stored := s.State.Issue(repository, a.Number)
	stored.ReviewRequests, stored.CauseRequests, stored.ReviewHead = 0, 0, pr.HeadCommit
	if err := s.State.Set(repository, a.Number, stored); err != nil {
		return fmt.Errorf(string(ActionRequestTheReview)+": keep the start of the review of issue #%d: %w", a.Number, err)
	}
	labels, err := s.moveIssue(ctx, token, target, a.Number, sub.Labels, LabelReviewing)
	if err != nil {
		return fmt.Errorf(string(ActionRequestTheReview)+": %w", err)
	}
	s.logger().Info(string(ActionRequestTheReview)+": the pull request is ready for review",
		"repository", repository, "issue", a.Number, "pull_request", a.PullRequest,
		"round", req.review.Round, "labels", labels)
	s.goReviewer(ctx, target, settings, a.Number, req)
	return nil
}

// reviewRequestOf reads what a request to the Reviewer needs, for the pull
// request as cumin read it: the login of the Reviewer App, the last
// cumin/status/ready of the issue, which starts the count of the rounds,
// and the login of the Issue Owner. It changes nothing.
func (s *Service) reviewRequestOf(ctx context.Context, token string, target Target, settings *RepositorySettings, number int, pr PullRequest) (reviewerRequest, error) {
	owner, repo := target.Repository.Owner, target.Repository.Name
	repository := target.Repository.String()
	if s.Agents == nil {
		return reviewerRequest{}, errors.New(string(ActionRequestTheReview) + ": no agent service is configured")
	}
	reviewer, err := s.Agents.BotLogin(ctx, owner, config.RoleReviewer)
	if err != nil {
		return reviewerRequest{}, fmt.Errorf(string(ActionRequestTheReview)+": read the login of the Reviewer App: %w", err)
	}
	// The query of the label times reads one issue when it gets the number
	// of an implementation issue.
	times, rate, err := s.GitHub.ReadLabelTimes(ctx, token, owner, repo, number)
	if err != nil {
		return reviewerRequest{}, fmt.Errorf(string(ActionRequestTheReview)+": read the label times of issue #%d: %w", number, err)
	}
	s.logger().Debug(string(ActionRequestTheReview)+": read the label times", "repository", repository, "issue", number,
		"rate_limit_cost", rate.Cost, "rate_limit_remaining", rate.Remaining)
	readyAt := times[number][LabelReady]
	issueOwnerLogin, err := s.readIssueOwnerLogin(ctx, token, target, number)
	if err != nil {
		return reviewerRequest{}, fmt.Errorf(string(ActionRequestTheReview)+": read the Issue Owner login of issue #%d: %w", number, err)
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
		reviewer:        reviewer,
		readyAt:         readyAt,
		issueOwnerLogin: issueOwnerLogin,
	}
	if round > 1 {
		req.sessionID = s.State.Issue(repository, number).ReviewerSessionID
	}
	return req, nil
}

// goReviewer runs one Reviewer request in its own goroutine, so that the
// poll goes on while the agent works.
func (s *Service) goReviewer(ctx context.Context, target Target, settings *RepositorySettings, number int, req reviewerRequest) {
	run := agentRun{role: config.RoleReviewer, request: req.kind(), title: req.title}
	s.goInWork(ctx, target, number, run, func(ctx context.Context) {
		s.runReviewer(ctx, target, settings, number, req)
	})
}

// goInWork runs a step of an issue in its own goroutine, and counts the
// issue as in work until the step ends, so that no poll decides for it. run
// is the run that the step starts with.
func (s *Service) goInWork(ctx context.Context, target Target, number int, run agentRun, step func(context.Context)) {
	done := s.markInProgress(ctx, target.Repository.String(), number, run)
	s.running.Add(1)
	go func() {
		defer s.running.Done()
		defer done()
		step(ctx)
	}()
}

// runReviewer prepares the Reviewer worktree at the head commit and runs
// one request to the Reviewer to its end: the review, the review again, or
// the cause at the round limit.
//
// Every end but a blocked result is the same case: a done result and an
// abnormal end are decided as the poll decides (endReview), from the facts
// on GitHub, and ReviewEnd decides. Nothing is retried inside the run: the
// second request is "request the review again", once for each stay in
// cumin/status/reviewing. A blocked result is "stop the review": cumin posts
// the blocked_reason and stops the issue for a Maintainer at once. A failed
// read changes nothing: the issue keeps cumin/status/reviewing, and the
// next poll decides from the same facts.
//
// A request that did not start (the work directory, the start of the
// agent) keeps its count. After the first one, the next poll requests
// again; the second one stops the review for a Maintainer
// (stopForReviewerStart).
func (s *Service) runReviewer(ctx context.Context, target Target, settings *RepositorySettings, number int, req reviewerRequest) {
	repository := target.Repository.String()
	log := s.logger().With("repository", repository, "issue", number,
		"role", config.RoleReviewer, "pull_request", req.review.PullRequest)
	s.noteRequest(repository, number, config.RoleReviewer, req.kind())
	role := settings.Settings.Roles[config.RoleReviewer]
	action := req.action()
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
		log.Error(string(action)+": the worktree of an earlier round was not removed", "error", err.Error())
		s.stopForReviewerStart(ctx, log, target, settings, number, req)
		return
	}
	workDir, err := s.Workspace.Prepare(ctx, target.RemoteURL, checkout)
	if err != nil {
		log.Error(string(action)+": the work directory was not prepared", "error", err.Error())
		s.stopForReviewerStart(ctx, log, target, settings, number, req)
		return
	}
	req.review.WorkDir = workDir
	request := agent.StartRequest{
		Owner:        target.Repository.Owner,
		Repo:         target.Repository.Name,
		Role:         config.RoleReviewer,
		RiskCriteria: settings.RiskCriteria,
		Facts:        agent.Facts{IssueNumber: number, IssueKind: agent.IssueKindImplementation, IssueOwnerLogin: req.issueOwnerLogin, ProtectedPaths: settings.ProtectedPaths},
		Text:         ReviewRequestText(req.review),
		WorkDir:      workDir,
		Settings:     &role,
		SessionID:    req.sessionID,
	}
	switch {
	case req.cause != nil:
		request.Text = ExplainCauseRequestText(req.review.Repository, number, req.review.PullRequest, req.review.Limit, workDir)
		log.Info(string(action)+": requested the explanation of the cause", "resumed", req.sessionID != "", "again", req.again)
	case req.again:
		// Only a session that got the whole request gets the short text.
		if req.resumes {
			request.Text = ReviewAgainRequestText(req.review)
		}
		log.Warn(string(action)+": no review on the head commit; the Reviewer is asked once more", "round", req.review.Round,
			"resumed", req.sessionID != "", "whole_request", !req.resumes)
	default:
		log.Info(string(action)+": requested the review", "round", req.review.Round, "limit", req.review.Limit,
			"head_commit", req.review.HeadCommit, "resumed", req.sessionID != "")
	}

	run, err := s.startAgent(ctx, req.permit, request)
	var abnormal *agent.AbnormalEnd
	switch {
	case errors.As(err, &abnormal):
		log.Info("the agent run ended abnormally", "kind", abnormal.Kind.String(),
			"session_id", abnormal.SessionID, "detail", abnormal.Detail)
		if ctx.Err() != nil {
			// cumin is stopping. The label stays, and the next start of
			// cumin decides from the facts on GitHub.
			return
		}
	case err != nil:
		log.Error("the agent was not started", "error", err.Error())
		s.stopForReviewerStart(ctx, log, target, settings, number, req)
		return
	default:
		log.Info("the agent run ended", "result", run.Result.Result, "session_id", run.SessionID)
		s.quotaAfterRun(ctx, log, target, number, run)
		s.keepSession(log, target, config.RoleReviewer, number, run.SessionID)
		if run.Result.Result != agent.ResultDone {
			s.stopAfterBlocked(ctx, log, target, settings, stopOfReviewerRequest(req), "Reviewer", number, run.Result.BlockedReason)
			return
		}
	}
	s.endReview(ctx, log, target, settings, number, req, abnormal)
}

// action is the action of the request: the request of the cause, the
// request of the review again, or the first request of the review.
func (r reviewerRequest) action() ActionName {
	switch {
	case r.cause != nil:
		return ActionRequestTheCause
	case r.again:
		return ActionRequestTheReviewAgain
	}
	return ActionRequestTheReview
}

// kind is the request kind of reviewer.md, for the monitor file.
func (r reviewerRequest) kind() string {
	if r.cause != nil {
		return "explain the cause"
	}
	return "review"
}

// stopOfReviewerRequest is the action that stops the review after a request
// to the Reviewer that failed: "stop at the round limit" for the request of
// the cause, which only exists at the limit of rounds, and "stop the
// review" for a review request.
func stopOfReviewerRequest(req reviewerRequest) ActionName {
	if req.cause != nil {
		return ActionStopAtTheRoundLimit
	}
	return ActionStopTheReview
}

// stopForReviewerStart stops the review for a Maintainer when the second
// request of this stay did not start: the work directory was not prepared,
// or the agent was not started. A third request would fail the same way.
// After a first request that did not start, nothing changes here, and the
// next poll requests again. While cumin is stopping, and when the issue
// left cumin/status/reviewing, nothing changes either.
func (s *Service) stopForReviewerStart(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, number int, req reviewerRequest) {
	if !req.again || ctx.Err() != nil {
		return
	}
	token, err := target.Token(ctx)
	if err != nil {
		log.Error(string(stopOfReviewerRequest(req))+": no token; the next poll decides the end of the review", "error", err.Error())
		return
	}
	sub, ok := s.subIssueNow(ctx, log, target, number)
	if !ok || !ReviewNeedsFacts(sub, false) {
		return
	}
	a := StopReview{Number: number, Action: stopOfReviewerRequest(req), Reason: ReviewerNotStartedReason(), PullRequest: req.review.PullRequest, Retried: true}
	if _, err := s.applyReviewEnd(ctx, log, token, target, settings, sub, "", nil, false, a); err != nil {
		log.Error("the issue was not moved; the next poll decides again", "error", err.Error())
	}
}

// endReview decides the end of a Reviewer run that returned done or that
// ended abnormally (abnormal is then its end), as the poll does: it reads
// the issue again with the facts of the way out of cumin/status/reviewing,
// and applies what ReviewEnd decides. A step that starts an agent (the
// review fix, the review again, the cause) runs here, in the goroutine of
// the run, so the issue stays in work. While cumin stops after its runs, no
// such step starts and no label changes: the issue keeps
// cumin/status/reviewing, and the next start of cumin decides.
func (s *Service) endReview(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, number int, req reviewerRequest, abnormal *agent.AbnormalEnd) {
	token, err := target.Token(ctx)
	if err != nil {
		log.Error(string(req.action())+": no token; the next poll decides the end of the review", "error", err.Error())
		return
	}
	sub, defaultBranch, ok, err := s.reviewingNow(ctx, log, token, target, settings, number)
	if err != nil || !ok {
		return
	}
	// The run knows its own request, also when the state file lost it. Only
	// a run that returned done says that the Reviewer wrote no cause.
	cause := req.cause != nil
	sub.Reviewing.CauseRequested = cause && abnormal == nil
	sub.Reviewing.CauseRequestedAgain = sub.Reviewing.CauseRequestedAgain || (cause && req.again)
	sub.Reviewing.RequestedAgain = sub.Reviewing.RequestedAgain || (!cause && req.again)
	sub.Reviewing.RequestedHead = req.review.HeadCommit
	action := ReviewEnd(sub, false)
	if action == nil {
		log.Info(string(req.action())+": the end of the review was not decided; the next poll decides", "labels", sub.Labels)
		return
	}
	// A Maintainer needs the kind of the end to know where to look.
	if stop, ok := action.(StopReview); ok && abnormal != nil && stop.Retried {
		stop.Reason = AfterAbnormalEndReason(stop.Reason, "Reviewer", abnormal.Kind)
		action = stop
	}
	issueOwnerLogin := func(context.Context, string) (string, error) { return req.issueOwnerLogin, nil }
	// Only a run that returned done left its session for the next request.
	rest, err := s.applyReviewEnd(ctx, log, token, target, settings, sub, defaultBranch, issueOwnerLogin, abnormal == nil, action)
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
// one notification goes out.
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
	facts.CauseRequestedAgain = stored.CauseRequests > 1
	pr, ok := sub.LatestPullRequest()
	if !ok {
		log.Error(string(ActionRequestTheReview) + ": the pull request of the review is no longer open")
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
	return toComments(read), nil
}

// countReviewRequest counts, in the state file, one request of this stay in
// cumin/status/reviewing before it starts: a second request of the review,
// or, with cause, a request of the cause. A request that did not start
// keeps its count. It returns the new count.
func (s *Service) countReviewRequest(repository string, number int, cause bool) (int, error) {
	stored := s.State.Issue(repository, number)
	count := &stored.ReviewRequests
	if cause {
		count = &stored.CauseRequests
	}
	*count++
	return *count, s.State.Set(repository, number, stored)
}

// applyReviewEnd applies one way out of cumin/status/reviewing that
// ReviewEnd decided, for the poll and for the end of a Reviewer run alike.
// It changes the label first where the action has one. It returns the rest
// of the step, which can take long (an agent run), or nil
// when the step ended: the poll runs the rest in a goroutine of its own,
// and the end of a run runs it in its goroutine.
//
// An error says that nothing more happened: the issue keeps
// cumin/status/reviewing, and the next poll decides again from the same
// facts. No request, comment, or notification goes out before the label
// changed, so none goes out twice. issueOwnerLogin gives the login of the
// Issue Owner for a review fix and for the review request of "ask for the
// merge decision". afterRun says that a Reviewer run of this stay just
// returned done and left its session; a poll passes false, because a restart
// of cumin can have cut the run before its session was kept, and so does the
// end of a run that ended abnormally.
func (s *Service) applyReviewEnd(ctx context.Context, log *slog.Logger, token string, target Target, settings *RepositorySettings, sub SubIssue, defaultBranch string, issueOwnerLogin func(ctx context.Context, token string) (string, error), afterRun bool, action Action) (func(context.Context), error) {
	owner, repo := target.Repository.Owner, target.Repository.Name
	repository := target.Repository.String()
	number := sub.Number
	pr, _ := sub.LatestPullRequest()
	move := func(name ActionName, label string) ([]string, error) {
		labels, err := s.moveIssue(ctx, token, target, number, sub.Labels, label)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		return labels, nil
	}
	switch a := action.(type) {
	case StopReview:
		labels, err := move(a.Action, LabelAwaitingDecision)
		if err != nil {
			return nil, err
		}
		if !a.Question {
			log.Warn(string(a.Action)+": the review stops for a Maintainer", "reason", a.Reason, "retried", a.Retried, "labels", labels)
			s.stopForMaintainer(ctx, log, target, settings, stop{
				action: a.Action, issue: number, labelDone: true, reason: a.Reason,
				comment: StopNote(a.Action, a.Reason, a.PullRequest, a.Retried),
			})
			return nil, nil
		}
		log.Info(string(ActionStopTheReview)+": the Reviewer asked a question; the issue waits for a Maintainer", "labels", labels)
		s.notify(ctx, log.With("action", ActionStopTheReview), settings.notificationOn(), notify.Notification{
			Action:     string(ActionStopTheReview),
			Reason:     "The Reviewer asked a question during the review.",
			Repository: repository,
			Subject:    fmt.Sprintf("issue #%d", number),
			Link:       github.IssueURL(owner, repo, number),
		})
	case BackToChecks:
		labels, err := move(ActionGoBackToTheChecks, LabelChecking)
		if err != nil {
			return nil, err
		}
		if a.HeadMoved {
			log.Info(string(ActionGoBackToTheChecks)+": the head commit moved during the review; the issue waits for the checks again",
				"head_commit", pr.HeadCommit, "labels", labels)
		} else {
			log.Info(string(ActionGoBackToTheChecks)+": a required check does not pass on the approved commit; the issue waits for the checks again", "labels", labels)
		}
	case AskMaintainerToMerge:
		log.Info(string(ActionAskForTheMergeDecision) + ": the Reviewer approved the head commit")
		return nil, s.askMaintainerToMerge(ctx, log, target, settings, token, sub, pr, issueOwnerLogin)
	case StopAtRoundLimit:
		labels, err := move(ActionStopAtTheRoundLimit, LabelAwaitingDecision)
		if err != nil {
			return nil, err
		}
		log.Info(string(ActionStopAtTheRoundLimit)+": the issue waits for a Maintainer", "labels", labels, "comment", a.Explanation.URL)
		s.notify(ctx, log.With("action", ActionStopAtTheRoundLimit), settings.notificationOn(), notify.Notification{
			Action:     string(ActionStopAtTheRoundLimit),
			Reason:     fmt.Sprintf("blocking comments remain after %d review rounds: %s", sub.Reviewing.Limit, strings.TrimSpace(firstLine(strings.TrimLeft(a.Explanation.Body, "\r\n")))),
			Repository: repository,
			Subject:    fmt.Sprintf("issue #%d", number),
			Link:       a.Explanation.URL,
		})
	case RequestReviewFix:
		permit, ok := s.permitStart(ctx, log, "review fix", config.RoleImplementer, target, number)
		if !ok {
			return nil, nil
		}
		login, err := issueOwnerLogin(ctx, token)
		if err != nil {
			return nil, fmt.Errorf(string(ActionRequestAReviewFix)+": read the Issue Owner login of issue #%d: %w", number, err)
		}
		if err := s.startStay(repository, number, false); err != nil {
			return nil, fmt.Errorf(string(ActionRequestAReviewFix)+": keep the start of the stay of issue #%d in implementing: %w", number, err)
		}
		labels, err := move(ActionRequestAReviewFix, LabelImplementing)
		if err != nil {
			return nil, err
		}
		log.Info(string(ActionRequestAReviewFix)+": the Reviewer requested changes; the issue goes back to the Implementer",
			"round", a.Round, "limit", sub.Reviewing.Limit, "review", a.Review.URL, "labels", labels)
		branch := pr.HeadBranch
		return func(ctx context.Context) {
			s.runImplementer(ctx, target, settings, number, implementerRequest{
				action: ActionRequestAReviewFix, kind: "review fix", title: sub.Title, branch: branch, pullRequest: pr.Number,
				sessionID:       s.State.Issue(repository, number).SessionID,
				issueOwnerLogin: login, permit: permit,
				text: func(workDir string) string {
					return ReviewFixRequestText(repository, number, pr.Number, branch, workDir, a.Review.URL)
				},
			})
		}, nil
	case RequestReviewAgain, RequestCause:
		cause, isCause := a.(RequestCause)
		name := ActionRequestTheReviewAgain
		request := "review again"
		if isCause {
			name, request = ActionRequestTheCause, "cause"
		}
		permit, ok := s.permitStart(ctx, log, request, config.RoleReviewer, target, number)
		if !ok {
			return nil, nil
		}
		req, err := s.reviewRequestOf(ctx, token, target, settings, number, pr)
		if err != nil {
			return nil, err
		}
		req.permit = permit
		count, err := s.countReviewRequest(repository, number, isCause)
		if err != nil {
			return nil, fmt.Errorf("%s: count the request to the Reviewer of issue #%d: %w", name, number, err)
		}
		if isCause {
			// The cause goes on in the session of the last Reviewer run,
			// which holds the rounds.
			log.Info(string(name)+": blocking comments remain at the limit of rounds", "limit", req.review.Limit)
			req.cause = &cause.Review
			req.again = count > 1
			req.sessionID = s.State.Issue(repository, number).ReviewerSessionID
		} else {
			// The second request has the round of the first one: no review
			// of this round is on GitHub. After a run of this stay that
			// returned done, it goes on in the session of that run, with
			// the short text. At a poll, and after an abnormal end, the
			// state file can hold no session of this stay, so the request
			// is the whole review request, in the session that a first
			// request of this round takes (reviewRequestOf).
			req.again = true
			if req.resumes = afterRun; afterRun {
				req.sessionID = s.State.Issue(repository, number).ReviewerSessionID
			}
		}
		return func(ctx context.Context) { s.runReviewer(ctx, target, settings, number, req) }, nil
	case StartMerge:
		labels, err := move(ActionStartTheMerge, LabelMerging)
		if err != nil {
			return nil, err
		}
		log.Info(string(ActionStartTheMerge)+": the Reviewer approved the head commit", "pull_request", a.PullRequest, "head_commit", pr.HeadCommit, "labels", labels)
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
	issueOwnerLogin := func(ctx context.Context, token string) (string, error) {
		return s.readIssueOwnerLogin(ctx, token, target, number)
	}
	rest, err := s.applyReviewEnd(ctx, log.With("issue", number), token, target, settings, sub, snapshot.DefaultBranch, issueOwnerLogin, false, action)
	if err != nil || rest == nil {
		return err
	}
	// The rest is the review fix of the Implementer, or a request to the
	// Reviewer.
	run := agentRun{role: config.RoleReviewer, request: "review", title: sub.Title}
	switch action.(type) {
	case RequestReviewFix:
		run.role, run.request = config.RoleImplementer, "review fix"
	case RequestCause:
		run.request = "explain the cause"
	}
	s.goInWork(ctx, target, number, run, rest)
	return nil
}

// MissingReviewReason is the sentence of the stop after the second run
// without a review on the head commit, for the comment and the
// notification alike.
const MissingReviewReason = "cumin requested the review twice, but the latest review of the Reviewer is not on the head commit of the pull request with APPROVE or REQUEST_CHANGES."

// MissingCauseReason is the sentence of "stop at the round limit" after the
// second request of the cause of one stay that left no decision request, for
// the comment and the notification alike.
const MissingCauseReason = "Blocking comments remain at the limit of review rounds. cumin requested the cause from the Reviewer twice, but the Reviewer wrote no decision request on the pull request after its last review."

// MissingExplanationReason is the sentence of "stop at the round limit"
// when the Reviewer wrote no decision request, for the comment and the
// notification alike.
const MissingExplanationReason = "Blocking comments remain at the limit of review rounds, and the Reviewer reported done, but it wrote no decision request on the pull request after its last review."
