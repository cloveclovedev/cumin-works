package workflow

// This file applies the rows that move a requirement issue with its
// sub-issues: R3 (a sub-issue may start) and R6 (only sub-issues without a
// status label are left), and reads the label times that R3 and a sub-issue
// that waits for its checks need.
// docs/ja/designs/poll.md, the topics on the label times and on the
// decision of the poll.

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cloveclovedev/cumin-works/internal/notify"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
)

// readLabelTimes adds the label times to each requirement issue of the
// snapshot that needs them (NeedsLabelTimes), with one small query for each.
// A failed read is logged and leaves LabelTimesRead false, so R3 and I13
// wait for the next poll and the other rules go on.
func (s *Service) readLabelTimes(ctx context.Context, log *slog.Logger, token string, target Target, snapshot *Snapshot) {
	for i := range snapshot.RequirementIssues {
		requirement := &snapshot.RequirementIssues[i]
		if !NeedsLabelTimes(*requirement) {
			continue
		}
		times, rate, err := s.GitHub.ReadLabelTimes(ctx, token, target.Repository.Owner, target.Repository.Name, requirement.Number)
		if err != nil {
			log.Error("the label times were not read", "issue", requirement.Number, "error", err.Error())
			continue
		}
		log.Info("read the label times", "issue", requirement.Number,
			"rate_limit_cost", rate.Cost, "rate_limit_remaining", rate.Remaining)
		requirement.LabelTimesRead = true
		requirement.ReviewAt = times[requirement.Number][LabelAwaitingOwnerReview]
		for j := range requirement.SubIssues {
			sub := &requirement.SubIssues[j]
			sub.ReadyAt = times[sub.Number][LabelReady]
			sub.AwaitingChecksAt = times[sub.Number][LabelAwaitingChecks]
			sub.AwaitingOwnerReviewAt = times[sub.Number][LabelAwaitingOwnerReview]
		}
	}
}

// startRequirement applies R3: the requirement issue moves to
// cumin/status/implementing. No agent starts; the sub-issues start by I1.
func (s *Service) startRequirement(ctx context.Context, token string, target Target, snapshot Snapshot, a StartRequirement) error {
	labels, err := s.moveRequirement(ctx, token, target, snapshot, a.Number, LabelImplementing)
	if err != nil {
		return fmt.Errorf("R3: %w", err)
	}
	s.logger().Info("R3: the sub-issues of the requirement issue are in progress",
		"repository", target.Repository.String(), "issue", a.Number, "labels", labels)
	return nil
}

// reviewRemaining applies R6: the requirement issue moves to
// cumin/status/awaiting-owner-review, and the Owner is notified. The
// notification follows the label change, and the next poll no longer sees
// cumin/status/implementing, so one move sends one notification. When the
// label cannot change, nothing is sent, and the next poll tries again.
func (s *Service) reviewRemaining(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, a ReviewRemaining) error {
	labels, err := s.moveRequirement(ctx, token, target, snapshot, a.Number, LabelAwaitingOwnerReview)
	if err != nil {
		return fmt.Errorf("R6: %w", err)
	}
	log := s.logger().With("repository", target.Repository.String(), "issue", a.Number, "row", RowR6)
	log.Info("R6: the remaining sub-issues wait for the Owner", "labels", labels)
	owner, repo := target.Repository.Owner, target.Repository.Name
	s.notifyOwner(ctx, log, settings.Settings.Notify.DiscordEnabled, notify.Notification{
		Row:        RowR6,
		Reason:     "The sub-issues that are left have no status label and need a review.",
		Repository: target.Repository.String(),
		Subject:    fmt.Sprintf("issue #%d", a.Number),
		Link:       github.IssueURL(owner, repo, a.Number),
	})
	return nil
}

// moveRequirement replaces the status label of a requirement issue.
func (s *Service) moveRequirement(ctx context.Context, token string, target Target, snapshot Snapshot, number int, status string) ([]string, error) {
	requirement, ok := snapshot.RequirementIssue(number)
	if !ok {
		return nil, fmt.Errorf("issue #%d is not in the snapshot", number)
	}
	labels := ReplaceStatusLabel(requirement.Labels, status)
	if err := s.GitHub.SetIssueLabels(ctx, token, target.Repository.Owner, target.Repository.Name, number, labels); err != nil {
		return nil, fmt.Errorf("move issue #%d to %s: %w", number, status, err)
	}
	return labels, nil
}
