package workflow

// This file applies the rows of a requirement issue that start and end a
// Planner run: R1 (start the split), the way out of cumin/status/planning
// (R2), and the acceptance check. docs/ja/designs/poll.md, the topics on
// the request to the Planner and on the end of a run.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/cloveclovedev/cumin-works/internal/agent"
	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/notify"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
)

// plan applies R1: replace the status label of the requirement issue with
// cumin/status/planning, and only then request the split. When the label
// change fails, nothing is requested; the next poll decides again.
//
// With Again, it applies "request the split again": the issue is already in
// cumin/status/planning, and the Planner left no split that passes the
// check. The count of the state file is raised when the run is about to
// start (runPlanner), so that the request is sent once for each stay in
// cumin/status/planning, and a start that failed does not use it up.
func (s *Service) plan(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, p Plan) error {
	owner, repo := target.Repository.Owner, target.Repository.Name
	requirement, ok := snapshot.RequirementIssue(p.Number)
	if !ok {
		return fmt.Errorf("R1: issue #%d is not in the snapshot", p.Number)
	}
	// Q1: the quota decides before the label changes.
	if ok, err := s.quotaAllowsStart(ctx, RowR1, config.RolePlanner, target, p.Number); err != nil || !ok {
		if err != nil {
			return fmt.Errorf("R1: issue #%d: %w", p.Number, err)
		}
		return nil
	}
	// The poll read the Owner of the newest cumin/status/ready before the
	// decision (readReadyOwners); R1 holds only with that Owner.
	req := planRequest
	req.ownerLogin = requirement.ReadyOwner
	repository := target.Repository.String()
	if p.Again {
		var err error
		if req.ownerLogin, err = s.readOwnerLogin(ctx, token, target, p.Number); err != nil {
			return fmt.Errorf("R2: read the login of the Owner of issue #%d: %w", p.Number, err)
		}
		req.again, req.count = true, true
		s.logger().Info("R2: the split does not pass the check; the split is requested again",
			"repository", repository, "issue", p.Number)
		return s.goPlanner(ctx, target, settings, p.Number, req)
	}
	// A new stay in cumin/status/planning starts with a count of zero.
	if err := s.State.Clear(repository, p.Number); err != nil {
		return fmt.Errorf("R1: clear the state of issue #%d: %w", p.Number, err)
	}
	labels := LabelsAfterPlan(requirement.Labels)
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, p.Number, labels); err != nil {
		return fmt.Errorf("R1: move issue #%d to planning: %w", p.Number, err)
	}
	s.logger().Info("R1: moved the requirement issue to planning",
		"repository", target.Repository.String(), "issue", p.Number, "labels", labels)
	return s.goPlanner(ctx, target, settings, p.Number, req)
}

// countPlannerRequest changes, in the state file, how many times the
// request of the requirement issue was sent again during this stay: the
// split for end R2, the acceptance check for end R4. delta is 1 before the
// second request starts, and -1 when that run did not start.
func (s *Service) countPlannerRequest(repository string, number int, end string, delta int) error {
	stored := s.State.Issue(repository, number)
	if end == RowR4 {
		stored.AcceptanceRequests = max(0, stored.AcceptanceRequests+delta)
	} else {
		stored.SplitRequests = max(0, stored.SplitRequests+delta)
	}
	return s.State.Set(repository, number, stored)
}

// notStarted takes back the count of a second request whose run did not
// start, so that the next poll sends that request. counted says that the
// count was raised for this start.
func (s *Service) notStarted(log *slog.Logger, repository string, number int, end string, counted bool) {
	if !counted {
		return
	}
	if err := s.countPlannerRequest(repository, number, end, -1); err != nil {
		log.Error(end+": the count of the request that did not start was not taken back", "error", err.Error())
	}
}

