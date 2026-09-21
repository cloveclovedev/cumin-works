// Package workflow holds the rules of cumin (the rows R*, I*, and Q* of
// docs/ja/requirements/workflow/issue-states.md) and the polling loop that
// applies them.
//
// This file is the pure part: the snapshot of the facts on GitHub, the
// actions, and the decision. It imports no HTTP client, no os/exec, and no
// provider package. The same snapshot always gives the same actions.
package workflow

import (
	"slices"
	"strings"
)

// Label names from the table in issue-states.md.
const (
	LabelRequirement = "cumin/type/requirement"

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
	RequirementIssues []RequirementIssue
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
	Closed    bool
	Labels    []string
	BlockedBy []BlockedBy
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

// Action is one thing that cumin does after a poll. Later rules add types.
type Action interface {
	isAction()
}

func (Claim) isAction() {}

// Decide returns the actions for the snapshot, in the order to apply them.
// maxInProgress is the setting "max_issues_in_progress": the number of issues
// of one repository that can be in cumin/status/planning, implementing,
// awaiting-checks, or reviewing at the same time (cumin-core.md, the
// settings table).
func Decide(snapshot Snapshot, maxInProgress int) []Action {
	room := maxInProgress - inProgress(snapshot)
	var actions []Action
	for _, claim := range readySubIssues(snapshot) {
		if room <= 0 {
			break
		}
		actions = append(actions, claim)
		room--
	}
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

// LabelsAfterClaim returns the labels of a sub-issue after I1: every
// cumin/status/* label is removed, and cumin/status/implementing is added.
// The other labels (risk/*, ...) stay. issue-states.md says that cumin
// removes the old status label when it starts the work.
func LabelsAfterClaim(labels []string) []string {
	after := []string{}
	for _, label := range labels {
		if !IsStatusLabel(label) {
			after = append(after, label)
		}
	}
	return append(after, LabelImplementing)
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
