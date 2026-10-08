package workflow

// This file applies the actions of a requirement issue that start and end a
// Planner run for the split: "request the split" and the way out of
// cumin/status/planning (the check of the split). It also holds what the
// split and the acceptance check share: the Planner request, the Planner
// run, and the read of the requirement issue at the end of a run.
// acceptance.go holds the acceptance check. docs/ja/designs/poll.md, the
// topics on the request to the Planner and on the end of a run.

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cloveclovedev/cumin-works/internal/agent"
	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/notify"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
)

// plan applies "request the split": replace the status label of the
// requirement issue with cumin/status/planning, and only then request the
// split. When the label change fails, nothing is requested; the next poll
// decides again.
//
// With Again, it applies "request the split again": the issue is already in
// cumin/status/planning, and the Planner left no split that passes the
// check. The count of the state file is raised when the run is about to
// start (runPlanner), so that the request is sent once for each stay in
// cumin/status/planning, and a start that failed does not use it up.
func (s *Service) plan(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, p Plan) error {
	action := ActionRequestTheSplit
	if p.Again {
		action = ActionRequestTheSplitAgain
	}
	requirement, ok := snapshot.RequirementIssue(p.Number)
	if !ok {
		return fmt.Errorf("%s: issue #%d is not in the snapshot", action, p.Number)
	}
	req := planRequest
	req.title = requirement.Title
	if req.permit, ok = s.permitStart(ctx, s.logger().With("repository", target.Repository.String(), "issue", p.Number), "split", config.RolePlanner, target, p.Number); !ok {
		return nil
	}
	// The poll read the Issue Owner of the newest cumin/status/ready before
	// the decision (readReadyOwners); "request the split" holds only with
	// that Issue Owner.
	req.issueOwnerLogin = requirement.ReadyOwner
	repository := target.Repository.String()
	if p.Again {
		var err error
		if req.issueOwnerLogin, err = s.readIssueOwnerLogin(ctx, token, target, p.Number); err != nil {
			return fmt.Errorf(string(ActionRequestTheSplitAgain)+": read the Issue Owner login of issue #%d: %w", p.Number, err)
		}
		req.again, req.count = true, true
		s.logger().Info(string(ActionRequestTheSplitAgain)+": the split does not pass the check; the split is requested again",
			"repository", repository, "issue", p.Number)
		return s.goPlanner(ctx, target, settings, p.Number, req)
	}
	// A new stay in cumin/status/planning starts with a count of zero.
	if err := s.State.Clear(repository, p.Number); err != nil {
		return fmt.Errorf(string(ActionRequestTheSplit)+": clear the state of issue #%d: %w", p.Number, err)
	}
	labels, err := s.moveIssue(ctx, token, target, p.Number, requirement.Labels, LabelPlanning)
	if err != nil {
		return fmt.Errorf(string(ActionRequestTheSplit)+": %w", err)
	}
	s.logger().Info(string(ActionRequestTheSplit)+": moved the requirement issue to planning",
		"repository", target.Repository.String(), "issue", p.Number, "labels", labels)
	return s.goPlanner(ctx, target, settings, p.Number, req)
}

// countPlannerRequest changes, in the state file, how many times the
// request of the requirement issue was sent again during this stay: the
// split or the acceptance check. delta is 1 before the second request
// starts, and -1 when that run did not start.
func (s *Service) countPlannerRequest(repository string, number int, work plannerWork, delta int) error {
	stored := s.State.Issue(repository, number)
	if work == workAcceptanceCheck {
		stored.AcceptanceRequests = max(0, stored.AcceptanceRequests+delta)
	} else {
		stored.SplitRequests = max(0, stored.SplitRequests+delta)
	}
	return s.State.Set(repository, number, stored)
}

// notStarted takes back the count of a second request whose run did not
// start, so that the next poll sends that request. counted says that the
// count was raised for this start.
func (s *Service) notStarted(log *slog.Logger, repository string, number int, work plannerWork, counted bool) {
	if !counted {
		return
	}
	if err := s.countPlannerRequest(repository, number, work, -1); err != nil {
		log.Error(string(work.requestAgain())+": the count of the request that did not start was not taken back", "error", err.Error())
	}
}