// checkAcceptance applies "request the acceptance check" (R4, and R2 with
// every sub-issue closed): replace the status label of the requirement
// issue with cumin/status/accepting, and only then request the acceptance
// check. When the label change fails, nothing is requested; the next poll
// decides again. No ready of the Owner is needed.
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
	requirement, ok := snapshot.RequirementIssue(a.Number)
	if !ok {
		return fmt.Errorf("R4: issue #%d is not in the snapshot", a.Number)
	}
	req := acceptanceRequest
	var err error
	if req.ownerLogin, err = s.readOwnerLogin(ctx, token, target, a.Number); err != nil {
		return fmt.Errorf("R4: read the login of the Owner of issue #%d: %w", a.Number, err)
	}
	if a.Again {
		req.again, req.count = true, true
		req.sessionID = s.State.Issue(repository, a.Number).SessionID
		s.logger().Info("R4: the Planner left no acceptance check comment; the acceptance check is requested again",
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
		return fmt.Errorf("R4: clear the state of issue #%d: %w", requirement.Number, err)
	}
	labels := ReplaceStatusLabel(requirement.Labels, LabelAccepting)
	if err := s.GitHub.SetIssueLabels(ctx, token, target.Repository.Owner, target.Repository.Name, requirement.Number, labels); err != nil {
		return fmt.Errorf("R4: move issue #%d to accepting: %w", requirement.Number, err)
	}
	s.logger().Info("R4: moved the requirement issue to accepting", "repository", repository, "issue", requirement.Number, "labels", labels)
	return nil
}

// plannerRequest is one kind of request to the Planner.
type plannerRequest struct {
	// start is the row that requested it, and end the row that judges the
	// end of the run: R1 and R2 for a split, R4 for an acceptance check.
	start, end string
	// kind is the request kind of planner.md, for the log.
	kind string
	text func(repository string, number int, workDir string) string
	// ownerLogin is the login of the Owner for the facts of the request,
	// read before the request. Empty says that there is none.
	ownerLogin string
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
}

var (
	planRequest       = plannerRequest{start: RowR1, end: RowR2, kind: "plan", text: PlanRequestText}
	acceptanceRequest = plannerRequest{start: RowR4, end: RowR4, kind: "acceptance check", text: AcceptanceRequestText}
)

// goPlanner runs one Planner request in its own goroutine, as the Implementer
// runs, so that the poll goes on.
func (s *Service) goPlanner(ctx context.Context, target Target, settings *RepositorySettings, number int, req plannerRequest) error {
	if s.Agents == nil {
		return fmt.Errorf("%s: request the %s for issue #%d: no agent service is configured", req.start, req.kind, number)
	}
	done := s.markInProgress(ctx, target.Repository.String(), number)
	s.running.Add(1)
	go func() {
		defer s.running.Done()
		defer done()
		s.runPlanner(ctx, target, settings, number, req)
	}()
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
	role := settings.Settings.Roles[config.RolePlanner]
	checkout := agent.Checkout{
		Owner: target.Repository.Owner,
		Repo:  target.Repository.Name,
		Issue: number,
		Role:  config.RolePlanner,
	}
	if err := s.Workspace.Remove(ctx, checkout); err != nil {
		log.Error(req.start+": the work directory of an earlier request was not removed", "error", err.Error())
		return
	}
	workDir, err := s.Workspace.Prepare(ctx, target.RemoteURL, checkout)
	if err != nil {
		log.Error(req.start+": the work directory was not prepared", "error", err.Error())
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
		if err := s.countPlannerRequest(target.Repository.String(), number, req.end, 1); err != nil {
			log.Error(req.end+": the request was not counted; the next poll decides again", "error", err.Error())
			return
		}
	}
	log.Info(req.start+": requested the Planner", "kind", req.kind)
	request := agent.StartRequest{
		Owner:        target.Repository.Owner,
		Repo:         target.Repository.Name,
		Role:         config.RolePlanner,
		RiskCriteria: settings.RiskCriteria,
		Facts:        agent.Facts{IssueNumber: number, IssueKind: agent.IssueKindRequirement, OwnerLogin: req.ownerLogin, ProtectedPaths: settings.ProtectedPaths},
		Text:         req.text(target.Repository.String(), number, workDir),
		WorkDir:      workDir,
		Settings:     &role,
	}
	if req.end == RowR4 {
		request.SessionID = req.sessionID
		s.runAcceptanceCheck(ctx, log, target, settings, number, request, req.again, req.count)
		return
	}

	s.runSplit(ctx, log, target, settings, number, request, req)
}

