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
	labels := LabelsAfterPlan(requirement.Labels)
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, p.Number, labels); err != nil {
		return fmt.Errorf("R1: move issue #%d to planning: %w", p.Number, err)
	}
	s.logger().Info("R1: moved the requirement issue to planning",
		"repository", target.Repository.String(), "issue", p.Number, "labels", labels)
	return s.goPlanner(ctx, target, settings, p.Number, planRequest)
}

// checkAcceptance applies R4: request the acceptance check. The label stays
// cumin/status/implementing, because the comment on GitHub tells whether
// the check is done (issue-states.md, the text below the table). The
// running set keeps a second poll from asking again while the Planner
// works.
func (s *Service) checkAcceptance(ctx context.Context, target Target, settings *RepositorySettings, a CheckAcceptance) error {
	return s.goPlanner(ctx, target, settings, a.Number, acceptanceRequest)
}

// plannerRequest is one kind of request to the Planner.
type plannerRequest struct {
	// start is the row that requested it, and end the row that judges the
	// end of the run: R1 and R2 for a split, R4 for an acceptance check.
	start, end string
	// kind is the request kind of planner.md, for the log.
	kind string
	text func(repository string, number int, workDir string) string
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
// directory of the first run.
//
// The end of the run: a blocked result stops the requirement issue for the
// Owner without a retry; an abnormal end runs the same request once more,
// and after the second one the requirement issue goes to the Owner. A done
// result of a split goes to verifySplit (R2). A done result of an
// acceptance check changes nothing: the next poll finds the comment (R7)
// or asks again (R4). While cumin is stopping, nothing is retried and no
// label changes, as for I2.
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
	log.Info(req.start+": requested the Planner", "kind", req.kind)
	request := agent.StartRequest{
		Owner:        target.Repository.Owner,
		Repo:         target.Repository.Name,
		Role:         config.RolePlanner,
		RiskCriteria: settings.RiskCriteria,
		Text:         req.text(target.Repository.String(), number, workDir),
		WorkDir:      workDir,
		Settings:     &role,
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
			s.stopForOwner(ctx, log, target, settings, stop{
				row:     req.end,
				issue:   number,
				labels:  labelsOf(s.requirementIssueNow(ctx, log, target, number)),
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
				question := firstLine(run.Result.BlockedReason)
				log.Warn("the agent returned blocked", "reason", question)
				s.stopForOwner(ctx, log, target, settings, stop{
					row:     req.end,
					issue:   number,
					labels:  labelsOf(s.requirementIssueNow(ctx, log, target, number)),
					reason:  "the Planner returned blocked: " + question,
					comment: run.Result.BlockedReason,
				})
				return
			}
			if req.end == RowR2 {
				s.verifySplit(ctx, log, target, settings, number)
			}
			return
		}
	}
}

