package workflow

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// The App that reports the checks of GitHub Actions on the sandbox.
const actionsApp = 15368

// TestChecksOf covers the text on required checks of issue-states.md: what
// the checks of a commit say together, for I3 (every check passed, to
// reviewing) and I4 (a check failed, fix request).
func TestChecksOf(t *testing.T) {
	tests := []struct {
		name     string
		required []RequiredCheck
		results  []CheckResult
		want     ChecksState
		failed   []string
	}{
		{
			name: "no required check passes at once, whatever the results say",
			results: []CheckResult{
				{Name: "optional", Conclusion: CheckFailed},
			},
			want: ChecksPassed,
		},
		{
			name:     "every required check passed",
			required: []RequiredCheck{{Name: "ci"}, {Name: "lint"}},
			results: []CheckResult{
				{Name: "ci", Conclusion: CheckPassed},
				{Name: "lint", Conclusion: CheckPassed},
				{Name: "other", Conclusion: CheckFailed},
			},
			want: ChecksPassed,
		},
		{
			name:     "skipped and neutral are passed (row 51)",
			required: []RequiredCheck{{Name: "protected-paths"}, {Name: "flaky"}},
			results: []CheckResult{
				{Name: "protected-paths", Conclusion: CheckPassed},
				{Name: "flaky", Conclusion: CheckPassed},
			},
			want: ChecksPassed,
		},
		{
			name:     "a required check that has not reported keeps the wait",
			required: []RequiredCheck{{Name: "ci"}, {Name: "slow"}},
			results:  []CheckResult{{Name: "ci", Conclusion: CheckPassed}},
			want:     ChecksWaiting,
		},
		{
			name:     "a required check that has not finished keeps the wait",
			required: []RequiredCheck{{Name: "ci"}},
			results:  []CheckResult{{Name: "ci", Conclusion: CheckPending}},
			want:     ChecksWaiting,
		},
		{
			name:     "a failed required check decides, even with another one still running",
			required: []RequiredCheck{{Name: "ci"}, {Name: "slow"}},
			results: []CheckResult{
				{Name: "ci", Conclusion: CheckFailed},
				{Name: "slow", Conclusion: CheckPending},
			},
			want:   ChecksFailed,
			failed: []string{"ci"},
		},
		{
			name:     "a failed check that no rule requires decides nothing",
			required: []RequiredCheck{{Name: "ci"}},
			results: []CheckResult{
				{Name: "ci", Conclusion: CheckPassed},
				{Name: "optional", Conclusion: CheckFailed},
			},
			want: ChecksPassed,
		},
		{
			name:     "a rule that names an App is not met by another App",
			required: []RequiredCheck{{Name: "protected-paths", Integration: actionsApp}},
			results:  []CheckResult{{Name: "protected-paths", Conclusion: CheckPassed, Integration: 999}},
			want:     ChecksWaiting,
		},
		{
			name:     "a rule that names an App is met by that App",
			required: []RequiredCheck{{Name: "protected-paths", Integration: actionsApp}},
			results: []CheckResult{
				{Name: "protected-paths", Conclusion: CheckPassed, Integration: actionsApp},
				{Name: "protected-paths", Conclusion: CheckFailed, Integration: 999},
			},
			want: ChecksPassed,
		},
		{
			name:     "a rule without an App is met by a commit status",
			required: []RequiredCheck{{Name: "deploy"}},
			results:  []CheckResult{{Name: "deploy", Conclusion: CheckPassed}},
			want:     ChecksPassed,
		},
		{
			name:     "two results of one required check: a failure decides",
			required: []RequiredCheck{{Name: "ci"}},
			results: []CheckResult{
				{Name: "ci", Conclusion: CheckPassed, Integration: actionsApp},
				{Name: "ci", Conclusion: CheckFailed, Integration: 999},
			},
			want:   ChecksFailed,
			failed: []string{"ci"},
		},
		{
			name:     "no result at all keeps the wait",
			required: []RequiredCheck{{Name: "ci"}},
			want:     ChecksWaiting,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ChecksOf(tt.required, tt.results); got != tt.want {
				t.Errorf("ChecksOf = %s, want %s", got, tt.want)
			}
			var names []string
			for _, check := range FailedChecks(tt.required, tt.results) {
				names = append(names, check.Name)
			}
			if got := names; !slices.Equal(got, tt.failed) {
				t.Errorf("FailedChecks = %v, want %v", got, tt.failed)
			}
		})
	}
}

