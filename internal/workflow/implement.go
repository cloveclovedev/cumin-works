package workflow

// This file starts the Implementer and applies the way out of
// cumin/status/implementing: the claim ("request the implementation"), the
// steps on the required checks ("request a check fix", "stop for missing
// checks", the copy of the labels to the pull request), the Implementer
// run, the facts that the end of a run needs, and the end itself ("wait for
// the checks", "stop the implementation", "request the implementation
// again"). docs/ja/designs/poll.md, the topics on the request to the
// Implementer and on the end of a run.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cloveclovedev/cumin-works/internal/agent"
	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/notify"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
)

// claim applies "request the implementation": replace the status label of
// the sub-issue with
// cumin/status/implementing, and only then request the work. When the label
// change fails, nothing is requested; the next poll decides again.
func (s *Service) claim(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, c Claim) error {
	sub, ok := snapshot.SubIssue(c.Number)
	if !ok {
		return fmt.Errorf(string(ActionRequestTheImplementation)+": issue #%d is not in the snapshot", c.Number)
	}
	permit, ok := s.permitStart(ctx, s.logger().With("repository", target.Repository.String(), "issue", c.Number), "claim", config.RoleImplementer, target, c.Number)
	if !ok {
		return nil
	}
	// A Maintainer added cumin/status/ready, so the work starts again from a
	// new session and a count of zero (issue-states.md, the section on the
	// sessions of an agent). This comes before the label change: a state
	// that cumin cannot clear would resume the old session of a request in
	// the same session ("request a check fix") after a restart, which the
	// intervention of a Maintainer must end. The issue keeps
	// cumin/status/ready, so the next poll
	// claims it again.
	if err := s.State.Clear(target.Repository.String(), c.Number); err != nil {
		return fmt.Errorf(string(ActionRequestTheImplementation)+": clear the state of issue #%d: %w", c.Number, err)
	}
	labels, err := s.moveIssue(ctx, token, target, c.Number, sub.Labels, LabelImplementing)
	if err != nil {
		return fmt.Errorf(string(ActionRequestTheImplementation)+": %w", err)
	}
	log := s.logger().With("repository", target.Repository.String(), "issue", c.Number)
	log.Info(string(ActionRequestTheImplementation)+": claimed the issue",
		"requirement_issue", c.RequirementIssue, "labels", labels)
	// The poll read the Issue Owner of the newest cumin/status/ready before
	// the decision (readReadyIssueOwners); "request the implementation" holds only
	// with that Issue Owner.
	if err := s.startImplementer(ctx, permit, target, settings, sub, sub.ReadyIssueOwner); err != nil {
		return fmt.Errorf(string(ActionRequestTheImplementation)+": request the work for issue #%d: %w", c.Number, err)
	}
	return nil
}

// copyLabels applies "copy the labels to the pull request": the pull
// request gets the cumin/status/* and
// risk/* labels of the issue that it closes. A pull request is an issue on
// the labels endpoint, so the call is the one for an issue.
func (s *Service) copyLabels(ctx context.Context, token string, target Target, a CopyLabels) error {
	owner, repo := target.Repository.Owner, target.Repository.Name
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, a.PullRequest, a.Labels); err != nil {
		return fmt.Errorf(string(ActionCopyTheLabelsToThePullRequest)+": copy the labels of issue #%d to pull request #%d: %w", a.Issue, a.PullRequest, err)
	}
	s.logger().Info(string(ActionCopyTheLabelsToThePullRequest)+": copied the labels of the issue to the pull request",
		"repository", target.Repository.String(), "issue", a.Issue,
		"pull_request", a.PullRequest, "labels", a.Labels)
	return nil
}

// implementerRequest is one request to the Implementer: its action, its kind,
// the branch of its worktree, the session that it resumes, and its text.
type implementerRequest struct {
	// action starts the log lines of the request: the action of
	// issue-states.md that requests the work.
	action ActionName
	// kind is the request kind of implementer.md, for the log and for the
	// monitor file.
	kind string
	// title is the title of the issue, for the monitor file.
	title  string
	branch string
	// pullRequest is the open pull request whose work the request
	// continues, or 0 for a first request.
	pullRequest int
	// sessionID resumes that session. Empty starts a new session.
	sessionID string
	// issueOwnerLogin is the login of the Issue Owner for the facts of the
	// request, read before the label changed. Empty says that there is none.
	issueOwnerLogin string
	// text builds the request text once the work directory is known.
	text func(workDir string) string
	// again says that the implementation was already requested again
	// during this stay in cumin/status/implementing.
	again bool
	// count says that the state file does not hold this second request
	// yet. runImplementer counts it before it prepares the work directory,
	// and takes the count back when the agent did not start.
	count bool
	// permit is the permit of the start of this request (permitStart).
	permit StartPermit
}

