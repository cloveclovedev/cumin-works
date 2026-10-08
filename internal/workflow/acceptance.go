package workflow

// This file applies the acceptance check of a requirement issue: "request
// the acceptance check", the run of the check, and the way out of
// cumin/status/accepting ("ask for the acceptance", the request again,
// "stop the acceptance check"). It also reads the acceptance check comment.
// plan.go holds the Planner request and the Planner run that this file
// uses. docs/ja/designs/poll.md, the topics on the request to the Planner
// and on the end of a run.

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

// checkAcceptance applies "request the acceptance check" (from
// cumin/status/implementing, and from cumin/status/planning with every
// sub-issue closed): replace the status label of the requirement issue
// with cumin/status/accepting, and only then request the acceptance check.
// When the label change fails, nothing is requested; the next poll decides
// again. No ready of a Maintainer is needed.
//
// With Again, it applies "request the acceptance check again": the issue is
// already in cumin/status/accepting, and the Planner left no result. The
// count of the state file is raised when the run is about to start
// (runPlanner), so that the request is sent once for each stay in
// cumin/status/accepting, and a start that failed does not use it up. The
// request resumes the session of the first run when the state file holds
// one.
func (s *Service) checkAcceptance(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, a CheckAcceptance) error {
	repository := target.Repository.String()
	action := ActionRequestTheAcceptanceCheck
	if a.Again {
		action = ActionRequestTheAcceptanceCheckAgain
	}
	requirement, ok := snapshot.RequirementIssue(a.Number)
	if !ok {
		return fmt.Errorf("%s: issue #%d is not in the snapshot", action, a.Number)
	}
	req := acceptanceRequest
	req.title = requirement.Title
	if req.permit, ok = s.permitStart(ctx, s.logger().With("repository", repository, "issue", a.Number), "acceptance check", config.RolePlanner, target, a.Number); !ok {
		return nil
	}
	var err error
	if req.issueOwnerLogin, err = s.readIssueOwnerLogin(ctx, token, target, a.Number); err != nil {
		return fmt.Errorf("%s: read the Issue Owner login of issue #%d: %w", action, a.Number, err)
	}
	if a.Again {
		req.again, req.count = true, true
		req.sessionID = s.State.Issue(repository, a.Number).SessionID
		s.logger().Info(string(ActionRequestTheAcceptanceCheckAgain)+": the Planner left no acceptance check comment; the acceptance check is requested again",
			"repository", repository, "issue", a.Number)
		return s.goPlanner(ctx, target, settings, a.Number, req)
	}
	if err := s.moveToAccepting(ctx, token, target, requirement); err != nil {
		return err
	}
	return s.goPlanner(ctx, target, settings, a.Number, req)
}

// moveToAccepting starts a new stay of the requirement issue in
// cumin/status/accepting: it clears the state file, so that the stay starts
// with no session and a count of zero, and then replaces the status label.
func (s *Service) moveToAccepting(ctx context.Context, token string, target Target, requirement RequirementIssue) error {
	repository := target.Repository.String()
	if err := s.State.Clear(repository, requirement.Number); err != nil {
		return fmt.Errorf(string(ActionRequestTheAcceptanceCheck)+": clear the state of issue #%d: %w", requirement.Number, err)
	}
	labels, err := s.moveIssue(ctx, token, target, requirement.Number, requirement.Labels, LabelAccepting)
	if err != nil {
		return fmt.Errorf(string(ActionRequestTheAcceptanceCheck)+": %w", err)
	}
	s.logger().Info(string(ActionRequestTheAcceptanceCheck)+": moved the requirement issue to accepting", "repository", repository, "issue", requirement.Number, "labels", labels)
	return nil
}

