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
	"strings"
	"time"

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
// notification only asks a Maintainer to look. A comment that was not written
// goes to the log as a whole, and the notification says so.
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
				labels := ReplaceStatusLabel(st.labels, LabelAwaitingDecision)
				if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, st.issue, labels); err != nil {
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

	s.notify(ctx, log, settings != nil && settings.Settings.Notify.DiscordEnabled, notify.Notification{
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
	read, err := s.GitHub.ReadSubIssue(ctx, token, target.Repository.Owner, target.Repository.Name, number)
	if err != nil {
		log.Error("the issue was not read again", "error", err.Error())
		return SubIssue{}, err
	}
	log.Debug("read the issue again", "issue", number, "rate_limit_cost", read.RateLimit.Cost, "rate_limit_remaining", read.RateLimit.Remaining)
	return toSubIssue(read.Issue), nil
}

// StopNote is the comment that cumin writes when the reason is its own: a
// check of cumin failed, or a run ended abnormally. The form is
// templates/stop-note.md; a test keeps the two equal. When an agent
// returns blocked, cumin posts the blocked_reason of the agent instead.
//
// pullRequest is 0 when there is none. retried says whether the same
// request had been run again before cumin gave up.
func StopNote(action ActionName, reason string, pullRequest int, retried bool) string {
	pr := "None"
	if pullRequest > 0 {
		pr = fmt.Sprintf("#%d", pullRequest)
	}
	tried := "no"
	if retried {
		tried = "once"
	}
	return fmt.Sprintf(`## Stopped for a Maintainer

Step: %s
Reason: %s
Pull request: %s
Retried: %s

To continue: a Maintainer reads the reason, fixes what it names, and says in a comment how to go on. Then a Maintainer adds the label `+"`cumin/status/ready`"+` to this issue.
`, action, reason, pr, tried)
}

// VerificationReason is the sentence of one failed check of the pull
// request after the Implementer ends. It goes into the comment on the issue
// and into the notification, so that a Maintainer reads the same words in
// both places.
func VerificationReason(failure VerificationFailure) string {
	switch failure {
	case FailureNoOpenPullRequest:
		return "The Implementer reported done, but no open pull request is on the branch of this issue."
	case FailureAuthorMismatch:
		return "The Implementer reported done, but the open pull request on the branch of this issue was not opened by the Implementer App."
	case FailureHeadNotPushed:
		return "The Implementer reported done, but the last commit of the work directory is not the head of the pull request, so it was not pushed."
	case FailureTooManyLinks:
		return "The Implementer reported done, but this issue already has other open pull requests that close it, so cumin-core does not link one more."
	}
	return "The verification of the pull request failed."
}

// LinkFailedReason is the sentence of "stop the implementation" when
// cumin-core could not add the closing link: it names the pull request and
// the answer of GitHub.
func LinkFailedReason(pullRequest int, answer string) string {
	return fmt.Sprintf("cumin-core could not link the pull request #%d to this issue as a closing reference; GitHub answered: %s.", pullRequest, strings.TrimSuffix(answer, "."))
}

// LinkMissingReason is the sentence of "stop the implementation" when
// GitHub accepted the closing link, but the issue does not show it when
// cumin reads it again.
func LinkMissingReason(pullRequest int) string {
	return fmt.Sprintf("cumin-core linked the pull request #%d to this issue, but the issue does not show the closing link when cumin reads it again.", pullRequest)
}

// RiskLabelReason is the sentence of the merge after the review of the
// Reviewer when the issue has no risk label, or more than one: cumin reads
// the risk from the issue only (principle 5).
func RiskLabelReason(decision MergeDecision) string {
	if decision == MergeTwoRiskLabels {
		return "The pull request is approved, but this issue has more than one risk label, so cumin does not merge it."
	}
	return "The pull request is approved, but this issue has no risk label, so cumin does not merge it."
}

// ConflictNotResolvedReason is the sentence of a conflict resolution that
// ended with done, but left the head of the pull request at the commit
// that conflicted.
func ConflictNotResolvedReason(pullRequest int) string {
	return fmt.Sprintf("The Implementer reported done after the conflict resolution, but the head of the pull request #%d is still the commit that conflicted with the default branch.", pullRequest)
}

// UnreportedChecksReason is the sentence of "stop for missing checks": the
// facts that cumin sees, without a cause. It names the head commit, each
// required check that has
// not reported, and the time that cumin waited. With no open pull request,
// it says that, and the time that cumin waited.
func UnreportedChecksReason(a StopForUnreportedChecks) string {
	if a.PullRequest == 0 {
		return fmt.Sprintf("No open pull request closes this issue, so no required check can report. cumin waited %s, longer than the wait time of this repository (checks_wait_time).",
			a.Waited.Round(time.Second))
	}
	names := make([]string, 0, len(a.Unreported))
	for _, check := range a.Unreported {
		names = append(names, check.Name)
	}
	return fmt.Sprintf("The required checks did not report (%s) on the head commit %s of the pull request #%d. cumin waited %s, longer than the wait time of this repository (checks_wait_time).",
		strings.Join(names, ", "), a.HeadCommit, a.PullRequest, a.Waited.Round(time.Second))
}

// MergeHeadMovedReason is the sentence of a merge whose head is no longer
// the approved commit.
func MergeHeadMovedReason(pullRequest int) string {
	return fmt.Sprintf("cumin-core did not merge the pull request #%d, because its head is no longer the approved commit.", pullRequest)
}

// MergeFailedReason is the sentence of any other failure of the merge, with
// the answer of GitHub.
func MergeFailedReason(pullRequest int, answer string) string {
	return fmt.Sprintf("cumin-core could not merge the pull request #%d; GitHub answered: %s.", pullRequest, strings.TrimSuffix(answer, "."))
}

// CloseFailedReason is the sentence of a close after the merge that
// failed. The pull request is merged; a Maintainer closes the issue.
func CloseFailedReason(pullRequest int, answer string) string {
	return fmt.Sprintf("cumin-core merged the pull request #%d, but could not close this issue; GitHub answered: %s. Close this issue by hand.", pullRequest, strings.TrimSuffix(answer, "."))
}

// SplitReason is the sentence of "stop the split" when the split fails the
// check of the split after two requests, for the comment and the
// notification alike.
func SplitReason(v SplitVerification) string {
	failed := "the verification of the split failed"
	switch v.Failure {
	case SplitNoSubIssue:
		failed = "this requirement issue has no sub-issue"
	case SplitNoRiskLabel:
		failed = fmt.Sprintf("the sub-issue #%d has no risk label", v.SubIssue)
	case SplitTwoRiskLabels:
		failed = fmt.Sprintf("the sub-issue #%d has more than one risk label", v.SubIssue)
	}
	return "After the Planner run, " + failed + ". cumin requested the split again, and the check of the split failed again."
}

// WorkDirectoryReason is the sentence of a work directory that cumin did
// not prepare for the first request and for the second request of one stay
// in cumin/status/implementing. The error stays in the log of the Host,
// because it can hold a path of the Host.
func WorkDirectoryReason() string {
	return "cumin did not prepare the work directory of this issue, so the Implementer did not start. cumin requested the implementation again, and the work directory was not prepared again. The log of the Host holds the error."
}

// ReviewerNotStartedReason is the sentence of a second request to the
// Reviewer of one stay in cumin/status/reviewing that did not start. The
// error stays in the log of the Host, because it can hold a path of the
// Host.
func ReviewerNotStartedReason() string {
	return "cumin sent two requests to the Reviewer during this review, and the Reviewer did not start for the last one: cumin did not prepare the work directory, or did not start the agent. The log of the Host holds the error."
}

// AfterAbnormalEndReason adds, to the reason of a stop, the kind of the
// abnormal end of the run of the role that the stop follows: a Maintainer
// needs the kind to know where to look.
func AfterAbnormalEndReason(reason, role string, kind fmt.Stringer) string {
	return fmt.Sprintf("%s The last %s run ended abnormally (%s).", reason, role, kind)
}