// stopForUnreportedChecks applies "stop for missing checks": a required
// check has not reported on
// the head commit of the pull request within the wait time of the
// repository, or no open pull request closes the issue. The issue goes to
// a Maintainer through the stop step with that action. No agent starts: cumin
// writes what it sees, and a Maintainer finds the cause.
//
// The label changes first: until it changes, the next poll decides the same
// stop, and must not post the comment and notify again (principle 3).
func (s *Service) stopForUnreportedChecks(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, a StopForUnreportedChecks) error {
	log := s.logger().With("repository", target.Repository.String(), "issue", a.Number, "pull_request", a.PullRequest)
	sub, ok := snapshot.SubIssue(a.Number)
	if !ok {
		return fmt.Errorf(string(ActionStopForMissingChecks)+": issue #%d is not in the snapshot", a.Number)
	}
	if a.PullRequest == 0 {
		log.Warn(string(ActionStopForMissingChecks)+": no open pull request closes the issue after the wait time", "waited", a.Waited.String())
	} else {
		names := make([]string, 0, len(a.Unreported))
		for _, check := range a.Unreported {
			names = append(names, check.Name)
		}
		log.Warn(string(ActionStopForMissingChecks)+": the required checks did not report within the wait time",
			"head_commit", a.HeadCommit, "unreported", names, "waited", a.Waited.String())
	}
	labels, err := s.moveIssue(ctx, token, target, a.Number, sub.Labels, LabelAwaitingDecision)
	if err != nil {
		return fmt.Errorf(string(ActionStopForMissingChecks)+": %w", err)
	}
	log.Info(string(ActionStopForMissingChecks)+": the issue waits for a Maintainer", "labels", labels)
	reason := UnreportedChecksReason(a)
	s.stopForMaintainer(ctx, log, target, settings, stop{
		action:    ActionStopForMissingChecks,
		issue:     a.Number,
		labels:    labels,
		reason:    reason,
		comment:   StopNote(ActionStopForMissingChecks, reason, a.PullRequest, false),
		labelDone: true,
	})
	return nil
}