// runSplit runs the split, and decides its end as the poll does: it reads
// the requirement issue again, and SplitEnd decides from the facts on
// GitHub. So every end is the same case: a done result, an abnormal end,
// and a run that a restart of cumin cut off, which the next poll finds.
// Only a blocked result is not read from GitHub: cumin posts the
// blocked_reason and stops the issue for the Owner at once. That stop is
// kept when it ends with a temporary failure of GitHub (keptstep.go).
//
// req.again says that the split was already requested again during this
// stay in cumin/status/planning. When the facts ask for the second request,
// it runs here in the same work directory. When every sub-issue is closed,
// the issue moves to cumin/status/accepting, and the acceptance check runs
// here. While cumin is stopping, nothing is requested and no label changes.
// A failed read changes nothing: the issue keeps cumin/status/planning, and
// the next poll decides.
func (s *Service) runSplit(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, number int, request agent.StartRequest, req plannerRequest) {
	repository := target.Repository.String()
	again, counted := req.again, req.count
	for {
		run, err := s.Agents.Start(ctx, request)
		var abnormal *agent.AbnormalEnd
		switch {
		case errors.As(err, &abnormal):
			log.Info("the agent run ended abnormally", "kind", abnormal.Kind.String(),
				"session_id", abnormal.SessionID, "detail", abnormal.Detail)
			if ctx.Err() != nil {
				return
			}
		case err != nil:
			log.Error("the agent was not started", "error", err.Error())
			s.notStarted(log, repository, number, RowR2, counted)
			return
		default:
			log.Info("the agent run ended", "result", run.Result.Result, "session_id", run.SessionID)
			s.quotaAfterRun(ctx, log, target, number, run)
			if run.Result.Result != agent.ResultDone {
				log.Warn("the agent returned blocked", "reason", firstLine(run.Result.BlockedReason))
				s.runAndKeep(ctx, log, target, number, "the stop after blocked", func(ctx context.Context) error {
					return s.stopAfterPlannerBlocked(ctx, log, target, settings, number, req.end, run.Result.BlockedReason, true)
				})
				return
			}
		}

		token, err := target.Token(ctx)
		if err != nil {
			log.Error("R2: no token; the next poll decides the end of the split", "error", err.Error())
			return
		}
		requirement, err := s.requirementIssueNow(ctx, log, target, number)
		if err != nil {
			return
		}
		s.readRequirementFacts(ctx, log, token, target, &requirement)
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
			if s.finishing.Load() {
				log.Info("R2: cumin stops after its runs; the acceptance check waits for the next start of cumin")
				return
			}
			log.Info("R2: every sub-issue is closed; the acceptance check follows", "sub_issues", len(requirement.SubIssues))
			if err := s.moveToAccepting(ctx, token, target, requirement); err != nil {
				log.Error("the requirement issue was not moved; the next poll decides again", "error", err.Error())
				return
			}
			acceptance := acceptanceRequest
			acceptance.ownerLogin = req.ownerLogin
			s.runPlanner(ctx, target, settings, number, acceptance)
			return
		case Plan:
			if s.finishing.Load() {
				log.Info("R2: cumin stops after its runs; the second request of the split waits for the next start of cumin")
				return
			}
			if err := s.countPlannerRequest(repository, number, RowR2, 1); err != nil {
				log.Error("R2: the request was not counted; the next poll decides again", "error", err.Error())
				return
			}
			again, counted = true, true
			log.Info("R2: the split does not pass the check; the same request runs again in the same work directory")
		default:
			log.Info("R2: the end of the split was not decided; the next poll decides", "labels", requirement.Labels)
			return
		}
	}
}