// TestDecide_I3 covers which issues the poll moves to the review.
func TestDecide_I3(t *testing.T) {
	required := []RequiredCheck{{Name: "ci"}}
	passed := []CheckResult{{Name: "ci", Conclusion: CheckPassed}}
	pending := []CheckResult{{Name: "ci", Conclusion: CheckPending}}
	failed := []CheckResult{{Name: "ci", Conclusion: CheckFailed}}

	sub := func(number int, labels []string, checks []CheckResult) SubIssue {
		return SubIssue{
			Number:       number,
			Labels:       labels,
			PullRequests: []PullRequest{{Number: number + 10, Checks: checks}},
		}
	}
	tests := []struct {
		name     string
		required []RequiredCheck
		subs     []SubIssue
		want     []Action
	}{
		{
			name:     "a green pull request moves to the review",
			required: required,
			subs:     []SubIssue{sub(10, []string{LabelAwaitingChecks, "risk/low"}, passed)},
			want:     []Action{StartReview{Number: 10, PullRequest: 20}},
		},
		{
			name: "no required check moves it at once",
			subs: []SubIssue{sub(10, []string{LabelAwaitingChecks}, nil)},
			want: []Action{StartReview{Number: 10, PullRequest: 20}},
		},
		{
			name:     "a check that has not finished waits",
			required: required,
			subs:     []SubIssue{sub(10, []string{LabelAwaitingChecks}, pending)},
		},
		{
			name:     "a failed check waits for I4, not for I3",
			required: required,
			subs:     []SubIssue{sub(10, []string{LabelAwaitingChecks}, failed)},
		},
		{
			name:     "another status label is not I3",
			required: required,
			subs:     []SubIssue{sub(10, []string{LabelImplementing}, passed)},
		},
		{
			name:     "a closed issue is not I3",
			required: required,
			subs:     []SubIssue{{Number: 10, Closed: true, Labels: []string{LabelAwaitingChecks}, PullRequests: []PullRequest{{Number: 20, Checks: passed}}}},
		},
		{
			name:     "no open pull request waits",
			required: required,
			subs:     []SubIssue{{Number: 10, Labels: []string{LabelAwaitingChecks}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := Snapshot{RequirementIssues: []RequirementIssue{{Number: 6, SubIssues: tt.subs}}}
			// The limit of issues in progress does not hold I3 back: the
			// issue is already counted in it.
			// I4 and I11 are other rules (TestDecide_I4, TestDecide_I11).
			var got []Action
			for _, action := range Decide(snapshot, 1, tt.required, nil, time.Time{}, 0) {
				if _, ok := action.(StartReview); ok {
					got = append(got, action)
				}
			}
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("Decide = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSnapshot_HasIssueAwaitingChecks: the poll reads the required checks
// only when an issue waits for them.
func TestSnapshot_HasIssueAwaitingChecks(t *testing.T) {
	waiting := Snapshot{RequirementIssues: []RequirementIssue{{Number: 6, SubIssues: []SubIssue{
		{Number: 10, Labels: []string{LabelImplementing}},
		{Number: 11, Labels: []string{LabelAwaitingChecks}},
	}}}}
	if !waiting.HasIssueAwaitingChecks() {
		t.Error("an issue in cumin/status/awaiting-checks was not found")
	}
	closed := Snapshot{RequirementIssues: []RequirementIssue{{Number: 6, SubIssues: []SubIssue{
		{Number: 10, Closed: true, Labels: []string{LabelAwaitingChecks}},
	}}}}
	if closed.HasIssueAwaitingChecks() {
		t.Error("a closed issue must not ask for the required checks")
	}
}

// TestDecide_I4 covers which issues the poll sends back for a check fix,
// and which failed checks the action names.
func TestDecide_I4(t *testing.T) {
	required := []RequiredCheck{{Name: "ci"}, {Name: "lint", Integration: 15368}}
	waiting := func(number int, checks ...CheckResult) SubIssue {
		return SubIssue{Number: number, Labels: []string{LabelAwaitingChecks, "risk/low"},
			PullRequests: []PullRequest{{Number: number + 10, Checks: checks}}}
	}
	ci := func(c CheckConclusion) CheckResult { return CheckResult{Name: "ci", Conclusion: c} }
	lint := func(c CheckConclusion) CheckResult {
		return CheckResult{Name: "lint", Conclusion: c, Integration: 15368}
	}

	tests := []struct {
		name string
		subs []SubIssue
		want []Action
	}{
		{
			name: "a failed check gives one fix with the failed check",
			subs: []SubIssue{waiting(10, ci(CheckFailed), lint(CheckPassed))},
			want: []Action{FixChecks{Number: 10, PullRequest: 20, Failed: []RequiredCheck{{Name: "ci"}}}},
		},
		{
			name: "a failure next to a check that has not finished is fixed at once",
			subs: []SubIssue{waiting(10, ci(CheckPending), lint(CheckFailed))},
			want: []Action{FixChecks{Number: 10, PullRequest: 20, Failed: []RequiredCheck{{Name: "lint", Integration: 15368}}}},
		},
		{
			name: "every failed check is named, in the order of the rules",
			subs: []SubIssue{waiting(10, lint(CheckFailed), ci(CheckFailed))},
			want: []Action{FixChecks{Number: 10, PullRequest: 20, Failed: required}},
		},
		{
			name: "passed and waiting checks give no fix",
			subs: []SubIssue{waiting(10, ci(CheckPassed), lint(CheckPassed)), waiting(11, ci(CheckPending))},
		},
		{
			name: "a failure of another App is not the required check",
			subs: []SubIssue{waiting(10, ci(CheckPassed), CheckResult{Name: "lint", Conclusion: CheckFailed, Integration: 99})},
		},
		{
			name: "another status label is not I4",
			subs: []SubIssue{{Number: 10, Labels: []string{LabelImplementing}, PullRequests: []PullRequest{{Number: 20, Checks: []CheckResult{ci(CheckFailed)}}}}},
		},
		{
			name: "lowest issue number first",
			subs: []SubIssue{waiting(12, ci(CheckFailed)), waiting(10, ci(CheckFailed))},
			want: []Action{
				FixChecks{Number: 10, PullRequest: 20, Failed: []RequiredCheck{{Name: "ci"}}},
				FixChecks{Number: 12, PullRequest: 22, Failed: []RequiredCheck{{Name: "ci"}}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := Snapshot{RequirementIssues: []RequirementIssue{{Number: 6, SubIssues: tt.subs}}}
			var got []Action
			for _, action := range Decide(snapshot, 1, required, nil, time.Time{}, 0) {
				if _, ok := action.(FixChecks); ok {
					got = append(got, action)
				}
			}
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("Decide = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDecide_I14 covers which issues the poll sends back for a conflict
// resolution: only a pull request that GitHub reports as CONFLICTING, and
// before the rows of the checks (I3, I4) for that issue.
func TestDecide_I14(t *testing.T) {
	required := []RequiredCheck{{Name: "ci"}}
	ci := func(c CheckConclusion) []CheckResult { return []CheckResult{{Name: "ci", Conclusion: c}} }
	waiting := func(number int, mergeable MergeableState, checks []CheckResult) SubIssue {
		return SubIssue{Number: number, Labels: []string{LabelAwaitingChecks, "risk/low"},
			PullRequests: []PullRequest{{Number: number + 10, Mergeable: mergeable, Checks: checks}}}
	}

	tests := []struct {
		name string
		subs []SubIssue
		want []Action
	}{
		{
			name: "a conflicting pull request gives one conflict resolution",
			subs: []SubIssue{waiting(10, Conflicting, nil)},
			want: []Action{ResolveConflict{Number: 10, PullRequest: 20}},
		},
		{
			name: "unknown gives nothing: GitHub is still calculating",
			subs: []SubIssue{waiting(10, MergeableUnknown, nil)},
		},
		{
			name: "mergeable gives nothing",
			subs: []SubIssue{waiting(10, Mergeable, nil)},
		},
		{
			name: "a conflict comes before the passed checks",
			subs: []SubIssue{waiting(10, Conflicting, ci(CheckPassed))},
			want: []Action{ResolveConflict{Number: 10, PullRequest: 20}},
		},
		{
			name: "a conflict comes before the failed check",
			subs: []SubIssue{waiting(10, Conflicting, ci(CheckFailed))},
			want: []Action{ResolveConflict{Number: 10, PullRequest: 20}},
		},
		{
			name: "unknown leaves the checks to decide",
			subs: []SubIssue{waiting(10, MergeableUnknown, ci(CheckPassed)), waiting(11, MergeableUnknown, ci(CheckFailed))},
			want: []Action{
				StartReview{Number: 10, PullRequest: 20},
				FixChecks{Number: 11, PullRequest: 21, Failed: required},
			},
		},
		{
			name: "another status label is not I14",
			subs: []SubIssue{{Number: 10, Labels: []string{LabelReviewing}, PullRequests: []PullRequest{{Number: 20, Mergeable: Conflicting}}}},
		},
		{
			name: "a closed issue is not I14",
			subs: []SubIssue{{Number: 10, Closed: true, Labels: []string{LabelAwaitingChecks}, PullRequests: []PullRequest{{Number: 20, Mergeable: Conflicting}}}},
		},
		{
			name: "lowest issue number first",
			subs: []SubIssue{waiting(12, Conflicting, nil), waiting(10, Conflicting, nil)},
			want: []Action{
				ResolveConflict{Number: 10, PullRequest: 20},
				ResolveConflict{Number: 12, PullRequest: 22},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := Snapshot{RequirementIssues: []RequirementIssue{{Number: 6, SubIssues: tt.subs}}}
			// I11 is another rule (TestDecide_I11).
			var got []Action
			for _, action := range Decide(snapshot, 1, required, nil, time.Time{}, 0) {
				switch action.(type) {
				case ResolveConflict, StartReview, FixChecks:
					got = append(got, action)
				}
			}
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("Decide = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDecide_I15 covers which issues the poll stops for the Owner because a
// required check did not report in time: only after the wait time, counted
// from the later one of the label time and the commit time of the head
// commit, and only after the rows of the conflict (I14) and of the checks
// (I3, I4).
func TestDecide_I15(t *testing.T) {
	required := []RequiredCheck{{Name: "ci"}, {Name: "lint"}, {Name: "unit"}}
	labeled := time.Date(2026, 10, 3, 1, 0, 0, 0, time.UTC)
	wait := time.Hour
	results := func(ci, lint CheckConclusion) []CheckResult {
		return []CheckResult{{Name: "ci", Conclusion: ci}, {Name: "lint", Conclusion: lint}, {Name: "unit", Conclusion: CheckPassed}}
	}
	waiting := func(number int, mergeable MergeableState, checks []CheckResult) SubIssue {
		return SubIssue{Number: number, Labels: []string{LabelAwaitingChecks, "risk/low"}, AwaitingChecksAt: labeled,
			PullRequests: []PullRequest{{Number: number + 10, HeadCommit: "abc", HeadCommittedAt: labeled.Add(-time.Minute), Mergeable: mergeable, Checks: checks}}}
	}
	pushed := func(sub SubIssue, at time.Time) SubIssue {
		sub.PullRequests[0].HeadCommittedAt = at
		return sub
	}
	stopped := func(number int, waited time.Duration, unreported ...RequiredCheck) StopForUnreportedChecks {
		return StopForUnreportedChecks{Number: number, PullRequest: number + 10, HeadCommit: "abc", Unreported: unreported, Waited: waited}
	}
	ci, lint := RequiredCheck{Name: "ci"}, RequiredCheck{Name: "lint"}

	tests := []struct {
		name string
		subs []SubIssue
		now  time.Time
		want []Action
	}{
		{
			name: "before the wait time is over, nothing",
			subs: []SubIssue{waiting(10, Mergeable, nil)},
			now:  labeled.Add(wait - time.Second),
		},
		{
			name: "at the wait time, a check without a result stops the issue",
			subs: []SubIssue{waiting(10, Mergeable, []CheckResult{{Name: "unit", Conclusion: CheckPassed}})},
			now:  labeled.Add(wait),
			want: []Action{stopped(10, wait, ci, lint)},
		},
		{
			name: "a check that has not finished has not reported",
			subs: []SubIssue{waiting(10, Mergeable, results(CheckPassed, CheckPending))},
			now:  labeled.Add(90 * time.Minute),
			want: []Action{stopped(10, 90*time.Minute, lint)},
		},
		{
			name: "unknown mergeability does not hold the stop back",
			subs: []SubIssue{waiting(10, MergeableUnknown, results(CheckPending, CheckPassed))},
			now:  labeled.Add(2 * wait),
			want: []Action{stopped(10, 2*wait, ci)},
		},
		{
			name: "checks that passed go to the review, however late",
			subs: []SubIssue{waiting(10, Mergeable, results(CheckPassed, CheckPassed))},
			now:  labeled.Add(2 * wait),
			want: []Action{StartReview{Number: 10, PullRequest: 20}},
		},
		{
			name: "a failed check goes to the check fix, even with another one not reported",
			subs: []SubIssue{waiting(10, Mergeable, []CheckResult{{Name: "ci", Conclusion: CheckFailed}})},
			now:  labeled.Add(2 * wait),
			want: []Action{FixChecks{Number: 10, PullRequest: 20, Failed: []RequiredCheck{ci}}},
		},
		{
			name: "a conflict comes first",
			subs: []SubIssue{waiting(10, Conflicting, nil)},
			now:  labeled.Add(2 * wait),
			want: []Action{ResolveConflict{Number: 10, PullRequest: 20}},
		},
		{
			name: "a newer head commit starts the wait time again",
			subs: []SubIssue{pushed(waiting(10, Mergeable, results(CheckPending, CheckPassed)), labeled.Add(30*time.Minute))},
			now:  labeled.Add(wait + 29*time.Minute),
		},
		{
			name: "the wait time after a newer head commit is counted from its commit time",
			subs: []SubIssue{pushed(waiting(10, Mergeable, results(CheckPending, CheckPassed)), labeled.Add(30*time.Minute))},
			now:  labeled.Add(wait + 30*time.Minute),
			want: []Action{stopped(10, wait, ci)},
		},
		{
			name: "a head commit older than the label counts from the label",
			subs: []SubIssue{pushed(waiting(10, Mergeable, results(CheckPending, CheckPassed)), labeled.Add(-3*time.Hour))},
			now:  labeled.Add(wait - time.Minute),
		},
		{
			name: "a label time that was not read gives nothing",
			subs: []SubIssue{{Number: 10, Labels: []string{LabelAwaitingChecks},
				PullRequests: []PullRequest{{Number: 20, HeadCommit: "abc", HeadCommittedAt: labeled}}}},
			now: labeled.Add(2 * wait),
		},
		{
			name: "a head commit time that was not read gives nothing",
			subs: []SubIssue{pushed(waiting(10, Mergeable, results(CheckPending, CheckPassed)), time.Time{})},
			now:  labeled.Add(2 * wait),
		},
		{
			name: "another status label is not I15",
			subs: []SubIssue{{Number: 10, Labels: []string{LabelReviewing}, AwaitingChecksAt: labeled,
				PullRequests: []PullRequest{{Number: 20, HeadCommit: "abc", HeadCommittedAt: labeled}}}},
			now: labeled.Add(2 * wait),
		},
		{
			name: "a closed issue is not I15",
			subs: []SubIssue{{Number: 10, Closed: true, Labels: []string{LabelAwaitingChecks}, AwaitingChecksAt: labeled,
				PullRequests: []PullRequest{{Number: 20, HeadCommit: "abc", HeadCommittedAt: labeled}}}},
			now: labeled.Add(2 * wait),
		},
		{
			name: "lowest issue number first",
			subs: []SubIssue{waiting(12, Mergeable, results(CheckPending, CheckPassed)), waiting(10, Mergeable, results(CheckPending, CheckPassed))},
			now:  labeled.Add(wait),
			want: []Action{stopped(10, wait, ci), stopped(12, wait, ci)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := Snapshot{RequirementIssues: []RequirementIssue{{Number: 6, LabelTimesRead: true, SubIssues: tt.subs}}}
			decide := func() []Action {
				// I11 is another rule (TestDecide_I11).
				var got []Action
				for _, action := range Decide(snapshot, 1, required, nil, tt.now, wait) {
					switch action.(type) {
					case ResolveConflict, StartReview, FixChecks, StopForUnreportedChecks:
						got = append(got, action)
					}
				}
				return got
			}
			got := decide()
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("Decide = %v, want %v", got, tt.want)
			}
			// The same snapshot, time, and setting give the same actions.
			if again := decide(); fmt.Sprint(again) != fmt.Sprint(got) {
				t.Errorf("Decide again = %v, want %v", again, got)
			}
		})
	}
}

// The sentence of I15 names the head commit, each required check that has
// not reported, and the time that cumin waited.
func TestUnreportedChecksReason_I15(t *testing.T) {
	got := UnreportedChecksReason(StopForUnreportedChecks{
		Number: 10, PullRequest: 21, HeadCommit: "0123abcd",
		Unreported: []RequiredCheck{{Name: "ci"}, {Name: "lint"}}, Waited: 61*time.Minute + 400*time.Millisecond,
	})
	for _, want := range []string{"(ci, lint)", "head commit 0123abcd", "pull request #21", "waited 1h1m0s", "checks_wait_time"} {
		if !strings.Contains(got, want) {
			t.Errorf("the reason has no %q: %s", want, got)
		}
	}
}

// CheckFixAllowed: max_check_fix_requests requests are sent, not one more.
func TestCheckFixAllowed_I4(t *testing.T) {
	for _, tt := range []struct {
		count, limit int
		want         bool
	}{{0, 3, true}, {2, 3, true}, {3, 3, false}, {4, 3, false}, {0, 1, true}, {1, 1, false}} {
		if got := CheckFixAllowed(tt.count, tt.limit); got != tt.want {
			t.Errorf("CheckFixAllowed(%d, %d) = %v, want %v", tt.count, tt.limit, got, tt.want)
		}
	}
}