// fixChecks applies "request a check fix": a required check failed on the
// head commit of the
// pull request. Below the limit of the repository, the count grows by one,
// the status label becomes cumin/status/implementing, and the Implementer
// fixes the checks in the session of its last run, on the branch of the
// pull request. At the limit, the issue goes to a Maintainer through the
// stop step with the action "stop for failed checks".
//
// The count is saved before the label changes: a count that cumin cannot
// keep would let the requests run past the limit, so the label stays and
// the next poll tries again. The same write starts the new stay in
// cumin/status/implementing. The label changes before the request, so that
// a later poll never requests the same fix twice (principle 3).
func (s *Service) fixChecks(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, a FixChecks) error {
	owner, repo := target.Repository.Owner, target.Repository.Name
	repository := target.Repository.String()
	log := s.logger().With("repository", repository, "issue", a.Number, "pull_request", a.PullRequest)
	sub, ok := snapshot.SubIssue(a.Number)
	if !ok {
		return fmt.Errorf(string(ActionRequestACheckFix)+": issue #%d is not in the snapshot", a.Number)
	}
	pr, ok := sub.LatestPullRequest()
	if !ok {
		return fmt.Errorf(string(ActionRequestACheckFix)+": issue #%d has no open pull request", a.Number)
	}
	names := make([]string, 0, len(a.Failed))
	for _, check := range a.Failed {
		names = append(names, check.Name)
	}

	stored := s.State.Issue(repository, a.Number)
	limit := settings.Settings.MaxCheckFixRequests
	if !CheckFixAllowed(stored.CheckFixRequests, limit) {
		reason := fmt.Sprintf("A required check failed again (%s) after %d check fix requests, the limit of this repository (max_check_fix_requests).",
			strings.Join(names, ", "), stored.CheckFixRequests)
		log.Warn(string(ActionStopForFailedChecks)+": the limit of check fix requests is reached", "failed", names, "check_fix_requests", stored.CheckFixRequests)
		// The label first: until it changes, the next poll decides the same
		// stop, and must not post the comment and notify again.
		labels, err := s.moveIssue(ctx, token, target, a.Number, sub.Labels, LabelAwaitingDecision)
		if err != nil {
			return fmt.Errorf(string(ActionStopForFailedChecks)+": %w", err)
		}
		log.Info(string(ActionStopForFailedChecks)+": the issue waits for a Maintainer", "labels", labels)
		s.stopForMaintainer(ctx, log, target, settings, stop{
			action:    ActionStopForFailedChecks,
			issue:     a.Number,
			labels:    labels,
			reason:    reason,
			comment:   StopNote(ActionStopForFailedChecks, reason, pr.Number, false),
			labelDone: true,
		})
		return nil
	}

	// The stop at the limit above starts no agent, so it needs no permit.
	permit, ok := s.permitStart(ctx, log, "check fix", config.RoleImplementer, target, a.Number)
	if !ok {
		return nil
	}
	issueOwnerLogin, err := s.readIssueOwnerLogin(ctx, token, target, a.Number)
	if err != nil {
		return fmt.Errorf(string(ActionRequestACheckFix)+": read the Issue Owner login of issue #%d: %w", a.Number, err)
	}
	counted := stored
	counted.CheckFixRequests++
	counted.ImplementationRequests, counted.ConflictResolution = 0, false
	if err := s.State.Set(repository, a.Number, counted); err != nil {
		return fmt.Errorf(string(ActionRequestACheckFix)+": keep the count of check fix requests of issue #%d: %w", a.Number, err)
	}
	labels, err := s.moveIssue(ctx, token, target, a.Number, sub.Labels, LabelImplementing)
	if err != nil {
		// No request starts, so the count goes back: a label that fails
		// again must not use up the limit without a single fix.
		if undo := s.State.Set(repository, a.Number, stored); undo != nil {
			log.Error(string(ActionRequestACheckFix)+": the count of check fix requests was not set back", "error", undo.Error())
		}
		return fmt.Errorf(string(ActionRequestACheckFix)+": %w", err)
	}
	stored = counted
	log.Info(string(ActionRequestACheckFix)+": a required check failed; the issue goes back to the Implementer",
		"failed", names, "check_fix_requests", stored.CheckFixRequests, "labels", labels)

	failed := make([]github.RequiredCheck, 0, len(a.Failed))
	for _, check := range a.Failed {
		failed = append(failed, github.RequiredCheck{Name: check.Name, Integration: check.Integration})
	}
	contents := s.GitHub.FailedCheckContent(ctx, token, owner, repo, pr.HeadCommit, failed, log)
	texts := make([]string, 0, len(contents))
	for _, content := range contents {
		texts = append(texts, content.Content)
	}
	branch := pr.HeadBranch
	if branch == "" {
		branch = BranchName(sub.Number, sub.Title)
	}
	return s.goImplementer(ctx, target, settings, a.Number, implementerRequest{
		action: ActionRequestACheckFix, kind: "check fix", title: sub.Title, branch: branch, pullRequest: pr.Number, sessionID: stored.SessionID,
		issueOwnerLogin: issueOwnerLogin, permit: permit,
		text: func(workDir string) string {
			return CheckFixRequestText(repository, a.Number, pr.Number, branch, workDir, texts)
		},
	})
}

// startImplementer requests the work of "request the implementation" from
// the Implementer. The
// request kind is "implement", or "continue" when an open pull request
// already closes the issue; the work then goes on on the branch of that
// pull request (ClaimBranch). The session is new in both cases
// (issue-states.md, the section on the sessions of an agent).
func (s *Service) startImplementer(ctx context.Context, permit StartPermit, target Target, settings *RepositorySettings, sub SubIssue, issueOwnerLogin string) error {
	branch, pullRequest := ClaimBranch(sub)
	repository := target.Repository.String()
	req := implementerRequest{
		action: ActionRequestTheImplementation, kind: "implement", title: sub.Title, branch: branch, issueOwnerLogin: issueOwnerLogin, permit: permit,
		text: func(workDir string) string {
			return ImplementRequestText(repository, sub.Number, branch, workDir)
		},
	}
	if pullRequest != 0 {
		req.kind, req.pullRequest = "continue", pullRequest
		req.text = func(workDir string) string {
			return ContinueRequestText(repository, sub.Number, pullRequest, branch, workDir)
		}
	}
	return s.goImplementer(ctx, target, settings, sub.Number, req)
}