// runAcceptanceCheck runs the acceptance check, and decides its end as the
// poll does: it reads the requirement issue again, and AcceptanceEnd decides
// from the facts on GitHub. So every end is the same case: a done result, an
// abnormal end, and a run that a restart of cumin cut off, which the next
// poll finds. Only a blocked result is not read from GitHub: cumin posts the
// blocked_reason and stops the issue for the Owner at once.
//
// again says that the acceptance check was already requested again during
// this stay in cumin/status/accepting, and counted that the state file got
// the count of that request for this start: a run that does not start
// takes it back. When the facts ask for the second
// request, it runs here in the session of the first run and in the same
// work directory. While cumin is stopping, nothing is requested again and
// no label changes. A failed read changes nothing: the issue keeps
// cumin/status/accepting, and the next poll decides.
func (s *Service) runAcceptanceCheck(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, number int, request agent.StartRequest, again, counted bool) {
	repository := target.Repository.String()
	for {
		run, err := s.Agents.Start(ctx, request)
		var abnormal *agent.AbnormalEnd
		switch {
		case errors.As(err, &abnormal):
			log.Info("the agent run ended abnormally", "kind", abnormal.Kind.String(),
				"session_id", abnormal.SessionID, "detail", abnormal.Detail)
			if ctx.Err() != nil {
				return
			}
			s.keepSession(log, target, config.RolePlanner, number, abnormal.SessionID)
		case err != nil:
			log.Error("the agent was not started", "error", err.Error())
			s.notStarted(log, repository, number, RowR4, counted)
			return
		default:
			log.Info("the agent run ended", "result", run.Result.Result, "session_id", run.SessionID)
			s.quotaAfterRun(ctx, log, target, number, run)
			if run.Result.Result != agent.ResultDone {
				log.Warn("the agent returned blocked", "reason", firstLine(run.Result.BlockedReason))
				_ = s.stopAfterPlannerBlocked(ctx, log, target, settings, number, RowR4, run.Result.BlockedReason, false)
				s.clearRequirementState(log, repository, number)
				return
			}
			s.keepSession(log, target, config.RolePlanner, number, run.SessionID)
		}

		token, err := target.Token(ctx)
		if err != nil {
			log.Error("R4: no token; the next poll decides the end of the acceptance check", "error", err.Error())
			return
		}
		requirement, err := s.requirementIssueNow(ctx, log, target, number)
		if err != nil {
			return
		}
		s.readRequirementFacts(ctx, log, token, target, &requirement)
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
			if err := s.countPlannerRequest(repository, number, RowR4, 1); err != nil {
				log.Error("R4: the request was not counted; the next poll decides again", "error", err.Error())
				return
			}
			again, counted = true, true
			request.SessionID = s.State.Issue(repository, number).SessionID
			log.Info("R4: the Planner left no acceptance check comment; the acceptance check is requested again")
		default:
			log.Info("R4: the end of the acceptance check was not decided; the next poll decides", "labels", requirement.Labels)
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

// NoAcceptanceCheckReason is the sentence of "stop the acceptance check for
// the Owner" when the Planner left no comment after two requests.
const NoAcceptanceCheckReason = "The Planner left no acceptance check comment. cumin requested the acceptance check again, and the Planner left no comment again."

// stopAcceptance applies "stop the acceptance check for the Owner": the
// requirement issue moves from cumin/status/accepting to
// cumin/status/awaiting-decision, and the Owner is notified. After a
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
		return fmt.Errorf("R4: %w", err)
	}
	s.clearRequirementState(log, target.Repository.String(), a.Number)
	if !a.Question {
		log.Info("R4: the Planner left no acceptance check comment after two requests; the issue waits for the Owner", "labels", labels)
		s.stopForOwner(ctx, log, target, settings, stop{
			row:       RowR4,
			issue:     a.Number,
			labelDone: true,
			reason:    NoAcceptanceCheckReason,
			comment:   StopNote(RowR4, NoAcceptanceCheckReason, 0, true),
		})
		return nil
	}
	log = log.With("row", RowR4)
	log.Info("R4: the Planner asked a question during the acceptance check; the issue waits for the Owner", "labels", labels)
	s.notifyOwner(ctx, log, settings.Settings.Notify.DiscordEnabled, notify.Notification{
		Row:        RowR4,
		Reason:     "The Planner asked a question during the acceptance check.",
		Repository: target.Repository.String(),
		Subject:    fmt.Sprintf("issue #%d", a.Number),
		Link:       github.IssueURL(target.Repository.Owner, target.Repository.Name, a.Number),
	})
	return nil
}

