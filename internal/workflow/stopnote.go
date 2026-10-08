package workflow

// This file holds the sentences of a stop: the stop note that cumin writes
// on the issue, and the reason of each action that hands work back to a
// Maintainer. It is pure: stop.go posts the text.

import (
	"fmt"
	"strings"
	"time"
)

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

// RiskLabelReason is the sentence of "stop the review" when the issue has no
// risk label, or more than one: cumin reads the risk from the issue only
// (principle 5).
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
