// Package workflow holds the rules of cumin (the transitions of
// docs/ja/requirements/workflow/issue-states.md) and the polling loop that
// applies them.
//
// This file is the pure part: the snapshot of the facts on GitHub, the
// actions, and the decision. It imports no HTTP client, no os/exec, and no
// provider package. The same snapshot always gives the same actions.
package workflow

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Label names from the table in issue-states.md.
const (
	LabelRequirement = "cumin/type/requirement"
	// LabelOwnerTask marks a sub-issue whose work a Maintainer does by hand.
	// cumin never claims it ("request the implementation").
	LabelOwnerTask = "cumin/type/owner-task"

	LabelReady                 = "cumin/status/ready"
	LabelPlanning              = "cumin/status/planning"
	LabelImplementing          = "cumin/status/implementing"
	LabelReviewing             = "cumin/status/reviewing"
	LabelChecking              = "cumin/status/checking"
	LabelAccepting             = "cumin/status/accepting"
	LabelMerging               = "cumin/status/merging"
	LabelAwaitingPlanReview    = "cumin/status/awaiting-plan-review"
	LabelAwaitingMergeDecision = "cumin/status/awaiting-merge-decision"
	LabelAwaitingAcceptance    = "cumin/status/awaiting-acceptance"
	LabelAwaitingDecision      = "cumin/status/awaiting-decision"

	statusLabelPrefix = "cumin/status/"
	riskLabelPrefix   = "risk/"
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
	// Unread are the requirement issues that cumin cannot read in full, as
	// much as was read of them. Only the count of the issues in progress
	// reads them (inProgress): no rule decides on a partial issue.
	Unread []RequirementIssue
	// Running are the issues whose agent runs in this cumin now. The rules
	// of a working label skip a running issue: its run decides its own end.
	Running map[int]bool
}

// SubIssuesWithPullRequestRules returns the sub-issues whose pull requests
// the poll reads in its second query: the open sub-issues with a
// cumin/status/* label. Every rule of the poll that reads a pull request
// applies to such a sub-issue only (docs/ja/designs/poll.md, the topic on
// the two queries). The order is the order of the snapshot.
func (s Snapshot) SubIssuesWithPullRequestRules() []SubIssue {
	var selected []SubIssue
	for _, requirement := range s.RequirementIssues {
		for _, sub := range requirement.SubIssues {
			if !sub.Closed && slices.ContainsFunc(sub.Labels, IsStatusLabel) {
				selected = append(selected, sub)
			}
		}
	}
	return selected
}

// WithPullRequests returns the snapshot with the pull requests of the second
// query of the poll, by the number of the sub-issue. A sub-issue that the
// second query did not read has no pull request.
func (s Snapshot) WithPullRequests(pullRequests map[int][]PullRequest) Snapshot {
	requirements := slices.Clone(s.RequirementIssues)
	for i, requirement := range requirements {
		requirements[i].SubIssues = slices.Clone(requirement.SubIssues)
		for j, sub := range requirements[i].SubIssues {
			requirements[i].SubIssues[j].PullRequests = pullRequests[sub.Number]
		}
	}
	s.RequirementIssues = requirements
	return s
}

// RequirementOf returns the number of the requirement issue that holds the
// sub-issue, or 0 when the snapshot has no such sub-issue.
func (s Snapshot) RequirementOf(subIssue int) int {
	for _, requirement := range s.RequirementIssues {
		for _, sub := range requirement.SubIssues {
			if sub.Number == subIssue {
				return requirement.Number
			}
		}
	}
	return 0
}

// WithoutRequirementIssues returns the snapshot without the requirement
// issues of the numbers, and so without their sub-issues. The poll removes
// the requirement issue of an issue that cumin cannot read in full: no rule
// decides on a partial issue. The removed issues go to Unread, so that
// their work still fills the limit of the issues in progress.
func (s Snapshot) WithoutRequirementIssues(numbers map[int]bool) Snapshot {
	if len(numbers) == 0 {
		return s
	}
	var read []RequirementIssue
	unread := slices.Clone(s.Unread)
	for _, requirement := range s.RequirementIssues {
		if numbers[requirement.Number] {
			unread = append(unread, requirement)
		} else {
			read = append(read, requirement)
		}
	}
	s.RequirementIssues, s.Unread = read, unread
	return s
}

