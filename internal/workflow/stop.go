package workflow

// This file holds the one place that stops an issue for the Owner. Every
// row of issue-states.md that hands work back uses it with its own row
// number: post one comment on the issue, replace the status label with
// cumin/status/awaiting-owner-decision, then notify the Owner. I2 uses it
// today; I4, I8, and I10 are later requirements.
//
// docs/ja/designs/poll.md, the topic on the failure paths.

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cloveclovedev/cumin-works/internal/notify"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
)

// RowI2 is the row of issue-states.md that this requirement stops on: the
// end of an Implementer run.
const RowI2 = "I2"

// stop is one issue that cumin hands back to the Owner.
type stop struct {
	// row is the row of issue-states.md that stopped the issue.
	row string
	// issue is the implementation issue.
	issue int
	// labels are the labels of the issue now. An empty list means that
	// cumin could not read them; the labels are then left alone, because
	// writing a list without the risk label would remove it.
	labels []string
	// reason is one line: what cumin found. It goes into the notification.
	reason string
	// comment is the text to post on the issue.
	comment string
}

// stopForOwner posts the comment, replaces the status label, and notifies
// the Owner, in that order. Every step is logged with the row.
//
// A step that fails is logged and does not stop the next one: the Owner
// must learn about a stopped issue even when one call failed. Nothing is
// undone. What cumin wrote on GitHub is the fact of the matter, and the
// notification only asks the Owner to look.
func (s *Service) stopForOwner(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, st stop) {
	owner, repo := target.Repository.Owner, target.Repository.Name
	log = log.With("row", st.row)
	// The comment holds the whole reason, so the notification links to it.
	// Until it is written, the issue itself is the link.
	link := github.IssueURL(owner, repo, st.issue)

	token, err := target.Token(ctx)
	if err != nil {
		log.Error(st.row+": no token; the issue keeps its label", "error", err.Error())
	} else {
		if comment, err := s.GitHub.CreateIssueComment(ctx, token, owner, repo, st.issue, st.comment); err != nil {
			log.Error(st.row+": the reason was not written on the issue", "error", err.Error())
		} else {
			link = comment.URL
			log.Info(st.row+": wrote the reason on the issue", "comment", comment.ID)
		}
		if len(st.labels) == 0 {
			log.Error(st.row + ": the labels of the issue were not read; the label was not changed")
		} else {
			labels := ReplaceStatusLabel(st.labels, LabelAwaitingOwnerDecision)
			if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, st.issue, labels); err != nil {
				log.Error(st.row+": the label was not changed", "error", err.Error())
			} else {
				log.Info(st.row+": the issue waits for the Owner", "labels", labels)
			}
		}
	}

	s.notifyOwner(ctx, log, settings, notify.Notification{
		Row:        st.row,
		Reason:     st.reason,
		Repository: target.Repository.String(),
		Subject:    fmt.Sprintf("issue #%d", st.issue),
		Link:       link,
	})
}

// notifyOwner sends one notification, when the repository wants one. The
// setting notify.discord.enabled of that repository decides. A failure is
// logged at error level and undoes nothing.
func (s *Service) notifyOwner(ctx context.Context, log *slog.Logger, settings *RepositorySettings, n notify.Notification) {
	if settings == nil || !settings.Settings.Notify.DiscordEnabled {
		log.Info("the notification is off for this repository")
		return
	}
	if err := s.Notify.Notify(ctx, n); err != nil {
		log.Error("the Owner was not notified", "error", err.Error())
		return
	}
	log.Info("the Owner was notified")
}

// issueLabels reads the labels that one sub-issue has now, from a new
// snapshot of the repository. A rule that the end of a run triggers judges
// on the facts of that moment (cumin-core.md, the topic on the GitHub
// client). An empty list means that the labels were not read; the caller
// then leaves them alone.
func (s *Service) issueLabels(ctx context.Context, log *slog.Logger, target Target, number int) []string {
	token, err := target.Token(ctx)
	if err != nil {
		log.Error("the labels of the issue were not read: no token", "error", err.Error())
		return nil
	}
	read, err := s.GitHub.ReadSnapshot(ctx, token, target.Repository.Owner, target.Repository.Name)
	if err != nil {
		log.Error("the labels of the issue were not read", "error", err.Error())
		return nil
	}
	sub, ok := toSnapshot(read).SubIssue(number)
	if !ok {
		log.Error("the labels of the issue were not read: the issue is not in the snapshot")
		return nil
	}
	return sub.Labels
}
