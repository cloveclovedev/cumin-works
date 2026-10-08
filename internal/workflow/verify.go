package workflow

// This file is pure, like domain.go: the checks after an agent returned
// done. It holds the check of the pull request (VerifyDone), the check of
// the split (VerifySplit), and the way out of cumin/status/implementing
// (ImplementationEnd).

import (
	"fmt"
	"slices"
	"time"
)

// ImplementationEnd decides the way out of cumin/status/implementing from
// the facts on GitHub, for an implementation issue whose Implementer does
// not run. The poll and the end of an Implementer run both decide with it,
// so a restart of cumin during the run, or a failed read after it, loses
// nothing.
//
//   - A decision request of the Implementer, written after the issue got
//     cumin/status/implementing: stop the implementation for a Maintainer.
//     cumin-core posts the blocked_reason of the Implementer, so its
//     decision request counts too (QuestionAt).
//   - The pull request passes the check (VerifyDone), but the request was
//     a conflict resolution and the head commit is older than the label:
//     the conflict resolution left the head commit, so stop the
//     implementation for a Maintainer.
//   - The pull request passes the check: wait for the checks.
//   - The pull request fails the check: request the implementation again,
//     once for each stay in cumin/status/implementing. The second time,
//     stop the implementation for a Maintainer.
//
// It returns nil while the Implementer runs, in every other state, for a
// closed issue, while the facts were not read, and while the status label
// does not count: the next poll decides.
func ImplementationEnd(sub SubIssue, running bool) Action {
	facts := sub.Implementing
	if !ImplementationNeedsFacts(sub, running) || facts == nil || !facts.StatusCounts || facts.ImplementingAt.IsZero() {
		return nil
	}
	if !facts.QuestionAt.IsZero() && !facts.QuestionAt.Before(facts.ImplementingAt) {
		return StopImplementation{Number: sub.Number, Question: true}
	}
	verification := VerifyDone(sub, facts.Branch, facts.OnBranch, facts.Implementer, facts.LocalHead, facts.MaxLinks)
	switch {
	case verification.Passed && facts.ConflictRequested && headOlderThan(sub, verification.PullRequest, facts.ImplementingAt):
		return StopImplementation{Number: sub.Number, Reason: ConflictNotResolvedReason(verification.PullRequest), PullRequest: verification.PullRequest}
	case verification.Passed:
		nodeID := ""
		for _, pr := range facts.OnBranch {
			if pr.Number == verification.PullRequest {
				nodeID = pr.NodeID
			}
		}
		return WaitForChecks{Number: sub.Number, PullRequest: verification.PullRequest, PullRequestNodeID: nodeID, AddLink: verification.AddLink}
	case facts.RequestedAgain:
		return StopImplementation{Number: sub.Number, Reason: VerificationReason(verification.Failure), PullRequest: verification.PullRequest, Retried: true}
	}
	return RequestImplementationAgain{Number: sub.Number}
}

// ImplementationNeedsFacts reports whether the way out of
// cumin/status/implementing needs the facts of the implementation issue: it
// is open, in cumin/status/implementing, and its Implementer does not run.
// While the Implementer runs, nothing is decided, so the poll reads nothing
// more.
func ImplementationNeedsFacts(sub SubIssue, running bool) bool {
	return !sub.Closed && statusLabel(sub.Labels) == LabelImplementing && !running
}

// headOlderThan reports whether the head commit of the pull request of the
// issue is older than at, the time of cumin/status/implementing: the
// request left the head commit (issue-states.md, the text below the table
// of the implementation issue). A commit time that was not read decides
// nothing.
func headOlderThan(sub SubIssue, pullRequest int, at time.Time) bool {
	for _, pr := range sub.PullRequests {
		if pr.Number == pullRequest {
			return !pr.HeadCommittedAt.IsZero() && pr.HeadCommittedAt.Before(at)
		}
	}
	return false
}

// implementationEnds returns the way out of cumin/status/implementing of
// every sub-issue that has one (ImplementationEnd), lowest issue number
// first.
func implementationEnds(snapshot Snapshot) []Action {
	var subs []SubIssue
	for _, requirement := range snapshot.RequirementIssues {
		subs = append(subs, requirement.SubIssues...)
	}
	slices.SortFunc(subs, func(a, b SubIssue) int { return a.Number - b.Number })
	var actions []Action
	for _, sub := range subs {
		if action := ImplementationEnd(sub, snapshot.Running[sub.Number]); action != nil {
			actions = append(actions, action)
		}
	}
	return actions
}

// VerificationFailure says which check of the pull request failed after the
// Implementer ended.
type VerificationFailure int

const (
	// FailureNone: the verification passed.
	FailureNone VerificationFailure = iota
	// FailureNoOpenPullRequest: no open pull request is on the branch of
	// the issue.
	FailureNoOpenPullRequest
	// FailureAuthorMismatch: the open pull requests on the branch of the
	// issue are not by the Implementer App.
	FailureAuthorMismatch
	// FailureHeadNotPushed: the head commit of the worktree is not the
	// head of the pull request.
	FailureHeadNotPushed
	// FailureTooManyLinks: the issue needs a closing link, but it has
	// maxLinks open closing pull requests already, and one more would make
	// the issue unreadable for the poll.
	FailureTooManyLinks
)

func (f VerificationFailure) String() string {
	switch f {
	case FailureNone:
		return "none"
	case FailureNoOpenPullRequest:
		return "no open pull request is on the branch of the issue"
	case FailureAuthorMismatch:
		return "the author of the pull request is not the Implementer App"
	case FailureHeadNotPushed:
		return "the head commit of the worktree is not pushed"
	case FailureTooManyLinks:
		return "the issue has too many open closing pull requests for one more link"
	}
	return fmt.Sprintf("VerificationFailure(%d)", int(f))
}

