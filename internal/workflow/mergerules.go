package workflow

// This file is pure, like domain.go: the merge. It holds the decision on an
// approved pull request (DecideMerge), the steps in cumin/status/merging
// (MergeEnd), the conditions of the merge (MergeConditionsHold), and the
// rules on the review of a Maintainer ("start the merge", "send back for
// changes").

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// HasMaintainerApprovalCandidate reports whether a sub-issue is a candidate of
// "start the merge" after the approval of a Maintainer, so that the poll
// reads the required checks for it.
func (s Snapshot) HasMaintainerApprovalCandidate() bool { return len(maintainerApprovals(s)) > 0 }

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
	risks := riskLabels(labels)
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

// riskLabels returns the risk/* labels of an issue, in their order. An issue
// in a correct state has exactly one.
func riskLabels(labels []string) []string {
	var risks []string
	for _, label := range labels {
		if strings.HasPrefix(label, riskLabelPrefix) {
			risks = append(risks, label)
		}
	}
	return risks
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
