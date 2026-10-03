package workflow

// This file holds the one place that stops an issue for the Owner. Every
// row of issue-states.md that hands work back uses it with its own row
// number: post one comment on the issue, replace the status label with
// cumin/status/awaiting-owner-decision, then notify the Owner. I2, I4, and
// R2, I3, I5, I8, I10, and I15 use it today.
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

// The rows of issue-states.md that stop an issue for the Owner.
const (
	// RowI2 is the end of an Implementer run.
	RowI2 = "I2"
	// RowI4 is a failed required check after the limit of check fix
	// requests.
	RowI4 = "I4"
	// RowR2 is the end of a Planner run that split a requirement issue.
	RowR2 = "R2"
	// RowR4 is the end of a Planner run that checked the acceptance.
	RowR4 = "R4"
	// RowI3 is the end of a Reviewer run that ended abnormally twice.
	RowI3 = "I3"
	// RowI5 is the end of a Reviewer run whose review cumin did not find
	// on the head commit, twice (the failure column of I5).
	RowI5 = "I5"
	// RowI10 is a Reviewer run that returned blocked.
	RowI10 = "I10"
	// RowI8 is the round limit of the review: the Reviewer explained the
	// cause, or could not.
	RowI8 = "I8"
	// RowI6 is the merge after the approval of the Reviewer: a risk label
	// that is not exactly one, a merge that failed, or a close that failed.
	RowI6 = "I6"
	// RowI7 asks the Owner for the merge decision. It does not stop the
	// issue; it notifies.
	RowI7 = "I7"
	// RowI12 is the merge after the approval of the Owner: a risk label
	// that is not exactly one, a merge that failed, or a close that failed.
	RowI12 = "I12"
	// RowI14 is a pull request that conflicts with the default branch
	// while its issue waits for the checks: a conflict resolution that
	// left the head where it was.
	RowI14 = "I14"
	// RowI15 is a required check that did not report on the head commit
	// within the wait time of the repository.
	RowI15 = "I15"
)

// The rows that start the Planner (R1, R4) and that move a requirement
// issue with a notification but without a stop (R6, R7).
const (
	RowR1 = "R1"
	RowR7 = "R7"
)

// RowR6 is the row that hands a requirement issue back to the Owner when
// only sub-issues without a status label are left. It notifies without a
// stop: the requirement issue waits for a review, not for a decision.
const RowR6 = "R6"

// stop is one issue that cumin hands back to the Owner.
type stop struct {
	// row is the row of issue-states.md that stopped the issue.
	row string
	// issue is the issue that stops: an implementation issue (I2) or a
	// requirement issue (R2).
	issue int
	// labels are the labels of the issue now. An empty list means that
	// cumin could not read them; the labels are then left alone, because
	// writing a list without the risk label would remove it.
	labels []string
	// reason is one line: what cumin found. It goes into the notification.
	reason string
	// comment is the text to post on the issue.
	comment string
	// labelDone says that the caller already replaced the status label.
	// A stop that a poll decides (I4, I15) changes the label first: a label that
	// cumin cannot change would otherwise repeat the comment and the
	// notification at every poll (principle 3).
	labelDone bool
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
		switch {
		case st.labelDone:
			// The caller changed the label and logged it.
		case len(st.labels) == 0:
			log.Error(st.row + ": the labels of the issue were not read; the label was not changed")
		default:
			labels := ReplaceStatusLabel(st.labels, LabelAwaitingOwnerDecision)
			if err := s.GitHub.SetIssueLabels(ctx, token, owner, repo, st.issue, labels); err != nil {
				log.Error(st.row+": the label was not changed", "error", err.Error())
			} else {
				log.Info(st.row+": the issue waits for the Owner", "labels", labels)
			}
		}
	}

	s.notifyOwner(ctx, log, settings != nil && settings.Settings.Notify.DiscordEnabled, notify.Notification{
		Row:        st.row,
		Reason:     st.reason,
		Repository: target.Repository.String(),
		Subject:    fmt.Sprintf("issue #%d", st.issue),
		Link:       link,
	})
}

// notifyOwner sends one notification, when the repository wants one. The
// caller reads the setting notify.discord.enabled of that repository. A
// failure is logged at error level and undoes nothing.
//
// It reports false only when a channel failed to take the notification, so
// that a caller that sends a notification once (Q1) can try again later.
// A notification that is off, or that has no channel, reports true: trying
// again changes nothing.
func (s *Service) notifyOwner(ctx context.Context, log *slog.Logger, enabled bool, n notify.Notification) bool {
	if !enabled {
		log.Info("the notification is off for this repository")
		return true
	}
	if err := s.Notify.Notify(ctx, n); err != nil {
		log.Error("the Owner was not notified", "error", err.Error())
		return errors.Is(err, notify.ErrNoSender)
	}
	log.Info("the Owner was notified")
	return true
}

