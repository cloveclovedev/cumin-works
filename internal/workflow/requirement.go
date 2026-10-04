package workflow

// This file applies the rows that move a requirement issue with its
// sub-issues: R3 (a sub-issue may start) and R6 (only sub-issues without a
// status label are left), and reads the label times that R3, a sub-issue
// that waits for its checks, and the send-back after a request for changes
// of the Owner (I13) need, the Owner of the newest cumin/status/ready
// that R1 and I1 need, and the account of the newest status label of a
// state that cumin is about to act from.
// docs/ja/designs/poll.md, the topics on the label times and on the
// decision of the poll.

import (
	"context"
	"fmt"
	"log/slog"
	"time"

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
		if !NeedsLabelTimes(*requirement) && !SplitNeedsFacts(*requirement, snapshot.Running[requirement.Number]) {
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
		requirement.ReviewAt = times[requirement.Number][statusLabel(requirement.Labels)]
		for j := range requirement.SubIssues {
			sub := &requirement.SubIssues[j]
			sub.ReadyAt = times[sub.Number][LabelReady]
			sub.CheckingAt = times[sub.Number][LabelChecking]
			sub.AwaitingMergeDecisionAt = times[sub.Number][LabelAwaitingMergeDecision]
		}
	}
}

// readReadyOwners reads, for the candidates of R1 and of I1, who added the
// newest cumin/status/ready, and stores in the snapshot whether that
// account is the Owner (issue-states.md, the ready of the Owner). It reads
// only when a slot is free, in the order of the starts, and stops when it
// has as many candidates of the Owner as free slots (ReadyActorReads). Only
// an event of the issue itself answers: a candidate carries the label, so
// no ready event among the events that are read means "not the Owner". A
// failed read is logged and leaves ReadyRead false, so the issue waits for
// the next poll and the other rules go on.
func (s *Service) readReadyOwners(ctx context.Context, log *slog.Logger, token string, target Target, settings *RepositorySettings, snapshot *Snapshot) {
	numbers, room := ReadyActorReads(*snapshot, s.Settings.MaxIssuesInProgress, settings.Settings.PriorityLabelNames())
	for _, number := range numbers {
		if room == 0 {
			return
		}
		actor, isOwner, err := s.readReadyActor(ctx, token, target, number, false)
		if err != nil {
			log.Error("the actor of the newest "+LabelReady+" was not read", "issue", number, "error", err.Error())
			continue
		}
		login := ""
		if isOwner {
			login = actor.Login
			room--
		} else {
			s.tellReadyOfAnother(ctx, log, target, settings, *snapshot, number, actor)
		}
		for i := range snapshot.RequirementIssues {
			requirement := &snapshot.RequirementIssues[i]
			if requirement.Number == number {
				requirement.ReadyRead, requirement.ReadyOwner = true, login
			}
			for j := range requirement.SubIssues {
				if sub := &requirement.SubIssues[j]; sub.Number == number {
					sub.ReadyRead, sub.ReadyOwner = true, login
				}
			}
		}
	}
}

// readStatusActors reads, for each requirement issue that cumin is about to
// act from the state of (StatusActorReads), who added its newest status
// label, and stores in the snapshot whether the label counts as a state
// (issue-states.md, the account that added a status label). Only an event
// of the issue itself answers: the issue carries the label, so no such
// event among the events that are read means "does not count". A failed
// read is logged and leaves StatusRead false, so the issue waits for the
// next poll and the other rules go on.
func (s *Service) readStatusActors(ctx context.Context, log *slog.Logger, token string, target Target, settings *RepositorySettings, snapshot *Snapshot) {
	for _, number := range StatusActorReads(*snapshot) {
		for i := range snapshot.RequirementIssues {
			if requirement := &snapshot.RequirementIssues[i]; requirement.Number == number {
				s.readStatusOfRequirement(ctx, log, token, target, settings, requirement)
			}
		}
	}
}

