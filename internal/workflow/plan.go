package workflow

// This file applies the rows of a requirement issue that start and end a
// Planner run: R1 (start the split) and R2 (verify the split after the
// run). docs/ja/designs/poll.md, the topics on the request to the Planner
// and on the end of a run.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/cloveclovedev/cumin-works/internal/agent"
	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/notify"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
)

// plan applies R1: replace the status label of the requirement issue with
// cumin/status/planning, and only then request the split. When the label
// change fails, nothing is requested; the next poll decides again.
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
	labels := LabelsAfterPlan(requirement.Labels)
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, p.Number, labels); err != nil {
		return fmt.Errorf("R1: move issue #%d to planning: %w", p.Number, err)
	}
	s.logger().Info("R1: moved the requirement issue to planning",
		"repository", target.Repository.String(), "issue", p.Number, "labels", labels)
	return s.goPlanner(ctx, target, settings, p.Number, req)
}

// checkAcceptance applies "request the acceptance check" (R4): replace the
// status label of the requirement issue with cumin/status/accepting, and
// only then request the acceptance check. When the label change fails,
// nothing is requested; the next poll decides again. No ready of the Owner
// is needed.
//
// With Again, it applies "request the acceptance check again": the issue is
// already in cumin/status/accepting, and the Planner left no result. The
// count of the state file is raised before the request, so that the request
// is sent once for each stay in cumin/status/accepting. The request resumes
// the session of the first run when the state file holds one.
func (s *Service) checkAcceptance(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, a CheckAcceptance) error {
	owner, repo := target.Repository.Owner, target.Repository.Name
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
		req.again = true
		req.sessionID = s.State.Issue(repository, a.Number).SessionID
		if err := s.countAcceptanceRequest(repository, a.Number); err != nil {
			return fmt.Errorf("R4: request the acceptance check of issue #%d again: %w", a.Number, err)
		}
		s.logger().Info("R4: the Planner left no acceptance check comment; the acceptance check is requested again",
			"repository", repository, "issue", a.Number)
		return s.goPlanner(ctx, target, settings, a.Number, req)
	}
	// A new stay in cumin/status/accepting starts with no session and a
	// count of zero.
	if err := s.State.Clear(repository, a.Number); err != nil {
		return fmt.Errorf("R4: clear the state of issue #%d: %w", a.Number, err)
	}
	labels := ReplaceStatusLabel(requirement.Labels, LabelAccepting)
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, a.Number, labels); err != nil {
		return fmt.Errorf("R4: move issue #%d to accepting: %w", a.Number, err)
	}
	s.logger().Info("R4: moved the requirement issue to accepting", "repository", repository, "issue", a.Number, "labels", labels)
	return s.goPlanner(ctx, target, settings, a.Number, req)
}