// runAndKeep runs a step after a Planner run, and keeps it when it ends
// with a temporary failure of GitHub. The first try runs here. A try of the
// kept step runs in a poll.
func (s *Service) runAndKeep(ctx context.Context, log *slog.Logger, target Target, number int, name string, run func(ctx context.Context) error) {
	step := &keptStep{name: name, log: log, run: run}
	s.tryStep(ctx, inProgressKey{repository: target.Repository.String(), issue: number}, step)
}

// stopAfterPlannerBlocked stops the requirement issue for the Owner after a
// blocked result, on a new read of the requirement issue. comment is the
// blocked_reason of the Planner.
//
// With keep, a temporary failure of the read is returned, and the caller
// keeps the step. The step then runs again from the read. The read comes
// before every write of the stop, so the kept step wrote nothing yet: it
// always posts the decision request, whatever the label is now. Every other
// failed read stops the issue without a label change, as before.
func (s *Service) stopAfterPlannerBlocked(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, number int, row, comment string, keep bool) error {
	requirement, err := s.requirementIssueNow(ctx, log, target, number)
	if keep && temporary(err) != nil {
		return err
	}
	s.stopForOwner(ctx, log, target, settings, stop{
		row:     row,
		issue:   number,
		labels:  labelsOf(requirement, err),
		reason:  "the Planner returned blocked: " + firstLine(comment),
		comment: comment,
	})
	return nil
}

// reviewPlan applies "ask the Owner to review the plan" (R2): the split
// passes the check and a sub-issue is open, so the requirement issue moves
// from cumin/status/planning to cumin/status/awaiting-plan-review and the
// Owner is notified. The next poll no longer sees cumin/status/planning, so
// one split sends one notification.
func (s *Service) reviewPlan(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, a ReviewPlan) error {
	labels, err := s.moveRequirement(ctx, token, target, snapshot, a.Number, LabelAwaitingPlanReview)
	if err != nil {
		return fmt.Errorf("R2: %w", err)
	}
	requirement, _ := snapshot.RequirementIssue(a.Number)
	log := s.logger().With("repository", target.Repository.String(), "issue", a.Number, "row", RowR2)
	log.Info("R2: the split waits for the Owner", "sub_issues", len(requirement.SubIssues), "labels", labels)
	s.clearRequirementState(log, target.Repository.String(), a.Number)
	s.notifyOwner(ctx, log, settings.Settings.Notify.DiscordEnabled, notify.Notification{
		Row:        RowR2,
		Reason:     "The split of the requirement issue needs a review.",
		Repository: target.Repository.String(),
		Subject:    fmt.Sprintf("issue #%d", a.Number),
		Link:       github.IssueURL(target.Repository.Owner, target.Repository.Name, a.Number),
	})
	return nil
}

// stopSplit applies "stop the split for the Owner": the requirement issue
// moves from cumin/status/planning to cumin/status/awaiting-decision, and
// the Owner is notified. After a question of the Planner, its comment holds
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
		return fmt.Errorf("R2: %w", err)
	}
	s.clearRequirementState(log, target.Repository.String(), a.Number)
	if !a.Question {
		log.Warn("R2: the split failed the check after two requests; the issue waits for the Owner", "reason", a.Reason, "labels", labels)
		s.stopForOwner(ctx, log, target, settings, stop{
			row:       RowR2,
			issue:     a.Number,
			labelDone: true,
			reason:    a.Reason,
			comment:   StopNote(RowR2, a.Reason, 0, true),
		})
		return nil
	}
	log = log.With("row", RowR2)
	log.Info("R2: the Planner asked a question during the split; the issue waits for the Owner", "labels", labels)
	s.notifyOwner(ctx, log, settings.Settings.Notify.DiscordEnabled, notify.Notification{
		Row:        RowR2,
		Reason:     "The Planner asked a question during the split.",
		Repository: target.Repository.String(),
		Subject:    fmt.Sprintf("issue #%d", a.Number),
		Link:       github.IssueURL(target.Repository.Owner, target.Repository.Name, a.Number),
	})
	return nil
}