// goImplementer runs one Implementer request in its own goroutine, so that
// the poll goes on while the agent works. What the goroutine does (the
// worktree, the start, the end of the run) is only logged and handled by
// the end of the run (the check of the pull request after the Implementer
// ends).
func (s *Service) goImplementer(ctx context.Context, target Target, settings *RepositorySettings, number int, req implementerRequest) error {
	if s.Agents == nil {
		return errors.New("no agent service is configured")
	}
	run := agentRun{role: config.RoleImplementer, request: req.kind, title: req.title}
	s.goInWork(ctx, target, number, run, func(ctx context.Context) {
		s.runImplementer(ctx, target, settings, number, req)
	})
	return nil
}

// countImplementationRequest changes, in the state file, how many times the
// implementation was requested again during this stay in
// cumin/status/implementing. delta is 1 before the second request starts,
// and -1 when that run did not start.
func (s *Service) countImplementationRequest(repository string, number, delta int) error {
	stored := s.State.Issue(repository, number)
	stored.ImplementationRequests = max(0, stored.ImplementationRequests+delta)
	return s.State.Set(repository, number, stored)
}

// runImplementer prepares the worktree and runs one Implementer request,
// and decides its end as the poll does: it reads the issue again with the
// facts of the way out of cumin/status/implementing, and ImplementationEnd
// decides from the facts on GitHub. So every end is the same case: a done
// result, an abnormal end, and a run that a restart of cumin cut off, which
// the next poll finds. Only a blocked result is not read from GitHub: cumin
// posts the blocked_reason and stops the issue for a Maintainer at once.
//
// When the facts ask for the second request, it runs here in the same work
// directory, in the kept session when the state file holds one. The quota
// decides before the request is counted, so a run that hit the quota limit
// does not use up the one second request. While cumin is stopping, nothing
// is requested and no label changes. A failed read changes nothing: the
// issue keeps cumin/status/implementing, and the next poll decides.
//
// A second request of a poll is counted before the work directory is
// prepared. A work directory that is not prepared sends no request: the
// next poll requests the implementation again, and the second failure stops
// the implementation for a Maintainer (stopForWorkDirectory).
func (s *Service) runImplementer(ctx context.Context, target Target, settings *RepositorySettings, number int, req implementerRequest) {
	log := s.logger().With("repository", target.Repository.String(), "issue", number, "role", config.RoleImplementer)
	s.noteRequest(target.Repository.String(), number, config.RoleImplementer, req.kind)
	checkout := agent.Checkout{
		Owner:  target.Repository.Owner,
		Repo:   target.Repository.Name,
		Issue:  number,
		Role:   config.RoleImplementer,
		Branch: req.branch,
	}
	repository := target.Repository.String()
	if req.count {
		if err := s.countImplementationRequest(repository, number, 1); err != nil {
			log.Error(string(req.action)+": the request was not counted; the next poll decides again", "error", err.Error())
			return
		}
	}
	// A request on an open pull request (a continuation of "request the
	// implementation", a check fix) starts from the pull request on GitHub.
	// A worktree of an
	// earlier round can be on another branch, or behind commits that were
	// pushed since, and Prepare reuses a worktree as it is; so it goes, and
	// Prepare creates the worktree again from origin/<branch>. A worktree
	// that holds work that is not on GitHub stays: a run that cumin stopped
	// leaves its work there, and a Maintainer restarts the issue with
	// cumin/status/ready.
	if req.pullRequest != 0 {
		removed, err := s.Workspace.RemoveIfPushed(ctx, checkout)
		if err != nil {
			log.Error(string(req.action)+": the worktree of an earlier round was not checked", "error", err.Error())
			s.stopForWorkDirectory(ctx, log, target, settings, number, req)
			return
		}
		if !removed {
			log.Warn(string(req.action)+": the worktree of an earlier round holds work that is not on GitHub; it is used as it is", "branch", req.branch)
		}
	}
	workDir, err := s.Workspace.Prepare(ctx, target.RemoteURL, checkout)
	if err != nil {
		log.Error(string(req.action)+": the work directory was not prepared", "error", err.Error())
		s.stopForWorkDirectory(ctx, log, target, settings, number, req)
		return
	}
	log.Info(string(req.action)+": requested the work", "kind", req.kind, "branch", req.branch,
		"pull_request", req.pullRequest, "resumed", req.sessionID != "")
	request := startRequest(target, settings, config.RoleImplementer, number, req.issueOwnerLogin, req.text(workDir), workDir)
	request.SessionID = req.sessionID

	again, counted, permit := req.again, req.count, req.permit
	action := req.action
	for {
		end := s.runRequest(ctx, log, target, number, permit, request, keepEndedSession)
		abnormal := end.abnormal
		switch end.kind {
		case runStopping:
			return
		case runNotStarted:
			if counted {
				if err := s.countImplementationRequest(repository, number, -1); err != nil {
					log.Error(string(ActionRequestTheImplementationAgain)+": the count of the request that did not start was not taken back", "error", err.Error())
				}
			}
			return
		case runBlocked:
			s.stopAfterBlocked(ctx, log, target, settings, ActionStopTheImplementation, "Implementer", number, end.run.Result.BlockedReason)
			return
		}

		token, err := target.Token(ctx)
		if err != nil {
			log.Error(string(action)+": no token; the next poll decides the end of the implementation", "error", err.Error())
			return
		}
		sub, ok := s.implementingNow(ctx, log, token, target, settings, number, req.branch, action)
		if !ok {
			return
		}
		sub.Implementing.RequestedAgain = sub.Implementing.RequestedAgain || again
		switch a := ImplementationEnd(sub, false).(type) {
		case WaitForChecks:
			if err := s.waitForChecks(ctx, log, token, target, settings, sub, a); err != nil {
				log.Error("the issue was not moved; the next poll decides again", "error", err.Error())
			}
			return
		case StopImplementation:
			// A Maintainer needs the kind of the end to know where to look.
			if abnormal != nil && !a.Question {
				a.Reason = AfterAbnormalEndReason(a.Reason, "Implementer", abnormal.Kind)
			}
			if err := s.stopImplementation(ctx, log, token, target, settings, sub, a); err != nil {
				log.Error("the issue was not moved; the next poll decides again", "error", err.Error())
			}
			return
		case RequestImplementationAgain:
			if permit, ok = s.permitStart(ctx, log, "implementation again", config.RoleImplementer, target, number); !ok {
				return
			}
			if err := s.countImplementationRequest(repository, number, 1); err != nil {
				log.Error(string(ActionRequestTheImplementationAgain)+": the request was not counted; the next poll decides again", "error", err.Error())
				return
			}
			again, counted = true, true
			action = ActionRequestTheImplementationAgain
			request.SessionID = s.State.Issue(repository, number).SessionID
			log.Info(string(ActionRequestTheImplementationAgain)+": the pull request does not pass the check; the same request runs again in the same work directory",
				"resumed", request.SessionID != "")
		default:
			log.Info(string(action)+": the end of the implementation was not decided; the next poll decides", "labels", sub.Labels)
			return
		}
	}
}

