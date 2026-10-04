package workflow

import "github.com/cloveclovedev/cumin-works/internal/platform/github"

// RepositoryLabels returns the labels that cumin creates in a target
// repository when it starts, when they are missing: the table "Labels" of
// docs/ja/requirements/workflow/issue-states.md. The colors are the ones of
// the cumin-works repository. A test compares the names with the document.
// Two type labels, fourteen status labels (seven of today and the seven new
// names of the move of the labels), and three risk labels.
func RepositoryLabels() []github.Label {
	return []github.Label{
		{Name: LabelRequirement, Color: "5319E7", Description: "This is a requirement issue"},
		{Name: LabelOwnerTask, Color: "5319E7", Description: "The Owner does this work by hand; cumin does not start it"},
		{Name: LabelReady, Color: "0E8A16", Description: "The Owner says: this issue can start"},
		{Name: LabelPlanning, Color: "1D76DB", Description: "The Planner splits the requirement"},
		{Name: LabelImplementing, Color: "1D76DB", Description: "The Implementer works on the issue, or the sub-issues are in progress"},
		{Name: LabelAwaitingChecks, Color: "BFD4F2", Description: "The Implementer is done; waiting for the required checks"},
		{Name: LabelReviewing, Color: "1D76DB", Description: "The Reviewer works on the pull request"},
		{Name: LabelAwaitingOwnerReview, Color: "FBCA04", Description: "Waiting for the Owner to review and approve"},
		{Name: LabelAwaitingOwnerDecision, Color: "D93F0B", Description: "The agent cannot continue; waiting for a decision of the Owner"},
		{Name: LabelChecking, Color: "BFD4F2", Description: "GitHub runs the required checks"},
		{Name: LabelAccepting, Color: "1D76DB", Description: "The Planner checks the merged work against the requirement"},
		{Name: LabelMerging, Color: "1D76DB", Description: "cumin merges the pull request and closes the issue"},
		{Name: LabelAwaitingPlanReview, Color: "FBCA04", Description: "Waiting for the Owner to review the plan and the sub-issues"},
		{Name: LabelAwaitingMergeDecision, Color: "FBCA04", Description: "Waiting for the Owner to review the pull request and decide the merge"},
		{Name: LabelAwaitingAcceptance, Color: "FBCA04", Description: "Waiting for the Owner to accept the requirement or send work back"},
		{Name: LabelAwaitingDecision, Color: "D93F0B", Description: "cumin cannot go on; waiting for an answer of the Owner"},
		{Name: "risk/low", Color: "C2E0C6", Description: "A few lines with an obvious effect; cumin merges"},
		{Name: "risk/medium", Color: "FEF2C0", Description: "Everything else; the Owner merges"},
		{Name: "risk/high", Color: "F9D0C4", Description: "Cannot be undone by a revert; the Owner merges"},
	}
}

// DefaultPriorityLabels returns the priority labels that cumin creates in a
// target repository whose settings name none (names are
// config.DefaultPriorityLabels, highest priority first). Labels that a
// settings file names belong to the organization: cumin never creates or
// changes them.
func DefaultPriorityLabels(names []string) []github.Label {
	labels := make([]github.Label, 0, len(names))
	for _, name := range names {
		labels = append(labels, github.Label{Name: name, Color: "D4C5F9", Description: "The Owner says: start this before a lower priority"})
	}
	return labels
}
