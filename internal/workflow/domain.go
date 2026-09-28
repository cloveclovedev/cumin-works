// Package workflow holds the rules of cumin (the rows R*, I*, and Q* of
// docs/ja/requirements/workflow/issue-states.md) and the polling loop that
// applies them.
//
// This file is the pure part: the snapshot of the facts on GitHub, the
// actions, and the decision. It imports no HTTP client, no os/exec, and no
// provider package. The same snapshot always gives the same actions.
package workflow

import (
	"fmt"
	"slices"
	"strings"
)

// Label names from the table in issue-states.md.
const (
	LabelRequirement = "cumin/type/requirement"
	// LabelOwnerTask marks a sub-issue whose work the Owner does by hand.
	// cumin never claims it (I1).
	LabelOwnerTask = "cumin/type/owner-task"

	LabelReady                 = "cumin/status/ready"
	LabelPlanning              = "cumin/status/planning"
	LabelImplementing          = "cumin/status/implementing"
	LabelAwaitingChecks        = "cumin/status/awaiting-checks"
	LabelReviewing             = "cumin/status/reviewing"
	LabelAwaitingOwnerReview   = "cumin/status/awaiting-owner-review"
	LabelAwaitingOwnerDecision = "cumin/status/awaiting-owner-decision"

	statusLabelPrefix = "cumin/status/"
)

// IsStatusLabel reports whether name is a cumin/status/* label.
func IsStatusLabel(name string) bool { return strings.HasPrefix(name, statusLabelPrefix) }

// Snapshot is what one poll read of one repository: the open requirement
// issues and their sub-issues. Closed requirement issues are not in it
// (issue-states.md, principle 6).
type Snapshot struct {
	// DefaultBranch is the branch whose rules name the required checks.
	DefaultBranch     string
	RequirementIssues []RequirementIssue
}

// HasIssueAwaitingChecks reports whether an open sub-issue waits for the
// required checks. The poll reads the required checks only then, because
// that read is a REST call of its own.
func (s Snapshot) HasIssueAwaitingChecks() bool {
	for _, requirement := range s.RequirementIssues {
		for _, sub := range requirement.SubIssues {
			if !sub.Closed && slices.Contains(sub.Labels, LabelAwaitingChecks) {
				return true
			}
		}
	}
	return false
}

// RequirementIssue is an open issue with cumin/type/requirement.
type RequirementIssue struct {
	Number    int
	Labels    []string
	SubIssues []SubIssue
}

// SubIssue is an implementation issue: a sub-issue of a requirement issue.
type SubIssue struct {
	Number    int
	Title     string
	Closed    bool
	Labels    []string
	BlockedBy []BlockedBy
	// PullRequests are the open pull requests that close the issue (the
	// link that "Closes #N" makes). I2 finds the pull request of the
	// Implementer here, not by the branch name.
	PullRequests []PullRequest
}

// PullRequest is an open pull request that closes a sub-issue.
type PullRequest struct {
	Number int
	// HeadCommit is the full SHA of the head of the pull request.
	HeadCommit string
	// HeadBranch is the branch of the pull request. A request that
	// continues its work runs on it (I1, I4).
	HeadBranch string
	// Author is the login of the author; a GitHub App is "<slug>[bot]".
	Author string
	// Labels are the labels of the pull request now. I11 makes them equal
	// to the labels of the issue; no rule decides on them (principle 5).
	Labels []string
	// Checks are the checks on the head commit. I3 and I4 read them with
	// the required checks of the default branch.
	Checks []CheckResult
}

// CheckConclusion is what one check says. GitHub has more conclusions;
// the rules need only these three (issue-states.md, the text on required
// checks).
type CheckConclusion int

const (
	// CheckPending: the check has not finished, or has not reported yet.
	CheckPending CheckConclusion = iota
	// CheckPassed: success, skipped, or neutral.
	CheckPassed
	// CheckFailed: the check finished with any other conclusion.
	CheckFailed
)

