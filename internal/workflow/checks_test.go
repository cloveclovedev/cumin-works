package workflow

import (
	"fmt"
	"slices"
	"testing"
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
			name:     "skipped and neutral are passed (rows 20 and 51)",
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
			if got := FailedChecks(tt.required, tt.results); !slices.Equal(got, tt.failed) {
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
			got := Decide(snapshot, 1, tt.required)
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