// plannerRequest is one kind of request to the Planner.
type plannerRequest struct {
	// work says what the Planner does: a split or an acceptance check.
	work plannerWork
	// kind is the request kind of planner.md, for the log and for the
	// monitor file.
	kind string
	// title is the title of the requirement issue, for the monitor file.
	title string
	text  func(repository string, number int, workDir string) string
	// issueOwnerLogin is the login of the Issue Owner for the facts of the
	// request, read before the request. Empty says that there is none.
	issueOwnerLogin string
	// sessionID is the session that an acceptance check that is requested
	// again resumes. Empty starts a new session.
	sessionID string
	// again says that the request was already sent again during this stay
	// in cumin/status/planning or in cumin/status/accepting.
	again bool
	// count says that the state file does not hold this second request
	// yet. runPlanner counts it when the work directory is ready, and takes
	// the count back when the agent did not start.
	count bool
	// permit is the permit of the start of this request (permitStart).
	permit StartPermit
}

var (
	planRequest       = plannerRequest{work: workSplit, kind: "plan", text: PlanRequestText}
	acceptanceRequest = plannerRequest{work: workAcceptanceCheck, kind: "acceptance check", text: AcceptanceRequestText}
)

// plannerWork is what one request asks of the Planner.
type plannerWork int

const (
	workSplit plannerWork = iota
	workAcceptanceCheck
)

// request is the action that requests the work for the first time.
func (w plannerWork) request() ActionName {
	if w == workAcceptanceCheck {
		return ActionRequestTheAcceptanceCheck
	}
	return ActionRequestTheSplit
}

// requestAgain is the action that requests the work once more.
func (w plannerWork) requestAgain() ActionName {
	if w == workAcceptanceCheck {
		return ActionRequestTheAcceptanceCheckAgain
	}
	return ActionRequestTheSplitAgain
}

// stop is the action that stops the work for a Maintainer.
func (w plannerWork) stop() ActionName {
	if w == workAcceptanceCheck {
		return ActionStopTheAcceptanceCheck
	}
	return ActionStopTheSplit
}

// action is the action of the request: the first request of the work, or
// the request again.
func (r plannerRequest) action() ActionName {
	if r.again {
		return r.work.requestAgain()
	}
	return r.work.request()
}

// goPlanner runs one Planner request in its own goroutine, as the Implementer
// runs, so that the poll goes on.
func (s *Service) goPlanner(ctx context.Context, target Target, settings *RepositorySettings, number int, req plannerRequest) error {
	if s.Agents == nil {
		return fmt.Errorf("%s: request the %s for issue #%d: no agent service is configured", req.action(), req.kind, number)
	}
	run := agentRun{role: config.RolePlanner, request: req.kind, title: req.title}
	s.goInWork(ctx, target, number, run, func(ctx context.Context) {
		s.runPlanner(ctx, target, settings, number, req)
	})
	return nil
}

// runPlanner prepares the work directory and runs one Planner request in a
// new session. The work directory is a detached checkout of the default
// branch, made again for every request: the Planner only reads, and an
// acceptance check must read the work of every merged sub-issue, which a
// worktree of an earlier request does not hold (agent-run.md, the topic on
// the work directory). The retry after an abnormal end keeps the work
// directory of the first run, and the work directory is removed when the
// request ends.
//
// The end of the run of a split is decided from the facts on GitHub
// (runSplit), and so is the end of the run of an acceptance check
// (runAcceptanceCheck).
func (s *Service) runPlanner(ctx context.Context, target Target, settings *RepositorySettings, number int, req plannerRequest) {
	log := s.logger().With("repository", target.Repository.String(), "issue", number, "role", config.RolePlanner)
	s.noteRequest(target.Repository.String(), number, config.RolePlanner, req.kind)
	checkout := agent.Checkout{
		Owner: target.Repository.Owner,
		Repo:  target.Repository.Name,
		Issue: number,
		Role:  config.RolePlanner,
	}
	if err := s.Workspace.Remove(ctx, checkout); err != nil {
		log.Error(string(req.action())+": the work directory of an earlier request was not removed", "error", err.Error())
		return
	}
	workDir, err := s.Workspace.Prepare(ctx, target.RemoteURL, checkout)
	if err != nil {
		log.Error(string(req.action())+": the work directory was not prepared", "error", err.Error())
		return
	}
	// The work directory holds nothing after the run: the Planner only
	// reads, and the next request makes it again. Removing it here leaves
	// no worktree behind when the requirement issue closes, which the poll
	// never reads again (principle 6). It is removed also while cumin
	// stops, so the removal does not use the ending context.
	defer func() {
		if err := s.Workspace.Remove(context.WithoutCancel(ctx), checkout); err != nil {
			log.Error("cleanup: the work directory of the Planner was not removed", "error", err.Error())
		}
	}()
	if req.count {
		if err := s.countPlannerRequest(target.Repository.String(), number, req.work, 1); err != nil {
			log.Error(string(req.work.requestAgain())+": the request was not counted; the next poll decides again", "error", err.Error())
			return
		}
	}
	log.Info(string(req.action())+": requested the Planner", "kind", req.kind)
	request := startRequest(target, settings, config.RolePlanner, number, req.issueOwnerLogin, req.text(target.Repository.String(), number, workDir), workDir)
	if req.work == workAcceptanceCheck {
		request.SessionID = req.sessionID
		s.runAcceptanceCheck(ctx, log, target, settings, number, request, req.permit, req.again, req.count)
		return
	}

	s.runSplit(ctx, log, target, settings, number, request, req)
}