// RequirementIssue is an open issue with cumin/type/requirement.
type RequirementIssue struct {
	Number int
	// Title is for the monitor file only. No rule reads it.
	Title  string
	Labels []string
	// LabelsUnread is true when the issue has more labels than Labels
	// holds. Only an issue of Snapshot.Unread can have it.
	LabelsUnread bool
	SubIssues    []SubIssue
	// BlockedBy are the issues that block the requirement issue. A Maintainer
	// links requirement issues to each other, and "request the split" waits
	// for them.
	BlockedBy []BlockedBy
	// LabelTimesRead says that ReviewAt and the ReadyAt, CheckingAt,
	// and AwaitingMergeDecisionAt of the sub-issues were read. The poll reads them
	// only when a rule needs them (NeedsLabelTimes).
	LabelTimesRead bool
	// ReviewAt is when the status label of the issue was last added. "mark
	// the requirement as in work"
	// reads it in cumin/status/awaiting-plan-review and in
	// cumin/status/awaiting-acceptance, and the end of the acceptance check
	// reads it in cumin/status/accepting.
	ReviewAt time.Time
	// CommentsRead says that AcceptanceCheckAt was read. The poll reads
	// the comments only when "request the acceptance check" or "ask for the
	// acceptance" needs them (NeedsComments).
	CommentsRead bool
	// AcceptanceCheckAt is when the newest acceptance check comment of the
	// Planner App was written; zero when there is none.
	AcceptanceCheckAt time.Time
	// QuestionAt is when the newest decision request of the Planner App was
	// written on the issue; zero when there is none. It is read with
	// AcceptanceCheckAt.
	QuestionAt time.Time
	// AcceptanceRequestedAgain says that cumin already requested the
	// acceptance check again during this stay in cumin/status/accepting.
	// The count lives in the state file of the Host; a lost file reads as
	// not requested again.
	AcceptanceRequestedAgain bool
	// SplitRequestedAgain says that cumin already requested the split again
	// during this stay in cumin/status/planning. The count lives in the
	// state file of the Host; a lost file reads as not requested again.
	SplitRequestedAgain bool
	// ReadyRead says that ReadyOwner was read. The poll reads it only for
	// a candidate of a start, and only when a slot is free
	// (ReadyActorReads).
	ReadyRead bool
	// ReadyOwner is the login of the account that added the newest
	// cumin/status/ready, when that account is a Maintainer (IsMaintainer). It is
	// empty when another account added it, and when it was not read.
	ReadyOwner string
	// StatusRead says that StatusCounts was read. The poll reads it only
	// when cumin is about to act from the status label (StatusActorReads).
	StatusRead bool
	// StatusCounts says that the account that added the newest status label
	// of the issue is the cumin-core App or a Maintainer (StatusLabelCounts).
	// It is false when another account added it, and when it was not read.
	StatusCounts bool
	// FollowUpsDone says that no closed sub-issue needs a follow-up note
	// any more: each one has its note, was closed without a merge, or
	// left nothing to copy. The poll sets it after "write the follow-up
	// note", and "request the acceptance check" waits for it.
	FollowUpsDone bool
}

// SubIssue is an implementation issue: a sub-issue of a requirement issue.
type SubIssue struct {
	Number int
	// NodeID is the GraphQL ID of the issue. "wait for the checks" adds the
	// closing link with it.
	NodeID string
	Title  string
	Closed bool
	Labels []string
	// LabelsUnread is true when the issue has more labels than Labels
	// holds. Only a sub-issue of Snapshot.Unread can have it.
	LabelsUnread bool
	BlockedBy    []BlockedBy
	// PullRequests are the open pull requests that close the issue (the
	// closing link). Every rule reads the pull request of the issue here.
	// Only the check of the pull request after the Implementer ends finds
	// it by the branch, and then adds the link when it is missing.
	PullRequests []PullRequest
	// ReadyAt is when cumin/status/ready was last added. It is read only
	// when RequirementIssue.LabelTimesRead is true.
	ReadyAt time.Time
	// CheckingAt is when cumin/status/checking was last added.
	// It is read only when RequirementIssue.LabelTimesRead is true.
	CheckingAt time.Time
	// AwaitingMergeDecisionAt is when cumin/status/awaiting-merge-decision was
	// last added. It is read only when RequirementIssue.LabelTimesRead is
	// true.
	AwaitingMergeDecisionAt time.Time
	// ClosedAt is when a closed sub-issue closed.
	ClosedAt time.Time
	// ReadyRead says that ReadyOwner was read. The poll reads it only for
	// a candidate of a start, and only when a slot is free
	// (ReadyActorReads).
	ReadyRead bool
	// ReadyOwner is the login of the account that added the newest
	// cumin/status/ready, when that account is a Maintainer (IsMaintainer). It is
	// empty when another account added it, and when it was not read.
	ReadyOwner string
	// Implementing are the facts that decide the way out of
	// cumin/status/implementing (ImplementationEnd). It is nil when they
	// were not read: the issue is in another state, its Implementer runs,
	// or a read failed.
	Implementing *ImplementingFacts
	// Reviewing are the facts that decide the way out of
	// cumin/status/reviewing (ReviewEnd). It is nil when they were not read:
	// the issue is in another state, its Reviewer runs, or a read failed.
	Reviewing *ReviewingFacts
	// Merging are the facts that decide each step in cumin/status/merging
	// (MergeEnd). It is nil when they were not read: the issue is in
	// another state, a step of it runs, or a read failed.
	Merging *MergingFacts
}