// stopForWorkDirectory stops the implementation for a Maintainer when the work
// directory of the second request of this stay was not prepared: the first
// request and the second one both sent nothing to the Implementer, and a
// third one would fail the same way. After the first failure nothing
// changes here, and the next poll requests the implementation again. While
// cumin is stopping, and when the issue left cumin/status/implementing,
// nothing changes either.
func (s *Service) stopForWorkDirectory(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, number int, req implementerRequest) {
	if !req.again || ctx.Err() != nil {
		return
	}
	token, err := target.Token(ctx)
	if err != nil {
		log.Error(string(ActionStopTheImplementation)+": no token; the next poll decides the end of the implementation", "error", err.Error())
		return
	}
	sub, ok := s.subIssueNow(ctx, log, target, number)
	if !ok || !ImplementationNeedsFacts(sub, false) {
		return
	}
	a := StopImplementation{Number: number, Reason: WorkDirectoryReason(), PullRequest: req.pullRequest, Retried: true}
	if err := s.stopImplementation(ctx, log, token, target, settings, sub, a); err != nil {
		log.Error("the issue was not moved; the next poll decides again", "error", err.Error())
	}
}

// readImplementingFacts adds the facts of the way out of
// cumin/status/implementing to each sub-issue of the snapshot that needs
// them (ImplementationNeedsFacts): it reads that issue again, so that the
// decision judges on the pull requests and the labels of this moment. A
// failed read leaves the facts out, so nothing is decided for that issue in
// this poll.
func (s *Service) readImplementingFacts(ctx context.Context, log *slog.Logger, token string, target Target, settings *RepositorySettings, snapshot *Snapshot) {
	readFacts(log, snapshot, ImplementationNeedsFacts, func(log *slog.Logger, number int) (SubIssue, bool) {
		return s.implementingNow(ctx, log, token, target, settings, number, "", "")
	})
}