// runSplit runs the split, and decides its end as the poll does: it reads
// the requirement issue again, and SplitEnd decides from the facts on
// GitHub. So every end is the same case: a done result, an abnormal end,
// and a run that a restart of cumin cut off, which the next poll finds.
// Only a blocked result is not read from GitHub: cumin posts the
// blocked_reason and stops the issue for a Maintainer at once. When the read
// before that stop fails for a temporary reason, cumin writes nothing on
// GitHub: the log holds the whole blocked_reason, one notification goes
// out, the issue keeps cumin/status/planning, and the next poll decides
// from the facts.
//
// req.again says that the split was already requested again during this
// stay in cumin/status/planning. When the facts ask for the second request,
// it runs here in the same work directory. When every sub-issue is closed,
// the issue moves to cumin/status/accepting, and the acceptance check runs
// here. While cumin is stopping, nothing is requested and no label changes.
// At a quota limit, the second request is not sent and not counted. A
// failed read changes nothing. In each of these cases the issue keeps
// cumin/status/planning, and a later poll decides.
func (s *Service) runSplit(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, number int, request agent.StartRequest, req plannerRequest) {
	repository := target.Repository.String()
	again, counted, permit := req.again, req.count, req.permit
	action := req.action()
	for {
		end := s.runRequest(ctx, log, target, number, permit, request, keepNoSession)
		switch end.kind {
		case runStopping:
			return
		case runNotStarted:
			s.notStarted(log, repository, number, workSplit, counted)
			return
		case runBlocked:
			log.Warn("the agent returned blocked", "reason", firstLine(strings.TrimSpace(end.run.Result.BlockedReason)))
			if err := s.stopAfterPlannerBlocked(ctx, log, target, settings, number, req.work.stop(), end.run.Result.BlockedReason, true); err != nil {
				log.Warn(string(req.work.stop())+": the stop after blocked failed for a temporary reason; the next poll decides", "reason", err.Error())
			}
			return
		}

		token, err := target.Token(ctx)
		if err != nil {
			log.Error(string(action)+": no token; the next poll decides the end of the split", "error", err.Error())
			return
		}
		requirement, err := s.requirementIssueNow(ctx, log, target, number)
		if err != nil {
			return
		}
		s.readRequirementFacts(ctx, log, token, target, settings, &requirement)
		requirement.SplitRequestedAgain = requirement.SplitRequestedAgain || again
		snapshot := Snapshot{RequirementIssues: []RequirementIssue{requirement}}
		switch a := SplitEnd(requirement, false).(type) {
		case ReviewPlan:
			if err := s.reviewPlan(ctx, token, target, snapshot, settings, a); err != nil {
				log.Error("the requirement issue was not moved; the next poll decides again", "error", err.Error())
			}
			return
		case StopSplit:
			if err := s.stopSplit(ctx, token, target, snapshot, settings, a); err != nil {
				log.Error("the requirement issue was not moved; the next poll decides again", "error", err.Error())
			}
			return
		case CheckAcceptance:
			acceptance := acceptanceRequest
			var ok bool
			if acceptance.permit, ok = s.permitStart(ctx, log, "acceptance check", config.RolePlanner, target, number); !ok {
				return
			}
			log.Info(string(ActionRequestTheAcceptanceCheck)+": every sub-issue is closed; the acceptance check follows", "sub_issues", len(requirement.SubIssues))
			if err := s.moveToAccepting(ctx, token, target, requirement); err != nil {
				log.Error("the requirement issue was not moved; the next poll decides again", "error", err.Error())
				return
			}
			acceptance.issueOwnerLogin = req.issueOwnerLogin
			s.runPlanner(ctx, target, settings, number, acceptance)
			return
		case Plan:
			var ok bool
			if permit, ok = s.permitStart(ctx, log, "split again", config.RolePlanner, target, number); !ok {
				return
			}
			if err := s.countPlannerRequest(repository, number, workSplit, 1); err != nil {
				log.Error(string(ActionRequestTheSplitAgain)+": the request was not counted; the next poll decides again", "error", err.Error())
				return
			}
			again, counted = true, true
			action = ActionRequestTheSplitAgain
			log.Info(string(ActionRequestTheSplitAgain) + ": the split does not pass the check; the same request runs again in the same work directory")
		default:
			log.Info(string(action)+": the end of the split was not decided; the next poll decides", "labels", requirement.Labels)
			return
		}
	}
}