func (c CheckConclusion) String() string {
	switch c {
	case CheckPending:
		return "pending"
	case CheckPassed:
		return "passed"
	case CheckFailed:
		return "failed"
	}
	return fmt.Sprintf("CheckConclusion(%d)", int(c))
}

// CheckResult is one check on the head commit of a pull request.
// Integration is the App that reported it, or 0 for a commit status.
type CheckResult struct {
	Name        string
	Conclusion  CheckConclusion
	Integration int64
}

// RequiredCheck is one check that the rules of the default branch require.
// Integration is the App that must report it, or 0 when the rule names no
// App. GitHub counts a check of another App as missing, so the match uses
// both.
type RequiredCheck struct {
	Name        string
	Integration int64
}

// BlockedBy is an issue that blocks a sub-issue.
type BlockedBy struct {
	Number int
	Closed bool
}

// Claim is the action of I1: replace the status label of the sub-issue with
// cumin/status/implementing, and only then request the work.
type Claim struct {
	Number           int
	RequirementIssue int
}

// StartReview is the action of I3: every required check passed on the head
// commit of the pull request, so the status label becomes
// cumin/status/reviewing. Starting the Reviewer is a later requirement.
type StartReview struct {
	Number      int
	PullRequest int
}

// Action is one thing that cumin does after a poll. Later rules add types.
type Action interface {
	isAction()
}

func (Claim) isAction()       {}
func (StartReview) isAction() {}

// Decide returns the actions for the snapshot, in the order to apply them.
// maxInProgress is the setting "max_issues_in_progress": the number of issues
// of one repository that can be in cumin/status/planning, implementing,
// awaiting-checks, or reviewing at the same time (cumin-core.md, the
// settings table). required are the checks that the rules of the default
// branch require; the caller reads them only when an issue of the
// repository waits for the checks.
//
// I3 comes before the claims of I1: an issue that leaves
// cumin/status/awaiting-checks keeps its place in the limit, so deciding it
// first never takes room from a claim.
func Decide(snapshot Snapshot, maxInProgress int, required []RequiredCheck) []Action {
	actions := reviewableSubIssues(snapshot, required)
	room := maxInProgress - inProgress(snapshot)
	for _, claim := range readySubIssues(snapshot) {
		if room <= 0 {
			break
		}
		actions = append(actions, claim)
		room--
	}
	return actions
}

// reviewableSubIssues returns the actions of I3: open sub-issues in
// cumin/status/awaiting-checks whose open pull request has every required
// check passed on its head commit.
func reviewableSubIssues(snapshot Snapshot, required []RequiredCheck) []Action {
	var actions []Action
	for _, requirement := range snapshot.RequirementIssues {
		for _, sub := range requirement.SubIssues {
			if sub.Closed || !slices.Contains(sub.Labels, LabelAwaitingChecks) {
				continue
			}
			pr, ok := sub.LatestPullRequest()
			if !ok {
				continue
			}
			if ChecksOf(required, pr.Checks) != ChecksPassed {
				continue
			}
			actions = append(actions, StartReview{Number: sub.Number, PullRequest: pr.Number})
		}
	}
	slices.SortFunc(actions, func(a, b Action) int {
		return a.(StartReview).Number - b.(StartReview).Number
	})
	return actions
}

// inProgress counts the issues that fill the limit: open sub-issues in
// implementing, awaiting-checks, or reviewing, and requirement issues in
// planning. A requirement issue in implementing (R3) has no agent of its
// own, so it does not count.
func inProgress(snapshot Snapshot) int {
	n := 0
	for _, requirement := range snapshot.RequirementIssues {
		if slices.Contains(requirement.Labels, LabelPlanning) {
			n++
		}
		for _, sub := range requirement.SubIssues {
			if sub.Closed {
				continue
			}
			for _, label := range []string{LabelImplementing, LabelAwaitingChecks, LabelReviewing} {
				if slices.Contains(sub.Labels, label) {
					n++
					break
				}
			}
		}
	}
	return n
}