// runAcceptanceCheck runs the acceptance check, and decides its end as the
// poll does: it reads the requirement issue again, and AcceptanceEnd decides
// from the facts on GitHub. So every end is the same case: a done result, an
// abnormal end, and a run that a restart of cumin cut off, which the next
// poll finds. Only a blocked result is not read from GitHub: cumin posts the
// blocked_reason and stops the issue for a Maintainer at once.
//
// again says that the acceptance check was already requested again during
// this stay in cumin/status/accepting, and counted that the state file got
// the count of that request for this start: a run that does not start
// takes it back. When the facts ask for the second
// request, it runs here in the session of the first run and in the same
// work directory. While cumin is stopping, nothing is requested again and
// no label changes. A failed read changes nothing: the issue keeps
// cumin/status/accepting, and the next poll decides.
func (s *Service) runAcceptanceCheck(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, number int, request agent.StartRequest, permit StartPermit, again, counted bool) {
	repository := target.Repository.String()
	action := ActionRequestTheAcceptanceCheck
	if again {
		action = ActionRequestTheAcceptanceCheckAgain
	}
	for {
		end := s.runRequest(ctx, log, target, number, permit, request, keepResumableSession)
		switch end.kind {
		case runStopping:
			return
		case runNotStarted:
			s.notStarted(log, repository, number, workAcceptanceCheck, counted)
			return
		case runBlocked:
			log.Warn("the agent returned blocked", "reason", firstLine(strings.TrimSpace(end.run.Result.BlockedReason)))
			_ = s.stopAfterPlannerBlocked(ctx, log, target, settings, number, ActionStopTheAcceptanceCheck, end.run.Result.BlockedReason, false)
			s.clearRequirementState(log, repository, number)
			return
		}

		token, err := target.Token(ctx)
		if err != nil {
			log.Error(string(action)+": no token; the next poll decides the end of the acceptance check", "error", err.Error())
			return
		}
		requirement, err := s.requirementIssueNow(ctx, log, target, number)
		if err != nil {
			return
		}
		s.readRequirementFacts(ctx, log, token, target, settings, &requirement)
		requirement.AcceptanceRequestedAgain = requirement.AcceptanceRequestedAgain || again
		snapshot := Snapshot{RequirementIssues: []RequirementIssue{requirement}}
		switch a := AcceptanceEnd(requirement, false).(type) {
		case Accept:
			if err := s.accept(ctx, token, target, snapshot, settings, a); err != nil {
				log.Error("the requirement issue was not moved; the next poll decides again", "error", err.Error())
			}
			return
		case StopAcceptance:
			if err := s.stopAcceptance(ctx, token, target, snapshot, settings, a); err != nil {
				log.Error("the requirement issue was not moved; the next poll decides again", "error", err.Error())
			}
			return
		case CheckAcceptance:
			var ok bool
			if permit, ok = s.permitStart(ctx, log, "acceptance check again", config.RolePlanner, target, number); !ok {
				return
			}
			if err := s.countPlannerRequest(repository, number, workAcceptanceCheck, 1); err != nil {
				log.Error(string(ActionRequestTheAcceptanceCheckAgain)+": the request was not counted; the next poll decides again", "error", err.Error())
				return
			}
			again, counted = true, true
			action = ActionRequestTheAcceptanceCheckAgain
			request.SessionID = s.State.Issue(repository, number).SessionID
			log.Info(string(ActionRequestTheAcceptanceCheckAgain) + ": the Planner left no acceptance check comment; the acceptance check is requested again")
		default:
			log.Info(string(action)+": the end of the acceptance check was not decided; the next poll decides", "labels", requirement.Labels)
			return
		}
	}
}

// NoAcceptanceCheckReason is the sentence of "stop the acceptance check"
// when the Planner left no comment after two requests.
const NoAcceptanceCheckReason = "The Planner left no acceptance check comment. cumin requested the acceptance check again, and the Planner left no comment again."

// stopAcceptance applies "stop the acceptance check": the
// requirement issue moves from cumin/status/accepting to
// cumin/status/awaiting-decision, and a notification goes out. After a
// question of the Planner, its comment holds the reason, and cumin writes
// none. Otherwise cumin writes the reason on the issue.
//
// The label moves first. When that fails, nothing else happens: the state
// file keeps the count of the second request, so the next poll decides the
// same stop and requests nothing.
func (s *Service) stopAcceptance(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, a StopAcceptance) error {
	log := s.logger().With("repository", target.Repository.String(), "issue", a.Number)
	labels, err := s.moveRequirement(ctx, token, target, snapshot, a.Number, LabelAwaitingDecision)
	if err != nil {
		return fmt.Errorf(string(ActionStopTheAcceptanceCheck)+": %w", err)
	}
	s.clearRequirementState(log, target.Repository.String(), a.Number)
	if !a.Question {
		log.Info(string(ActionStopTheAcceptanceCheck)+": the Planner left no acceptance check comment after two requests; the issue waits for a Maintainer", "labels", labels)
		s.stopForMaintainer(ctx, log, target, settings, stop{
			action:    ActionStopTheAcceptanceCheck,
			issue:     a.Number,
			labelDone: true,
			reason:    NoAcceptanceCheckReason,
			comment:   StopNote(ActionStopTheAcceptanceCheck, NoAcceptanceCheckReason, 0, true),
		})
		return nil
	}
	log = log.With("action", ActionStopTheAcceptanceCheck)
	log.Info(string(ActionStopTheAcceptanceCheck)+": the Planner asked a question during the acceptance check; the issue waits for a Maintainer", "labels", labels)
	s.notify(ctx, log, settings.notificationOn(), notify.Notification{
		Action:     string(ActionStopTheAcceptanceCheck),
		Reason:     "The Planner asked a question during the acceptance check.",
		Repository: target.Repository.String(),
		Subject:    fmt.Sprintf("issue #%d", a.Number),
		Link:       github.IssueURL(target.Repository.Owner, target.Repository.Name, a.Number),
	})
	return nil
}

