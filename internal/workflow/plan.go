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
	labels := LabelsAfterPlan(requirement.Labels)
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, p.Number, labels); err != nil {
		return fmt.Errorf("R1: move issue #%d to planning: %w", p.Number, err)
	}
	s.logger().Info("R1: moved the requirement issue to planning",
		"repository", target.Repository.String(), "issue", p.Number, "labels", labels)
	if s.Agents == nil {
		return fmt.Errorf("R1: request the split for issue #%d: no agent service is configured", p.Number)
	}
	done := s.markInProgress(ctx, target.Repository.String(), p.Number)
	s.running.Add(1)
	go func() {
		defer s.running.Done()
		defer done()
		s.runPlanner(ctx, target, settings, p.Number)
	}()
	return nil
}

// runPlanner prepares the work directory and runs one Planner request of
// the kind "plan" in a new session. The work directory is a detached
// checkout of the default branch, because the Planner only reads
// (agent-run.md, the topic on the work directory).
//
// The end of the run is the trigger of R2: a done result goes to
// verifySplit, and a blocked result stops the requirement issue for the
// Owner without a retry. An abnormal end starts the same request once
// more, in the same work directory and in a new session; after the second
// one, the requirement issue goes to the Owner. While cumin is stopping,
// nothing is retried and no label changes, as for I2.
func (s *Service) runPlanner(ctx context.Context, target Target, settings *RepositorySettings, number int) {
	log := s.logger().With("repository", target.Repository.String(), "issue", number, "role", config.RolePlanner)
	role := settings.Settings.Roles[config.RolePlanner]
	workDir, err := s.Workspace.Prepare(ctx, target.RemoteURL, agent.Checkout{
		Owner: target.Repository.Owner,
		Repo:  target.Repository.Name,
		Issue: number,
		Role:  config.RolePlanner,
	})
	if err != nil {
		log.Error("R1: the work directory was not prepared", "error", err.Error())
		return
	}
	log.Info("R1: requested the split")
	request := agent.StartRequest{
		Owner:        target.Repository.Owner,
		Repo:         target.Repository.Name,
		Role:         config.RolePlanner,
		RiskCriteria: settings.RiskCriteria,
		Text:         PlanRequestText(target.Repository.String(), number, workDir),
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
				log.Info("R2: the same request runs again in the same work directory", "attempt", attempt+1)
				continue
			}
			reason := abnormalReason("Planner", firstKind, abnormal.Kind)
			s.stopForOwner(ctx, log, target, settings, stop{
				row:     RowR2,
				issue:   number,
				labels:  labelsOf(s.requirementIssueNow(ctx, log, target, number)),
				reason:  reason,
				comment: StopNote(RowR2, reason, 0, true),
			})
			return
		case err != nil:
			log.Error("the agent was not started", "error", err.Error())
			return
		default:
			log.Info("the agent run ended", "result", run.Result.Result, "session_id", run.SessionID)
			if run.Result.Result != agent.ResultDone {
				question := firstLine(run.Result.BlockedReason)
				log.Warn("the agent returned blocked", "reason", question)
				s.stopForOwner(ctx, log, target, settings, stop{
					row:     RowR2,
					issue:   number,
					labels:  labelsOf(s.requirementIssueNow(ctx, log, target, number)),
					reason:  "the Planner returned blocked: " + question,
					comment: run.Result.BlockedReason,
				})
				return
			}
			s.verifySplit(ctx, log, target, settings, number)
			return
		}
	}
}

// verifySplit applies R2 after a done result, on a new snapshot of the
// repository. On a pass the requirement issue moves to
// cumin/status/awaiting-owner-review and the Owner is told that the split
// needs a review. A failed check stops the requirement issue for the
// Owner with the sentence of that check.
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
	labels := LabelsAfterSplit(requirement.Labels)
	if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, number, labels); err != nil {
		log.Error("R2: the label was not changed", "error", err.Error())
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