// implementingNow reads one implementation issue again, and only that
// issue, with the facts that ImplementationEnd decides from: the account
// and the time of the newest cumin/status/implementing, the decision
// requests after it, the open pull requests of the branch, the head commit
// of the worktree, and what the state file holds for this stay. branch is the branch of
// the run; the poll passes none and takes the branch that a claim would
// choose (ClaimBranch). action is the action of the request in work, and
// the log messages start with it; the poll passes none.
//
// The second value is false when a read failed, and when the issue is not
// an open issue in cumin/status/implementing any more: nothing is decided
// then. A label that does not count ends the read, and cumin notifies
// once. A worktree that the Host does not hold gives an empty head commit,
// so the pull request does not pass the check.
func (s *Service) implementingNow(ctx context.Context, log *slog.Logger, token string, target Target, settings *RepositorySettings, number int, branch string, action ActionName) (SubIssue, bool) {
	prefix := ""
	if action != "" {
		prefix = string(action) + ": "
	}
	owner, repo := target.Repository.Owner, target.Repository.Name
	sub, _, err := s.readSubIssueAgain(ctx, log, token, target, number)
	if err != nil {
		log.Error(prefix+"the issue was not read again; the next poll decides", "error", err.Error())
		return SubIssue{}, false
	}
	if !ImplementationNeedsFacts(sub, false) {
		log.Info(prefix+"the issue is not in cumin/status/implementing; nothing changes", "labels", sub.Labels)
		return sub, false
	}
	actor, counts, err := s.readStatusActor(ctx, token, target, number, LabelImplementing, false)
	if err != nil {
		log.Error("the actor of the newest "+LabelImplementing+" was not read; the next poll decides", "error", err.Error())
		return sub, false
	}
	facts := &ImplementingFacts{StatusCounts: counts, ImplementingAt: actor.At, MaxLinks: github.MaxOpenClosingPullRequests}
	sub.Implementing = facts
	if !counts {
		s.tellStatusOfAnother(ctx, log, target, settings, number, LabelImplementing, actor)
		return sub, true
	}
	if s.Agents == nil {
		return sub, false
	}
	if facts.Implementer, err = s.Agents.BotLogin(ctx, owner, config.RoleImplementer); err != nil {
		log.Error(prefix+"the login of the Implementer App was not read; the next poll decides", "error", err.Error())
		return sub, false
	}
	// cumin-core posts the blocked_reason of the Implementer, so its
	// decision request is a question too.
	askers := []string{facts.Implementer}
	if target.Login != nil {
		core, err := target.Login(ctx)
		if err != nil {
			log.Error(prefix+"the login of cumin-core was not read; the next poll decides", "error", err.Error())
			return sub, false
		}
		askers = append(askers, core)
	}
	comments, rate, err := s.GitHub.ReadIssueComments(ctx, token, owner, repo, number, facts.ImplementingAt)
	if err != nil {
		log.Error(prefix+"the comments were not read; the next poll decides", "error", err.Error())
		return sub, false
	}
	log.Debug(prefix+"read the comments", "rate_limit_cost", rate.Cost, "rate_limit_remaining", rate.Remaining)
	facts.QuestionAt = QuestionAt(toComments(comments), askers...)
	if facts.Branch = branch; branch == "" {
		facts.Branch, _ = ClaimBranch(sub)
	}
	listed, err := s.GitHub.ListOpenPullRequestsOfBranch(ctx, token, owner, repo, facts.Branch)
	if err != nil {
		log.Error(prefix+"the open pull requests of the branch were not read; the next poll decides", "branch", facts.Branch, "error", err.Error())
		return sub, false
	}
	for _, pr := range listed {
		facts.OnBranch = append(facts.OnBranch, PullRequest{Number: pr.Number, NodeID: pr.NodeID, HeadCommit: pr.HeadCommit, HeadBranch: pr.HeadBranch, Author: pr.Author})
	}
	workDir := s.Workspace.Dir(agent.Checkout{Owner: owner, Repo: repo, Issue: number, Role: config.RoleImplementer, Branch: facts.Branch})
	if facts.LocalHead, err = s.Workspace.Head(ctx, workDir); err != nil {
		log.Info(prefix+"the head commit of the work directory was not read", "error", err.Error())
		facts.LocalHead = ""
	}
	stored := s.State.Issue(target.Repository.String(), number)
	facts.RequestedAgain, facts.ConflictRequested = stored.ImplementationRequests > 0, stored.ConflictResolution
	return sub, true
}