// accept applies "ask for the acceptance": the acceptance check comment
// exists, so the requirement issue moves to
// cumin/status/awaiting-acceptance and a notification goes out, whatever
// the table of the comment says. The next poll no longer sees
// cumin/status/accepting, so one comment sends one notification.
func (s *Service) accept(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, a Accept) error {
	labels, err := s.moveRequirement(ctx, token, target, snapshot, a.Number, LabelAwaitingAcceptance)
	if err != nil {
		return fmt.Errorf(string(ActionAskForTheAcceptance)+": %w", err)
	}
	log := s.logger().With("repository", target.Repository.String(), "issue", a.Number, "action", ActionAskForTheAcceptance)
	log.Info(string(ActionAskForTheAcceptance)+": the requirement issue waits for the acceptance of a Maintainer", "labels", labels)
	s.clearRequirementState(log, target.Repository.String(), a.Number)
	owner, repo := target.Repository.Owner, target.Repository.Name
	s.notify(ctx, log, settings.notificationOn(), notify.Notification{
		Action:     string(ActionAskForTheAcceptance),
		Reason:     "The acceptance check is done; the requirement issue can be accepted.",
		Repository: target.Repository.String(),
		Subject:    fmt.Sprintf("issue #%d", a.Number),
		Link:       github.IssueURL(owner, repo, a.Number),
	})
	return nil
}

// readAcceptanceComments adds the facts of the acceptance check to each
// requirement issue of the snapshot that "request the acceptance check",
// "ask for the acceptance", or the end of the acceptance check needs them
// for (NeedsComments), and that the way out of
// cumin/status/planning needs them for (SplitNeedsFacts).
func (s *Service) readAcceptanceComments(ctx context.Context, log *slog.Logger, token string, target Target, snapshot *Snapshot) {
	for i := range snapshot.RequirementIssues {
		if requirement := &snapshot.RequirementIssues[i]; NeedsComments(*requirement) || SplitNeedsFacts(*requirement, snapshot.Running[requirement.Number]) {
			s.readAcceptanceComment(ctx, log, token, target, requirement)
		}
	}
}

// readAcceptanceComment adds the time of the acceptance check comment and
// of the decision request of the Planner, and whether the state file says
// that the acceptance check or the split was requested again. A comment counts only when
// the Planner App wrote it. A failed read is logged and leaves CommentsRead
// false, so no row applies to that requirement issue in this poll.
func (s *Service) readAcceptanceComment(ctx context.Context, log *slog.Logger, token string, target Target, requirement *RequirementIssue) {
	if s.Agents == nil {
		return
	}
	planner, err := s.Agents.BotLogin(ctx, target.Repository.Owner, config.RolePlanner)
	if err != nil {
		log.Error(string(ActionRequestTheAcceptanceCheck)+": the login of the Planner App was not read", "issue", requirement.Number, "error", err.Error())
		return
	}
	// In cumin/status/planning, only a question after that label counts. A
	// sub-issue that closes later must not hide it. Without the time of the
	// label, nothing is decided, so nothing is read. cumin-core posts the
	// blocked_reason of the Planner there, so its decision request is a
	// question too.
	since := lastClose(*requirement)
	askers := []string{planner}
	if statusLabel(requirement.Labels) == LabelPlanning {
		if !requirement.LabelTimesRead || requirement.ReviewAt.IsZero() {
			return
		}
		since = requirement.ReviewAt
		if target.Login != nil {
			core, err := target.Login(ctx)
			if err != nil {
				log.Error(string(ActionStopTheSplit)+": the login of cumin-core was not read", "issue", requirement.Number, "error", err.Error())
				return
			}
			askers = append(askers, core)
		}
	}
	read, rate, err := s.GitHub.ReadIssueComments(ctx, token, target.Repository.Owner, target.Repository.Name, requirement.Number, since)
	if err != nil {
		log.Error(string(ActionRequestTheAcceptanceCheck)+": the comments were not read", "issue", requirement.Number, "error", err.Error())
		return
	}
	log.Info(string(ActionRequestTheAcceptanceCheck)+": read the comments", "issue", requirement.Number,
		"rate_limit_cost", rate.Cost, "rate_limit_remaining", rate.Remaining)
	comments := toComments(read)
	requirement.CommentsRead = true
	requirement.AcceptanceCheckAt = AcceptanceCheckAt(comments, planner)
	requirement.QuestionAt = QuestionAt(comments, askers...)
	stored := s.State.Issue(target.Repository.String(), requirement.Number)
	requirement.AcceptanceRequestedAgain = stored.AcceptanceRequests > 0
	requirement.SplitRequestedAgain = stored.SplitRequests > 0
}
