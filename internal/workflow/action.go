package workflow

// This file holds the one list of the actions of cumin with their names.
// A name is the name of one transition in the tables of
// docs/ja/requirements/workflow/issue-states.md (the column of names), as
// it is written there, in lower case. The logs, the notifications, and the
// stop note name an action by it.
//
// docs/ja/designs/cumin-core.md, the topic on the names of the actions.

// ActionName is the name of one action of cumin.
type ActionName string

// The actions on a requirement issue.
const (
	ActionRequestTheSplit                ActionName = "request the split"
	ActionAskForThePlanReview            ActionName = "ask for the plan review"
	ActionRequestTheAcceptanceCheck      ActionName = "request the acceptance check"
	ActionStopTheSplit                   ActionName = "stop the split"
	ActionRequestTheSplitAgain           ActionName = "request the split again"
	ActionMarkTheRequirementAsInWork     ActionName = "mark the requirement as in work"
	ActionAskAboutTheRemainingSubIssues  ActionName = "ask about the remaining sub-issues"
	ActionAskForTheAcceptance            ActionName = "ask for the acceptance"
	ActionRequestTheAcceptanceCheckAgain ActionName = "request the acceptance check again"
	ActionStopTheAcceptanceCheck         ActionName = "stop the acceptance check"
)

// The actions on an implementation issue.
const (
	ActionRequestTheImplementation      ActionName = "request the implementation"
	ActionWaitForTheChecks              ActionName = "wait for the checks"
	ActionStopTheImplementation         ActionName = "stop the implementation"
	ActionRequestTheImplementationAgain ActionName = "request the implementation again"
	ActionRequestTheReview              ActionName = "request the review"
	ActionRequestACheckFix              ActionName = "request a check fix"
	ActionStopForFailedChecks           ActionName = "stop for failed checks"
	ActionRequestAConflictResolution    ActionName = "request a conflict resolution"
	ActionStopForMissingChecks          ActionName = "stop for missing checks"
	ActionRequestAReviewFix             ActionName = "request a review fix"
	ActionStartTheMerge                 ActionName = "start the merge"
	ActionAskForTheMergeDecision        ActionName = "ask for the merge decision"
	ActionRequestTheCause               ActionName = "request the cause"
	ActionStopAtTheRoundLimit           ActionName = "stop at the round limit"
	ActionStopTheReview                 ActionName = "stop the review"
	ActionGoBackToTheChecks             ActionName = "go back to the checks"
	ActionRequestTheReviewAgain         ActionName = "request the review again"
	ActionSendBackForChanges            ActionName = "send back for changes"
	ActionCloseTheMergedIssue           ActionName = "close the merged issue"
	ActionStopTheMerge                  ActionName = "stop the merge"
)

// The actions that follow no status label.
const (
	ActionWriteTheFollowUpNote          ActionName = "write the follow-up note"
	ActionCopyTheLabelsToThePullRequest ActionName = "copy the labels to the pull request"
	ActionStopAgentStarts               ActionName = "stop agent starts"
	ActionResumeAgentStarts             ActionName = "resume agent starts"
	ActionTellThatCuminWaits            ActionName = "tell that cumin waits"
)

// ActionNames is every action of cumin, in the order of the tables of
// issue-states.md.
var ActionNames = []ActionName{
	ActionRequestTheSplit,
	ActionAskForThePlanReview,
	ActionRequestTheAcceptanceCheck,
	ActionStopTheSplit,
	ActionRequestTheSplitAgain,
	ActionMarkTheRequirementAsInWork,
	ActionAskAboutTheRemainingSubIssues,
	ActionAskForTheAcceptance,
	ActionRequestTheAcceptanceCheckAgain,
	ActionStopTheAcceptanceCheck,
	ActionRequestTheImplementation,
	ActionWaitForTheChecks,
	ActionStopTheImplementation,
	ActionRequestTheImplementationAgain,
	ActionRequestTheReview,
	ActionRequestACheckFix,
	ActionStopForFailedChecks,
	ActionRequestAConflictResolution,
	ActionStopForMissingChecks,
	ActionRequestAReviewFix,
	ActionStartTheMerge,
	ActionAskForTheMergeDecision,
	ActionRequestTheCause,
	ActionStopAtTheRoundLimit,
	ActionStopTheReview,
	ActionGoBackToTheChecks,
	ActionRequestTheReviewAgain,
	ActionSendBackForChanges,
	ActionCloseTheMergedIssue,
	ActionStopTheMerge,
	ActionWriteTheFollowUpNote,
	ActionCopyTheLabelsToThePullRequest,
	ActionStopAgentStarts,
	ActionResumeAgentStarts,
	ActionTellThatCuminWaits,
}
