// Package workflow holds the rules of cumin (the rows R*, I*, and Q* of
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
	// Running are the issues whose agent runs in this cumin now. A label
	// shows most of them; an acceptance check (R4) keeps the label of the
	// requirement issue, so only this shows that its Planner runs.
	Running map[int]bool
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

// HasIssueInWork reports whether an issue of the repository is in work:
// an open requirement issue with cumin/status/ready or
// cumin/status/planning, or an open sub-issue with cumin/status/ready,
// cumin/status/implementing, cumin/status/awaiting-checks, or
// cumin/status/reviewing. An issue that waits for the Owner is not in work.
func (s Snapshot) HasIssueInWork() bool {
	for _, requirement := range s.RequirementIssues {
		if slices.Contains(requirement.Labels, LabelReady) || slices.Contains(requirement.Labels, LabelPlanning) {
			return true
		}
		for _, sub := range requirement.SubIssues {
			if sub.Closed {
				continue
			}
			for _, label := range []string{LabelReady, LabelImplementing, LabelAwaitingChecks, LabelReviewing} {
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
// repository that is not in work has only issues that wait for the Owner,
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

// HasOwnerApprovalCandidate reports whether a sub-issue is a candidate of
// I12, so that the poll reads the required checks for it.
func (s Snapshot) HasOwnerApprovalCandidate() bool { return len(ownerApprovals(s)) > 0 }

// RequirementIssue is an open issue with cumin/type/requirement.
type RequirementIssue struct {
	Number    int
	Labels    []string
	SubIssues []SubIssue
	// BlockedBy are the issues that block the requirement issue. The Owner
	// links requirement issues to each other, and R1 waits for them.
	BlockedBy []BlockedBy
	// LabelTimesRead says that ReviewAt and the ReadyAt, AwaitingChecksAt,
	// and AwaitingOwnerReviewAt of the sub-issues were read. The poll reads them
	// only when a rule needs them (NeedsLabelTimes).
	LabelTimesRead bool
	// ReviewAt is when cumin/status/awaiting-owner-review was last added.
	ReviewAt time.Time
	// CommentsRead says that AcceptanceCheckAt was read. The poll reads
	// the comments only when R4 or R7 needs them (NeedsComments).
	CommentsRead bool
	// AcceptanceCheckAt is when the newest acceptance check comment of the
	// Planner App was written; zero when there is none.
	AcceptanceCheckAt time.Time
	// FollowUpsDone says that no closed sub-issue needs a follow-up note
	// (I9) any more: each one has its note, was closed without a merge, or
	// left nothing to copy. The poll sets it after I9, and R4 waits for it.
	FollowUpsDone bool
}

// SubIssue is an implementation issue: a sub-issue of a requirement issue.
type SubIssue struct {
	Number int
	// NodeID is the GraphQL ID of the issue. I2 adds the closing link
	// with it.
	NodeID    string
	Title     string
	Closed    bool
	Labels    []string
	BlockedBy []BlockedBy
	// PullRequests are the open pull requests that close the issue (the
	// closing link). Every row reads the pull request of the issue here.
	// Only I2 finds it by the branch, and then adds the link when it is
	// missing.
	PullRequests []PullRequest
	// ReadyAt is when cumin/status/ready was last added. It is read only
	// when RequirementIssue.LabelTimesRead is true.
	ReadyAt time.Time
	// AwaitingChecksAt is when cumin/status/awaiting-checks was last added.
	// It is read only when RequirementIssue.LabelTimesRead is true.
	AwaitingChecksAt time.Time
	// AwaitingOwnerReviewAt is when cumin/status/awaiting-owner-review was
	// last added. It is read only when RequirementIssue.LabelTimesRead is
	// true.
	AwaitingOwnerReviewAt time.Time
	// ClosedAt is when a closed sub-issue closed.
	ClosedAt time.Time
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
	// Reviews are the reviews of the pull request. The round of the review
	// and the check after a Reviewer run read them (I3, I5, I8).
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

// FixChecks is the action of I4: a required check failed on the head
// commit of the pull request, so the issue goes back to the Implementer
// with the failed checks, in the same session. Whether the limit of check
// fix requests allows it is decided when it is applied, from the count
// that the Host keeps (CheckFixAllowed).
type FixChecks struct {
	Number      int
	PullRequest int
	Failed      []RequiredCheck
}

// ResolveConflict is the action of I14: the pull request of an issue that
// waits for the checks conflicts with the default branch, so the issue
// goes back to the Implementer for a conflict resolution, in the same
// session. GitHub runs no pull_request workflow on a pull request that
// conflicts, so its required checks would never report.
type ResolveConflict struct {
	Number      int
	PullRequest int
}

// StopForUnreportedChecks is the action of I15: a required check has not
// reported on the head commit of the pull request, and the wait time of the
// repository is over, so the issue stops for the Owner. It carries the facts
// that cumin sees; it names no cause. The row also holds when no open pull
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

// CopyLabels is the action of I11: the pull request gets Labels, so that its
// cumin/status/* and risk/* labels are those of the issue that it closes.
type CopyLabels struct {
	Issue       int
	PullRequest int
	Labels      []string
}

// Plan is the action of R1: replace the status label of the requirement
// issue with cumin/status/planning, and only then request the split from
// the Planner.
type Plan struct {
	Number int
}

// StartRequirement is the action of R3: the Owner let a sub-issue start, so
// the requirement issue moves to cumin/status/implementing.
type StartRequirement struct {
	Number int
}

// ReviewRemaining is the action of R6: every open sub-issue has no status
// label, so the requirement issue moves to
// cumin/status/awaiting-owner-review and the Owner is told that the
// remaining sub-issues need a look.
type ReviewRemaining struct {
	Number int
}

// CheckAcceptance is the action of R4: request the acceptance check from
// the Planner. The label stays cumin/status/implementing.
type CheckAcceptance struct {
	Number int
}

// Accept is the action of R7: the acceptance check comment exists, so the
// requirement issue moves to cumin/status/awaiting-owner-review and the
// Owner is told that it can be accepted.
type Accept struct {
	Number int
}

// MergeOwnerApproval is the candidate of I12: an implementation issue in
// cumin/status/awaiting-owner-review whose pull request has an APPROVED
// review of a person on its head commit. Reviewers are the people whose
// reviews decide (APPROVED or CHANGES_REQUESTED); the caller reads their
// permission, and only then knows which of them is an Owner
// (OwnerApproved).
type MergeOwnerApproval struct {
	Number      int
	PullRequest int
	Reviewers   []string
}

// FixOwnerReview is the candidate of I13: an implementation issue in
// cumin/status/awaiting-owner-review whose pull request has a
// CHANGES_REQUESTED review of a person on its head commit, submitted after
// that label was last added. Reviewers are the people whose reviews decide,
// as for MergeOwnerApproval; the caller reads their permission, and only
// then knows whether the latest review of an Owner requests changes
// (OwnerRequestedChanges).
type FixOwnerReview struct {
	Number      int
	PullRequest int
	Reviewers   []string
}

// Action is one thing that cumin does after a poll. Later rules add types.
type Action interface {
	isAction()
}

func (Claim) isAction()                   {}
func (StartReview) isAction()             {}
func (FixChecks) isAction()               {}
func (ResolveConflict) isAction()         {}
func (StopForUnreportedChecks) isAction() {}
func (CopyLabels) isAction()              {}
func (Plan) isAction()                    {}
func (StartRequirement) isAction()        {}
func (ReviewRemaining) isAction()         {}
func (CheckAcceptance) isAction()         {}
func (Accept) isAction()                  {}
func (MergeOwnerApproval) isAction()      {}
func (FixOwnerReview) isAction()          {}

// Decide returns the actions for the snapshot, in the order to apply them.
// maxInProgress is the setting "max_issues_in_progress": the number of issues
// of one repository that can be in cumin/status/planning, implementing,
// awaiting-checks, or reviewing at the same time (cumin-core.md, the
// settings table). required are the checks that the rules of the default
// branch require; the caller reads them only when an issue of the
// repository waits for the checks. now is the time of the poll, and
// checksWait is the setting "checks_wait_time"; I15 reads both.
//
// I14, I3, I4, and I15 come before the starts of R1 and I1: an issue that
// leaves cumin/status/awaiting-checks keeps its place in the limit, so
// deciding it first never takes room from a start. I14 (the pull request
// conflicts) comes before I3 and I4 for its issue, and I15 (the required
// checks did not report in time) comes after them: only the first row that
// holds moves the issue at one poll.
//
// R1 and I1 both start an agent, so they share the room under the limit.
// The starts are taken highest priority first and then lowest issue number
// first, whichever row they belong to. priority are the priority labels of
// the repository, highest first (PriorityRank). The priority only orders
// the starts that can begin: an open blocked-by issue and the limit decide
// before it, and the quota decides when a start is applied.
//
// R3 and R6 move a requirement issue and start no agent, so they come
// first and take no room.
func Decide(snapshot Snapshot, maxInProgress int, required []RequiredCheck, priority []string, now time.Time, checksWait time.Duration) []Action {
	actions := requirementMoves(snapshot)
	actions = append(actions, conflictingSubIssues(snapshot)...)
	actions = append(actions, reviewableSubIssues(snapshot, required)...)
	actions = append(actions, failedSubIssues(snapshot, required)...)
	actions = append(actions, unreportedSubIssues(snapshot, required, now, checksWait)...)
	room := maxInProgress - inProgress(snapshot)
	type start struct {
		rank   int
		number int
		action Action
	}
	// A start on a requirement issue has the priority of that issue.
	requirementRank := func(number int) int {
		requirement, _ := snapshot.RequirementIssue(number)
		return PriorityRank(requirement.Labels, nil, priority)
	}
	var starts []start
	for _, plan := range readyRequirementIssues(snapshot) {
		starts = append(starts, start{requirementRank(plan.Number), plan.Number, plan})
	}
	for _, check := range acceptanceChecks(snapshot) {
		starts = append(starts, start{requirementRank(check.Number), check.Number, check})
	}
	for _, claim := range readySubIssues(snapshot) {
		sub, _ := snapshot.SubIssue(claim.Number)
		requirement, _ := snapshot.RequirementIssue(claim.RequirementIssue)
		starts = append(starts, start{PriorityRank(sub.Labels, requirement.Labels, priority), claim.Number, claim})
	}
	slices.SortFunc(starts, func(a, b start) int {
		return cmp.Or(a.rank-b.rank, a.number-b.number)
	})
	for _, s := range starts[:max(0, min(room, len(starts)))] {
		actions = append(actions, s.action)
	}
	actions = append(actions, ownerApprovals(snapshot)...)
	actions = append(actions, ownerChangeRequests(snapshot)...)
	return append(actions, labelCopies(snapshot)...)
}

// WithoutNewWork returns the actions that cumin still applies while it
// stops after its runs: the ones that ask no agent for new work. A requirement issue
// still changes its label, a pull request still gets the labels of its
// issue, an approval of an Owner still merges, and required checks that do
// not report in time still stop the issue for the Owner. The split, the
// acceptance check, the claim, the review, the check fix, the conflict
// resolution, and the fix of the
// Owner's review wait for the next start of cumin; each of them starts from a label that no agent
// works under, so nothing is lost (designs/cumin-core.md, the topic on the
// stop).
func WithoutNewWork(actions []Action) []Action {
	kept := make([]Action, 0, len(actions))
	for _, action := range actions {
		switch action.(type) {
		case Plan, CheckAcceptance, Claim, StartReview, FixChecks, ResolveConflict, FixOwnerReview:
		default:
			kept = append(kept, action)
		}
	}
	return kept
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

// requirementMoves returns the actions of R3 and R6, lowest requirement
// issue number first.
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
		case accepted(requirement):
			actions = append(actions, Accept{Number: requirement.Number})
		}
	}
	return actions
}

// startsImplementing is R3. Without a status label, an open sub-issue with
// cumin/status/ready is enough: the Owner wrote the sub-issues without the
// Planner. In cumin/status/awaiting-owner-review, cumin/status/ready must
// have been added after that label. A sub-issue that kept its
// cumin/status/ready from an earlier split must not move the requirement
// issue before the Owner looked at the new sub-issues (issue-states.md, the
// text below the table).
func startsImplementing(requirement RequirementIssue) bool {
	switch statusLabel(requirement.Labels) {
	case "":
		return slices.ContainsFunc(requirement.SubIssues, openReady)
	case LabelAwaitingOwnerReview:
		if !requirement.LabelTimesRead {
			return false
		}
		return slices.ContainsFunc(requirement.SubIssues, func(sub SubIssue) bool {
			return openReady(sub) && sub.ReadyAt.After(requirement.ReviewAt)
		})
	}
	return false
}

// remainingNeedReview is R6: the requirement issue is in
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

// NeedsComments reports whether R4 or R7 needs the comments of the
// requirement issue: it is in cumin/status/implementing, and it has one or
// more sub-issues, all closed.
func NeedsComments(requirement RequirementIssue) bool {
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
// last sub-issue closed. An older comment belongs to an earlier round: the
// Owner added sub-issues after it (issue-states.md, the text below the
// table).
//
// A comment at the same second as the last close counts: the times of
// GitHub cannot order two events inside one second, and a check that never
// counts would ask again at every poll.
func checked(requirement RequirementIssue) bool {
	return requirement.CommentsRead && !requirement.AcceptanceCheckAt.IsZero() &&
		!requirement.AcceptanceCheckAt.Before(lastClose(requirement))
}

// accepted is R7.
func accepted(requirement RequirementIssue) bool {
	return NeedsComments(requirement) && checked(requirement)
}

// acceptanceChecks returns the starts of R4 before the limit: every
// sub-issue closed, the comments read, no acceptance check after the last
// close, the follow-up notes of the closed sub-issues written (I9), and no
// agent of the requirement issue running.
func acceptanceChecks(snapshot Snapshot) []CheckAcceptance {
	var checks []CheckAcceptance
	for _, requirement := range snapshot.RequirementIssues {
		if NeedsComments(requirement) && requirement.CommentsRead && !checked(requirement) &&
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

// acceptanceCheckHeading is the first heading of templates/acceptance-check.md.
const acceptanceCheckHeading = "## Acceptance check"

// Comment is one comment of a requirement issue, as R4 and R7 read it.
type Comment struct {
	// Author is the login, "<slug>[bot]" for a GitHub App.
	Author    string
	CreatedAt time.Time
	Body      string
	// URL is the address of the comment; the notification of I8 links it.
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

// NeedsLabelTimes reports whether a rule needs the label times of the
// requirement issue: R3 needs them (startNeedsLabelTimes), an open
// sub-issue waits in cumin/status/awaiting-checks, whose wait is counted
// from the time of that label, or an open sub-issue in
// cumin/status/awaiting-owner-review has a request for changes of a person
// on its head commit, which I13 compares with the time of that label. Only
// then does the poll read the times, so that the poll query keeps its cost.
func NeedsLabelTimes(requirement RequirementIssue) bool {
	return startNeedsLabelTimes(requirement) ||
		slices.ContainsFunc(requirement.SubIssues, openAwaitingChecks) ||
		slices.ContainsFunc(requirement.SubIssues, openWithChangeRequest)
}

// startNeedsLabelTimes reports whether R3 needs the label times of the
// requirement issue: it waits in cumin/status/awaiting-owner-review, and an
// open sub-issue carries cumin/status/ready.
func startNeedsLabelTimes(requirement RequirementIssue) bool {
	return statusLabel(requirement.Labels) == LabelAwaitingOwnerReview &&
		slices.ContainsFunc(requirement.SubIssues, openReady)
}

func openAwaitingChecks(sub SubIssue) bool {
	return !sub.Closed && slices.Contains(sub.Labels, LabelAwaitingChecks)
}

// openWithChangeRequest reports whether an open sub-issue in
// cumin/status/awaiting-owner-review has a CHANGES_REQUESTED review of a
// person on the head commit of its pull request.
func openWithChangeRequest(sub SubIssue) bool {
	if sub.Closed || !slices.Contains(sub.Labels, LabelAwaitingOwnerReview) {
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

// readyRequirementIssues returns the plans of R1 before the limit: open
// requirement issues with cumin/status/ready whose blocked-by issues are
// all closed. Whether the requirement issue has sub-issues does not
// matter (issue-states.md, R1).
func readyRequirementIssues(snapshot Snapshot) []Plan {
	var plans []Plan
	for _, requirement := range snapshot.RequirementIssues {
		if slices.Contains(requirement.Labels, LabelReady) && !anyOpen(requirement.BlockedBy) {
			plans = append(plans, Plan{Number: requirement.Number})
		}
	}
	return plans
}

// labelCopies returns the actions of I11: one for each open pull request
// that closes a sub-issue and whose copied labels differ from the issue.
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

// conflictingSubIssues returns the actions of I14: open sub-issues in
// cumin/status/awaiting-checks whose open pull request GitHub reports as
// CONFLICTING, lowest issue number first. UNKNOWN says that GitHub is still
// calculating, so it gives no action: a later poll decides.
func conflictingSubIssues(snapshot Snapshot) []Action {
	var actions []Action
	for _, requirement := range snapshot.RequirementIssues {
		for _, sub := range requirement.SubIssues {
			if sub.Closed || !slices.Contains(sub.Labels, LabelAwaitingChecks) {
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

// reviewableSubIssues returns the actions of I3: open sub-issues in
// cumin/status/awaiting-checks whose open pull request has every required
// check passed on its head commit. A pull request that conflicts belongs
// to I14.
func reviewableSubIssues(snapshot Snapshot, required []RequiredCheck) []Action {
	var actions []Action
	for _, requirement := range snapshot.RequirementIssues {
		for _, sub := range requirement.SubIssues {
			if sub.Closed || !slices.Contains(sub.Labels, LabelAwaitingChecks) {
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

// failedSubIssues returns the actions of I4: open sub-issues in
// cumin/status/awaiting-checks whose open pull request has a failed
// required check on its head commit, lowest issue number first. A pull
// request that conflicts belongs to I14.
func failedSubIssues(snapshot Snapshot, required []RequiredCheck) []Action {
	var actions []Action
	for _, requirement := range snapshot.RequirementIssues {
		for _, sub := range requirement.SubIssues {
			if sub.Closed || !slices.Contains(sub.Labels, LabelAwaitingChecks) {
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

// unreportedSubIssues returns the actions of I15: open sub-issues in
// cumin/status/awaiting-checks whose open pull request has a required check
// that has not reported on its head commit after the wait time, lowest
// issue number first. A pull request that conflicts belongs to I14, and a
// failed required check belongs to I4. A sub-issue whose label time or
// whose head commit time was not read gives no action: a later poll decides.
// A commit time that was not read can hide a new head commit. A sub-issue
// with no open pull request stops too, after the wait time since the label.
func unreportedSubIssues(snapshot Snapshot, required []RequiredCheck, now time.Time, checksWait time.Duration) []Action {
	var actions []Action
	for _, requirement := range snapshot.RequirementIssues {
		for _, sub := range requirement.SubIssues {
			if sub.Closed || !slices.Contains(sub.Labels, LabelAwaitingChecks) || sub.AwaitingChecksAt.IsZero() {
				continue
			}
			pr, ok := sub.LatestPullRequest()
			if !ok {
				if waited := now.Sub(sub.AwaitingChecksAt); waited >= checksWait {
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
			waited := now.Sub(ChecksWaitStart(sub.AwaitingChecksAt, pr.HeadCommittedAt))
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

// ChecksWaitStart returns the start of the wait time of I15: the later one
// of the time that the issue entered cumin/status/awaiting-checks and the
// commit time of the head commit. A push while the issue waits gives a
// newer head commit, so the wait starts again.
func ChecksWaitStart(awaitingChecksAt, headCommittedAt time.Time) time.Time {
	if headCommittedAt.After(awaitingChecksAt) {
		return headCommittedAt
	}
	return awaitingChecksAt
}

// CheckFixAllowed reports whether I4 may send one more check fix request:
// count requests were sent since the Owner last added cumin/status/ready,
// and limit is max_check_fix_requests of the repository. At the limit, I4
// stops the issue for the Owner instead.
func CheckFixAllowed(count, limit int) bool { return count < limit }

// LabelsAfterCheckFix returns the labels of a sub-issue after I4:
// cumin/status/implementing in place of cumin/status/awaiting-checks.
func LabelsAfterCheckFix(labels []string) []string {
	return ReplaceStatusLabel(labels, LabelImplementing)
}

// PullRequestLabels returns the labels that a pull request has after I11:
// its own labels that are neither cumin/status/* nor risk/*, then those two
// kinds from the issue.
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

// isCopiedLabel reports whether I11 copies the label from the issue.
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

// inProgress counts the issues that fill the limit: open sub-issues in
// implementing, awaiting-checks, or reviewing, and requirement issues in
// planning. A requirement issue in implementing (R3) has no agent of its
// own, so it does not count.
func inProgress(snapshot Snapshot) int {
	n := 0
	for _, requirement := range snapshot.RequirementIssues {
		// An acceptance check (R4) keeps cumin/status/implementing, so its
		// running Planner counts by the running set.
		if slices.Contains(requirement.Labels, LabelPlanning) || snapshot.Running[requirement.Number] {
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
		// R3 could not be judged without the label times. A claim would
		// take away the cumin/status/ready that R3 must still see, so the
		// sub-issues wait for the next poll.
		if startNeedsLabelTimes(requirement) && !requirement.LabelTimesRead {
			continue
		}
		for _, sub := range requirement.SubIssues {
			// The Owner does an owner task by hand, so no agent ever
			// starts for it, even with cumin/status/ready (I1).
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

// MovesWithoutOwner reports whether an issue exists that cumin moves on
// without the Owner, so that cumin is not waiting (issue-states.md, the
// table under Q4): an open sub-issue that waits for the required checks,
// or a ready issue that can start and waits only for room under the limit.
// A ready issue with an open blocked-by issue, an issue that waits for the
// Owner, and an issue whose agent no longer runs do not count.
func (s Snapshot) MovesWithoutOwner() bool {
	return s.HasIssueAwaitingChecks() || len(readyRequirementIssues(s)) > 0 || len(readySubIssues(s)) > 0
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

// LabelsAfterPlan returns the labels of a requirement issue after R1:
// cumin/status/planning in place of cumin/status/ready. cumin/type/requirement
// stays.
func LabelsAfterPlan(labels []string) []string {
	return ReplaceStatusLabel(labels, LabelPlanning)
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
//     success, skipped, or neutral (row 51 of
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

// FailedChecks returns the required checks that failed, in the order of the
// required checks. I4 reads what each one says and names it in its request.
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
// that of the required checks. I15 names them for the Owner.
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

// Verification is the result of I2 after done. Passed is true when every
// check held; otherwise Failure names the first check that failed.
// PullRequest is the pull request that was checked, or 0 when there is
// none. AddLink is true when the verification passed and the issue has no
// closing link to that pull request, so cumin-core adds it.
type Verification struct {
	Passed      bool
	Failure     VerificationFailure
	PullRequest int
	AddLink     bool
}

// VerifyDone applies the checks of I2 (issue-states.md) to a sub-issue
// after the Implementer returned done. onBranch holds the open pull
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

// SplitFailure says which check of R2 failed.
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

// SplitVerification is the result of R2 after done. Passed is true when
// every check held; otherwise Failure names the first check that failed,
// and SubIssue the sub-issue that failed it (0 when there is none).
type SplitVerification struct {
	Passed   bool
	Failure  SplitFailure
	SubIssue int
}

// VerifySplit applies the checks of R2 (issue-states.md) to a requirement
// issue after the Planner returned done: it has one or more sub-issues, and
// every sub-issue carries exactly one risk/* label. The sub-issues are
// checked lowest number first, so the same issue always names the same
// failure. cumin judges nothing of the content of the split; the Owner
// reviews it.
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

// SplitStatus returns the status label of a requirement issue after R2
// passed. With one or more open sub-issues, the Owner reviews the split:
// cumin/status/awaiting-owner-review. With every sub-issue closed, the
// Planner created none, as when the Owner resumes a requirement issue after
// a blocked acceptance check: cumin/status/implementing, so that R4 asks for
// the acceptance check again (issue-states.md, R2).
func SplitStatus(requirement RequirementIssue) string {
	for _, sub := range requirement.SubIssues {
		if !sub.Closed {
			return LabelAwaitingOwnerReview
		}
	}
	return LabelImplementing
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
// for changes steps in as the Owner does, and the count may start again.

// roundStart is the later of the last cumin/status/ready of the issue and
// the last APPROVE of the Reviewer, or its last dismissed review. The
// APPROVE is a start too, because a merge conflict fixed after it changes
// the head commit, and the review starts again (I6).
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
// review that asked for changes, it is the round of that review: I5 asks for
// a fix below max_review_rounds, and I8 stops at it. Before a review, the
// next round is ReviewRounds + 1 (I3).
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
	// ReviewApprovedOnHead is APPROVE on the head commit (I6, I7).
	ReviewApprovedOnHead
	// ReviewChangesRequestedOnHead is REQUEST_CHANGES on the head commit
	// (I5, I8).
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

// ReviewFixAllowed is the check of I5: the round of the review that asked
// for changes is below max_review_rounds. At the limit, I8 applies instead.
func ReviewFixAllowed(round, limit int) bool { return round < limit }

// DecisionRequestHeading starts every decision request
// (templates/decision-request.md). I8 looks for a comment of the Reviewer
// that starts with it.
const DecisionRequestHeading = "## Decision needed"

// ExplanationOf returns the decision request that the Reviewer wrote on the
// pull request after its last review (I8): the newest comment of the
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
// commit of the pull request (I6, I7). I12 uses the same decision after the
// approval of the Owner.
type MergeDecision int

const (
	// MergeNow: risk/low; cumin merges (I6).
	MergeNow MergeDecision = iota
	// MergeAskOwner: risk/medium or risk/high; the Owner decides (I7).
	MergeAskOwner
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
	case MergeAskOwner:
		return "ask the Owner"
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
	return MergeAskOwner
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

// ownerApprovals returns the candidates of I12, lowest issue number first:
// open sub-issues in cumin/status/awaiting-owner-review, not running now,
// whose open pull request has an APPROVED review of a person on its head
// commit. The candidate names every person whose review decides, because
// the latest review of any Owner among them counts.
func ownerApprovals(snapshot Snapshot) []Action {
	var actions []Action
	for _, c := range ownerReviewCandidates(snapshot, ReviewApproved) {
		actions = append(actions, MergeOwnerApproval(c))
	}
	return actions
}

// ownerChangeRequests returns the candidates of I13, lowest issue number
// first: as ownerApprovals, with a CHANGES_REQUESTED review of a person on
// the head commit. An issue that also has cumin/status/ready is not a
// candidate: the Owner asked for a new start, and I1 takes it. The review
// is newer than the last cumin/status/awaiting-owner-review of the issue
// (newChangeRequest), so that one review sends the pull request back once.
func ownerChangeRequests(snapshot Snapshot) []Action {
	var actions []Action
	for _, c := range ownerReviewCandidates(snapshot, ReviewChangesRequested) {
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
// submitted after cumin/status/awaiting-owner-review was last added to the
// issue. While the label times are not read, or hold no time of that label,
// the answer is no: I13 sends nothing back without the time.
func newChangeRequest(snapshot Snapshot, sub SubIssue) bool {
	read := slices.ContainsFunc(snapshot.RequirementIssues, func(requirement RequirementIssue) bool {
		return requirement.LabelTimesRead &&
			slices.ContainsFunc(requirement.SubIssues, func(s SubIssue) bool { return s.Number == sub.Number })
	})
	pr, ok := sub.LatestPullRequest()
	return read && ok && !sub.AwaitingOwnerReviewAt.IsZero() && slices.ContainsFunc(pr.Reviews, func(review Review) bool {
		return !isBot(review.Author) && review.State == ReviewChangesRequested &&
			review.Commit == pr.HeadCommit && review.SubmittedAt.After(sub.AwaitingOwnerReviewAt)
	})
}

// ownerReviewCandidates returns, lowest issue number first, the open
// sub-issues in cumin/status/awaiting-owner-review, not running now, whose
// open pull request has a review of a person with the state on its head
// commit. Each one names every person whose review decides.
func ownerReviewCandidates(snapshot Snapshot, state ReviewState) []FixOwnerReview {
	var candidates []FixOwnerReview
	for _, requirement := range snapshot.RequirementIssues {
		for _, sub := range requirement.SubIssues {
			if sub.Closed || !slices.Contains(sub.Labels, LabelAwaitingOwnerReview) || snapshot.Running[sub.Number] {
				continue
			}
			pr, ok := sub.LatestPullRequest()
			if !ok {
				continue
			}
			candidate := false
			var reviewers []string
			for _, review := range pr.Reviews {
				if review.Author == "" || isBot(review.Author) || !decides(review.State) {
					continue
				}
				if !slices.Contains(reviewers, review.Author) {
					reviewers = append(reviewers, review.Author)
				}
				if review.State == state && review.Commit == pr.HeadCommit {
					candidate = true
				}
			}
			if candidate {
				slices.Sort(reviewers)
				candidates = append(candidates, FixOwnerReview{Number: sub.Number, PullRequest: pr.Number, Reviewers: reviewers})
			}
		}
	}
	slices.SortFunc(candidates, func(a, b FixOwnerReview) int { return a.Number - b.Number })
	return candidates
}

// OwnerApproved applies the check of I12: of the reviews of the Owners
// (owners holds their logins), the latest one that decides is APPROVED on
// the head commit. An approval on an older commit does not count, and a
// later CHANGES_REQUESTED of an Owner takes the approval back. A review of
// a bot never counts, whatever owners says.
func OwnerApproved(reviews []Review, head string, owners map[string]bool) bool {
	latest, found := latestOwnerReview(reviews, owners)
	return found && latest.State == ReviewApproved && head != "" && latest.Commit == head
}

// OwnerRequestedChanges applies the check of I13: of the reviews of the
// Owners (owners holds their logins), the latest one that decides is
// CHANGES_REQUESTED on the head commit, submitted after awaitingOwnerAt:
// the time that cumin/status/awaiting-owner-review was last added to the
// issue. It returns that review, whose address the request names. A request
// for changes on an older commit does not count, and a later APPROVED of an
// Owner takes it back. A comment-only review decides nothing, and a review
// of a bot never counts. A review that is not newer than the label sent the
// pull request back already, or came before the Owner was asked, so one
// review sends the pull request back once. A zero awaitingOwnerAt means
// that the time of the label is not known, and no review counts.
func OwnerRequestedChanges(reviews []Review, head string, owners map[string]bool, awaitingOwnerAt time.Time) (Review, bool) {
	latest, found := latestOwnerReview(reviews, owners)
	if !found || latest.State != ReviewChangesRequested || head == "" || latest.Commit != head ||
		awaitingOwnerAt.IsZero() || !latest.SubmittedAt.After(awaitingOwnerAt) {
		return Review{}, false
	}
	return latest, true
}

// latestOwnerReview returns the latest review that decides among the
// reviews of the Owners. A bot is never an Owner, whatever owners says.
func latestOwnerReview(reviews []Review, owners map[string]bool) (Review, bool) {
	var latest Review
	found := false
	for _, review := range reviews {
		if !owners[review.Author] || isBot(review.Author) || !decides(review.State) {
			continue
		}
		if !found || !review.SubmittedAt.Before(latest.SubmittedAt) {
			latest, found = review, true
		}
	}
	return latest, found
}

// IsOwner applies the definition of the Owner (cumin-core.md): a person,
// not a bot, with write or admin permission on the repository. GitHub
// reports maintain as write ("Get repository permissions for a user").
func IsOwner(permission, userType string) bool {
	return userType == "User" && (permission == "admin" || permission == "write")
}