// countAcceptanceRequest writes into the state file that the acceptance
// check of the requirement issue was requested again.
func (s *Service) countAcceptanceRequest(repository string, number int) error {
	stored := s.State.Issue(repository, number)
	stored.AcceptanceRequests++
	return s.State.Set(repository, number, stored)
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
	// again says that the acceptance check was already requested again
	// during this stay in cumin/status/accepting.
	again bool
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
// The end of the run of a split: a blocked result stops the requirement
// issue for the Owner without a retry; an abnormal end runs the same request
// once more, and after the second one the requirement issue goes to the
// Owner. A done result goes to verifySplit (R2). While cumin is stopping,
// nothing is retried and no label changes, as for I2.
//
// The end of the run of an acceptance check is decided from the facts on
// GitHub (runAcceptanceCheck).
//
// The step after a done or a blocked result of a split that ends with a
// temporary failure of GitHub is kept (keptstep.go): a later poll runs it
// again from the read of the requirement issue.
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
		s.runAcceptanceCheck(ctx, log, target, settings, number, request, req.again)
		return
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
				log.Info(req.end+": the same request runs again in the same work directory", "attempt", attempt+1)
				continue
			}
			reason := abnormalReason("Planner", firstKind, abnormal.Kind)
			requirement, err := s.requirementIssueNow(ctx, log, target, number)
			s.stopForOwner(ctx, log, target, settings, stop{
				row:     req.end,
				issue:   number,
				labels:  labelsOf(requirement, err),
				reason:  reason,
				comment: StopNote(req.end, reason, 0, true),
			})
			return
		case err != nil:
			log.Error("the agent was not started", "error", err.Error())
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
			try := &splitTry{}
			s.runAndKeep(ctx, log, target, number, "the check of the split", func(ctx context.Context) error {
				err := s.verifySplit(ctx, log, target, settings, number, try)
				try.again = true
				return err
			})
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
// this stay in cumin/status/accepting. When the facts ask for the second
// request, it runs here in the session of the first run and in the same
// work directory. While cumin is stopping, nothing is requested again and
// no label changes. A failed read changes nothing: the issue keeps
// cumin/status/accepting, and the next poll decides.
func (s *Service) runAcceptanceCheck(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, number int, request agent.StartRequest, again bool) {
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
			return
		default:
			log.Info("the agent run ended", "result", run.Result.Result, "session_id", run.SessionID)
			s.quotaAfterRun(ctx, log, target, number, run)
			if run.Result.Result != agent.ResultDone {
				log.Warn("the agent returned blocked", "reason", firstLine(run.Result.BlockedReason))
				_ = s.stopAfterPlannerBlocked(ctx, log, target, settings, number, RowR4, run.Result.BlockedReason, false)
				s.clearAcceptance(log, repository, number)
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
		s.readAcceptanceFacts(ctx, log, token, target, &requirement)
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
			if err := s.countAcceptanceRequest(repository, number); err != nil {
				log.Error("R4: the request was not counted; the next poll decides again", "error", err.Error())
				return
			}
			again = true
			request.SessionID = s.State.Issue(repository, number).SessionID
			log.Info("R4: the Planner left no acceptance check comment; the acceptance check is requested again")
		default:
			log.Info("R4: the end of the acceptance check was not decided; the next poll decides", "labels", requirement.Labels)
			return
		}
	}
}