// MergingFacts is what cumin reads at every poll to decide the step of one
// implementation issue in cumin/status/merging.
type MergingFacts struct {
	// StatusCounts says that the cumin-core App or a Maintainer added the
	// newest cumin/status/merging (StatusLabelCounts).
	StatusCounts bool
	// Merged is the number of the newest pull request that is linked to
	// close the issue, when that one is merged, or 0. A merged pull request
	// of an earlier stay with a newer closed one does not count. It is read
	// only when the issue has no open pull request.
	Merged int
	// Reviewer is the login "<slug>[bot]" of the Reviewer App.
	Reviewer string
	// Maintainers says, for each person whose review decides, whether that
	// person is a Maintainer (IsMaintainer).
	Maintainers map[string]bool
	// Required are the checks that the rules of the default branch
	// require.
	Required []RequiredCheck
}

// ReviewingFacts is what cumin reads to decide the way out of
// cumin/status/reviewing for one implementation issue whose Reviewer does
// not run. The poll and the end of a Reviewer run read the same facts.
type ReviewingFacts struct {
	// StatusCounts says that the cumin-core App or a Maintainer added the
	// newest cumin/status/reviewing (StatusLabelCounts).
	StatusCounts bool
	// ReviewingAt is when cumin/status/reviewing was last added.
	ReviewingAt time.Time
	// ReadyAt is when cumin/status/ready was last added: the start of the
	// count of the rounds.
	ReadyAt time.Time
	// QuestionAt is when the newest decision request of the Reviewer App or
	// of cumin-core was written on the issue, or zero when there is none.
	QuestionAt time.Time
	// Reviewer is the login "<slug>[bot]" of the Reviewer App.
	Reviewer string
	// RequestedHead is the head commit that the review of this stay was
	// requested on, from the state file. It is empty when the state file
	// does not hold it; then no head commit counts as moved.
	RequestedHead string
	// RequestedAgain says that the state file holds a second request of
	// the review during this stay in cumin/status/reviewing.
	RequestedAgain bool
	// CauseRequested says that the Reviewer run that just returned done was
	// the request of the cause at the round limit. Only the end of that run
	// sets it.
	CauseRequested bool
	// CauseRequestedAgain says that the state file holds a second request
	// of the cause during this stay in cumin/status/reviewing.
	CauseRequestedAgain bool
	// Required are the checks that the rules of the default branch
	// require. They are read only after an approval of the head commit.
	Required []RequiredCheck
	// Limit is the setting max_review_rounds.
	Limit int
	// Explanation is the decision request that the Reviewer wrote on the
	// pull request after its latest review, and Explained says that there
	// is one. They are read only at the round limit.
	Explanation Comment
	Explained   bool
}

// ImplementingFacts is what cumin reads to decide the way out of
// cumin/status/implementing for one implementation issue whose Implementer
// does not run. The poll and the end of an Implementer run read the same
// facts.
type ImplementingFacts struct {
	// StatusCounts says that the cumin-core App or a Maintainer added the
	// newest cumin/status/implementing (StatusLabelCounts).
	StatusCounts bool
	// ImplementingAt is when cumin/status/implementing was last added.
	ImplementingAt time.Time
	// QuestionAt is when the newest decision request of the Implementer
	// App or of cumin-core was written, or zero when there is none.
	QuestionAt time.Time
	// RequestedAgain says that the state file holds a second request of
	// the implementation during this stay in cumin/status/implementing.
	RequestedAgain bool
	// ConflictRequested says that the state file holds a conflict
	// resolution as the request of this stay.
	ConflictRequested bool
	// Branch is the branch that cumin chose for the issue, and OnBranch
	// are the open pull requests whose head is that branch.
	Branch   string
	OnBranch []PullRequest
	// Implementer is the login "<slug>[bot]" of the Implementer App.
	Implementer string
	// LocalHead is the head commit of the worktree of the issue, or empty
	// when the Host holds no worktree.
	LocalHead string
	// MaxLinks is the most open closing pull requests that the poll reads
	// for one issue (VerifyDone).
	MaxLinks int
}

// PullRequest is an open pull request that closes a sub-issue.
type PullRequest struct {
	Number int
	// NodeID is the GraphQL ID of the pull request. Only the pull requests
	// of ImplementingFacts.OnBranch hold it; "wait for the checks" adds the
	// closing link with it.
	NodeID string
	// HeadCommit is the full SHA of the head of the pull request.
	HeadCommit string
	// HeadBranch is the branch of the pull request. A request that
	// continues its work runs on it ("request the implementation",
	// "request a check fix").
	HeadBranch string
	// Author is the login of the author; a GitHub App is "<slug>[bot]".
	Author string
	// Labels are the labels of the pull request now. "copy the labels to
	// the pull request" makes them equal
	// to the labels of the issue; no rule decides on them (principle 5).
	Labels []string
	// Checks are the checks on the head commit. "request the review" and
	// "request a check fix" read them with
	// the required checks of the default branch.
	Checks []CheckResult
	// Reviews are the reviews of the pull request. The round of the review
	// and the check after a Reviewer run read them ("request the review",
	// "request a review fix", "request the cause").
	Reviews []Review
	// Mergeable is what GitHub says about a merge into the base branch.
	Mergeable MergeableState
	// HeadCommittedAt is the commit time of the head commit, or zero when
	// the poll could not read it.
	HeadCommittedAt time.Time
}