// subIssueNow reads one sub-issue from a new snapshot of the repository.
// A rule that the end of a run triggers judges on the facts of that
// moment (cumin-core.md, the topic on the GitHub client). The second
// value is false when the snapshot could not be read; the caller then
// leaves the labels alone, because writing a list without the risk label
// would remove it.
func (s *Service) subIssueNow(ctx context.Context, log *slog.Logger, target Target, number int) (SubIssue, bool) {
	token, err := target.Token(ctx)
	if err != nil {
		log.Error("the issue was not read again: no token", "error", err.Error())
		return SubIssue{}, false
	}
	read, err := s.GitHub.ReadSnapshot(ctx, token, target.Repository.Owner, target.Repository.Name)
	if err != nil {
		log.Error("the issue was not read again", "error", err.Error())
		return SubIssue{}, false
	}
	sub, ok := toSnapshot(read).SubIssue(number)
	if !ok {
		log.Error("the issue was not read again: it is not in the snapshot")
		return SubIssue{}, false
	}
	return sub, true
}

// StopNote is the comment that cumin writes when the reason is its own: a
// check of cumin failed, or a run ended abnormally. The form is
// templates/stop-note.md; a test keeps the two equal. When an agent
// returns blocked, cumin posts the blocked_reason of the agent instead.
//
// pullRequest is 0 when there is none. retried says whether the same
// request had been run again before cumin gave up.
func StopNote(row, reason string, pullRequest int, retried bool) string {
	pr := "None"
	if pullRequest > 0 {
		pr = fmt.Sprintf("#%d", pullRequest)
	}
	tried := "no"
	if retried {
		tried = "once"
	}
	return fmt.Sprintf(`## Stopped for the Owner

Row: %s
Reason: %s
Pull request: %s
Retried: %s

To continue: read the reason, fix what it names, and say in a comment how to go on. Then add the label `+"`cumin/status/ready`"+` to this issue.
`, row, reason, pr, tried)
}

// VerificationReason is the sentence of one failed check of I2. It goes
// into the comment on the issue and into the notification, so that the
// Owner reads the same words in both places.
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

// LinkFailedReason is the sentence of I2 when cumin-core could not add the
// closing link: it names the pull request and the answer of GitHub.
func LinkFailedReason(pullRequest int, answer string) string {
	return fmt.Sprintf("cumin-core could not link the pull request #%d to this issue as a closing reference; GitHub answered: %s.", pullRequest, strings.TrimSuffix(answer, "."))
}

// LinkMissingReason is the sentence of I2 when GitHub accepted the closing
// link, but the issue does not show it when cumin reads it again.
func LinkMissingReason(pullRequest int) string {
	return fmt.Sprintf("cumin-core linked the pull request #%d to this issue, but the issue does not show the closing link when cumin reads it again.", pullRequest)
}

// RiskLabelReason is the sentence of I6 when the issue has no risk label,
// or more than one: cumin reads the risk from the issue only (principle 5).
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

// UnreportedChecksReason is the sentence of I15: the facts that cumin sees,
// without a cause. It names the head commit, each required check that has
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
// failed. The pull request is merged; the Owner closes the issue.
func CloseFailedReason(pullRequest int, answer string) string {
	return fmt.Sprintf("cumin-core merged the pull request #%d, but could not close this issue; GitHub answered: %s. Close this issue by hand.", pullRequest, strings.TrimSuffix(answer, "."))
}

// SplitReason is the sentence of one failed check of R2, for the comment
// and the notification alike.
func SplitReason(v SplitVerification) string {
	switch v.Failure {
	case SplitNoSubIssue:
		return "The Planner reported done, but this requirement issue has no sub-issue."
	case SplitNoRiskLabel:
		return fmt.Sprintf("The Planner reported done, but the sub-issue #%d has no risk label.", v.SubIssue)
	case SplitTwoRiskLabels:
		return fmt.Sprintf("The Planner reported done, but the sub-issue #%d has more than one risk label.", v.SubIssue)
	}
	return "The verification of the split failed."
}

// abnormalReason is the sentence of a second abnormal end of the same
// request. The two runs can end in different ways, and the Owner needs the
// kind of each one to know where to look.
func abnormalReason(role string, first, second fmt.Stringer) string {
	return fmt.Sprintf("The %s run ended abnormally (%s). cumin ran the same request again, and it ended abnormally too (%s).", role, first, second)
}