// clearAcceptance removes what the state file holds for a requirement issue
// that left cumin/status/accepting. A failure is logged: the next stay
// clears the entry before its request.
func (s *Service) clearAcceptance(log *slog.Logger, repository string, number int) {
	if err := s.State.Clear(repository, number); err != nil {
		log.Error("the state of the acceptance check was not cleared", "error", err.Error())
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
func (s *Service) stopAcceptance(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, a StopAcceptance) error {
	log := s.logger().With("repository", target.Repository.String(), "issue", a.Number)
	if !a.Question {
		requirement, ok := snapshot.RequirementIssue(a.Number)
		if !ok {
			return fmt.Errorf("R4: issue #%d is not in the snapshot", a.Number)
		}
		s.stopForOwner(ctx, log, target, settings, stop{
			row:     RowR4,
			issue:   a.Number,
			labels:  requirement.Labels,
			reason:  NoAcceptanceCheckReason,
			comment: StopNote(RowR4, NoAcceptanceCheckReason, 0, true),
		})
		s.clearAcceptance(log, target.Repository.String(), a.Number)
		return nil
	}
	labels, err := s.moveRequirement(ctx, token, target, snapshot, a.Number, LabelAwaitingDecision)
	if err != nil {
		return fmt.Errorf("R4: %w", err)
	}
	log = log.With("row", RowR4)
	log.Info("R4: the Planner asked a question during the acceptance check; the issue waits for the Owner", "labels", labels)
	s.clearAcceptance(log, target.Repository.String(), a.Number)
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

// splitTry is what the check of the split remembers between its tries.
type splitTry struct {
	// again says that an earlier try was kept.
	again bool
	// lost is the status label of the label change of the last try, when
	// that change ended with a temporary failure: it may have reached
	// GitHub. Empty when the last try sent no label change.
	lost string
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
	s.clearAcceptance(log, target.Repository.String(), a.Number)
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
// acceptance check needs them for (NeedsComments).
func (s *Service) readAcceptanceComments(ctx context.Context, log *slog.Logger, token string, target Target, snapshot *Snapshot) {
	for i := range snapshot.RequirementIssues {
		if requirement := &snapshot.RequirementIssues[i]; NeedsComments(*requirement) {
			s.readAcceptanceComment(ctx, log, token, target, requirement)
		}
	}
}

// readAcceptanceComment adds the time of the acceptance check comment and
// of the decision request of the Planner, and whether the state file says
// that the acceptance check was requested again. A comment counts only when
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
	read, rate, err := s.GitHub.ReadIssueComments(ctx, token, target.Repository.Owner, target.Repository.Name, requirement.Number, lastClose(*requirement))
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
	requirement.QuestionAt = QuestionAt(comments, planner)
	requirement.AcceptanceRequestedAgain = s.State.Issue(target.Repository.String(), requirement.Number).AcceptanceRequests > 0
}

// readAcceptanceFacts reads, for one requirement issue that was read again
// at the end of a Planner run, what the poll reads for AcceptanceEnd: the
// time of its status label, and the comments.
func (s *Service) readAcceptanceFacts(ctx context.Context, log *slog.Logger, token string, target Target, requirement *RequirementIssue) {
	if statusLabel(requirement.Labels) != LabelAccepting {
		return
	}
	times, _, err := s.GitHub.ReadLabelTimes(ctx, token, target.Repository.Owner, target.Repository.Name, requirement.Number)
	if err != nil {
		log.Error("the label times were not read", "error", err.Error())
	} else {
		requirement.LabelTimesRead = true
		requirement.ReviewAt = times[requirement.Number][LabelAccepting]
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

// verifySplit applies R2 after a done result, on a new read of the
// requirement issue. On a pass with an open sub-issue, the requirement issue moves
// to cumin/status/awaiting-plan-review and the Owner is told that the
// split needs a review. On a pass with every sub-issue closed, it moves to
// cumin/status/implementing without a notification, so that R4 asks for the
// acceptance check again (SplitStatus). A failed check stops the
// requirement issue for the Owner with the sentence of that check.
//
// A temporary failure of a call to GitHub (the token, the read, the label)
// is returned, and the caller keeps the step (keptstep.go). The step then
// runs again from its start. When the requirement issue of the new read has
// left cumin/status/planning, the step does not change the label. When the
// issue carries the label of a lost label change of the last try, that
// change reached GitHub. The notification follows the label change, so it
// was not sent yet: the step sends it when that label is
// cumin/status/awaiting-plan-review. With any other label, another hand
// changed it, and the step leaves the issue. Every other failure is logged
// and returns nil, as before.
func (s *Service) verifySplit(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, number int, try *splitTry) error {
	requirement, err := s.requirementIssueNow(ctx, log, target, number)
	if err != nil {
		return temporary(err)
	}
	owner, repo := target.Repository.Owner, target.Repository.Name
	notifySplit := func() {
		s.notifyOwner(ctx, log.With("row", RowR2), settings.Settings.Notify.DiscordEnabled, notify.Notification{
			Row:        RowR2,
			Reason:     "The split of the requirement issue needs a review.",
			Repository: target.Repository.String(),
			Subject:    fmt.Sprintf("issue #%d", number),
			Link:       github.IssueURL(owner, repo, number),
		})
	}
	lost := try.lost
	try.lost = ""
	if try.again && !slices.Contains(requirement.Labels, LabelPlanning) {
		if lost != LabelAwaitingPlanReview || !slices.Contains(requirement.Labels, lost) {
			log.Info("R2: the issue left cumin/status/planning while the check of the split was kept; nothing changes", "labels", requirement.Labels)
			return nil
		}
		log.Info("R2: the label was already changed while the check of the split was kept; the split waits for the Owner", "labels", requirement.Labels)
		notifySplit()
		return nil
	}
	verification := VerifySplit(requirement)
	if !verification.Passed {
		reason := SplitReason(verification)
		log.Warn("R2: the verification failed", "reason", reason)
		s.stopForOwner(ctx, log, target, settings, stop{
			row:     RowR2,
			issue:   number,
			labels:  requirement.Labels,
			reason:  reason,
			comment: StopNote(RowR2, reason, 0, false),
		})
		return nil
	}
	token, err := target.Token(ctx)
	if err != nil {
		log.Error("R2: no token; the label was not changed", "error", err.Error())
		return temporary(err)
	}
	status := SplitStatus(requirement)
	labels := ReplaceStatusLabel(requirement.Labels, status)
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, number, labels); err != nil {
		log.Error("R2: the label was not changed", "error", err.Error())
		if temporary(err) != nil {
			try.lost = status
		}
		return temporary(err)
	}
	if status == LabelImplementing {
		// Nothing waits for the Owner: R4 asks for the acceptance check at
		// the next poll, and R7 notifies when it is done.
		log.Info("R2: every sub-issue is closed; the acceptance check follows", "sub_issues", len(requirement.SubIssues), "labels", labels)
		return nil
	}
	log.Info("R2: the split waits for the Owner", "sub_issues", len(requirement.SubIssues), "labels", labels)
	notifySplit()
	return nil
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