// clearRequirementState removes what the state file holds for a requirement
// issue that left cumin/status/planning or cumin/status/accepting. A
// failure is logged: the next stay clears the entry before its request.
func (s *Service) clearRequirementState(log *slog.Logger, repository string, number int) {
	if err := s.State.Clear(repository, number); err != nil {
		log.Error("the state of the requirement issue was not cleared", "error", err.Error())
	}
}

// PlannerQuestionNotWrittenReason is the sentence of the notification
// after a blocked result of the Planner whose blocked_reason cumin did not
// write, because the read of the issue failed for a temporary reason.
const PlannerQuestionNotWrittenReason = "The Planner asked a question." + notWrittenNote

// stopAfterPlannerBlocked stops the requirement issue for a Maintainer after a
// blocked result, on a new read of the requirement issue. comment is the
// blocked_reason of the Planner.
//
// With leave, a temporary failure of the read is returned, and nothing is
// written on GitHub: the read comes before every write of the stop, so the
// issue keeps its label for the next poll. cumin keeps nothing for that
// poll, so the question of the Planner would be lost: the whole
// blocked_reason goes to the log, and one notification goes out that
// says so. Every other failed read stops the issue without a label change.
func (s *Service) stopAfterPlannerBlocked(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, number int, action ActionName, comment string, leave bool) error {
	requirement, err := s.requirementIssueNow(ctx, log, target, number)
	if leave && temporary(err) != nil {
		log = log.With("action", action)
		log.Error(string(action)+": the issue was not read after blocked; the issue keeps its label, and the blocked_reason was not written on the issue; the whole text is here",
			"error", err.Error(), "comment", comment)
		s.notify(ctx, log, settings.notificationOn(), notify.Notification{
			Action:     string(action),
			Reason:     PlannerQuestionNotWrittenReason,
			Repository: target.Repository.String(),
			Subject:    fmt.Sprintf("issue #%d", number),
			Link:       github.IssueURL(target.Repository.Owner, target.Repository.Name, number),
		})
		return err
	}
	s.stopForMaintainer(ctx, log, target, settings, stop{
		action:  action,
		issue:   number,
		labels:  labelsOf(requirement, err),
		reason:  "the Planner returned blocked: " + firstLine(strings.TrimSpace(comment)),
		comment: comment,
	})
	return nil
}

// reviewPlan applies "ask for the plan review": the split
// passes the check and a sub-issue is open, so the requirement issue moves
// from cumin/status/planning to cumin/status/awaiting-plan-review and a
// notification goes out. The next poll no longer sees
// cumin/status/planning, so one split sends one notification.
func (s *Service) reviewPlan(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, a ReviewPlan) error {
	labels, err := s.moveRequirement(ctx, token, target, snapshot, a.Number, LabelAwaitingPlanReview)
	if err != nil {
		return fmt.Errorf(string(ActionAskForThePlanReview)+": %w", err)
	}
	requirement, _ := snapshot.RequirementIssue(a.Number)
	log := s.logger().With("repository", target.Repository.String(), "issue", a.Number, "action", ActionAskForThePlanReview)
	log.Info(string(ActionAskForThePlanReview)+": the split waits for a Maintainer", "sub_issues", len(requirement.SubIssues), "labels", labels)
	s.clearRequirementState(log, target.Repository.String(), a.Number)
	s.notify(ctx, log, settings.notificationOn(), notify.Notification{
		Action:     string(ActionAskForThePlanReview),
		Reason:     "The split of the requirement issue needs a review.",
		Repository: target.Repository.String(),
		Subject:    fmt.Sprintf("issue #%d", a.Number),
		Link:       github.IssueURL(target.Repository.Owner, target.Repository.Name, a.Number),
	})
	return nil
}