// accept applies R7: the acceptance check comment exists, so the
// requirement issue moves to cumin/status/awaiting-acceptance and the
// Owner is notified, whatever the table of the comment says. The next poll
// no longer sees cumin/status/accepting, so one comment sends one
// notification.
func (s *Service) accept(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, a Accept) error {
	labels, err := s.moveRequirement(ctx, token, target, snapshot, a.Number, LabelAwaitingAcceptance)
	if err != nil {
		return fmt.Errorf("R7: %w", err)
	}
	log := s.logger().With("repository", target.Repository.String(), "issue", a.Number, "row", RowR7)
	log.Info("R7: the requirement issue waits for the acceptance of the Owner", "labels", labels)
	s.clearRequirementState(log, target.Repository.String(), a.Number)
	owner, repo := target.Repository.Owner, target.Repository.Name
	s.notifyOwner(ctx, log, settings.Settings.Notify.DiscordEnabled, notify.Notification{
		Row:        RowR7,
		Reason:     "The acceptance check is done; the requirement issue can be accepted.",
		Repository: target.Repository.String(),
		Subject:    fmt.Sprintf("issue #%d", a.Number),
		Link:       github.IssueURL(owner, repo, a.Number),
	})
	return nil
}

// readAcceptanceComments adds the facts of the acceptance check to each
// requirement issue of the snapshot that R4, R7, or the end of the
// acceptance check needs them for (NeedsComments), and that the way out of
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
		log.Error("R4: the login of the Planner App was not read", "issue", requirement.Number, "error", err.Error())
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
				log.Error("R2: the login of cumin-core was not read", "issue", requirement.Number, "error", err.Error())
				return
			}
			askers = append(askers, core)
		}
	}
	read, rate, err := s.GitHub.ReadIssueComments(ctx, token, target.Repository.Owner, target.Repository.Name, requirement.Number, since)
	if err != nil {
		log.Error("R4: the comments were not read", "issue", requirement.Number, "error", err.Error())
		return
	}
	log.Info("R4: read the comments", "issue", requirement.Number,
		"rate_limit_cost", rate.Cost, "rate_limit_remaining", rate.Remaining)
	comments := make([]Comment, 0, len(read))
	for _, c := range read {
		comments = append(comments, Comment{Author: c.Author, CreatedAt: c.CreatedAt, Body: c.Body, URL: c.URL})
	}
	requirement.CommentsRead = true
	requirement.AcceptanceCheckAt = AcceptanceCheckAt(comments, planner)
	requirement.QuestionAt = QuestionAt(comments, askers...)
	stored := s.State.Issue(target.Repository.String(), requirement.Number)
	requirement.AcceptanceRequestedAgain = stored.AcceptanceRequests > 0
	requirement.SplitRequestedAgain = stored.SplitRequests > 0
}

// readRequirementFacts reads, for one requirement issue that was read again
// at the end of a Planner run, what the poll reads for SplitEnd and for
// AcceptanceEnd: the time of its status label, and the comments.
func (s *Service) readRequirementFacts(ctx context.Context, log *slog.Logger, token string, target Target, requirement *RequirementIssue) {
	status := statusLabel(requirement.Labels)
	if status != LabelPlanning && status != LabelAccepting {
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

// runningIssues returns the issues of the repository whose agent runs now.
func (s *Service) runningIssues(repository string) map[int]bool {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	running := map[int]bool{}
	for key := range s.inProgress {
		if key.repository == repository {
			running[key.issue] = true
		}
	}
	return running
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

// labelsOf is the labels of the requirement issue that requirementIssueNow
// read, or none when it could not be read.
func labelsOf(requirement RequirementIssue, err error) []string {
	if err != nil {
		return nil
	}
	return requirement.Labels
}