// accept applies R7: the acceptance check comment exists, so the
// requirement issue moves to cumin/status/awaiting-owner-review and the
// Owner is notified, whatever the table of the comment says. The next poll
// no longer sees cumin/status/implementing, so one comment sends one
// notification.
func (s *Service) accept(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, a Accept) error {
	labels, err := s.moveRequirement(ctx, token, target, snapshot, a.Number, LabelAwaitingOwnerReview)
	if err != nil {
		return fmt.Errorf("R7: %w", err)
	}
	log := s.logger().With("repository", target.Repository.String(), "issue", a.Number, "row", RowR7)
	log.Info("R7: the requirement issue waits for the acceptance of the Owner", "labels", labels)
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

// readAcceptanceComments adds the time of the acceptance check comment to
// each requirement issue of the snapshot that R4 or R7 needs it for. The
// comment counts only when the Planner App wrote it. A failed read is
// logged and leaves CommentsRead false, so neither row applies to that
// requirement issue in this poll.
func (s *Service) readAcceptanceComments(ctx context.Context, log *slog.Logger, token string, target Target, snapshot *Snapshot) {
	for i := range snapshot.RequirementIssues {
		requirement := &snapshot.RequirementIssues[i]
		if !NeedsComments(*requirement) || s.Agents == nil {
			continue
		}
		planner, err := s.Agents.BotLogin(ctx, target.Repository.Owner, config.RolePlanner)
		if err != nil {
			log.Error("R4: the login of the Planner App was not read", "issue", requirement.Number, "error", err.Error())
			return
		}
		read, rate, err := s.GitHub.ReadIssueComments(ctx, token, target.Repository.Owner, target.Repository.Name, requirement.Number, lastClose(*requirement))
		if err != nil {
			log.Error("R4: the comments were not read", "issue", requirement.Number, "error", err.Error())
			continue
		}
		log.Info("R4: read the comments", "issue", requirement.Number,
			"rate_limit_cost", rate.Cost, "rate_limit_remaining", rate.Remaining)
		comments := make([]Comment, 0, len(read))
		for _, c := range read {
			comments = append(comments, Comment{Author: c.Author, CreatedAt: c.CreatedAt, Body: c.Body})
		}
		requirement.CommentsRead = true
		requirement.AcceptanceCheckAt = AcceptanceCheckAt(comments, planner)
	}
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

// verifySplit applies R2 after a done result, on a new snapshot of the
// repository. On a pass with an open sub-issue, the requirement issue moves
// to cumin/status/awaiting-owner-review and the Owner is told that the
// split needs a review. On a pass with every sub-issue closed, it moves to
// cumin/status/implementing without a notification, so that R4 asks for the
// acceptance check again (SplitStatus). A failed check stops the
// requirement issue for the Owner with the sentence of that check.
func (s *Service) verifySplit(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, number int) {
	requirement, ok := s.requirementIssueNow(ctx, log, target, number)
	if !ok {
		return
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
		return
	}
	owner, repo := target.Repository.Owner, target.Repository.Name
	token, err := target.Token(ctx)
	if err != nil {
		log.Error("R2: no token; the label was not changed", "error", err.Error())
		return
	}
	status := SplitStatus(requirement)
	labels := ReplaceStatusLabel(requirement.Labels, status)
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, number, labels); err != nil {
		log.Error("R2: the label was not changed", "error", err.Error())
		return
	}
	if status == LabelImplementing {
		// Nothing waits for the Owner: R4 asks for the acceptance check at
		// the next poll, and R7 notifies when it is done.
		log.Info("R2: every sub-issue is closed; the acceptance check follows", "sub_issues", len(requirement.SubIssues), "labels", labels)
		return
	}
	log.Info("R2: the split waits for the Owner", "sub_issues", len(requirement.SubIssues), "labels", labels)
	s.notifyOwner(ctx, log.With("row", RowR2), settings.Settings.Notify.DiscordEnabled, notify.Notification{
		Row:        RowR2,
		Reason:     "The split of the requirement issue needs a review.",
		Repository: target.Repository.String(),
		Subject:    fmt.Sprintf("issue #%d", number),
		Link:       github.IssueURL(owner, repo, number),
	})
}

// requirementIssueNow reads one requirement issue from a new snapshot of
// the repository, as subIssueNow does for a sub-issue.
func (s *Service) requirementIssueNow(ctx context.Context, log *slog.Logger, target Target, number int) (RequirementIssue, bool) {
	token, err := target.Token(ctx)
	if err != nil {
		log.Error("the issue was not read again: no token", "error", err.Error())
		return RequirementIssue{}, false
	}
	read, err := s.GitHub.ReadSnapshot(ctx, token, target.Repository.Owner, target.Repository.Name)
	if err != nil {
		log.Error("the issue was not read again", "error", err.Error())
		return RequirementIssue{}, false
	}
	requirement, ok := toSnapshot(read).RequirementIssue(number)
	if !ok {
		log.Error("the issue was not read again: it is not in the snapshot")
		return RequirementIssue{}, false
	}
	return requirement, true
}

// labelsOf is the labels of the requirement issue that requirementIssueNow
// read, or none when it could not be read.
func labelsOf(requirement RequirementIssue, ok bool) []string {
	if !ok {
		return nil
	}
	return requirement.Labels
}
