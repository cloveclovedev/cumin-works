package workflow

// This file is pure, like domain.go: the review. It holds the rounds of the
// review, the result of a Reviewer run (CheckReview), the way out of
// cumin/status/reviewing (ReviewEnd), and the explanation of the cause.

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

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
//   - No open pull request closes the issue: stop the review for a
//     Maintainer. The reason is the risk label when the issue has not
//     exactly one, and the pull request that is no longer open otherwise.
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
// closed issue, while the facts were not read, and while the status label
// does not count: the next poll decides.
func ReviewEnd(sub SubIssue, running bool) Action {
	facts := sub.Reviewing
	if !ReviewNeedsFacts(sub, running) || facts == nil || !facts.StatusCounts || facts.ReviewingAt.IsZero() {
		return nil
	}
	// Without an open pull request, the number is 0: the stop has none.
	pr, open := sub.LatestPullRequest()
	if !facts.QuestionAt.IsZero() && !facts.QuestionAt.Before(facts.ReviewingAt) {
		return StopReview{Number: sub.Number, Question: true, Action: ActionStopTheReview, PullRequest: pr.Number}
	}
	if !open {
		reason := NoOpenPullRequestReason
		if risks := len(riskLabels(sub.Labels)); risks == 0 {
			reason = RiskLabelReason(MergeNoRiskLabel)
		} else if risks > 1 {
			reason = RiskLabelReason(MergeTwoRiskLabels)
		}
		return StopReview{Number: sub.Number, Action: ActionStopTheReview, Reason: reason}
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
		if c.Author != reviewer || c.CreatedAt.Before(since) || !strings.HasPrefix(firstTextLine(c.Body, "\r\n"), DecisionRequestHeading) {
			continue
		}
		if !ok || !c.CreatedAt.Before(found.CreatedAt) {
			found, ok = c, true
		}
	}
	return found, ok
}

// firstLine is the text up to the first line break. It trims nothing: each
// caller trims the text before, and the line after, as its rule says.
func firstLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}

// firstTextLine is the first line of the text, without the white space
// around the line. It first trims the characters of the cutset from the start
// of the text, so that a text that starts with them still gives its first
// line with text: each caller passes the cutset that its rule says.
func firstTextLine(text, cutset string) string {
	return strings.TrimSpace(firstLine(strings.TrimLeft(text, cutset)))
}

// stopOfReviewerRequest is the action that stops the review after a request
// to the Reviewer that failed: "stop at the round limit" for the request of
// the cause, which only exists at the limit of rounds, and "stop the
// review" for a review request.
func stopOfReviewerRequest(req reviewerRequest) ActionName {
	if req.cause != nil {
		return ActionStopAtTheRoundLimit
	}
	return ActionStopTheReview
}