// waitForChecks applies "wait for the checks": the pull request passes
// the check. When the issue has no closing link to the pull request,
// cumin-core adds it and reads the issue once more to see it; then the
// status label becomes cumin/status/checking. A link that GitHub refuses,
// and a link that is still missing, hand the issue back to a Maintainer
// through the stop step, with one sentence.
//
// A failed read or label change is returned and changes nothing more: the
// issue keeps cumin/status/implementing, and the next poll decides again
// from the same facts.
func (s *Service) waitForChecks(ctx context.Context, log *slog.Logger, token string, target Target, settings *RepositorySettings, sub SubIssue, a WaitForChecks) error {
	stopI2 := func(reason string) {
		s.stopForMaintainer(ctx, log, target, settings, stop{
			action:  ActionStopTheImplementation,
			issue:   a.Number,
			labels:  sub.Labels,
			reason:  reason,
			comment: StopNote(ActionStopTheImplementation, reason, a.PullRequest, false),
		})
	}
	if a.AddLink {
		pr := a.PullRequest
		if err := s.GitHub.AddClosingLink(ctx, token, sub.NodeID, a.PullRequestNodeID); err != nil {
			if temporary(err) != nil {
				return fmt.Errorf(string(ActionWaitForTheChecks)+": add the closing link of issue #%d: %w", a.Number, err)
			}
			log.Warn(string(ActionStopTheImplementation)+": the closing link was not added", "pull_request", pr, "error", err.Error())
			stopI2(LinkFailedReason(pr, closingLinkAnswer(err)))
			return nil
		}
		again, _, err := s.readSubIssueAgain(ctx, log, token, target, a.Number)
		if err != nil {
			return fmt.Errorf(string(ActionWaitForTheChecks)+": read issue #%d after the closing link: %w", a.Number, err)
		}
		sub = again
		if !linksPullRequest(sub, pr) {
			log.Warn(string(ActionStopTheImplementation)+": the closing link is missing after cumin-core added it", "pull_request", pr)
			stopI2(LinkMissingReason(pr))
			return nil
		}
		log.Info(string(ActionWaitForTheChecks)+": added the closing link", "pull_request", pr)
	}
	labels, err := s.moveIssue(ctx, token, target, a.Number, sub.Labels, LabelChecking)
	if err != nil {
		return fmt.Errorf(string(ActionWaitForTheChecks)+": %w", err)
	}
	log.Info(string(ActionWaitForTheChecks)+": verified the pull request", "pull_request", a.PullRequest, "labels", labels)
	return nil
}