// readStatusOfRequirement reads who added the newest status label of one
// requirement issue, stores whether the label counts, and tells the Owner
// when it does not (tellStatusOfAnother).
func (s *Service) readStatusOfRequirement(ctx context.Context, log *slog.Logger, token string, target Target, settings *RepositorySettings, requirement *RequirementIssue) {
	label := statusLabel(requirement.Labels)
	actor, counts, err := s.readStatusActor(ctx, token, target, requirement.Number, label, false)
	if err != nil {
		log.Error("the actor of the newest "+label+" was not read", "issue", requirement.Number, "error", err.Error())
		return
	}
	requirement.StatusRead, requirement.StatusCounts = true, counts
	if !counts {
		s.tellStatusOfAnother(ctx, log, target, settings, requirement.Number, label, actor)
	}
}

// toldLabelEvent reports whether cumin already told the Owner about the
// label event of the issue at that time, and remembers the event. The time
// of the event names it, so one event is told once, not at every poll.
func (s *Service) toldLabelEvent(target Target, number int, at time.Time) bool {
	key := fmt.Sprintf("%s#%d", repositoryKey(target.Repository), number)
	s.readyMu.Lock()
	defer s.readyMu.Unlock()
	told, ok := s.readyTold[key]
	if s.readyTold == nil {
		s.readyTold = map[string]time.Time{}
	}
	s.readyTold[key] = at
	return ok && told.Equal(at)
}

// labelActorName names the account of a label event for the Owner.
func labelActorName(actor github.LabelActor) string {
	switch {
	case actor.Login != "":
		return actor.Login
	case !actor.At.IsZero():
		return "an account that no longer exists"
	}
	return "an account that cumin could not find among the newest label events"
}

// tellStatusOfAnother logs, and tells the Owner, that another account than
// cumin-core or an Owner added the newest status label of the issue, so
// cumin does nothing from that state: no agent, no merge, no label change.
// It does so once for one such event, not at every poll.
func (s *Service) tellStatusOfAnother(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, number int, label string, actor github.LabelActor) {
	if s.toldLabelEvent(target, number, actor.At) {
		return
	}
	log = log.With("issue", number)
	log.Warn("the newest "+label+" is not of cumin-core or of an Owner: cumin does nothing until an Owner adds the right label again",
		"actor", actor.Login, "actor_type", actor.Type)
	s.notifyOwner(ctx, log, settings.Settings.Notify.DiscordEnabled, notify.Notification{
		Reason:     fmt.Sprintf("%s was added by %s, who is not cumin-core or an Owner. cumin does nothing until an Owner adds the right label again.", label, labelActorName(actor)),
		Repository: target.Repository.String(),
		Subject:    fmt.Sprintf("issue #%d", number),
		Link:       github.IssueURL(target.Repository.Owner, target.Repository.Name, number),
	})
}

// tellReadyOfAnother logs, and tells the Owner, that another account than
// the Owner added the newest cumin/status/ready of the issue, so cumin
// starts nothing for it. It does so once for one such event, not at every
// poll: the time of the event names it.
func (s *Service) tellReadyOfAnother(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, snapshot Snapshot, number int, actor github.LabelActor) {
	if s.toldLabelEvent(target, number, actor.At) {
		return
	}
	row := RowR1
	if _, isSub := snapshot.SubIssue(number); isSub {
		row = "I1"
	}
	log = log.With("row", row, "issue", number)
	log.Warn("the newest "+LabelReady+" is not of the Owner: nothing starts until the Owner adds the label again",
		"actor", actor.Login, "actor_type", actor.Type)
	by := labelActorName(actor)
	s.notifyOwner(ctx, log, settings.Settings.Notify.DiscordEnabled, notify.Notification{
		Row:        row,
		Reason:     fmt.Sprintf("%s was added by %s, who is not the Owner. cumin starts nothing until the Owner adds the label again.", LabelReady, by),
		Repository: target.Repository.String(),
		Subject:    fmt.Sprintf("issue #%d", number),
		Link:       github.IssueURL(target.Repository.Owner, target.Repository.Name, number),
	})
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
// cumin/status/awaiting-plan-review, and the Owner is notified. The
// notification follows the label change, and the next poll no longer sees
// cumin/status/implementing, so one move sends one notification. When the
// label cannot change, nothing is sent, and the next poll tries again.
func (s *Service) reviewRemaining(ctx context.Context, token string, target Target, snapshot Snapshot, settings *RepositorySettings, a ReviewRemaining) error {
	labels, err := s.moveRequirement(ctx, token, target, snapshot, a.Number, LabelAwaitingPlanReview)
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