// stopSplit applies "stop the split": the requirement issue
// moves from cumin/status/planning to cumin/status/awaiting-decision, and
// a notification goes out. After a question of the Planner, its comment holds
// the reason, and cumin writes none. Otherwise cumin writes the reason on
// the issue.
//
// The label moves first. When that fails, nothing else happens: the state
// file keeps the count of the second request, so the next poll decides the
// same stop and requests nothing.
func (s *Service) stopSplit(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, a StopSplit) error {
	log := s.logger().With("repository", target.Repository.String(), "issue", a.Number)
	labels, err := s.moveRequirement(ctx, token, target, snapshot, a.Number, LabelAwaitingDecision)
	if err != nil {
		return fmt.Errorf(string(ActionStopTheSplit)+": %w", err)
	}
	s.clearRequirementState(log, target.Repository.String(), a.Number)
	if !a.Question {
		log.Warn(string(ActionStopTheSplit)+": the split failed the check after two requests; the issue waits for a Maintainer", "reason", a.Reason, "labels", labels)
		s.stopForMaintainer(ctx, log, target, settings, stop{
			action:    ActionStopTheSplit,
			issue:     a.Number,
			labelDone: true,
			reason:    a.Reason,
			comment:   StopNote(ActionStopTheSplit, a.Reason, 0, true),
		})
		return nil
	}
	log = log.With("action", ActionStopTheSplit)
	log.Info(string(ActionStopTheSplit)+": the Planner asked a question during the split; the issue waits for a Maintainer", "labels", labels)
	s.notify(ctx, log, settings.notificationOn(), notify.Notification{
		Action:     string(ActionStopTheSplit),
		Reason:     "The Planner asked a question during the split.",
		Repository: target.Repository.String(),
		Subject:    fmt.Sprintf("issue #%d", a.Number),
		Link:       github.IssueURL(target.Repository.Owner, target.Repository.Name, a.Number),
	})
	return nil
}

// readRequirementFacts reads, for one requirement issue that was read again
// at the end of a Planner run, what the poll reads for SplitEnd and for
// AcceptanceEnd: the account that added its status label, the time of that
// label, and the comments. A label that does not count ends the read: the
// end of the run decides nothing from it.
func (s *Service) readRequirementFacts(ctx context.Context, log *slog.Logger, token string, target Target, settings *RepositorySettings, requirement *RequirementIssue) {
	status := statusLabel(requirement.Labels)
	if status != LabelPlanning && status != LabelAccepting {
		return
	}
	s.readStatusOfRequirement(ctx, log, token, target, settings, requirement)
	if !statusCounts(*requirement) {
		return
	}
	times, _, err := s.GitHub.ReadLabelTimes(ctx, token, target.Repository.Owner, target.Repository.Name, requirement.Number)
	if err != nil {
		log.Error("the label times were not read", "error", err.Error())
	} else {
		requirement.LabelTimesRead = true
		requirement.ReviewAt = times[requirement.Number][status]
	}
	s.readAcceptanceComment(ctx, log, token, target, requirement)
}

// requirementIssueNow reads one requirement issue again with its
// sub-issues, as subIssueNow does for a sub-issue. The read fails for an
// issue that a poll does not read: a closed one, or one without the
// requirement label (issue-states.md, principle 6). The error is logged
// here; the caller tells a temporary failure from it.
func (s *Service) requirementIssueNow(ctx context.Context, log *slog.Logger, target Target, number int) (RequirementIssue, error) {
	token, err := target.Token(ctx)
	if err != nil {
		log.Error("the issue was not read again: no token", "error", err.Error())
		return RequirementIssue{}, err
	}
	read, err := s.GitHub.ReadRequirementIssue(ctx, token, target.Repository.Owner, target.Repository.Name, number)
	if err != nil {
		log.Error("the issue was not read again", "error", err.Error())
		return RequirementIssue{}, err
	}
	log.Debug("read the issue again", "issue", number, "rate_limit_cost", read.RateLimit.Cost, "rate_limit_remaining", read.RateLimit.Remaining)
	return toRequirementIssue(read.Issue), nil
}