// readySubIssues returns the claims of I1 before the limit: open sub-issues
// with cumin/status/ready whose blocked-by issues are all closed, lowest
// issue number first across all requirement issues.
func readySubIssues(snapshot Snapshot) []Claim {
	var claims []Claim
	for _, requirement := range snapshot.RequirementIssues {
		for _, sub := range requirement.SubIssues {
			if sub.Closed || !slices.Contains(sub.Labels, LabelReady) || blocked(sub) {
				continue
			}
			claims = append(claims, Claim{Number: sub.Number, RequirementIssue: requirement.Number})
		}
	}
	slices.SortFunc(claims, func(a, b Claim) int { return a.Number - b.Number })
	return claims
}

// ReplaceStatusLabel returns the labels of an issue with every
// cumin/status/* label removed and status added. The other labels
// (risk/*, ...) stay. A status label is always exactly one
// (issue-states.md, principle 4).
func ReplaceStatusLabel(labels []string, status string) []string {
	after := []string{}
	for _, label := range labels {
		if !IsStatusLabel(label) {
			after = append(after, label)
		}
	}
	return append(after, status)
}

// LabelsAfterClaim returns the labels of a sub-issue after I1:
// cumin/status/implementing in place of the old status label.
// issue-states.md says that cumin removes the old status label when it
// starts the work.
func LabelsAfterClaim(labels []string) []string {
	return ReplaceStatusLabel(labels, LabelImplementing)
}

// ChecksState says what the required checks of a pull request say together.
type ChecksState int

const (
	// ChecksWaiting: a required check has not reported yet, or has not
	// finished. cumin waits (I3 and I4 both need an answer first).
	ChecksWaiting ChecksState = iota
	// ChecksPassed: every required check passed. I3 applies.
	ChecksPassed
	// ChecksFailed: a required check failed. I4 applies.
	ChecksFailed
)

func (s ChecksState) String() string {
	switch s {
	case ChecksWaiting:
		return "waiting"
	case ChecksPassed:
		return "passed"
	case ChecksFailed:
		return "failed"
	}
	return fmt.Sprintf("ChecksState(%d)", int(s))
}

// ChecksOf says what the required checks say on one commit
// (issue-states.md, the text on required checks).
//
//   - An empty list of required checks passes at once.
//   - A required check is met by the results with its name. A rule that
//     names an App is met only by the results of that App; GitHub counts a
//     check of another App as missing.
//   - A required check passes when every result of it passed. Passed means
//     success, skipped, or neutral (rows 20 and 51 of
//     measured-constraints.md); the caller receives them folded already.
//   - A failed result decides the whole answer, even when another required
//     check has not reported yet: the fix of I4 comes before the wait.
//   - A required check without a result, or with one that has not
//     finished, makes the answer "waiting". The list of required checks is
//     known before a push, so a check that is missing is a check that is
//     still to come.
//
// Results that no rule requires are ignored, whatever they say.
func ChecksOf(required []RequiredCheck, results []CheckResult) ChecksState {
	state := ChecksPassed
	for _, check := range required {
		switch checkState(check, results) {
		case ChecksFailed:
			return ChecksFailed
		case ChecksWaiting:
			state = ChecksWaiting
		}
	}
	return state
}

// FailedChecks returns the names of the required checks that failed, in the
// order of the required checks. I4 names them in its request.
func FailedChecks(required []RequiredCheck, results []CheckResult) []string {
	var names []string
	for _, check := range required {
		if checkState(check, results) == ChecksFailed {
			names = append(names, check.Name)
		}
	}
	return names
}