// Verification is the result of the check of the pull request after the
// Implementer ends. Passed is true when every check held; otherwise Failure
// names the first check that failed.
// PullRequest is the pull request that was checked, or 0 when there is
// none. AddLink is true when the verification passed and the issue has no
// closing link to that pull request, so cumin-core adds it.
type Verification struct {
	Passed      bool
	Failure     VerificationFailure
	PullRequest int
	AddLink     bool
}

// VerifyDone applies the checks of "wait for the checks" (issue-states.md)
// to a sub-issue after the Implementer returned done. onBranch holds the
// open pull
// requests whose head is branch, the branch that cumin chose for the
// request; one of them is by implementer (the login "<slug>[bot]" of the
// Implementer App), the one with the highest number of two or more; its
// head commit is localHead, the head of the worktree (so the last commit is
// pushed). A pull request on another branch is never taken. When only
// another author has a pull request on the branch, the one with the highest
// number is named. An empty branch, implementer, or localHead never
// matches.
//
// The pull request is found by the branch, not by the closing link,
// because GitHub does not always make the link from "Closes #N". When the
// issue has no link to it, AddLink asks cumin-core to add one; every other
// row reads the link. maxLinks is the most open closing pull requests
// that the poll reads for one issue; a link that would go over it is not
// added, and the issue stops instead.
func VerifyDone(sub SubIssue, branch string, onBranch []PullRequest, implementer, localHead string, maxLinks int) Verification {
	var mine, other PullRequest
	for _, pr := range onBranch {
		if branch == "" || pr.HeadBranch != branch {
			continue
		}
		if implementer != "" && pr.Author == implementer {
			if pr.Number > mine.Number {
				mine = pr
			}
		} else if pr.Number > other.Number {
			other = pr
		}
	}
	switch {
	case mine.Number == 0 && other.Number == 0:
		return Verification{Failure: FailureNoOpenPullRequest}
	case mine.Number == 0:
		return Verification{Failure: FailureAuthorMismatch, PullRequest: other.Number}
	case localHead == "" || mine.HeadCommit != localHead:
		return Verification{Failure: FailureHeadNotPushed, PullRequest: mine.Number}
	}
	if linksPullRequest(sub, mine.Number) {
		return Verification{Passed: true, PullRequest: mine.Number}
	}
	if len(sub.PullRequests) >= maxLinks {
		return Verification{Failure: FailureTooManyLinks, PullRequest: mine.Number}
	}
	return Verification{Passed: true, PullRequest: mine.Number, AddLink: true}
}

// linksPullRequest reports whether the issue has a closing link to the pull
// request.
func linksPullRequest(sub SubIssue, number int) bool {
	for _, pr := range sub.PullRequests {
		if pr.Number == number {
			return true
		}
	}
	return false
}

// SplitFailure says which check of the split failed.
type SplitFailure int

const (
	// SplitNone: the verification passed.
	SplitNone SplitFailure = iota
	// SplitNoSubIssue: the requirement issue has no sub-issue.
	SplitNoSubIssue
	// SplitNoRiskLabel: a sub-issue has no risk/* label.
	SplitNoRiskLabel
	// SplitTwoRiskLabels: a sub-issue has more than one risk/* label.
	SplitTwoRiskLabels
)

// SplitVerification is the result of the check of the split after done.
// Passed is true when
// every check held; otherwise Failure names the first check that failed,
// and SubIssue the sub-issue that failed it (0 when there is none).
type SplitVerification struct {
	Passed   bool
	Failure  SplitFailure
	SubIssue int
}

// VerifySplit applies the check of the split (issue-states.md) to a
// requirement issue after the Planner returned done: it has one or more
// sub-issues, and every sub-issue carries exactly one risk/* label. The
// sub-issues are checked lowest number first, so the same issue always
// names the same failure. cumin judges nothing of the content of the split;
// a Maintainer reviews it.
func VerifySplit(requirement RequirementIssue) SplitVerification {
	if len(requirement.SubIssues) == 0 {
		return SplitVerification{Failure: SplitNoSubIssue}
	}
	subs := slices.Clone(requirement.SubIssues)
	slices.SortFunc(subs, func(a, b SubIssue) int { return a.Number - b.Number })
	for _, sub := range subs {
		risks := len(riskLabels(sub.Labels))
		switch {
		case risks == 0:
			return SplitVerification{Failure: SplitNoRiskLabel, SubIssue: sub.Number}
		case risks > 1:
			return SplitVerification{Failure: SplitTwoRiskLabels, SubIssue: sub.Number}
		}
	}
	return SplitVerification{Passed: true}
}

// SplitStatus returns the status label of a requirement issue after the
// check of the split passed. With one or more open sub-issues, a Maintainer
// reviews the split: cumin/status/awaiting-plan-review ("ask for the plan
// review"). With every sub-issue closed, the Planner created none, as when
// a Maintainer resumes a requirement issue after a blocked acceptance
// check: cumin/status/accepting, and the Planner checks the acceptance
// again (issue-states.md, "request the acceptance check").
func SplitStatus(requirement RequirementIssue) string {
	for _, sub := range requirement.SubIssues {
		if !sub.Closed {
			return LabelAwaitingPlanReview
		}
	}
	return LabelAccepting
}

// labelsNow is the labels of the sub-issue that subIssueNow read, or none
// when it could not be read.
func labelsNow(sub SubIssue, ok bool) []string {
	if !ok {
		return nil
	}
	return sub.Labels
}
