package workflow

// This file is pure, like domain.go: the required checks. It holds what the
// required checks say on a commit (ChecksOf, FailedChecks,
// UnreportedChecks), and the decisions on an issue that waits for the
// checks: "request the review", "request a check fix", "stop for missing
// checks", and "request a conflict resolution".

import (
	"fmt"
	"slices"
	"time"
)

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

func openChecking(sub SubIssue) bool {
	return !sub.Closed && slices.Contains(sub.Labels, LabelChecking)
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