// checkState says what one required check says.
func checkState(check RequiredCheck, results []CheckResult) ChecksState {
	found := false
	state := ChecksPassed
	for _, result := range results {
		if result.Name != check.Name {
			continue
		}
		// A rule that names an App is met only by that App.
		if check.Integration != 0 && result.Integration != check.Integration {
			continue
		}
		found = true
		switch result.Conclusion {
		case CheckFailed:
			return ChecksFailed
		case CheckPending:
			state = ChecksWaiting
		}
	}
	if !found {
		return ChecksWaiting
	}
	return state
}

// LabelsAfterReview returns the labels of a sub-issue after I3:
// cumin/status/reviewing in place of cumin/status/awaiting-checks.
func LabelsAfterReview(labels []string) []string {
	return ReplaceStatusLabel(labels, LabelReviewing)
}

// VerificationFailure says which check of I2 failed.
type VerificationFailure int

const (
	// FailureNone: the verification passed.
	FailureNone VerificationFailure = iota
	// FailureNoOpenPullRequest: no open pull request closes the issue.
	FailureNoOpenPullRequest
	// FailureAuthorMismatch: the author of the pull request is not the
	// Implementer App.
	FailureAuthorMismatch
	// FailureHeadNotPushed: the head commit of the worktree is not the
	// head of the pull request.
	FailureHeadNotPushed
)

func (f VerificationFailure) String() string {
	switch f {
	case FailureNone:
		return "none"
	case FailureNoOpenPullRequest:
		return "no open pull request closes the issue"
	case FailureAuthorMismatch:
		return "the author of the pull request is not the Implementer App"
	case FailureHeadNotPushed:
		return "the head commit of the worktree is not pushed"
	}
	return fmt.Sprintf("VerificationFailure(%d)", int(f))
}

// Verification is the result of I2 after done. Passed is true when every
// check held; otherwise Failure names the first check that failed. The
// failure paths (label, comment, notification) read Failure; this
// requirement only logs it. PullRequest is the pull request that was
// checked, or 0 when there is none.
type Verification struct {
	Passed      bool
	Failure     VerificationFailure
	PullRequest int
}

// VerifyDone applies the checks of I2 (issue-states.md) to a sub-issue
// after the Implementer returned done: an open pull request closes the
// issue; its author is implementer (the login "<slug>[bot]" of the
// Implementer App); its head commit is localHead, the head of the worktree
// (so the last commit is pushed). The snapshot holds open pull requests
// only; when two or more close the issue, the one with the highest number
// is checked. An empty implementer or an empty localHead never matches.
func VerifyDone(sub SubIssue, implementer, localHead string) Verification {
	pr, ok := sub.LatestPullRequest()
	if !ok {
		return Verification{Failure: FailureNoOpenPullRequest}
	}
	if implementer == "" || pr.Author != implementer {
		return Verification{Failure: FailureAuthorMismatch, PullRequest: pr.Number}
	}
	if localHead == "" || pr.HeadCommit != localHead {
		return Verification{Failure: FailureHeadNotPushed, PullRequest: pr.Number}
	}
	return Verification{Passed: true, PullRequest: pr.Number}
}

// LatestPullRequest returns the open pull request with the highest number
// that closes the issue. The snapshot holds open pull requests only, and
// there is normally one; when there are more, the newest one is the one
// that cumin looks at.
func (s SubIssue) LatestPullRequest() (PullRequest, bool) {
	var latest PullRequest
	found := false
	for _, pr := range s.PullRequests {
		if !found || pr.Number > latest.Number {
			latest, found = pr, true
		}
	}
	return latest, found
}

// SubIssue returns the sub-issue with the number, from any requirement issue.
func (s Snapshot) SubIssue(number int) (SubIssue, bool) {
	for _, requirement := range s.RequirementIssues {
		for _, sub := range requirement.SubIssues {
			if sub.Number == number {
				return sub, true
			}
		}
	}
	return SubIssue{}, false
}

func blocked(sub SubIssue) bool {
	for _, blocker := range sub.BlockedBy {
		if !blocker.Closed {
			return true
		}
	}
	return false
}