// MergeableState is the mergeability of a pull request on GitHub (the
// GraphQL schema, MergeableState).
type MergeableState string

const (
	// Mergeable: the pull request can be merged.
	Mergeable MergeableState = "MERGEABLE"
	// Conflicting: the pull request has merge conflicts.
	Conflicting MergeableState = "CONFLICTING"
	// MergeableUnknown: GitHub is still calculating the mergeability.
	MergeableUnknown MergeableState = "UNKNOWN"
)

// ReviewState is the state of a review on GitHub (the GraphQL schema,
// PullRequestReviewState).
type ReviewState string

const (
	ReviewApproved         ReviewState = "APPROVED"
	ReviewChangesRequested ReviewState = "CHANGES_REQUESTED"
	ReviewCommented        ReviewState = "COMMENTED"
	ReviewDismissed        ReviewState = "DISMISSED"
	ReviewPending          ReviewState = "PENDING"
)

// Review is one review of a pull request.
type Review struct {
	// Author is the login; a GitHub App is "<slug>[bot]".
	Author string
	State  ReviewState
	// Commit is the full SHA that the review is on, or empty when that
	// commit is gone.
	Commit string
	// SubmittedAt is zero for a pending review.
	SubmittedAt time.Time
	URL         string
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

// Claim is the action "request the implementation": replace the status label
// of the sub-issue with cumin/status/implementing, and only then request the
// work.
type Claim struct {
	Number           int
	RequirementIssue int
}

// StartReview is the action "request the review": every required check
// passed on the head commit of the pull request, so the status label becomes
// cumin/status/reviewing, and only then cumin starts the Reviewer
// (review.go).
type StartReview struct {
	Number      int
	PullRequest int
}

// FixChecks is the action "request a check fix": a required check failed on
// the head commit of the pull request, so the issue goes back to the
// Implementer with the failed checks, in the same session. Whether the limit
// of check fix requests allows it is decided when it is applied, from the
// count that the Host keeps (CheckFixAllowed).
type FixChecks struct {
	Number      int
	PullRequest int
	Failed      []RequiredCheck
}

// ResolveConflict is the action "request a conflict resolution": the pull
// request of an issue that waits for the checks, or for the review of a
// Maintainer, conflicts with the default branch, so the issue
// goes back to the Implementer for a conflict resolution, in the same
// session. GitHub runs no pull_request workflow on a pull request that
// conflicts, so its required checks would never report.
type ResolveConflict struct {
	Number      int
	PullRequest int
}

// StopForUnreportedChecks is the action "stop for missing checks": a
// required check has not
// reported on the head commit of the pull request, and the wait time of the
// repository is over, so the issue stops for a Maintainer. It carries the facts
// that cumin sees; it names no cause. The rule also holds when no open pull
// request closes the issue, for example after someone closed it: the wait
// then counts from the label alone.
type StopForUnreportedChecks struct {
	Number int
	// PullRequest is the number of the open pull request, or 0 when no
	// open pull request closes the issue. HeadCommit and Unreported are
	// then empty.
	PullRequest int
	// HeadCommit is the full SHA of the head of the pull request.
	HeadCommit string
	// Unreported are the required checks without a finished result on the
	// head commit, in the order of the required checks.
	Unreported []RequiredCheck
	// Waited is the time from the start of the wait to the poll.
	Waited time.Duration
}

// CopyLabels is the action "copy the labels to the pull request": the pull
// request gets Labels, so that its
// cumin/status/* and risk/* labels are those of the issue that it closes.
type CopyLabels struct {
	Issue       int
	PullRequest int
	Labels      []string
}

// Plan is the action "request the split": replace the status label of the
// requirement issue with cumin/status/planning, and only then request the
// split from the Planner. With Again, it is the action "request the split
// again": the issue is already in cumin/status/planning, and the Planner left
// no split that passes the check there.
type Plan struct {
	Number int
	Again  bool
}

// ReviewPlan is the action "ask for the plan review": the
// split passes the check and a sub-issue is open, so the requirement issue
// moves from cumin/status/planning to cumin/status/awaiting-plan-review and
// cumin notifies that the split needs a review.
type ReviewPlan struct {
	Number int
}

// StopSplit is the action "stop the split": the requirement
// issue moves from cumin/status/planning to cumin/status/awaiting-decision.
// With Question, the Planner wrote a decision request, and cumin writes no
// reason of its own. Otherwise Reason is the sentence of the check that the
// split failed the second time.
type StopSplit struct {
	Number   int
	Question bool
	Reason   string
}

// WaitForChecks is the action "wait for the checks": the pull request
// of the implementation issue passes the check, so the issue moves from
// cumin/status/implementing to cumin/status/checking. With AddLink,
// cumin-core first adds the closing link to the pull request.
type WaitForChecks struct {
	Number            int
	PullRequest       int
	PullRequestNodeID string
	AddLink           bool
}

// RequestImplementationAgain is the action "request the implementation
// again": the issue is in cumin/status/implementing, no Implementer runs,
// and the pull request does not pass the check. It is sent once for each
// stay in cumin/status/implementing.
type RequestImplementationAgain struct {
	Number int
}

// StopImplementation is the action "stop the implementation":
// the implementation issue moves from cumin/status/implementing to
// cumin/status/awaiting-decision. With Question, the Implementer wrote a
// decision request, and cumin writes no reason of its own. Otherwise Reason
// is the sentence for a Maintainer, PullRequest the pull request that it is
// about (0 for none), and Retried says that the implementation was
// requested again before.
type StopImplementation struct {
	Number      int
	Question    bool
	Reason      string
	PullRequest int
	Retried     bool
}

// RequestReviewFix is the action "request a review fix": the Reviewer
// requested changes on the head commit below the round limit, so the issue
// moves from cumin/status/reviewing to cumin/status/implementing, and the
// Implementer fixes the comments of Review.
type RequestReviewFix struct {
	Number      int
	PullRequest int
	Round       int
	Review      Review
}

// AskMaintainerToMerge is the action "ask for the merge decision":
// the Reviewer approved the head commit, the required checks pass, and the
// risk is risk/medium or risk/high.
type AskMaintainerToMerge struct {
	Number      int
	PullRequest int
}

// StartMerge is the action "start the merge" after an approval of the
// Reviewer with risk/low: only the label changes to cumin/status/merging.
// The merge is sent inside that state (MergeEnd).
type StartMerge struct {
	Number      int
	PullRequest int
}

// CloseMergedIssue is the action "close the merged issue": the pull request
// of an issue in cumin/status/merging is merged, so cumin closes the issue
// when GitHub did not.
type CloseMergedIssue struct {
	Number      int
	PullRequest int
}

// SendMerge sends the merge of the approved head commit for an issue in
// cumin/status/merging: the pull request is open, and the conditions of
// the merge hold (MergeConditionsHold).
type SendMerge struct {
	Number      int
	PullRequest int
	HeadCommit  string
}

// ResolveMergeConflict is the action "request a conflict resolution" from
// cumin/status/merging: the pull request is open, the conditions of the
// merge hold, and GitHub reports that it conflicts with the default branch.
// No merge is sent, and the issue goes back to the Implementer for a
// conflict resolution, in the same session.
type ResolveMergeConflict struct {
	Number      int
	PullRequest int
}

// LeaveMerge is the action "go back to the checks" from
// cumin/status/merging: the pull request is not merged, and the conditions
// of the merge do not hold. The issue moves to cumin/status/checking, and
// no merge is sent.
type LeaveMerge struct {
	Number int
}

// RequestCause is the action "request the cause" from the Reviewer:
// blocking comments remain at the round limit, and the Reviewer wrote no
// decision request after Review yet.
type RequestCause struct {
	Number      int
	PullRequest int
	Review      Review
}

// StopAtRoundLimit is the action "stop at the round limit": the
// Reviewer wrote Explanation on the pull request, so the issue moves to
// cumin/status/awaiting-decision with one notification.
type StopAtRoundLimit struct {
	Number      int
	Explanation Comment
}

// BackToChecks is the action "go back to the checks": the head commit moved
// during the review, or a required check does not pass on the approved head
// commit. The issue moves from cumin/status/reviewing to
// cumin/status/checking. A review of the old head commit stays, and counts
// as a round.
type BackToChecks struct {
	Number    int
	HeadMoved bool
}

// RequestReviewAgain is the action "request the review again": the head
// commit has no review of the Reviewer. It is sent once for each stay in
// cumin/status/reviewing.
type RequestReviewAgain struct {
	Number      int
	PullRequest int
}

// StopReview is the action "stop the review": the
// implementation issue moves from cumin/status/reviewing to
// cumin/status/awaiting-decision. With Question, the Reviewer wrote a
// decision request, and cumin writes no reason of its own. Otherwise Action
// and Reason are the action and the sentence of the stop note, and Retried says that
// the review was requested again before.
type StopReview struct {
	Number      int
	Question    bool
	Action      ActionName
	Reason      string
	PullRequest int
	Retried     bool
}

// StartRequirement is the action "mark the requirement as in work": a
// Maintainer let a sub-issue start, so the requirement issue moves to
// cumin/status/implementing.
type StartRequirement struct {
	Number int
}

// ReviewRemaining is the action "ask about the remaining sub-issues": every
// open sub-issue has no status label, so the requirement issue moves to
// cumin/status/awaiting-plan-review and cumin notifies that the
// remaining sub-issues need a look.
type ReviewRemaining struct {
	Number int
}

// CheckAcceptance is the action "request the acceptance check": move
// the requirement issue to cumin/status/accepting, and then request the
// acceptance check from the Planner. With Again, it is the action "request
// the acceptance check again": the issue is already in
// cumin/status/accepting, and the Planner left no result there.
type CheckAcceptance struct {
	Number int
	Again  bool
}

// StopAcceptance is the action "stop the acceptance check":
// the requirement issue moves from cumin/status/accepting to
// cumin/status/awaiting-decision. With Question, the Planner wrote a
// decision request, and cumin writes no reason of its own.
type StopAcceptance struct {
	Number   int
	Question bool
}

// Accept is the action "ask for the acceptance": the acceptance check
// comment exists, so the
// requirement issue moves to cumin/status/awaiting-acceptance and cumin
// notifies that it can be accepted.
type Accept struct {
	Number int
}

// MergeMaintainerApproval is the candidate of "start the merge" after the
// approval of a Maintainer: an implementation issue in
// cumin/status/awaiting-merge-decision whose pull request has an APPROVED
// review of a person on its head commit. Reviewers are the people whose
// reviews decide (APPROVED or CHANGES_REQUESTED); the caller reads their
// permission, and only then knows which of them is a Maintainer
// (MaintainerApproved).
type MergeMaintainerApproval struct {
	Number      int
	PullRequest int
	Reviewers   []string
}

// FixMaintainerReview is the candidate of "send back for changes": an
// implementation issue in
// cumin/status/awaiting-merge-decision whose pull request has a
// CHANGES_REQUESTED review of a person on its head commit, submitted after
// that label was last added. Reviewers are the people whose reviews decide,
// as for MergeMaintainerApproval; the caller reads their permission, and only
// then knows whether the latest review of a Maintainer requests changes
// (MaintainerRequestedChanges).
type FixMaintainerReview struct {
	Number      int
	PullRequest int
	Reviewers   []string
}

// Action is one thing that cumin does after a poll.
type Action interface {
	isAction()
}

func (Claim) isAction()                      {}
func (StartReview) isAction()                {}
func (FixChecks) isAction()                  {}
func (ResolveConflict) isAction()            {}
func (StopForUnreportedChecks) isAction()    {}
func (CopyLabels) isAction()                 {}
func (Plan) isAction()                       {}
func (ReviewPlan) isAction()                 {}
func (StopSplit) isAction()                  {}
func (StartRequirement) isAction()           {}
func (ReviewRemaining) isAction()            {}
func (CheckAcceptance) isAction()            {}
func (Accept) isAction()                     {}
func (StopAcceptance) isAction()             {}
func (MergeMaintainerApproval) isAction()    {}
func (FixMaintainerReview) isAction()        {}
func (WaitForChecks) isAction()              {}
func (RequestImplementationAgain) isAction() {}
func (StopImplementation) isAction()         {}
func (RequestReviewFix) isAction()           {}
func (AskMaintainerToMerge) isAction()       {}
func (StartMerge) isAction()                 {}
func (CloseMergedIssue) isAction()           {}
func (SendMerge) isAction()                  {}
func (ResolveMergeConflict) isAction()       {}
func (LeaveMerge) isAction()                 {}
func (RequestCause) isAction()               {}
func (StopAtRoundLimit) isAction()           {}
func (BackToChecks) isAction()               {}
func (RequestReviewAgain) isAction()         {}
func (StopReview) isAction()                 {}

// Decide returns the actions for the snapshot, in the order to apply them.
// maxInProgress is the setting "max_issues_in_progress": the number of issues
// of one repository that can be in cumin/status/planning, implementing,
// checking, reviewing, or merging at the same time (cumin-core.md, the
// settings table). required are the checks that the rules of the default
// branch require; the caller reads them only when an issue of the
// repository waits for the checks. now is the time of the poll, and
// checksWait is the setting "checks_wait_time"; "stop for missing checks"
// reads both.
//
// "request a conflict resolution", "request the review", "request a check
// fix", and "stop for missing checks" come before the starts of "request the
// split" and "request the implementation": an issue that
// leaves cumin/status/checking keeps its place in the limit, so
// deciding it first never takes room from a start. "request a conflict
// resolution" (the pull request conflicts) comes before "request the review"
// and "request a check fix" for its issue, and "stop for missing checks" (the
// required checks did not report in time) comes after them: only the first
// rule that holds moves the issue at one poll.
//
// "request the split" and "request the implementation" both start an agent,
// so they share the room under the limit.
// The starts are taken highest priority first and then lowest issue number
// first, whichever rule they belong to. priority are the priority labels of
// the repository, highest first (PriorityRank). The priority only orders
// the starts that can begin: an open blocked-by issue and the limit decide
// before it, and the quota decides when a start is applied.
//
// "mark the requirement as in work" and "ask about the remaining
// sub-issues" move a requirement issue and start no agent, so they come
// first and take no room.
//
// The way out of cumin/status/implementing (ImplementationEnd) starts no
// new issue, so it takes no room either: its issue already counts. The same
// holds for the way out of cumin/status/reviewing (ReviewEnd), and for the
// steps in cumin/status/merging (MergeEnd).
//
// "request a conflict resolution" also holds for an issue in
// cumin/status/awaiting-merge-decision. Those actions come after the
// candidates of "start the merge" after the approval of a Maintainer and of
// "send back for changes": a review of a Maintainer
// on the conflicting head decides first, and the caller drops the conflict
// resolution of an issue that one of those two moved at this poll.
func Decide(snapshot Snapshot, maxInProgress int, required []RequiredCheck, priority []string, now time.Time, checksWait time.Duration) []Action {
	actions := requirementMoves(snapshot)
	actions = append(actions, implementationEnds(snapshot)...)
	actions = append(actions, reviewEnds(snapshot)...)
	actions = append(actions, mergeEnds(snapshot)...)
	actions = append(actions, conflictingSubIssues(snapshot)...)
	actions = append(actions, reviewableSubIssues(snapshot, required)...)
	actions = append(actions, failedSubIssues(snapshot, required)...)
	actions = append(actions, unreportedSubIssues(snapshot, required, now, checksWait)...)
	room := maxInProgress - inProgress(snapshot)
	starts := orderedStarts(snapshot, priority, readyRequirementIssues(snapshot), readySubIssues(snapshot), true)
	for _, s := range starts[:max(0, min(room, len(starts)))] {
		actions = append(actions, s.action)
	}
	actions = append(actions, maintainerApprovals(snapshot)...)
	actions = append(actions, maintainerChangeRequests(snapshot)...)
	actions = append(actions, conflictingMaintainerReviews(snapshot)...)
	return append(actions, labelCopies(snapshot)...)
}

// StartPermit is the permit of one start of an agent. Only PermitStart
// gives one that holds, and the one function that starts an agent takes it
// (startAgent). The zero value permits nothing.
type StartPermit struct{ granted bool }

// PermitStart is the one check before every start of an agent: the split,
// the acceptance check, the claim, the review, the check fix, the conflict
// resolution, the fix of a review, the fix of a Maintainer's review, the
// explanation of the cause, and the second request of each of them. A
// request gets the permit before its label change and before its count, so
// a start without a permit leaves the issue as it was, and a later poll
// decides the same step again.
//
// stopsAfterRuns says that cumin stops after the current runs: then no
// agent starts (designs/cumin-core.md, the topic on the stop). quotaAllows
// says what the quota usage decides: at a limit, or when the usage was not
// read, agent starts are stopped (issue-states.md, "stop agent starts").
// The steps that start no agent need no permit, and go on. A run that is
// going on is never stopped.
func PermitStart(stopsAfterRuns, quotaAllows bool) (StartPermit, bool) {
	if stopsAfterRuns || !quotaAllows {
		return StartPermit{}, false
	}
	return StartPermit{granted: true}, true
}

// PriorityRank returns the place of an issue in the order of the starts: 0
// is the highest priority. priority are the priority labels of the
// repository, highest first. An issue with two of them has the higher one.
// An issue without one takes the rank of parent, the labels of its
// requirement issue; a requirement issue has no parent. An issue with no
// priority label at either place comes after every label (issue-states.md,
// the order of the starts). GitHub label names ignore case, so the
// comparison does too.
func PriorityRank(labels, parent, priority []string) int {
	for _, own := range [][]string{labels, parent} {
		for rank, name := range priority {
			if slices.ContainsFunc(own, func(label string) bool { return strings.EqualFold(label, name) }) {
				return rank
			}
		}
	}
	return len(priority)
}

// Comment is one comment of a requirement issue, as "request the acceptance
// check" and "ask for the acceptance" read it.
type Comment struct {
	// Author is the login, "<slug>[bot]" for a GitHub App.
	Author    string
	CreatedAt time.Time
	Body      string
	// URL is the address of the comment; the notification of "stop at the
	// round limit" links it.
	URL string
}

// start is one start of an agent with its place in the order of the starts.
type start struct {
	rank   int
	number int
	action Action
}

// orderedStarts returns the starts of the plans ("request the split"), of
// the acceptance checks ("request the acceptance check") when withChecks is
// set, and of the claims ("request the implementation"), in the order of
// the starts: by priority label, then by issue number.
func orderedStarts(snapshot Snapshot, priority []string, plans []Plan, claims []Claim, withChecks bool) []start {
	// A start on a requirement issue has the priority of that issue.
	requirementRank := func(number int) int {
		requirement, _ := snapshot.RequirementIssue(number)
		return PriorityRank(requirement.Labels, nil, priority)
	}
	var starts []start
	for _, plan := range plans {
		starts = append(starts, start{requirementRank(plan.Number), plan.Number, plan})
	}
	if withChecks {
		for _, check := range acceptanceChecks(snapshot) {
			starts = append(starts, start{requirementRank(check.Number), check.Number, check})
		}
	}
	for _, claim := range claims {
		sub, _ := snapshot.SubIssue(claim.Number)
		requirement, _ := snapshot.RequirementIssue(claim.RequirementIssue)
		starts = append(starts, start{PriorityRank(sub.Labels, requirement.Labels, priority), claim.Number, claim})
	}
	slices.SortFunc(starts, func(a, b start) int {
		return cmp.Or(a.rank-b.rank, a.number-b.number)
	})
	return starts
}

// ReadyActorReads names the issues whose newest cumin/status/ready the poll
// must read the actor of, and the free slots: the candidates of "request
// the split" and of "request the implementation", in the order of the
// starts. With no free slot it names none, so that a poll that can start
// nothing makes no read. The poll reads in this order and stops when it has
// as many candidates of a Maintainer as free slots: a later candidate
// cannot start at this poll.
func ReadyActorReads(snapshot Snapshot, maxInProgress int, priority []string) (numbers []int, room int) {
	room = maxInProgress - inProgress(snapshot)
	if room <= 0 {
		return nil, 0
	}
	for _, s := range orderedStarts(snapshot, priority, requirementCandidates(snapshot), subIssueCandidates(snapshot), false) {
		numbers = append(numbers, s.number)
	}
	return numbers, room
}

// statusLabel returns the cumin/status/* label of an issue, or "" when it
// has none.
func statusLabel(labels []string) string {
	for _, label := range labels {
		if IsStatusLabel(label) {
			return label
		}
	}
	return ""
}

// inProgress counts the issues that fill the limit, by their working label
// only: open sub-issues in implementing, checking, reviewing, or merging,
// and requirement issues in planning or accepting. A requirement issue in
// implementing ("mark the requirement as in work") has no agent of its own,
// so it does not count. An issue with cumin/status/ready never counts, also
// when the running set names it.
//
// The requirement issues that cumin cannot read in full (Snapshot.Unread)
// count in the same way, by what was read of them: no rule moves them, but
// their work goes on. An issue of them whose labels were not read in full
// counts as in progress, so that the limit never fails on the open side.
func inProgress(snapshot Snapshot) int {
	n := 0
	for _, requirement := range slices.Concat(snapshot.RequirementIssues, snapshot.Unread) {
		if requirement.LabelsUnread || slices.Contains(requirement.Labels, LabelPlanning) || slices.Contains(requirement.Labels, LabelAccepting) {
			n++
		}
		for _, sub := range requirement.SubIssues {
			if sub.Closed {
				continue
			}
			if sub.LabelsUnread {
				n++
				continue
			}
			for _, label := range []string{LabelImplementing, LabelChecking, LabelReviewing, LabelMerging} {
				if slices.Contains(sub.Labels, label) {
					n++
					break
				}
			}
		}
	}
	return n
}

// readySubIssues returns the claims of "request the implementation" before
// the limit: the candidates (subIssueCandidates) whose newest
// cumin/status/ready a Maintainer added. A candidate whose ready is of
// another account, or was not read, is skipped.
func readySubIssues(snapshot Snapshot) []Claim {
	var claims []Claim
	for _, claim := range subIssueCandidates(snapshot) {
		sub, _ := snapshot.SubIssue(claim.Number)
		if readyOfMaintainer(sub.ReadyRead, sub.ReadyOwner) {
			claims = append(claims, claim)
		}
	}
	return claims
}

// subIssueCandidates returns the candidates of "request the implementation"
// before the check of the Maintainer: open sub-issues with
// cumin/status/ready whose blocked-by issues are all closed, lowest issue
// number first across all requirement issues.
func subIssueCandidates(snapshot Snapshot) []Claim {
	var claims []Claim
	for _, requirement := range snapshot.RequirementIssues {
		// "mark the requirement as in work" could not be judged without the
		// label times. A claim would take away the cumin/status/ready that
		// it must still see, so the sub-issues wait for the next poll.
		if startNeedsLabelTimes(requirement) && !requirement.LabelTimesRead {
			continue
		}
		for _, sub := range requirement.SubIssues {
			// A Maintainer does an owner task by hand, so no agent ever
			// starts for it, even with cumin/status/ready ("request the
			// implementation").
			if sub.Closed || !slices.Contains(sub.Labels, LabelReady) || anyOpen(sub.BlockedBy) ||
				slices.Contains(sub.Labels, LabelOwnerTask) {
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

// RequirementIssue returns the requirement issue with the number.
func (s Snapshot) RequirementIssue(number int) (RequirementIssue, bool) {
	for _, requirement := range s.RequirementIssues {
		if requirement.Number == number {
			return requirement, true
		}
	}
	return RequirementIssue{}, false
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

// anyOpen reports whether one of the blocking issues is still open.
func anyOpen(blockedBy []BlockedBy) bool {
	for _, blocker := range blockedBy {
		if !blocker.Closed {
			return true
		}
	}
	return false
}