// stopImplementation applies "stop the implementation": the
// implementation issue moves from cumin/status/implementing to
// cumin/status/awaiting-decision, and cumin notifies. After a
// question of the Implementer, its comment holds the reason, and cumin
// writes none. Otherwise cumin writes the reason on the issue.
//
// The label moves first. When that fails, nothing else happens: the state
// file keeps the count of the second request, so the next poll decides the
// same stop and requests nothing.
func (s *Service) stopImplementation(ctx context.Context, log *slog.Logger, token string, target Target, settings *RepositorySettings, sub SubIssue, a StopImplementation) error {
	owner, repo := target.Repository.Owner, target.Repository.Name
	labels, err := s.moveIssue(ctx, token, target, a.Number, sub.Labels, LabelAwaitingDecision)
	if err != nil {
		return fmt.Errorf(string(ActionStopTheImplementation)+": %w", err)
	}
	if !a.Question {
		log.Warn(string(ActionStopTheImplementation)+": the implementation stops for a Maintainer", "reason", a.Reason, "pull_request", a.PullRequest, "retried", a.Retried, "labels", labels)
		s.stopForMaintainer(ctx, log, target, settings, stop{
			action:    ActionStopTheImplementation,
			issue:     a.Number,
			labelDone: true,
			reason:    a.Reason,
			comment:   StopNote(ActionStopTheImplementation, a.Reason, a.PullRequest, a.Retried),
		})
		return nil
	}
	log = log.With("action", ActionStopTheImplementation)
	log.Info(string(ActionStopTheImplementation)+": the Implementer asked a question; the issue waits for a Maintainer", "labels", labels)
	s.notify(ctx, log, settings.notificationOn(), notify.Notification{
		Action:     string(ActionStopTheImplementation),
		Reason:     "The Implementer asked a question during the implementation.",
		Repository: target.Repository.String(),
		Subject:    fmt.Sprintf("issue #%d", a.Number),
		Link:       github.IssueURL(owner, repo, a.Number),
	})
	return nil
}

// requestImplementationAgain applies "request the implementation again" at
// a poll: the issue is in cumin/status/implementing, no Implementer runs,
// and the pull request does not pass the check. The work continues on the
// branch of the issue, in the kept session when the state file holds one.
// When the state file says that the stay is a conflict resolution, the
// request is the conflict resolution again, as at the end of a run.
// The count of the state file is raised when the run is about to start
// (runImplementer), so that the request is sent once for each stay in
// cumin/status/implementing, and a start that failed does not use it up.
func (s *Service) requestImplementationAgain(ctx context.Context, token string, target Target, settings *RepositorySettings, sub SubIssue, defaultBranch string, a RequestImplementationAgain) error {
	permit, ok := s.permitStart(ctx, s.logger().With("repository", target.Repository.String(), "issue", a.Number), "implementation again", config.RoleImplementer, target, a.Number)
	if !ok {
		return nil
	}
	issueOwnerLogin, err := s.readIssueOwnerLogin(ctx, token, target, a.Number)
	if err != nil {
		return fmt.Errorf(string(ActionRequestTheImplementationAgain)+": read the Issue Owner login of issue #%d: %w", a.Number, err)
	}
	repository := target.Repository.String()
	branch := sub.Implementing.Branch
	req := implementerRequest{
		action: ActionRequestTheImplementationAgain, kind: "implement", title: sub.Title, branch: branch, issueOwnerLogin: issueOwnerLogin, again: true, count: true, permit: permit,
		sessionID: s.State.Issue(repository, a.Number).SessionID,
		text: func(workDir string) string {
			return ImplementRequestText(repository, a.Number, branch, workDir)
		},
	}
	if _, pullRequest := ClaimBranch(sub); pullRequest != 0 {
		req.kind, req.pullRequest = "continue", pullRequest
		req.text = func(workDir string) string {
			return ContinueRequestText(repository, a.Number, pullRequest, branch, workDir)
		}
		if sub.Implementing.ConflictRequested {
			req.kind = "conflict resolution"
			req.text = func(workDir string) string {
				return ConflictResolutionRequestText(repository, a.Number, pullRequest, branch, workDir, defaultBranch)
			}
		}
	}
	s.logger().Info(string(ActionRequestTheImplementationAgain)+": the pull request does not pass the check; the implementation is requested again",
		"repository", repository, "issue", a.Number, "kind", req.kind)
	return s.goImplementer(ctx, target, settings, a.Number, req)
}

// closingLinkAnswer is the answer of GitHub in an error of AddClosingLink,
// or the whole error when it holds no answer.
func closingLinkAnswer(err error) string {
	var link *github.ClosingLinkError
	if errors.As(err, &link) {
		return link.Answer
	}
	return err.Error()
}
