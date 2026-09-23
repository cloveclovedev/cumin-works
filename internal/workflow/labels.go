package workflow

import "github.com/cloveclovedev/cumin-works/internal/platform/github"

// RepositoryLabels returns the labels that cumin creates in a target
// repository when it starts, when they are missing: the table "Labels" of
// docs/ja/requirements/workflow/issue-states.md. The colors are the ones of
// the cumin-works repository. A test compares the names with the document.
// One type label, seven status labels, and three risk labels.
func RepositoryLabels() []github.Label {
	return []github.Label{
		{Name: LabelRequirement, Color: "5319E7", Description: "This is a requirement issue"},
		{Name: LabelReady, Color: "0E8A16", Description: "The Owner says: this issue can start"},
		{Name: LabelPlanning, Color: "1D76DB", Description: "The Planner splits the requirement"},
		{Name: LabelImplementing, Color: "1D76DB", Description: "The Implementer works on the issue, or the sub-issues are in progress"},
		{Name: LabelAwaitingChecks, Color: "BFD4F2", Description: "The Implementer is done; waiting for the required checks"},
		{Name: LabelReviewing, Color: "1D76DB", Description: "The Reviewer works on the pull request"},
		{Name: LabelAwaitingOwnerReview, Color: "FBCA04", Description: "Waiting for the Owner to review and approve"},
		{Name: LabelAwaitingOwnerDecision, Color: "D93F0B", Description: "The agent cannot continue; waiting for a decision of the Owner"},
		{Name: "risk/low", Color: "C2E0C6", Description: "A few lines with an obvious effect; cumin merges"},
		{Name: "risk/medium", Color: "FEF2C0", Description: "Everything else; the Owner merges"},
		{Name: "risk/high", Color: "F9D0C4", Description: "Cannot be undone by a revert; the Owner merges"},
	}
}
