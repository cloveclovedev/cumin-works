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
	"maps"
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
	// Running are the issues whose agent runs in this cumin now. The rules
	// of a working label skip a running issue: its run decides its own end.
	Running map[int]bool
}

// HasIssueChecking reports whether an open sub-issue waits for the
// required checks. The poll reads the required checks only then, because
// that read is a REST call of its own.
func (s Snapshot) HasIssueChecking() bool {
	for _, requirement := range s.RequirementIssues {
		for _, sub := range requirement.SubIssues {
			if !sub.Closed && slices.Contains(sub.Labels, LabelChecking) {
				return true
			}
		}
	}
	return false
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
// decides on a partial issue.
func (s Snapshot) WithoutRequirementIssues(numbers map[int]bool) Snapshot {
	if len(numbers) == 0 {
		return s
	}
	s.RequirementIssues = slices.DeleteFunc(slices.Clone(s.RequirementIssues), func(requirement RequirementIssue) bool {
		return numbers[requirement.Number]
	})
	return s
}

// HasIssueInWork reports whether an issue of the repository is in work:
// an open requirement issue with cumin/status/ready,
// cumin/status/planning, or cumin/status/accepting, or an open sub-issue with cumin/status/ready,
// cumin/status/implementing, cumin/status/checking,
// cumin/status/reviewing, or cumin/status/merging. An issue that waits for
// a Maintainer is not in work.
func (s Snapshot) HasIssueInWork() bool {
	for _, requirement := range s.RequirementIssues {
		if slices.Contains(requirement.Labels, LabelReady) || slices.Contains(requirement.Labels, LabelPlanning) ||
			slices.Contains(requirement.Labels, LabelAccepting) {
			return true
		}
		for _, sub := range requirement.SubIssues {
			if sub.Closed {
				continue
			}
			for _, label := range []string{LabelReady, LabelImplementing, LabelChecking, LabelReviewing, LabelMerging} {
				if slices.Contains(sub.Labels, label) {
					return true
				}
			}
		}
	}
	return false
}

// LastPoll is what the last poll of a repository left for the decision on
// its next poll. The zero value means that no poll ran yet.
type LastPoll struct {
	// Ran says that a poll of the repository ran.
	Ran bool
	// Failed says that the poll, or one of its actions, failed.
	Failed bool
	// Acted says that the poll took an action.
	Acted bool
	// IssueInWork is Snapshot.HasIssueInWork of the snapshot of the poll.
	IssueInWork bool
}

// RepositoryInWork decides whether a repository is in work, so that cumin
// polls it at every poll interval. agentRun says that an agent run of the
// repository is in progress, or that one ended since the last poll. A
// repository that is not in work has only issues that wait for a Maintainer,
// and cumin polls it at the idle poll interval.
func RepositoryInWork(last LastPoll, agentRun bool) bool {
	return agentRun || !last.Ran || last.Failed || last.Acted || last.IssueInWork
}

// PollIsDue decides, at a tick of the poll interval, whether cumin polls a
// repository now. A repository in work is polled at every tick. A
// repository that is not in work is polled at the tick that is nearest to
// the idle poll interval after its last poll: half a poll interval of
// tolerance keeps a tick that comes a moment early from waiting one more
// tick.
func PollIsDue(inWork bool, sinceLastPoll, pollInterval, idlePollInterval time.Duration) bool {
	return inWork || sinceLastPoll >= idlePollInterval-pollInterval/2
}

// HasMaintainerApprovalCandidate reports whether a sub-issue is a candidate of
// "start the merge" after the approval of a Maintainer, so that the poll
// reads the required checks for it.
func (s Snapshot) HasMaintainerApprovalCandidate() bool { return len(maintainerApprovals(s)) > 0 }

// RequirementIssue is an open issue with cumin/type/requirement.
type RequirementIssue struct {
	Number int
	// Title is for the monitor file only. No rule reads it.
	Title     string
	Labels    []string
	SubIssues []SubIssue
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
	NodeID    string
	Title     string
	Closed    bool
	Labels    []string
	BlockedBy []BlockedBy
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
// cumin/status/reviewing. Starting the Reviewer is a later requirement.
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

// Action is one thing that cumin does after a poll. Later rules add types.
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

// requirementMoves returns the actions "mark the requirement as in work"
// and "ask about the remaining sub-issues", and the way out of
// cumin/status/accepting (AcceptanceEnd) and of cumin/status/planning
// (SplitEnd), lowest requirement issue number first.
func requirementMoves(snapshot Snapshot) []Action {
	requirements := slices.Clone(snapshot.RequirementIssues)
	slices.SortFunc(requirements, func(a, b RequirementIssue) int { return a.Number - b.Number })
	var actions []Action
	for _, requirement := range requirements {
		switch {
		case startsImplementing(requirement):
			actions = append(actions, StartRequirement{Number: requirement.Number})
		case remainingNeedReview(requirement):
			actions = append(actions, ReviewRemaining{Number: requirement.Number})
		default:
			running := snapshot.Running[requirement.Number]
			if action := AcceptanceEnd(requirement, running); action != nil {
				actions = append(actions, action)
			} else if action := SplitEnd(requirement, running); action != nil {
				actions = append(actions, action)
			}
		}
	}
	return actions
}

// startsImplementing is the condition of "mark the requirement as in work".
// Without a status label, an open sub-issue with
// cumin/status/ready is enough: a Maintainer wrote the sub-issues without the
// Planner. In cumin/status/awaiting-plan-review and in
// cumin/status/awaiting-acceptance, cumin/status/ready must have been added
// after that label. A sub-issue that kept its
// cumin/status/ready from an earlier split must not move the requirement
// issue before a Maintainer looked at the new sub-issues (issue-states.md, the
// text below the table).
func startsImplementing(requirement RequirementIssue) bool {
	switch statusLabel(requirement.Labels) {
	case "":
		return slices.ContainsFunc(requirement.SubIssues, openReady)
	case LabelAwaitingPlanReview, LabelAwaitingAcceptance:
		if !requirement.LabelTimesRead {
			return false
		}
		return slices.ContainsFunc(requirement.SubIssues, func(sub SubIssue) bool {
			return openReady(sub) && sub.ReadyAt.After(requirement.ReviewAt)
		})
	}
	return false
}

// remainingNeedReview is the condition of "ask about the remaining
// sub-issues": the requirement issue is in
// cumin/status/implementing, one or more sub-issues are open, and none of
// the open ones has a status label.
func remainingNeedReview(requirement RequirementIssue) bool {
	if statusLabel(requirement.Labels) != LabelImplementing {
		return false
	}
	open := 0
	for _, sub := range requirement.SubIssues {
		if sub.Closed {
			continue
		}
		if statusLabel(sub.Labels) != "" {
			return false
		}
		open++
	}
	return open > 0
}

// NeedsComments reports whether "request the acceptance check" or "ask for
// the acceptance" needs the comments of the requirement issue: it is in
// cumin/status/accepting, or it is in cumin/status/implementing and has one
// or more sub-issues, all closed.
func NeedsComments(requirement RequirementIssue) bool {
	if statusLabel(requirement.Labels) == LabelAccepting {
		return !statusOfAnother(requirement)
	}
	return everySubIssueClosed(requirement)
}

// everySubIssueClosed reports whether the requirement issue is in
// cumin/status/implementing with one or more sub-issues, all closed.
func everySubIssueClosed(requirement RequirementIssue) bool {
	if statusLabel(requirement.Labels) != LabelImplementing || len(requirement.SubIssues) == 0 {
		return false
	}
	for _, sub := range requirement.SubIssues {
		if !sub.Closed {
			return false
		}
	}
	return true
}

// lastClose is when the last sub-issue closed.
func lastClose(requirement RequirementIssue) time.Time {
	var last time.Time
	for _, sub := range requirement.SubIssues {
		if sub.ClosedAt.After(last) {
			last = sub.ClosedAt
		}
	}
	return last
}

// checked reports whether an acceptance check comment was written after the
// last sub-issue closed. An older comment belongs to an earlier round: a
// Maintainer added sub-issues after it (issue-states.md, the text below the
// table).
//
// A comment at the same second as the last close counts: the times of
// GitHub cannot order two events inside one second, and a check that never
// counts would ask again at every poll.
func checked(requirement RequirementIssue) bool {
	return requirement.CommentsRead && !requirement.AcceptanceCheckAt.IsZero() &&
		!requirement.AcceptanceCheckAt.Before(lastClose(requirement))
}

// AcceptanceEnd decides the way out of cumin/status/accepting from the
// facts on GitHub, for a requirement issue whose Planner does not run. The
// poll and the end of a Planner run both decide with it, so a restart of
// cumin during the check loses nothing.
//
//   - An acceptance check comment after the last close: ask for the
//     acceptance (Accept).
//   - A decision request of the Planner, written after the issue got
//     cumin/status/accepting: stop the acceptance check.
//   - Neither comment: request the acceptance check again, once for each
//     stay in cumin/status/accepting. The second time, stop the acceptance
//     check.
//
// It returns nil while the Planner runs, in every other state, while the
// comments or the label times were not read, and while the status label
// does not count (statusCounts): the next poll decides.
func AcceptanceEnd(requirement RequirementIssue, running bool) Action {
	if statusLabel(requirement.Labels) != LabelAccepting || running || !statusCounts(requirement) || !requirement.CommentsRead {
		return nil
	}
	switch {
	case checked(requirement):
		return Accept{Number: requirement.Number}
	case !requirement.LabelTimesRead:
		return nil
	case !requirement.QuestionAt.IsZero() && !requirement.QuestionAt.Before(requirement.ReviewAt):
		return StopAcceptance{Number: requirement.Number, Question: true}
	case requirement.AcceptanceRequestedAgain:
		return StopAcceptance{Number: requirement.Number}
	}
	return CheckAcceptance{Number: requirement.Number, Again: true}
}

// SplitEnd decides the way out of cumin/status/planning from the facts on
// GitHub, for a requirement issue whose Planner does not run. The poll and
// the end of a Planner run both decide with it, so a restart of cumin
// during the split, or a failed read after it, loses nothing.
//
//   - A decision request of the Planner, written after the issue got
//     cumin/status/planning: stop the split. cumin-core
//     posts the blocked_reason of the Planner, so its decision request
//     counts too (QuestionAt). The question
//     decides before the check of the split: an issue that is planned again
//     can hold sub-issues of an earlier split that pass the check.
//   - The split passes the check (VerifySplit) and a sub-issue is open: ask
//     for the plan review.
//   - The split passes the check and every sub-issue is closed: request the
//     acceptance check.
//   - The split fails the check: request the split again, once for each
//     stay in cumin/status/planning. The second time, stop the split.
//
// It returns nil while the Planner runs, in every other state, while the
// comments or the label times were not read, and while the status label
// does not count (statusCounts): the next poll decides.
func SplitEnd(requirement RequirementIssue, running bool) Action {
	if !SplitNeedsFacts(requirement, running) || !statusCounts(requirement) || !requirement.CommentsRead || !requirement.LabelTimesRead {
		return nil
	}
	verification := VerifySplit(requirement)
	switch {
	case !requirement.QuestionAt.IsZero() && !requirement.QuestionAt.Before(requirement.ReviewAt):
		return StopSplit{Number: requirement.Number, Question: true}
	case verification.Passed && SplitStatus(requirement) == LabelAccepting:
		return CheckAcceptance{Number: requirement.Number}
	case verification.Passed:
		return ReviewPlan{Number: requirement.Number}
	case requirement.SplitRequestedAgain:
		return StopSplit{Number: requirement.Number, Reason: SplitReason(verification)}
	}
	return Plan{Number: requirement.Number, Again: true}
}

// SplitNeedsFacts reports whether the way out of cumin/status/planning
// needs the comments and the label times of the requirement issue: it is in
// cumin/status/planning, and its Planner does not run. While the Planner
// runs, nothing is decided, so the poll reads nothing more. The same holds
// for a label that another account than cumin-core or a Maintainer added.
func SplitNeedsFacts(requirement RequirementIssue, running bool) bool {
	return statusLabel(requirement.Labels) == LabelPlanning && !running && !statusOfAnother(requirement)
}

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

// ReviewEnd decides the way out of cumin/status/reviewing from the facts on
// GitHub, for an implementation issue whose Reviewer does not run. The poll
// and the end of a Reviewer run both decide with it, so a restart of cumin
// during the run, or a failed read after it, loses nothing. The first case
// that holds decides:
//
//   - A decision request of the Reviewer, written after the issue got
//     cumin/status/reviewing: stop the review for a Maintainer. cumin-core
//     posts the blocked_reason of the Reviewer, so its decision request
//     counts too.
//   - The head commit is not the one of the request: go back to the checks.
//   - APPROVE on the head commit: by DecideMerge, the merge of risk/low, ask
//     for the merge decision, go back to the checks, or stop the review for
//     a Maintainer (not exactly one risk label).
//   - REQUEST_CHANGES on the head commit: below the round limit, request a
//     review fix. At the limit, stop at the round limit when the Reviewer
//     explained the cause, and request the cause from the Reviewer
//     otherwise; a request of the cause that returned done and left no
//     explanation stops the review for a Maintainer, and so does a second
//     request of the cause of this stay that left none.
//   - No review on the head commit: request the review again, once for each
//     stay in cumin/status/reviewing. The second time, stop the review for
//     a Maintainer.
//
// It returns nil while the Reviewer runs, in every other state, for a
// closed issue, for an issue with no open pull request, while the facts
// were not read, and while the status label does not count: the next poll
// decides.
func ReviewEnd(sub SubIssue, running bool) Action {
	facts := sub.Reviewing
	if !ReviewNeedsFacts(sub, running) || facts == nil || !facts.StatusCounts || facts.ReviewingAt.IsZero() {
		return nil
	}
	pr, ok := sub.LatestPullRequest()
	if !ok {
		return nil
	}
	if !facts.QuestionAt.IsZero() && !facts.QuestionAt.Before(facts.ReviewingAt) {
		return StopReview{Number: sub.Number, Question: true, Action: ActionStopTheReview, PullRequest: pr.Number}
	}
	if facts.RequestedHead != "" && facts.RequestedHead != pr.HeadCommit {
		return BackToChecks{Number: sub.Number, HeadMoved: true}
	}
	switch CheckReview(pr, facts.Reviewer) {
	case ReviewApprovedOnHead:
		switch decision := DecideMerge(sub.Labels, facts.Required, pr.Checks); decision {
		case MergeNow:
			return StartMerge{Number: sub.Number, PullRequest: pr.Number}
		case MergeAskMaintainer:
			return AskMaintainerToMerge{Number: sub.Number, PullRequest: pr.Number}
		case MergeChecksNotPassed:
			return BackToChecks{Number: sub.Number}
		default:
			return StopReview{Number: sub.Number, Action: ActionStopTheReview, Reason: RiskLabelReason(decision), PullRequest: pr.Number}
		}
	case ReviewChangesRequestedOnHead:
		latest, _ := LatestReview(pr.Reviews, facts.Reviewer)
		round := ReviewRounds(pr.Reviews, facts.Reviewer, facts.ReadyAt)
		switch {
		case ReviewFixAllowed(round, facts.Limit):
			return RequestReviewFix{Number: sub.Number, PullRequest: pr.Number, Round: round, Review: latest}
		case facts.Explained:
			return StopAtRoundLimit{Number: sub.Number, Explanation: facts.Explanation}
		case facts.CauseRequestedAgain:
			return StopReview{Number: sub.Number, Action: ActionStopAtTheRoundLimit, Reason: MissingCauseReason, PullRequest: pr.Number, Retried: true}
		case facts.CauseRequested:
			return StopReview{Number: sub.Number, Action: ActionStopAtTheRoundLimit, Reason: MissingExplanationReason, PullRequest: pr.Number}
		}
		return RequestCause{Number: sub.Number, PullRequest: pr.Number, Review: latest}
	}
	if facts.RequestedAgain {
		return StopReview{Number: sub.Number, Action: ActionStopTheReview, Reason: MissingReviewReason, PullRequest: pr.Number, Retried: true}
	}
	return RequestReviewAgain{Number: sub.Number, PullRequest: pr.Number}
}

// ReviewEndIssue returns the number of the implementation issue of an
// action that ReviewEnd decided, or 0 for another action.
func ReviewEndIssue(action Action) int {
	switch a := action.(type) {
	case RequestReviewFix:
		return a.Number
	case AskMaintainerToMerge:
		return a.Number
	case StartMerge:
		return a.Number
	case RequestCause:
		return a.Number
	case StopAtRoundLimit:
		return a.Number
	case BackToChecks:
		return a.Number
	case RequestReviewAgain:
		return a.Number
	case StopReview:
		return a.Number
	}
	return 0
}

// ReviewNeedsFacts reports whether the way out of cumin/status/reviewing
// needs the facts of the implementation issue: it is open, in
// cumin/status/reviewing, and its Reviewer does not run. While the Reviewer
// runs, nothing is decided, so the poll reads nothing more.
func ReviewNeedsFacts(sub SubIssue, running bool) bool {
	return !sub.Closed && statusLabel(sub.Labels) == LabelReviewing && !running
}

// ReviewNeedsExplanation reports whether the decision needs the decision
// request of the Reviewer on the pull request: the latest review requests
// changes on the head commit, at the round limit. It returns that review.
func ReviewNeedsExplanation(pr PullRequest, reviewer string, readyAt time.Time, limit int) (Review, bool) {
	if CheckReview(pr, reviewer) != ReviewChangesRequestedOnHead || ReviewFixAllowed(ReviewRounds(pr.Reviews, reviewer, readyAt), limit) {
		return Review{}, false
	}
	return LatestReview(pr.Reviews, reviewer)
}

// reviewEnds returns the way out of cumin/status/reviewing of every
// sub-issue that has one (ReviewEnd), lowest issue number first.
func reviewEnds(snapshot Snapshot) []Action {
	var subs []SubIssue
	for _, requirement := range snapshot.RequirementIssues {
		subs = append(subs, requirement.SubIssues...)
	}
	slices.SortFunc(subs, func(a, b SubIssue) int { return a.Number - b.Number })
	var actions []Action
	for _, sub := range subs {
		if action := ReviewEnd(sub, snapshot.Running[sub.Number]); action != nil {
			actions = append(actions, action)
		}
	}
	return actions
}

// MergeEnd decides the step of an implementation issue in
// cumin/status/merging from the facts on GitHub (issue-states.md, what
// cumin does inside merging). The same facts always give the same step, so
// a merge whose answer got lost and a restart of cumin need no memory:
//
//   - No open pull request closes the issue, and the newest linked pull
//     request is merged: "close the merged issue".
//   - The pull request is open, the conditions of the merge hold
//     (MergeConditionsHold), and GitHub reports a conflict with the default
//     branch (Conflicting): "request a conflict resolution", with no merge.
//   - The pull request is open, and the conditions of the merge hold: cumin
//     sends the merge of the head commit, which is the approved commit. This
//     is so for MERGEABLE and for UNKNOWN; a conflict that only the merge
//     shows comes back as the answer of the merge.
//   - Else "go back to the checks": the approval or the required checks no
//     longer hold, or no pull request is left to merge.
//
// The label cumin/status/merging never stands in for the conditions. It
// returns nil in every other state, for a closed issue, while a step of the
// issue runs, while the facts were not read, and while the status label
// does not count: the next poll decides.
func MergeEnd(sub SubIssue, running bool) Action {
	facts := sub.Merging
	if !MergeNeedsFacts(sub, running) || facts == nil || !facts.StatusCounts {
		return nil
	}
	pr, ok := sub.LatestPullRequest()
	switch {
	case !ok && facts.Merged > 0:
		return CloseMergedIssue{Number: sub.Number, PullRequest: facts.Merged}
	case ok && MergeConditionsHold(sub.Labels, facts.Required, pr, facts.Reviewer, facts.Maintainers):
		if pr.Mergeable == Conflicting {
			return ResolveMergeConflict{Number: sub.Number, PullRequest: pr.Number}
		}
		return SendMerge{Number: sub.Number, PullRequest: pr.Number, HeadCommit: pr.HeadCommit}
	}
	return LeaveMerge{Number: sub.Number}
}

// MergeNeedsFacts reports whether the step in cumin/status/merging needs
// the facts of the implementation issue: it is open, in
// cumin/status/merging, and no step of it runs.
func MergeNeedsFacts(sub SubIssue, running bool) bool {
	return !sub.Closed && statusLabel(sub.Labels) == LabelMerging && !running
}

// MergeConditionsHold applies the conditions of the merge, the same as the
// conditions of the transitions into cumin/status/merging: the issue has
// exactly one risk/* label, every required check passes on the head commit,
// and the latest review of the Reviewer is APPROVE on the head commit. With
// risk/medium or risk/high, the latest review of a Maintainer that decides is
// APPROVE on the head commit too (MaintainerApproved). cumin checks them before
// every merge that it sends.
func MergeConditionsHold(labels []string, required []RequiredCheck, pr PullRequest, reviewer string, maintainers map[string]bool) bool {
	if CheckReview(pr, reviewer) != ReviewApprovedOnHead {
		return false
	}
	switch DecideMerge(labels, required, pr.Checks) {
	case MergeNow:
		return true
	case MergeAskMaintainer:
		return MaintainerApproved(pr.Reviews, pr.HeadCommit, maintainers)
	}
	return false
}

// mergeEnds returns the step in cumin/status/merging of every sub-issue
// that has one (MergeEnd), lowest issue number first.
func mergeEnds(snapshot Snapshot) []Action {
	var subs []SubIssue
	for _, requirement := range snapshot.RequirementIssues {
		subs = append(subs, requirement.SubIssues...)
	}
	slices.SortFunc(subs, func(a, b SubIssue) int { return a.Number - b.Number })
	var actions []Action
	for _, sub := range subs {
		if action := MergeEnd(sub, snapshot.Running[sub.Number]); action != nil {
			actions = append(actions, action)
		}
	}
	return actions
}

// acceptanceChecks returns the starts of "request the acceptance check"
// before the limit: every sub-issue closed, the comments read, no
// acceptance check after the last close, the follow-up notes of the closed
// sub-issues written ("write the follow-up note"), and no agent of the
// requirement issue running.
func acceptanceChecks(snapshot Snapshot) []CheckAcceptance {
	var checks []CheckAcceptance
	for _, requirement := range snapshot.RequirementIssues {
		if everySubIssueClosed(requirement) && requirement.CommentsRead && !checked(requirement) &&
			requirement.FollowUpsDone && !snapshot.Running[requirement.Number] {
			checks = append(checks, CheckAcceptance{Number: requirement.Number})
		}
	}
	return checks
}

// AcceptanceCheckAt returns when the newest acceptance check comment was
// written: a comment of the Planner App whose first line is the heading
// "## Acceptance check" (issue-states.md, the text below the table). A
// comment of anyone else never counts. cumin does not read the result
// table.
func AcceptanceCheckAt(comments []Comment, planner string) time.Time {
	var newest time.Time
	for _, comment := range comments {
		if planner == "" || comment.Author != planner {
			continue
		}
		first, _, _ := strings.Cut(strings.TrimLeft(comment.Body, " \t\r\n"), "\n")
		if strings.TrimSpace(first) != acceptanceCheckHeading {
			continue
		}
		if comment.CreatedAt.After(newest) {
			newest = comment.CreatedAt
		}
	}
	return newest
}

// QuestionAt returns when the newest decision request of one of the authors
// was written: a comment whose first line starts with
// DecisionRequestHeading. The authors are the Planner App and, for the
// split, the App of cumin-core, which posts the blocked_reason of the
// Planner. An empty author matches nothing, and a comment of anyone else
// never counts.
func QuestionAt(comments []Comment, authors ...string) time.Time {
	var newest time.Time
	for _, comment := range comments {
		if comment.Author == "" || !slices.Contains(authors, comment.Author) || !strings.HasPrefix(firstBodyLine(comment.Body), DecisionRequestHeading) {
			continue
		}
		if comment.CreatedAt.After(newest) {
			newest = comment.CreatedAt
		}
	}
	return newest
}

// acceptanceCheckHeading is the first heading of templates/acceptance-check.md.
const acceptanceCheckHeading = "## Acceptance check"

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

// IssuesToCleanUp are the closed sub-issues of the snapshot whose agent
// does not run now. The Host keeps nothing for them (agent-run.md, the
// topic on the work directory).
func IssuesToCleanUp(snapshot Snapshot) []int {
	var numbers []int
	for _, requirement := range snapshot.RequirementIssues {
		for _, sub := range requirement.SubIssues {
			if sub.Closed && !snapshot.Running[sub.Number] {
				numbers = append(numbers, sub.Number)
			}
		}
	}
	return numbers
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

// StatusActor is the account of the newest event that added a status
// label. Login and Type are the ones of the event as GraphQL names them
// ("User" for a person, "Bot" for a GitHub App); both are empty when the
// account no longer exists or no event was found. Permission and UserType
// are the answer of GitHub on the permission of a person on the
// repository; they are empty for an account that is not a person.
type StatusActor struct {
	Login      string
	Type       string
	Permission string
	UserType   string
}

// StatusLabelCounts reports whether cumin treats a status label as a state
// (issue-states.md, the account that added a status label): the account of
// its newest label event is a Maintainer (IsMaintainer) or, for every label but
// cumin/status/ready, the cumin-core App. core is the login of the bot of
// cumin-core; GraphQL names a bot without "[bot]", so both forms match.
// Every rule that acts from a state calls it before it acts.
func StatusLabelCounts(label string, actor StatusActor, core string) bool {
	if actor.Login == "" {
		return false
	}
	if actor.Type == "User" {
		return IsMaintainer(actor.Permission, actor.UserType)
	}
	core = strings.TrimSuffix(core, "[bot]")
	return label != LabelReady && actor.Type == "Bot" && core != "" &&
		strings.TrimSuffix(actor.Login, "[bot]") == core
}

// StatusActorReads names the requirement issues whose newest status label
// the poll must read the actor of: the ones that cumin is about to act
// from, in cumin/status/planning or in cumin/status/accepting with no
// Planner running. While the Planner runs, and in every other state,
// nothing is decided from the label, so a poll with no such issue makes no
// read.
func StatusActorReads(snapshot Snapshot) []int {
	var numbers []int
	for _, requirement := range snapshot.RequirementIssues {
		status := statusLabel(requirement.Labels)
		if (status == LabelPlanning || status == LabelAccepting) && !snapshot.Running[requirement.Number] {
			numbers = append(numbers, requirement.Number)
		}
	}
	slices.Sort(numbers)
	return numbers
}

// statusCounts reports whether the actor of the status label was read and
// is the cumin-core App or a Maintainer. A rule that acts from the state holds
// only then.
func statusCounts(requirement RequirementIssue) bool {
	return requirement.StatusRead && requirement.StatusCounts
}

// statusOfAnother reports whether the actor of the status label was read
// and is neither the cumin-core App nor a Maintainer. Such an issue waits
// for a Maintainer.
func statusOfAnother(requirement RequirementIssue) bool {
	return requirement.StatusRead && !requirement.StatusCounts
}

// subStatusOfAnother reports whether the facts of the working label of a
// sub-issue were read, and say that another account than cumin-core or a
// Maintainer added that label. No rule moves such an issue. Facts that were not
// read do not say it.
func subStatusOfAnother(sub SubIssue) bool {
	return sub.Implementing != nil && !sub.Implementing.StatusCounts ||
		sub.Reviewing != nil && !sub.Reviewing.StatusCounts ||
		sub.Merging != nil && !sub.Merging.StatusCounts
}

// readyOfMaintainer reports whether the newest cumin/status/ready was read and
// is a Maintainer's. "request the split" and "request the implementation"
// hold only then (issue-states.md, the ready of a Maintainer).
func readyOfMaintainer(read bool, owner string) bool { return read && owner != "" }

// readyOfAnother reports whether the newest cumin/status/ready was read and
// is not a Maintainer's. Such an issue waits for a Maintainer.
func readyOfAnother(read bool, owner string) bool { return read && owner == "" }

// NeedsLabelTimes reports whether a rule needs the label times of the
// requirement issue: "mark the requirement as in work" needs them
// (startNeedsLabelTimes), the requirement issue is in
// cumin/status/accepting, whose end compares a decision request of the
// Planner with the time of that label, an open
// sub-issue waits in cumin/status/checking, whose wait is counted
// from the time of that label, or an open sub-issue in
// cumin/status/awaiting-merge-decision has a request for changes of a person
// on its head commit, which "send back for changes" compares with the time
// of that label. Only then does the poll read the times, so that the poll
// query keeps its cost.
func NeedsLabelTimes(requirement RequirementIssue) bool {
	return startNeedsLabelTimes(requirement) ||
		statusLabel(requirement.Labels) == LabelAccepting && !statusOfAnother(requirement) ||
		slices.ContainsFunc(requirement.SubIssues, openChecking) ||
		slices.ContainsFunc(requirement.SubIssues, openWithChangeRequest)
}

// startNeedsLabelTimes reports whether "mark the requirement as in work"
// needs the label times of the requirement issue: it waits in
// cumin/status/awaiting-plan-review or in
// cumin/status/awaiting-acceptance, and an open sub-issue carries
// cumin/status/ready.
func startNeedsLabelTimes(requirement RequirementIssue) bool {
	status := statusLabel(requirement.Labels)
	return (status == LabelAwaitingPlanReview || status == LabelAwaitingAcceptance) &&
		slices.ContainsFunc(requirement.SubIssues, openReady)
}

func openChecking(sub SubIssue) bool {
	return !sub.Closed && slices.Contains(sub.Labels, LabelChecking)
}

// openWithChangeRequest reports whether an open sub-issue in
// cumin/status/awaiting-merge-decision has a CHANGES_REQUESTED review of a
// person on the head commit of its pull request.
func openWithChangeRequest(sub SubIssue) bool {
	if sub.Closed || !slices.Contains(sub.Labels, LabelAwaitingMergeDecision) {
		return false
	}
	pr, ok := sub.LatestPullRequest()
	return ok && slices.ContainsFunc(pr.Reviews, func(review Review) bool {
		return review.Author != "" && !isBot(review.Author) &&
			review.State == ReviewChangesRequested && review.Commit == pr.HeadCommit
	})
}

func openReady(sub SubIssue) bool {
	return !sub.Closed && slices.Contains(sub.Labels, LabelReady)
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

// readyRequirementIssues returns the plans of "request the split" before
// the limit: the candidates (requirementCandidates) whose newest
// cumin/status/ready a Maintainer added. A candidate whose ready is of
// another account, or was not read, is skipped.
func readyRequirementIssues(snapshot Snapshot) []Plan {
	var plans []Plan
	for _, plan := range requirementCandidates(snapshot) {
		requirement, _ := snapshot.RequirementIssue(plan.Number)
		if readyOfMaintainer(requirement.ReadyRead, requirement.ReadyOwner) {
			plans = append(plans, plan)
		}
	}
	return plans
}

// requirementCandidates returns the candidates of "request the split"
// before the check of the Maintainer: open requirement issues with
// cumin/status/ready whose blocked-by issues are all closed. Whether the
// requirement issue has sub-issues does not matter (issue-states.md,
// "request the split").
func requirementCandidates(snapshot Snapshot) []Plan {
	var plans []Plan
	for _, requirement := range snapshot.RequirementIssues {
		if slices.Contains(requirement.Labels, LabelReady) && !anyOpen(requirement.BlockedBy) {
			plans = append(plans, Plan{Number: requirement.Number})
		}
	}
	return plans
}

// labelCopies returns the actions of "copy the labels to the pull
// request": one for each open pull request that closes a sub-issue and
// whose copied labels differ from the issue.
// The copy is of the labels in the snapshot, so a label that this poll
// changes reaches the pull request at the next poll. No other rule reads
// the labels of a pull request (principle 5). A pull request that closes two
// issues follows the one with the lowest number, so that the two do not
// replace each other's labels at every poll, and the order of the snapshot
// changes nothing.
func labelCopies(snapshot Snapshot) []Action {
	type source struct {
		issue int
		pr    PullRequest
		want  []string
	}
	sources := map[int]source{}
	for _, requirement := range snapshot.RequirementIssues {
		for _, sub := range requirement.SubIssues {
			for _, pr := range sub.PullRequests {
				if old, ok := sources[pr.Number]; ok && old.issue < sub.Number {
					continue
				}
				sources[pr.Number] = source{issue: sub.Number, pr: pr, want: PullRequestLabels(sub.Labels, pr.Labels)}
			}
		}
	}
	var actions []Action
	for _, number := range slices.Sorted(maps.Keys(sources)) {
		src := sources[number]
		if sameLabels(src.want, src.pr.Labels) {
			continue
		}
		actions = append(actions, CopyLabels{Issue: src.issue, PullRequest: number, Labels: src.want})
	}
	return actions
}

// conflictingSubIssues returns the actions of "request a conflict
// resolution" for the issues that wait for the checks: open sub-issues in
// cumin/status/checking whose
// open pull request GitHub reports as CONFLICTING, lowest issue number
// first. UNKNOWN says that GitHub is still calculating, so it gives no
// action: a later poll decides. A running issue gives no action.
func conflictingSubIssues(snapshot Snapshot) []Action {
	var actions []Action
	for _, action := range conflictsUnder(snapshot, LabelChecking) {
		if !snapshot.Running[action.(ResolveConflict).Number] {
			actions = append(actions, action)
		}
	}
	return actions
}

// conflictingMaintainerReviews returns the actions of "request a conflict
// resolution" for the issues that wait for a Maintainer: open sub-issues in
// cumin/status/awaiting-merge-decision, not running now, whose open pull
// request GitHub reports as CONFLICTING, lowest issue number first. A
// Maintainer then approves only a head that can merge. An issue that also
// has cumin/status/ready is left to "request the implementation", as for
// "send back for changes". A merge step of "start the merge" after the
// approval of a Maintainer that runs keeps the label, so a running issue
// gives no action.
func conflictingMaintainerReviews(snapshot Snapshot) []Action {
	var actions []Action
	for _, action := range conflictsUnder(snapshot, LabelAwaitingMergeDecision) {
		sub, _ := snapshot.SubIssue(action.(ResolveConflict).Number)
		if snapshot.Running[sub.Number] || slices.Contains(sub.Labels, LabelReady) {
			continue
		}
		actions = append(actions, action)
	}
	return actions
}

// conflictsUnder returns a conflict resolution for each open sub-issue with
// the status label whose open pull request is CONFLICTING, lowest issue
// number first.
func conflictsUnder(snapshot Snapshot, status string) []Action {
	var actions []Action
	for _, requirement := range snapshot.RequirementIssues {
		for _, sub := range requirement.SubIssues {
			if sub.Closed || !slices.Contains(sub.Labels, status) {
				continue
			}
			pr, ok := sub.LatestPullRequest()
			if !ok || pr.Mergeable != Conflicting {
				continue
			}
			actions = append(actions, ResolveConflict{Number: sub.Number, PullRequest: pr.Number})
		}
	}
	slices.SortFunc(actions, func(a, b Action) int {
		return a.(ResolveConflict).Number - b.(ResolveConflict).Number
	})
	return actions
}

// reviewableSubIssues returns the actions of "request the review": open
// sub-issues in cumin/status/checking whose open pull request has every
// required check passed on its head commit. A pull request that conflicts
// belongs to "request a conflict resolution". A running issue gives no
// action: its agent runs, and one issue has one agent.
func reviewableSubIssues(snapshot Snapshot, required []RequiredCheck) []Action {
	var actions []Action
	for _, requirement := range snapshot.RequirementIssues {
		for _, sub := range requirement.SubIssues {
			if sub.Closed || !slices.Contains(sub.Labels, LabelChecking) || snapshot.Running[sub.Number] {
				continue
			}
			pr, ok := sub.LatestPullRequest()
			if !ok || pr.Mergeable == Conflicting {
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

// failedSubIssues returns the actions of "request a check fix": open
// sub-issues in cumin/status/checking whose open pull request has a failed
// required check on its head commit, lowest issue number first. A pull
// request that conflicts belongs to "request a conflict resolution". A
// running issue gives no action.
func failedSubIssues(snapshot Snapshot, required []RequiredCheck) []Action {
	var actions []Action
	for _, requirement := range snapshot.RequirementIssues {
		for _, sub := range requirement.SubIssues {
			if sub.Closed || !slices.Contains(sub.Labels, LabelChecking) || snapshot.Running[sub.Number] {
				continue
			}
			pr, ok := sub.LatestPullRequest()
			if !ok || pr.Mergeable == Conflicting || ChecksOf(required, pr.Checks) != ChecksFailed {
				continue
			}
			actions = append(actions, FixChecks{Number: sub.Number, PullRequest: pr.Number, Failed: FailedChecks(required, pr.Checks)})
		}
	}
	slices.SortFunc(actions, func(a, b Action) int {
		return a.(FixChecks).Number - b.(FixChecks).Number
	})
	return actions
}

// unreportedSubIssues returns the actions of "stop for missing checks":
// open sub-issues in cumin/status/checking whose open pull request has a
// required check that has not reported on its head commit after the wait
// time, lowest issue number first. A pull request that conflicts belongs
// to "request a conflict resolution", and a failed required check belongs
// to "request a check fix". A sub-issue whose label time or
// whose head commit time was not read gives no action: a later poll decides.
// A commit time that was not read can hide a new head commit. A sub-issue
// with no open pull request stops too, after the wait time since the label.
// A running issue gives no action.
func unreportedSubIssues(snapshot Snapshot, required []RequiredCheck, now time.Time, checksWait time.Duration) []Action {
	var actions []Action
	for _, requirement := range snapshot.RequirementIssues {
		for _, sub := range requirement.SubIssues {
			if sub.Closed || !slices.Contains(sub.Labels, LabelChecking) || sub.CheckingAt.IsZero() || snapshot.Running[sub.Number] {
				continue
			}
			pr, ok := sub.LatestPullRequest()
			if !ok {
				if waited := now.Sub(sub.CheckingAt); waited >= checksWait {
					actions = append(actions, StopForUnreportedChecks{Number: sub.Number, Waited: waited})
				}
				continue
			}
			if pr.Mergeable == Conflicting || ChecksOf(required, pr.Checks) != ChecksWaiting {
				continue
			}
			if pr.HeadCommittedAt.IsZero() {
				continue
			}
			waited := now.Sub(ChecksWaitStart(sub.CheckingAt, pr.HeadCommittedAt))
			if waited < checksWait {
				continue
			}
			actions = append(actions, StopForUnreportedChecks{
				Number: sub.Number, PullRequest: pr.Number, HeadCommit: pr.HeadCommit,
				Unreported: UnreportedChecks(required, pr.Checks), Waited: waited,
			})
		}
	}
	slices.SortFunc(actions, func(a, b Action) int {
		return a.(StopForUnreportedChecks).Number - b.(StopForUnreportedChecks).Number
	})
	return actions
}

// ChecksWaitStart returns the start of the wait time of "stop for missing
// checks": the later one of the time that the issue entered
// cumin/status/checking and the commit time of the head commit. A push
// while the issue waits gives a newer head commit, so the wait starts again.
func ChecksWaitStart(awaitingChecksAt, headCommittedAt time.Time) time.Time {
	if headCommittedAt.After(awaitingChecksAt) {
		return headCommittedAt
	}
	return awaitingChecksAt
}

// CheckFixAllowed reports whether "request a check fix" may send one more
// check fix request: count requests were sent since a Maintainer last added
// cumin/status/ready, and limit is max_check_fix_requests of the
// repository. At the limit, "stop for failed checks" stops the issue for a
// Maintainer instead.
func CheckFixAllowed(count, limit int) bool { return count < limit }

// LabelsAfterCheckFix returns the labels of a sub-issue after "request a
// check fix": cumin/status/implementing in place of cumin/status/checking.
func LabelsAfterCheckFix(labels []string) []string {
	return ReplaceStatusLabel(labels, LabelImplementing)
}

// PullRequestLabels returns the labels that a pull request has after "copy
// the labels to the pull request": its own labels that are neither
// cumin/status/* nor risk/*, then those two kinds from the issue.
func PullRequestLabels(issue, pullRequest []string) []string {
	after := []string{}
	for _, label := range pullRequest {
		if !isCopiedLabel(label) {
			after = append(after, label)
		}
	}
	for _, label := range issue {
		if isCopiedLabel(label) {
			after = append(after, label)
		}
	}
	return after
}

// isCopiedLabel reports whether "copy the labels to the pull request"
// copies the label from the issue.
func isCopiedLabel(name string) bool {
	return IsStatusLabel(name) || strings.HasPrefix(name, riskLabelPrefix)
}

// sameLabels reports whether a and b hold the same labels, in any order.
func sameLabels(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(slices.Compact(a), slices.Compact(b))
}

// inProgress counts the issues that fill the limit, by their working label
// only: open sub-issues in implementing, checking, reviewing, or merging,
// and requirement issues in planning or accepting. A requirement issue in
// implementing ("mark the requirement as in work") has no agent of its own,
// so it does not count. An issue with cumin/status/ready never counts, also
// when the running set names it.
func inProgress(snapshot Snapshot) int {
	n := 0
	for _, requirement := range snapshot.RequirementIssues {
		if slices.Contains(requirement.Labels, LabelPlanning) || slices.Contains(requirement.Labels, LabelAccepting) {
			n++
		}
		for _, sub := range requirement.SubIssues {
			if sub.Closed {
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

// MovesWithoutMaintainer reports whether an issue exists that cumin moves on
// without a Maintainer, so that cumin is not waiting (issue-states.md, the
// table under "tell that cumin waits"): an open sub-issue that waits for
// the required checks,
// an issue in planning, implementing, reviewing, accepting, or merging,
// or a ready issue that can start and waits only for room under the limit.
// A ready issue with an open blocked-by issue, a ready issue whose ready
// another account than a Maintainer added, a status label that another
// account than cumin-core or a Maintainer added, and an issue that waits
// for a Maintainer do not count. A ready that was not read counts: the poll
// reads it when a slot is free.
func (s Snapshot) MovesWithoutMaintainer() bool {
	if s.HasIssueChecking() {
		return true
	}
	// The next poll decides the way out of a working label from the facts,
	// also when no agent runs.
	for _, requirement := range s.RequirementIssues {
		if (slices.Contains(requirement.Labels, LabelPlanning) || slices.Contains(requirement.Labels, LabelAccepting)) &&
			!statusOfAnother(requirement) {
			return true
		}
		for _, sub := range requirement.SubIssues {
			if sub.Closed || subStatusOfAnother(sub) {
				continue
			}
			for _, label := range []string{LabelImplementing, LabelReviewing, LabelMerging} {
				if slices.Contains(sub.Labels, label) {
					return true
				}
			}
		}
	}
	for _, plan := range requirementCandidates(s) {
		if requirement, _ := s.RequirementIssue(plan.Number); !readyOfAnother(requirement.ReadyRead, requirement.ReadyOwner) {
			return true
		}
	}
	for _, claim := range subIssueCandidates(s) {
		if sub, _ := s.SubIssue(claim.Number); !readyOfAnother(sub.ReadyRead, sub.ReadyOwner) {
			return true
		}
	}
	return false
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

// LabelsAfterPlan returns the labels of a requirement issue after "request
// the split": cumin/status/planning in place of cumin/status/ready.
// cumin/type/requirement stays.
func LabelsAfterPlan(labels []string) []string {
	return ReplaceStatusLabel(labels, LabelPlanning)
}

// LabelsAfterClaim returns the labels of a sub-issue after "request the
// implementation": cumin/status/implementing in place of the old status label.
// issue-states.md says that cumin removes the old status label when it
// starts the work.
func LabelsAfterClaim(labels []string) []string {
	return ReplaceStatusLabel(labels, LabelImplementing)
}

// ChecksState says what the required checks of a pull request say together.
type ChecksState int

const (
	// ChecksWaiting: a required check has not reported yet, or has not
	// finished. cumin waits ("request the review" and "request a check
	// fix" both need an answer first).
	ChecksWaiting ChecksState = iota
	// ChecksPassed: every required check passed. "request the review"
	// applies.
	ChecksPassed
	// ChecksFailed: a required check failed. "request a check fix" applies.
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
//     success, skipped, or neutral (row 51 of
//     measured-constraints.md); the caller receives them folded already.
//   - A failed result decides the whole answer, even when another required
//     check has not reported yet: the check fix comes before the wait.
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

// FailedChecks returns the required checks that failed, in the order of the
// required checks. "request a check fix" reads what each one says and names
// it in its request.
// A check keeps its App, because two rules can require the same name from
// two Apps.
func FailedChecks(required []RequiredCheck, results []CheckResult) []RequiredCheck {
	var failed []RequiredCheck
	for _, check := range required {
		if checkState(check, results) == ChecksFailed {
			failed = append(failed, check)
		}
	}
	return failed
}

// UnreportedChecks returns the required checks that have not reported on
// the commit: no result, or a result that has not finished. The order is
// that of the required checks. "stop for missing checks" names them for a
// Maintainer.
func UnreportedChecks(required []RequiredCheck, results []CheckResult) []RequiredCheck {
	var unreported []RequiredCheck
	for _, check := range required {
		if checkState(check, results) == ChecksWaiting {
			unreported = append(unreported, check)
		}
	}
	return unreported
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

// LabelsAfterReview returns the labels of a sub-issue after "request the
// review": cumin/status/reviewing in place of cumin/status/checking.
func LabelsAfterReview(labels []string) []string {
	return ReplaceStatusLabel(labels, LabelReviewing)
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
		risks := 0
		for _, label := range sub.Labels {
			if strings.HasPrefix(label, riskLabelPrefix) {
				risks++
			}
		}
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

// The round of the review (issue-states.md, the text on rounds): cumin
// counts the reviews of cumin-reviewer after the later of two times, the
// last cumin/status/ready of the implementation issue and the last APPROVE
// of cumin-reviewer. Both are facts on GitHub, so the count survives a
// restart and cumin keeps nothing for it.
//
// A round is a review that asked for changes: CHANGES_REQUESTED. A review
// with only COMMENT is not a result of the Reviewer (the Reviewer
// requirement, completion): cumin asks for the review again, and that
// request is not a new round. A pending review is not submitted.
//
// A DISMISSED review starts the count again, as an APPROVE does. GitHub
// shows only the state now, not the state before the dismissal. The rule
// "Dismiss stale pull request approvals when new commits are pushed" of a
// ruleset dismisses approvals, so a dismissed review is most often a former
// APPROVE; counting it as a round would bring back the rounds before that
// approval and reach the limit too early. A person who dismisses a request
// for changes steps in as a Maintainer does, and the count may start again.

// roundStart is the later of the last cumin/status/ready of the issue and
// the last APPROVE of the Reviewer, or its last dismissed review. The
// APPROVE is a start too, because a merge conflict fixed after it changes
// the head commit, and the review starts again (the merge of "start the
// merge" after the review of the Reviewer).
func roundStart(reviews []Review, reviewer string, readyAt time.Time) time.Time {
	start := readyAt
	for _, review := range reviews {
		restarts := review.State == ReviewApproved || review.State == ReviewDismissed
		if review.Author == reviewer && restarts && review.SubmittedAt.After(start) {
			start = review.SubmittedAt
		}
	}
	return start
}

// roundReviews are the reviews of the Reviewer that count as rounds since
// roundStart, oldest first.
func roundReviews(reviews []Review, reviewer string, readyAt time.Time) []Review {
	start := roundStart(reviews, reviewer, readyAt)
	var rounds []Review
	for _, review := range reviews {
		if review.Author != reviewer || !review.SubmittedAt.After(start) {
			continue
		}
		if review.State == ReviewChangesRequested {
			rounds = append(rounds, review)
		}
	}
	slices.SortStableFunc(rounds, func(a, b Review) int { return a.SubmittedAt.Compare(b.SubmittedAt) })
	return rounds
}

// ReviewRounds is the number of rounds of the Reviewer since the last
// cumin/status/ready of the issue (readyAt) or its last APPROVE. After a
// review that asked for changes, it is the round of that review: "request a
// review fix" asks for a fix below max_review_rounds, and "stop at the
// round limit" stops at it. Before a review, the next round is
// ReviewRounds + 1 ("request the review").
func ReviewRounds(reviews []Review, reviewer string, readyAt time.Time) int {
	return len(roundReviews(reviews, reviewer, readyAt))
}

// LastReviewedCommit is the commit of the last round of the Reviewer since
// the start of the rounds, or "" in round 1. Round 2 and later look at the
// diff from this commit to the head commit.
func LastReviewedCommit(reviews []Review, reviewer string, readyAt time.Time) string {
	rounds := roundReviews(reviews, reviewer, readyAt)
	if len(rounds) == 0 {
		return ""
	}
	return rounds[len(rounds)-1].Commit
}

// LastApprovedCommit is the commit of the newest review of the Reviewer with
// the state APPROVED, or "" when the Reviewer approved no commit. After an
// approval the rounds start again at 1, and round 1 looks only at the diff
// from this commit to the head commit (agents/reviewer.md, the scope of each
// round). A DISMISSED review does not count: GitHub does not show the state
// before the dismissal. A review of another author and a pending review do
// not count either.
func LastApprovedCommit(reviews []Review, reviewer string) string {
	var latest Review
	found := false
	for _, review := range reviews {
		if review.Author != reviewer || review.State != ReviewApproved {
			continue
		}
		if !found || !review.SubmittedAt.Before(latest.SubmittedAt) {
			latest, found = review, true
		}
	}
	return latest.Commit
}

// LatestReview is the last submitted review of the Reviewer, in any state.
// The check after a Reviewer run reads it: it must be on the head commit,
// with APPROVE or REQUEST_CHANGES (the Reviewer requirement, completion).
func LatestReview(reviews []Review, reviewer string) (Review, bool) {
	var latest Review
	found := false
	for _, review := range reviews {
		if review.Author != reviewer || review.State == ReviewPending || review.SubmittedAt.IsZero() {
			continue
		}
		if !found || !review.SubmittedAt.Before(latest.SubmittedAt) {
			latest, found = review, true
		}
	}
	return latest, found
}

// ReviewResult is what cumin finds after a Reviewer run that returned done
// (the Reviewer requirement, completion).
type ReviewResult int

const (
	// ReviewMissing: the latest review of the Reviewer is not on the head
	// commit, is not APPROVE or REQUEST_CHANGES, or does not exist.
	ReviewMissing ReviewResult = iota
	// ReviewApprovedOnHead is APPROVE on the head commit ("start the
	// merge", "ask for the merge decision").
	ReviewApprovedOnHead
	// ReviewChangesRequestedOnHead is REQUEST_CHANGES on the head commit
	// ("request a review fix", "request the cause", "stop at the round
	// limit").
	ReviewChangesRequestedOnHead
)

func (r ReviewResult) String() string {
	switch r {
	case ReviewMissing:
		return "missing"
	case ReviewApprovedOnHead:
		return "approved"
	case ReviewChangesRequestedOnHead:
		return "changes requested"
	}
	return fmt.Sprintf("ReviewResult(%d)", int(r))
}

// CheckReview reads the latest review of the Reviewer on the pull request.
// Only a review on the head commit counts: a review of an older commit did
// not see the change that is there now.
func CheckReview(pr PullRequest, reviewer string) ReviewResult {
	latest, ok := LatestReview(pr.Reviews, reviewer)
	if !ok || latest.Commit == "" || latest.Commit != pr.HeadCommit {
		return ReviewMissing
	}
	switch latest.State {
	case ReviewApproved:
		return ReviewApprovedOnHead
	case ReviewChangesRequested:
		return ReviewChangesRequestedOnHead
	}
	return ReviewMissing
}

// ReviewFixAllowed is the check of "request a review fix": the round of the
// review that asked for changes is below max_review_rounds. At the limit,
// "request the cause" or "stop at the round limit" applies instead.
func ReviewFixAllowed(round, limit int) bool { return round < limit }

// DecisionRequestHeading starts every decision request
// (templates/decision-request.md). At the round limit, cumin looks for a
// comment of the Reviewer that starts with it.
const DecisionRequestHeading = "## Decision needed"

// ExplanationOf returns the decision request that the Reviewer wrote on the
// pull request after its last review (the cause comment of "stop at the
// round limit"): the newest comment of the
// Reviewer whose first line starts with DecisionRequestHeading, created at
// or after since. The comments are the ones that cumin read after since.
func ExplanationOf(comments []Comment, reviewer string, since time.Time) (Comment, bool) {
	var found Comment
	ok := false
	for _, c := range comments {
		if c.Author != reviewer || c.CreatedAt.Before(since) || !strings.HasPrefix(firstBodyLine(c.Body), DecisionRequestHeading) {
			continue
		}
		if !ok || !c.CreatedAt.Before(found.CreatedAt) {
			found, ok = c, true
		}
	}
	return found, ok
}

func firstBodyLine(body string) string {
	line, _, _ := strings.Cut(strings.TrimLeft(body, "\r\n"), "\n")
	return strings.TrimSpace(line)
}

// MergeDecision is what cumin does after the Reviewer approved the head
// commit of the pull request ("start the merge", "ask for the merge
// decision"). "start the merge" after the approval of a Maintainer uses the
// same decision.
type MergeDecision int

const (
	// MergeNow: risk/low; cumin merges ("start the merge").
	MergeNow MergeDecision = iota
	// MergeAskMaintainer: risk/medium or risk/high; a Maintainer decides
	// ("ask for the merge decision").
	MergeAskMaintainer
	// MergeNoRiskLabel: the issue has no risk/* label; cumin stops it.
	MergeNoRiskLabel
	// MergeTwoRiskLabels: the issue has more than one risk/* label; cumin
	// stops it.
	MergeTwoRiskLabels
	// MergeChecksNotPassed: a required check does not pass on the head
	// commit; the issue waits for the checks again, as after a head that
	// moved during the review.
	MergeChecksNotPassed
)

func (d MergeDecision) String() string {
	switch d {
	case MergeNow:
		return "merge"
	case MergeAskMaintainer:
		return "ask a Maintainer"
	case MergeNoRiskLabel:
		return "no risk label"
	case MergeTwoRiskLabels:
		return "more than one risk label"
	case MergeChecksNotPassed:
		return "the required checks do not pass"
	}
	return fmt.Sprintf("MergeDecision(%d)", int(d))
}

// DecideMerge decides on an approved pull request from the labels of its
// implementation issue and the checks on its head commit. The risk is read
// from the issue, never from the pull request (principle 5); an issue
// without exactly one risk/* label is an unexpected state. The risk comes
// before the checks, so that a wrong label always stops the issue.
func DecideMerge(labels []string, required []RequiredCheck, checks []CheckResult) MergeDecision {
	var risks []string
	for _, label := range labels {
		if strings.HasPrefix(label, riskLabelPrefix) {
			risks = append(risks, label)
		}
	}
	switch {
	case len(risks) == 0:
		return MergeNoRiskLabel
	case len(risks) > 1:
		return MergeTwoRiskLabels
	case ChecksOf(required, checks) != ChecksPassed:
		return MergeChecksNotPassed
	case risks[0] == "risk/low":
		return MergeNow
	}
	return MergeAskMaintainer
}

// isBot reports whether a login is the bot of a GitHub App: the client
// gives a Bot as "<slug>[bot]".
func isBot(login string) bool { return strings.HasSuffix(login, "[bot]") }

// decides reports whether a review state counts for a merge decision.
// GitHub decides on APPROVED and CHANGES_REQUESTED; a comment changes
// neither.
func decides(state ReviewState) bool {
	return state == ReviewApproved || state == ReviewChangesRequested
}

// maintainerApprovals returns the candidates of "start the merge" after the
// approval of a Maintainer, lowest issue number first: open sub-issues in
// cumin/status/awaiting-merge-decision, not running now,
// whose open pull request has an APPROVED review of a person on its head
// commit. The candidate names every person whose review decides, because
// the latest review of any Maintainer among them counts.
func maintainerApprovals(snapshot Snapshot) []Action {
	var actions []Action
	for _, c := range maintainerReviewCandidates(snapshot, ReviewApproved) {
		actions = append(actions, MergeMaintainerApproval(c))
	}
	return actions
}

// maintainerChangeRequests returns the candidates of "send back for
// changes", lowest issue number first: as maintainerApprovals, with a
// CHANGES_REQUESTED review of a person on the head commit. An issue that
// also has cumin/status/ready is not a candidate: a Maintainer asked for a
// new start, and "request the implementation" takes it. The review
// is newer than the last cumin/status/awaiting-merge-decision of the issue
// (newChangeRequest), so that one review sends the pull request back once.
func maintainerChangeRequests(snapshot Snapshot) []Action {
	var actions []Action
	for _, c := range maintainerReviewCandidates(snapshot, ReviewChangesRequested) {
		sub, _ := snapshot.SubIssue(c.Number)
		if slices.Contains(sub.Labels, LabelReady) || !newChangeRequest(snapshot, sub) {
			continue
		}
		actions = append(actions, c)
	}
	return actions
}

// newChangeRequest reports whether the pull request of the sub-issue has a
// CHANGES_REQUESTED review of a person on its head commit that was
// submitted after cumin/status/awaiting-merge-decision was last added to the
// issue. While the label times are not read, or hold no time of that label,
// the answer is no: "send back for changes" sends nothing back without the
// time.
func newChangeRequest(snapshot Snapshot, sub SubIssue) bool {
	read := slices.ContainsFunc(snapshot.RequirementIssues, func(requirement RequirementIssue) bool {
		return requirement.LabelTimesRead &&
			slices.ContainsFunc(requirement.SubIssues, func(s SubIssue) bool { return s.Number == sub.Number })
	})
	pr, ok := sub.LatestPullRequest()
	return read && ok && !sub.AwaitingMergeDecisionAt.IsZero() && slices.ContainsFunc(pr.Reviews, func(review Review) bool {
		return !isBot(review.Author) && review.State == ReviewChangesRequested &&
			review.Commit == pr.HeadCommit && review.SubmittedAt.After(sub.AwaitingMergeDecisionAt)
	})
}

// DecidingReviewers returns, in the order of their logins, every person
// whose review on the pull request decides (APPROVED or CHANGES_REQUESTED).
// A bot is no person.
func DecidingReviewers(reviews []Review) []string {
	var reviewers []string
	for _, review := range reviews {
		if review.Author == "" || isBot(review.Author) || !decides(review.State) {
			continue
		}
		if !slices.Contains(reviewers, review.Author) {
			reviewers = append(reviewers, review.Author)
		}
	}
	slices.Sort(reviewers)
	return reviewers
}

// maintainerReviewCandidates returns, lowest issue number first, the open
// sub-issues in cumin/status/awaiting-merge-decision, not running now, whose
// open pull request has a review of a person with the state on its head
// commit. Each one names every person whose review decides.
func maintainerReviewCandidates(snapshot Snapshot, state ReviewState) []FixMaintainerReview {
	var candidates []FixMaintainerReview
	for _, requirement := range snapshot.RequirementIssues {
		for _, sub := range requirement.SubIssues {
			if sub.Closed || !slices.Contains(sub.Labels, LabelAwaitingMergeDecision) || snapshot.Running[sub.Number] {
				continue
			}
			pr, ok := sub.LatestPullRequest()
			if !ok {
				continue
			}
			candidate := slices.ContainsFunc(pr.Reviews, func(review Review) bool {
				return review.Author != "" && !isBot(review.Author) && review.State == state && review.Commit == pr.HeadCommit
			})
			if candidate {
				reviewers := DecidingReviewers(pr.Reviews)
				candidates = append(candidates, FixMaintainerReview{Number: sub.Number, PullRequest: pr.Number, Reviewers: reviewers})
			}
		}
	}
	slices.SortFunc(candidates, func(a, b FixMaintainerReview) int { return a.Number - b.Number })
	return candidates
}

// MaintainerApproved applies the check of "start the merge" after the
// approval of a Maintainer: of the reviews of the Maintainers (maintainers
// holds their logins), the latest one that decides is APPROVED on the head
// commit. An approval on an older commit does not count, and a later
// CHANGES_REQUESTED of a Maintainer takes the approval back. A review of
// a bot never counts, whatever maintainers says.
func MaintainerApproved(reviews []Review, head string, maintainers map[string]bool) bool {
	latest, found := latestMaintainerReview(reviews, maintainers)
	return found && latest.State == ReviewApproved && head != "" && latest.Commit == head
}

// MaintainerRequestedChanges applies the check of "send back for changes":
// of the reviews of the Maintainers (maintainers holds their logins), the
// latest one that decides is CHANGES_REQUESTED on the head commit,
// submitted after awaitingMaintainerAt:
// the time that cumin/status/awaiting-merge-decision was last added to the
// issue. It returns that review, whose address the request names. A request
// for changes on an older commit does not count, and a later APPROVED of a
// Maintainer takes it back. A comment-only review decides nothing, and a review
// of a bot never counts. A review that is not newer than the label sent the
// pull request back already, or came before the Issue Owner was asked, so one
// review sends the pull request back once. A zero awaitingMaintainerAt means
// that the time of the label is not known, and no review counts.
func MaintainerRequestedChanges(reviews []Review, head string, maintainers map[string]bool, awaitingMaintainerAt time.Time) (Review, bool) {
	latest, found := latestMaintainerReview(reviews, maintainers)
	if !found || latest.State != ReviewChangesRequested || head == "" || latest.Commit != head ||
		awaitingMaintainerAt.IsZero() || !latest.SubmittedAt.After(awaitingMaintainerAt) {
		return Review{}, false
	}
	return latest, true
}

// latestMaintainerReview returns the latest review that decides among the
// reviews of the Maintainers. A bot is never a Maintainer, whatever
// maintainers says.
func latestMaintainerReview(reviews []Review, maintainers map[string]bool) (Review, bool) {
	var latest Review
	found := false
	for _, review := range reviews {
		if !maintainers[review.Author] || isBot(review.Author) || !decides(review.State) {
			continue
		}
		if !found || !review.SubmittedAt.Before(latest.SubmittedAt) {
			latest, found = review, true
		}
	}
	return latest, found
}

// IsMaintainer applies the definition of the Maintainer (cumin-core.md): a
// person, not a bot, with write or admin permission on the repository.
// GitHub reports maintain as write ("Get repository permissions for a user").
func IsMaintainer(permission, userType string) bool {
	return userType == "User" && (permission == "admin" || permission == "write")
}
