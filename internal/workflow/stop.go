package workflow

// This file holds the one place that stops an issue for a Maintainer. Every
// action of issue-states.md that hands work back uses it with its own
// name (action.go): post one comment on the issue, replace the status
// label with cumin/status/awaiting-decision, then notify.
//
// docs/ja/designs/poll.md, the topic on the failure paths.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/cloveclovedev/cumin-works/internal/notify"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
)

// stop is one issue that cumin hands back to a Maintainer.
type stop struct {
	// action is the action of issue-states.md that stopped the issue.
	action ActionName
	// issue is the issue that stops: an implementation issue or a
	// requirement issue.
	issue int
	// labels are the labels of the issue now. An empty list means that
	// cumin could not read them; the labels are then left alone, because
	// writing a list without the risk label would remove it.
	labels []string
	// reason is one line: what cumin found. It goes into the notification.
	reason string
	// comment is the text to post on the issue.
	comment string
	// labelFirst replaces the status label before the comment is written:
	// the stop after a blocked result. A comment that GitHub refuses then
	// leaves the issue with a Maintainer, and no poll requests the work again.
	labelFirst bool
	// labelDone says that the caller already replaced the status label.
	// A stop that a poll decides ("stop for failed checks", "stop for missing
	// checks") changes the label first: a label that cumin cannot change
	// would otherwise repeat the comment and the
	// notification at every poll (principle 3).
	labelDone bool
}

// notWrittenNote is what the notification adds to the reason when the
// comment was not written on the issue.
const notWrittenNote = " cumin did not write the comment on the issue; the log of the Host holds the whole text."

// stopForMaintainer posts the comment, replaces the status label, and
// notifies, in that order; with labelFirst, the label comes before the
// comment. Every step is logged with the action.
//
// A step that fails is logged and does not stop the next one: a Maintainer
// must learn about a stopped issue even when one call failed. Nothing is
// undone. What cumin wrote on GitHub is the fact of the matter, and the
// notification only asks to look. A comment that was not written goes to
// the log as a whole, and the notification says so.
func (s *Service) stopForMaintainer(ctx context.Context, log *slog.Logger, target Target, settings *RepositorySettings, st stop) {
	owner, repo := target.Repository.Owner, target.Repository.Name
	log = log.With("action", st.action)
	// The comment holds the whole reason, so the notification links to it.
	// Until it is written, the issue itself is the link.
	link := github.IssueURL(owner, repo, st.issue)
	reason := st.reason

	token, err := target.Token(ctx)
	if err != nil {
		log.Error(string(st.action)+": no token; the issue keeps its label, and the reason was not written on the issue; the whole text is here", "error", err.Error(), "comment", st.comment)
		reason += notWrittenNote
	} else {
		move := func() {
			switch {
			case st.labelDone:
				// The caller changed the label and logged it.
			case len(st.labels) == 0:
				log.Error(string(st.action) + ": the labels of the issue were not read; the label was not changed")
			default:
				labels, err := s.moveIssue(ctx, token, target, st.issue, st.labels, LabelAwaitingDecision)
				if err != nil {
					log.Error(string(st.action)+": the label was not changed", "error", err.Error())
				} else {
					log.Info(string(st.action)+": the issue waits for a Maintainer", "labels", labels)
				}
			}
		}
		if st.labelFirst {
			move()
		}
		if comment, err := s.GitHub.CreateIssueComment(ctx, token, owner, repo, st.issue, st.comment); err != nil {
			log.Error(string(st.action)+": the reason was not written on the issue; the whole text is here", "error", err.Error(), "comment", st.comment)
			reason += notWrittenNote
		} else {
			link = comment.URL
			log.Info(string(st.action)+": wrote the reason on the issue", "comment", comment.ID)
		}
		if !st.labelFirst {
			move()
		}
	}

	s.notify(ctx, log, settings.notificationOn(), notify.Notification{
		Action:     string(st.action),
		Reason:     reason,
		Repository: target.Repository.String(),
		Subject:    fmt.Sprintf("issue #%d", st.issue),
		Link:       link,
	})
}

// notify sends one notification, when the repository wants one. The
// caller reads the setting notify.discord.enabled of that repository. A
// failure is logged at error level and undoes nothing.
//
// It reports false only when a channel failed to take the notification, so
// that a caller that sends a notification once ("stop agent starts") can
// try again later.
// A notification that is off, or that has no channel, reports true: trying
// again changes nothing.
func (s *Service) notify(ctx context.Context, log *slog.Logger, enabled bool, n notify.Notification) bool {
	if !enabled {
		log.Info("the notification is off for this repository")
		return true
	}
	if err := s.Notify.Notify(ctx, n); err != nil {
		log.Error("the notification was not sent", "error", err.Error())
		return errors.Is(err, notify.ErrNoSender)
	}
	log.Info("the notification was sent")
	return true
}

// subIssueNow reads one sub-issue again, and only that issue. A rule that
// the end of a run triggers judges on the facts of that moment
// (cumin-core.md, the topic on the GitHub client). The second value is
// false when the issue could not be read, or when a poll does not read it
// (its requirement issue is closed, issue-states.md, principle 6); the
// caller then leaves the labels alone, because writing a list without the
// risk label would remove it.
func (s *Service) subIssueNow(ctx context.Context, log *slog.Logger, target Target, number int) (SubIssue, bool) {
	sub, err := s.readSubIssueNow(ctx, log, target, number)
	return sub, err == nil
}

// readSubIssueNow is subIssueNow with the error of the read.
func (s *Service) readSubIssueNow(ctx context.Context, log *slog.Logger, target Target, number int) (SubIssue, error) {
	token, err := target.Token(ctx)
	if err != nil {
		log.Error("the issue was not read again: no token", "error", err.Error())
		return SubIssue{}, err
	}
	sub, _, err := s.readSubIssueAgain(ctx, log, token, target, number)
	if err != nil {
		log.Error("the issue was not read again", "error", err.Error())
		return SubIssue{}, err
	}
	return sub, nil
}

// readSubIssueAgain reads one sub-issue again with the given token, and
// only that issue. The string is the default branch of the repository. The
// caller logs a failed read, in the words of its own step.
func (s *Service) readSubIssueAgain(ctx context.Context, log *slog.Logger, token string, target Target, number int) (SubIssue, string, error) {
	read, err := s.GitHub.ReadSubIssue(ctx, token, target.Repository.Owner, target.Repository.Name, number)
	if err != nil {
		return SubIssue{}, "", err
	}
	log.Debug("read the issue again", "issue", number, "rate_limit_cost", read.RateLimit.Cost, "rate_limit_remaining", read.RateLimit.Remaining)
	return toSubIssue(read.Issue), read.DefaultBranch, nil
}
