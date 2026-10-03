package workflow

import (
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// I1 (issue-states.md): claim an open sub-issue with cumin/status/ready
// whose blocked-by issues are all closed, lowest number first, within the
// limit of issues in progress.
func TestDecide_I1(t *testing.T) {
	ready := func(number int, blockedBy ...BlockedBy) SubIssue {
		return SubIssue{Number: number, Labels: []string{LabelReady, "risk/low"}, BlockedBy: blockedBy}
	}
	withStatus := func(number int, status string) SubIssue {
		return SubIssue{Number: number, Labels: []string{status, "risk/low"}}
	}
	requirement := func(number int, labels []string, subs ...SubIssue) RequirementIssue {
		return RequirementIssue{Number: number, Labels: append([]string{LabelRequirement}, labels...), SubIssues: subs}
	}
	implementing := []string{LabelImplementing}

	tests := []struct {
		name          string
		snapshot      Snapshot
		maxInProgress int
		want          []Action
	}{
		{
			name:          "no ready sub-issue gives no action",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, implementing, withStatus(10, LabelAwaitingOwnerReview))}},
			maxInProgress: 1,
		},
		{
			name:          "one ready sub-issue gives one claim",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, implementing, ready(10))}},
			maxInProgress: 1,
			want:          []Action{Claim{Number: 10, RequirementIssue: 6}},
		},
		{
			name:          "two ready sub-issues with limit 1 give the lower number",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, implementing, ready(12), ready(10))}},
			maxInProgress: 1,
			want:          []Action{Claim{Number: 10, RequirementIssue: 6}},
		},
		{
			name:          "two ready sub-issues with limit 2 give both in ascending order",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, implementing, ready(12), ready(10))}},
			maxInProgress: 2,
			want:          []Action{Claim{Number: 10, RequirementIssue: 6}, Claim{Number: 12, RequirementIssue: 6}},
		},
		{
			name: "the order is by issue number across requirement issues",
			snapshot: Snapshot{RequirementIssues: []RequirementIssue{
				requirement(6, implementing, ready(20)),
				requirement(7, implementing, ready(11)),
			}},
			maxInProgress: 1,
			want:          []Action{Claim{Number: 11, RequirementIssue: 7}},
		},
		{
			name:          "a ready sub-issue with an open blocker is skipped and the next one is taken",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, implementing, ready(10, BlockedBy{Number: 9}), ready(11))}},
			maxInProgress: 1,
			want:          []Action{Claim{Number: 11, RequirementIssue: 6}},
		},
		{
			name:          "a ready sub-issue whose blockers are all closed is claimed",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, implementing, ready(10, BlockedBy{Number: 8, Closed: true}, BlockedBy{Number: 9, Closed: true}))}},
			maxInProgress: 1,
			want:          []Action{Claim{Number: 10, RequirementIssue: 6}},
		},
		{
			name:          "an implementing sub-issue fills limit 1",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, implementing, withStatus(10, LabelImplementing), ready(11))}},
			maxInProgress: 1,
		},
		{
			name:          "an awaiting-checks sub-issue fills limit 1",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, implementing, withStatus(10, LabelAwaitingChecks), ready(11))}},
			maxInProgress: 1,
		},
		{
			name:          "a reviewing sub-issue fills limit 1",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, implementing, withStatus(10, LabelReviewing), ready(11))}},
			maxInProgress: 1,
		},
		{
			name:          "a sub-issue that waits for the Owner does not fill the limit",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, implementing, withStatus(10, LabelAwaitingOwnerDecision), ready(11))}},
			maxInProgress: 1,
			want:          []Action{Claim{Number: 11, RequirementIssue: 6}},
		},
		{
			name:          "a closed implementing sub-issue does not fill the limit",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, implementing, SubIssue{Number: 10, Closed: true, Labels: []string{LabelImplementing}}, ready(11))}},
			maxInProgress: 1,
			want:          []Action{Claim{Number: 11, RequirementIssue: 6}},
		},
		{
			name: "a requirement issue in planning fills limit 1",
			snapshot: Snapshot{RequirementIssues: []RequirementIssue{
				requirement(6, implementing, ready(10)),
				requirement(7, []string{LabelPlanning}),
			}},
			maxInProgress: 1,
		},
		{
			name: "a requirement issue in implementing does not fill the limit",
			snapshot: Snapshot{RequirementIssues: []RequirementIssue{
				requirement(6, implementing, ready(10)),
				requirement(7, implementing),
			}},
			maxInProgress: 1,
			want:          []Action{Claim{Number: 10, RequirementIssue: 6}},
		},
		{
			name:          "a closed sub-issue with ready gives no claim",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, implementing, SubIssue{Number: 10, Closed: true, Labels: []string{LabelReady}})}},
			maxInProgress: 1,
		},
		{
			name:          "ready on a requirement issue gives a plan, not a claim",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, []string{LabelReady})}},
			maxInProgress: 1,
			want:          []Action{Plan{Number: 6}},
		},
		{
			name:          "an empty snapshot gives no action",
			snapshot:      Snapshot{},
			maxInProgress: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Decide(tt.snapshot, tt.maxInProgress, nil, nil, time.Time{}, 0)
			if !slices.Equal(got, tt.want) {
				t.Errorf("Decide = %+v, want %+v", got, tt.want)
			}
			// The same snapshot in another order gives the same actions.
			shuffled := shuffle(tt.snapshot)
			if again := Decide(shuffled, tt.maxInProgress, nil, nil, time.Time{}, 0); !slices.Equal(again, tt.want) {
				t.Errorf("Decide on the shuffled snapshot = %+v, want %+v", again, tt.want)
			}
		})
	}
}

// R1 (issue-states.md): plan an open requirement issue with
// cumin/status/ready whose blocked-by issues are all closed, with or
// without sub-issues. R1 and I1 share the limit of issues in progress.
func TestDecide_R1(t *testing.T) {
	requirement := func(number int, status string, blockedBy []BlockedBy, subs ...SubIssue) RequirementIssue {
		return RequirementIssue{Number: number, Labels: []string{LabelRequirement, status}, BlockedBy: blockedBy, SubIssues: subs}
	}
	readySub := SubIssue{Number: 10, Labels: []string{LabelReady, "risk/low"}}
	open := []BlockedBy{{Number: 3}}
	closed := []BlockedBy{{Number: 3, Closed: true}}

	tests := []struct {
		name          string
		snapshot      Snapshot
		maxInProgress int
		want          []Action
	}{
		{
			name:          "a ready requirement issue gives one plan",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, LabelReady, nil)}},
			maxInProgress: 1,
			want:          []Action{Plan{Number: 6}},
		},
		{
			name:          "a ready requirement issue with sub-issues gives a plan",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, LabelReady, nil, SubIssue{Number: 10, Closed: true})}},
			maxInProgress: 1,
			want:          []Action{Plan{Number: 6}},
		},
		{
			name:          "an open blocked-by issue gives no plan",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, LabelReady, open)}},
			maxInProgress: 1,
		},
		{
			name:          "a closed blocked-by issue gives a plan",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, LabelReady, closed)}},
			maxInProgress: 1,
			want:          []Action{Plan{Number: 6}},
		},
		{
			name:          "a requirement issue in planning gives no plan and fills the limit",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, LabelPlanning, nil), requirement(7, LabelReady, nil)}},
			maxInProgress: 1,
		},
		{
			name:          "a requirement issue in planning fills the limit for a sub-issue as well",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, LabelPlanning, nil), requirement(7, LabelImplementing, nil, readySub)}},
			maxInProgress: 1,
		},
		{
			name:          "a plan and a claim share the limit, lowest issue number first",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(12, LabelReady, nil), requirement(7, LabelImplementing, nil, readySub)}},
			maxInProgress: 1,
			want:          []Action{Claim{Number: 10, RequirementIssue: 7}},
		},
		{
			name:          "with room for two, the plan follows the claim",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(12, LabelReady, nil), requirement(7, LabelImplementing, nil, readySub)}},
			maxInProgress: 2,
			want:          []Action{Claim{Number: 10, RequirementIssue: 7}, Plan{Number: 12}},
		},
		{
			name:          "awaiting-owner-decision without ready gives no plan",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, LabelAwaitingOwnerDecision, nil)}},
			maxInProgress: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Decide(tt.snapshot, tt.maxInProgress, nil, nil, time.Time{}, 0)
			if !slices.Equal(got, tt.want) {
				t.Errorf("Decide = %+v, want %+v", got, tt.want)
			}
			if again := Decide(shuffle(tt.snapshot), tt.maxInProgress, nil, nil, time.Time{}, 0); !slices.Equal(again, tt.want) {
				t.Errorf("Decide on the shuffled snapshot = %+v, want %+v", again, tt.want)
			}
		})
	}
}

func TestLabelsAfterPlan_R1(t *testing.T) {
	got := LabelsAfterPlan([]string{LabelRequirement, LabelReady})
	if want := []string{LabelRequirement, LabelPlanning}; !slices.Equal(got, want) {
		t.Errorf("LabelsAfterPlan = %v, want %v", got, want)
	}
}

// R2 (issue-states.md): one or more sub-issues, each with exactly one risk
// label. The first failing sub-issue by number is named.
func TestVerifySplit_R2(t *testing.T) {
	sub := func(number int, labels ...string) SubIssue { return SubIssue{Number: number, Labels: labels} }
	tests := []struct {
		name string
		subs []SubIssue
		want SplitVerification
	}{
		{"no sub-issue", nil, SplitVerification{Failure: SplitNoSubIssue}},
		{"one risk label each", []SubIssue{sub(10, "risk/low"), sub(11, "risk/high", LabelReady)}, SplitVerification{Passed: true}},
		{"a closed sub-issue counts as well", []SubIssue{{Number: 10, Closed: true}}, SplitVerification{Failure: SplitNoRiskLabel, SubIssue: 10}},
		{"the lowest failing number is named", []SubIssue{sub(12), sub(11, "risk/low", "risk/medium")}, SplitVerification{Failure: SplitTwoRiskLabels, SubIssue: 11}},
		{"an owner task with risk/high passes", []SubIssue{sub(10, LabelOwnerTask, "risk/high")}, SplitVerification{Passed: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := VerifySplit(RequirementIssue{Number: 6, SubIssues: tt.subs}); got != tt.want {
				t.Errorf("VerifySplit = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// R3 and R6 (issue-states.md): a requirement issue follows its sub-issues.
func TestDecide_R3AndR6(t *testing.T) {
	t0 := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	sub := func(number int, labels ...string) SubIssue { return SubIssue{Number: number, Labels: labels} }
	readyAt := func(number int, at time.Time) SubIssue {
		return SubIssue{Number: number, Labels: []string{LabelReady, "risk/low"}, ReadyAt: at}
	}
	requirement := func(status string, subs ...SubIssue) RequirementIssue {
		labels := []string{LabelRequirement}
		if status != "" {
			labels = append(labels, status)
		}
		return RequirementIssue{Number: 6, Labels: labels, SubIssues: subs, LabelTimesRead: true, ReviewAt: t0}
	}
	closed := SubIssue{Number: 12, Closed: true, Labels: []string{"risk/low"}}

	tests := []struct {
		name        string
		requirement RequirementIssue
		want        []Action
	}{
		{"R3: ready added after the review label", requirement(LabelAwaitingOwnerReview, readyAt(10, t0.Add(time.Minute))), []Action{StartRequirement{Number: 6}}},
		{"R3: ready from before the review label waits", requirement(LabelAwaitingOwnerReview, readyAt(10, t0.Add(-time.Minute))), nil},
		{"R3: without the label times nothing moves", func() RequirementIssue {
			r := requirement(LabelAwaitingOwnerReview, readyAt(10, t0.Add(time.Minute)))
			r.LabelTimesRead = false
			return r
		}(), nil},
		{"R3: no status label and a ready sub-issue", requirement("", readyAt(10, time.Time{})), []Action{StartRequirement{Number: 6}}},
		{"R3: no status label and no ready sub-issue", requirement("", sub(10, "risk/low")), nil},
		{"R3: a closed sub-issue with ready does not count", requirement("", SubIssue{Number: 10, Closed: true, Labels: []string{LabelReady}}), nil},
		{"R6: only sub-issues without a status label are open", requirement(LabelImplementing, closed, sub(10, "risk/low")), []Action{ReviewRemaining{Number: 6}}},
		{"R6: an owner task left alone", requirement(LabelImplementing, closed, sub(10, LabelOwnerTask, "risk/high")), []Action{ReviewRemaining{Number: 6}}},
		{"R6: an open sub-issue with a status label", requirement(LabelImplementing, sub(10, "risk/low"), sub(11, LabelAwaitingChecks, "risk/low")), nil},
		{"R6: every sub-issue closed", requirement(LabelImplementing, closed), nil},
		{"R6: not in awaiting-owner-review", requirement(LabelAwaitingOwnerReview, sub(10, "risk/low")), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Decide(Snapshot{RequirementIssues: []RequirementIssue{tt.requirement}}, 0, nil, nil, time.Time{}, 0)
			if !slices.EqualFunc(got, tt.want, func(a, b Action) bool { return a == b }) {
				t.Errorf("Decide = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// Without the label times, R3 cannot be judged, and a claim would take
// away the cumin/status/ready that it needs: the sub-issues wait.
func TestDecide_ClaimsWaitForTheLabelTimes(t *testing.T) {
	requirement := RequirementIssue{Number: 6, Labels: []string{LabelRequirement, LabelAwaitingOwnerReview},
		SubIssues: []SubIssue{{Number: 10, Labels: []string{LabelReady, "risk/low"}}}}
	if got := Decide(Snapshot{RequirementIssues: []RequirementIssue{requirement}}, 1, nil, nil, time.Time{}, 0); len(got) != 0 {
		t.Errorf("Decide without the label times = %+v, want no action", got)
	}
	requirement.LabelTimesRead = true
	want := []Action{Claim{Number: 10, RequirementIssue: 6}}
	if got := Decide(Snapshot{RequirementIssues: []RequirementIssue{requirement}}, 1, nil, nil, time.Time{}, 0); !slices.EqualFunc(got, want, func(a, b Action) bool { return a == b }) {
		t.Errorf("Decide with the label times = %+v, want %+v", got, want)
	}
}

// R4 and R7 (issue-states.md): every sub-issue closed; an acceptance check
// comment after the last close moves the requirement issue to the Owner,
// otherwise the Planner is asked.
func TestDecide_R4AndR7(t *testing.T) {
	closedAt := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	requirement := func(checkAt time.Time, subs ...SubIssue) RequirementIssue {
		return RequirementIssue{Number: 6, Labels: []string{LabelRequirement, LabelImplementing}, SubIssues: subs,
			CommentsRead: true, AcceptanceCheckAt: checkAt, FollowUpsDone: true}
	}
	closed := SubIssue{Number: 10, Closed: true, ClosedAt: closedAt, Labels: []string{"risk/low"}}
	later := SubIssue{Number: 11, Closed: true, ClosedAt: closedAt.Add(2 * time.Hour), Labels: []string{"risk/low"}}

	tests := []struct {
		name     string
		snapshot Snapshot
		room     int
		want     []Action
	}{
		{"R4: all closed and no comment", Snapshot{RequirementIssues: []RequirementIssue{requirement(time.Time{}, closed)}}, 1, []Action{CheckAcceptance{Number: 6}}},
		{"R4: a comment from before the last close", Snapshot{RequirementIssues: []RequirementIssue{requirement(closedAt.Add(time.Hour), closed, later)}}, 1, []Action{CheckAcceptance{Number: 6}}},
		{"R4: no room", Snapshot{RequirementIssues: []RequirementIssue{requirement(time.Time{}, closed)}}, 0, nil},
		{"R4: the Planner of the requirement issue runs", Snapshot{RequirementIssues: []RequirementIssue{requirement(time.Time{}, closed)}, Running: map[int]bool{6: true}}, 2, nil},
		{"R4: no sub-issue", Snapshot{RequirementIssues: []RequirementIssue{requirement(time.Time{})}}, 1, nil},
		{"R4: an open sub-issue", Snapshot{RequirementIssues: []RequirementIssue{requirement(time.Time{}, closed, SubIssue{Number: 12, Labels: []string{LabelReviewing}})}}, 1, nil},
		{"R4: the comments were not read", Snapshot{RequirementIssues: []RequirementIssue{func() RequirementIssue {
			r := requirement(time.Time{}, closed)
			r.CommentsRead = false
			return r
		}()}}, 1, nil},
		{"R4: a follow-up note is still missing", Snapshot{RequirementIssues: []RequirementIssue{func() RequirementIssue {
			r := requirement(time.Time{}, closed)
			r.FollowUpsDone = false
			return r
		}()}}, 1, nil},
		{"R7: a missing follow-up note does not stop it", Snapshot{RequirementIssues: []RequirementIssue{func() RequirementIssue {
			r := requirement(closedAt.Add(3*time.Hour), closed, later)
			r.FollowUpsDone = false
			return r
		}()}}, 0, []Action{Accept{Number: 6}}},
		{"R7: a comment at the same second as the last close", Snapshot{RequirementIssues: []RequirementIssue{requirement(closedAt.Add(2*time.Hour), closed, later)}}, 0, []Action{Accept{Number: 6}}},
		{"R7: a comment after the last close", Snapshot{RequirementIssues: []RequirementIssue{requirement(closedAt.Add(3*time.Hour), closed, later)}}, 0, []Action{Accept{Number: 6}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Decide(tt.snapshot, tt.room, nil, nil, time.Time{}, 0)
			if !slices.EqualFunc(got, tt.want, func(a, b Action) bool { return a == b }) {
				t.Errorf("Decide = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// A running acceptance check fills the limit, although the label of the
// requirement issue does not show it.
func TestDecide_ARunningAcceptanceCheckFillsTheLimit(t *testing.T) {
	snapshot := Snapshot{
		RequirementIssues: []RequirementIssue{
			{Number: 6, Labels: []string{LabelRequirement, LabelImplementing}, SubIssues: []SubIssue{{Number: 10, Closed: true}}, CommentsRead: true},
			{Number: 7, Labels: []string{LabelRequirement, LabelImplementing}, SubIssues: []SubIssue{{Number: 11, Labels: []string{LabelReady, "risk/low"}}}},
		},
		Running: map[int]bool{6: true},
	}
	if got := Decide(snapshot, 1, nil, nil, time.Time{}, 0); len(got) != 0 {
		t.Errorf("Decide = %+v, want no action while the acceptance check runs", got)
	}
}

func TestAcceptanceCheckAt_R7(t *testing.T) {
	t0 := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	const planner = "example-planner[bot]"
	comments := []Comment{
		{Author: planner, CreatedAt: t0, Body: "## Acceptance check\n\n| Rule | Result |"},
		{Author: planner, CreatedAt: t0.Add(time.Hour), Body: "## Plan for approval\n"},
		{Author: "octocat", CreatedAt: t0.Add(2 * time.Hour), Body: "## Acceptance check\n"},
		{Author: planner, CreatedAt: t0.Add(3 * time.Hour), Body: "Quote:\n## Acceptance check\n"},
	}
	if got := AcceptanceCheckAt(comments, planner); !got.Equal(t0) {
		t.Errorf("AcceptanceCheckAt = %v, want %v: only the Planner, only the first line", got, t0)
	}
	if got := AcceptanceCheckAt(comments, ""); !got.IsZero() {
		t.Errorf("AcceptanceCheckAt without the Planner login = %v, want zero", got)
	}
}

// I1 never claims a sub-issue with cumin/type/owner-task. It has no status
// label of work in progress, so it takes no place under the limit.
func TestDecide_I1SkipsAnOwnerTask(t *testing.T) {
	ownerTask := SubIssue{Number: 10, Labels: []string{LabelOwnerTask, LabelReady, "risk/high"}}
	ready := SubIssue{Number: 11, Labels: []string{LabelReady, "risk/low"}}
	snapshot := Snapshot{RequirementIssues: []RequirementIssue{{Number: 6, Labels: []string{LabelRequirement, LabelImplementing}, SubIssues: []SubIssue{ownerTask, ready}}}}
	want := []Action{Claim{Number: 11, RequirementIssue: 6}}
	if got := Decide(snapshot, 1, nil, nil, time.Time{}, 0); !slices.EqualFunc(got, want, func(a, b Action) bool { return a == b }) {
		t.Errorf("Decide = %+v, want %+v", got, want)
	}

	// An issue that became an owner task while an agent works on it still
	// counts by its status label: an agent may run or start for it (I4).
	snapshot.RequirementIssues[0].SubIssues[0].Labels = []string{LabelOwnerTask, LabelAwaitingChecks, "risk/high"}
	if got := Decide(snapshot, 1, nil, nil, time.Time{}, 0); len(got) != 0 {
		t.Errorf("Decide with an owner task in awaiting-checks = %+v, want no claim", got)
	}
}

func TestNeedsLabelTimes(t *testing.T) {
	ready := SubIssue{Number: 10, Labels: []string{LabelReady}}
	tests := []struct {
		name string
		r    RequirementIssue
		want bool
	}{
		{"review with a ready sub-issue", RequirementIssue{Labels: []string{LabelAwaitingOwnerReview}, SubIssues: []SubIssue{ready}}, true},
		{"review without a ready sub-issue", RequirementIssue{Labels: []string{LabelAwaitingOwnerReview}, SubIssues: []SubIssue{{Number: 10}}}, false},
		{"implementing with a ready sub-issue", RequirementIssue{Labels: []string{LabelImplementing}, SubIssues: []SubIssue{ready}}, false},
		{"a sub-issue waits for its checks", RequirementIssue{Labels: []string{LabelImplementing}, SubIssues: []SubIssue{{Number: 10, Labels: []string{LabelAwaitingChecks}}}}, true},
		{"a closed sub-issue in awaiting-checks", RequirementIssue{Labels: []string{LabelImplementing}, SubIssues: []SubIssue{{Number: 10, Closed: true, Labels: []string{LabelAwaitingChecks}}}}, false},
	}
	for _, tt := range tests {
		if got := NeedsLabelTimes(tt.r); got != tt.want {
			t.Errorf("%s: NeedsLabelTimes = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// R2 (issue-states.md): after a pass, an open sub-issue sends the split to
// the Owner; every sub-issue closed sends the requirement issue back to
// implementing, where R4 asks for the acceptance check again.
func TestSplitStatus_R2(t *testing.T) {
	tests := []struct {
		name string
		subs []SubIssue
		want string
	}{
		{"one open sub-issue", []SubIssue{{Number: 10, Closed: true}, {Number: 11}}, LabelAwaitingOwnerReview},
		{"every sub-issue closed", []SubIssue{{Number: 10, Closed: true}, {Number: 11, Closed: true}}, LabelImplementing},
	}
	for _, tt := range tests {
		if got := SplitStatus(RequirementIssue{Number: 6, SubIssues: tt.subs}); got != tt.want {
			t.Errorf("%s: SplitStatus = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// shuffle returns a copy of the snapshot with the issues in a random order.
func shuffle(snapshot Snapshot) Snapshot {
	r := rand.New(rand.NewPCG(1, 2))
	copied := Snapshot{RequirementIssues: slices.Clone(snapshot.RequirementIssues)}
	r.Shuffle(len(copied.RequirementIssues), func(i, j int) {
		copied.RequirementIssues[i], copied.RequirementIssues[j] = copied.RequirementIssues[j], copied.RequirementIssues[i]
	})
	for i := range copied.RequirementIssues {
		subs := slices.Clone(copied.RequirementIssues[i].SubIssues)
		r.Shuffle(len(subs), func(a, b int) { subs[a], subs[b] = subs[b], subs[a] })
		copied.RequirementIssues[i].SubIssues = subs
	}
	return copied
}

func TestIsStatusLabel(t *testing.T) {
	for name, want := range map[string]bool{
		LabelReady: true, LabelImplementing: true, LabelAwaitingOwnerDecision: true,
		LabelRequirement: false, "risk/low": false, "status": false,
	} {
		if got := IsStatusLabel(name); got != want {
			t.Errorf("IsStatusLabel(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestLabelsAfterClaim(t *testing.T) {
	got := LabelsAfterClaim([]string{"cumin/status/awaiting-owner-decision", "risk/low", "cumin/status/ready", "question"})
	if want := []string{"risk/low", "question", LabelImplementing}; !slices.Equal(got, want) {
		t.Errorf("LabelsAfterClaim = %v, want %v", got, want)
	}
	if got := LabelsAfterClaim(nil); !slices.Equal(got, []string{LabelImplementing}) {
		t.Errorf("LabelsAfterClaim(nil) = %v", got)
	}
}

func TestReplaceStatusLabel(t *testing.T) {
	got := ReplaceStatusLabel([]string{"cumin/status/implementing", "risk/low", "question"}, LabelAwaitingChecks)
	if want := []string{"risk/low", "question", LabelAwaitingChecks}; !slices.Equal(got, want) {
		t.Errorf("ReplaceStatusLabel = %v, want %v", got, want)
	}
}

// I2 (issue-states.md): after done, an open pull request is on the branch
// that cumin chose, its author is the Implementer App, and the head of the
// worktree is pushed. The issue gets a closing link when it has none.
func TestVerifyDone_I2(t *testing.T) {
	const bot = "example-implementer[bot]"
	const head = "2222222222222222222222222222222222222222"
	const branch = "cumin/10-add-the-login-screen"
	pr := func(number int, headBranch, author, headCommit string) PullRequest {
		return PullRequest{Number: number, HeadBranch: headBranch, Author: author, HeadCommit: headCommit}
	}
	linked := []PullRequest{pr(21, branch, bot, head)}
	tests := []struct {
		name      string
		linked    []PullRequest
		onBranch  []PullRequest
		localHead string
		want      Verification
	}{
		{"a linked pull request of the bot at the pushed head passes with no new link", linked, []PullRequest{pr(21, branch, bot, head)}, head, Verification{Passed: true, PullRequest: 21}},
		{"a pull request without a link passes and gets a link", nil, []PullRequest{pr(21, branch, bot, head)}, head, Verification{Passed: true, PullRequest: 21, AddLink: true}},
		{"a link to another pull request does not count", []PullRequest{pr(19, "other", bot, head)}, []PullRequest{pr(21, branch, bot, head)}, head, Verification{Passed: true, PullRequest: 21, AddLink: true}},
		{"no pull request", nil, nil, head, Verification{Failure: FailureNoOpenPullRequest}},
		{"a pull request on another branch is not taken", linked, []PullRequest{pr(21, "cumin/10-other", bot, head)}, head, Verification{Failure: FailureNoOpenPullRequest}},
		{"another author", nil, []PullRequest{pr(21, branch, "octocat", head)}, head, Verification{Failure: FailureAuthorMismatch, PullRequest: 21}},
		{"an author without an account never matches", nil, []PullRequest{pr(21, branch, "", head)}, head, Verification{Failure: FailureAuthorMismatch, PullRequest: 21}},
		{"the pull request of the bot is taken before a newer one of another author", nil, []PullRequest{pr(21, branch, bot, head), pr(25, branch, "octocat", head)}, head, Verification{Passed: true, PullRequest: 21, AddLink: true}},
		{"two pull requests of the bot: the highest number is checked", nil, []PullRequest{pr(21, branch, bot, head), pr(25, branch, bot, "4444444444444444444444444444444444444444")}, head, Verification{Failure: FailureHeadNotPushed, PullRequest: 25}},
		{"a local commit that is not pushed", nil, []PullRequest{pr(21, branch, bot, head)}, "3333333333333333333333333333333333333333", Verification{Failure: FailureHeadNotPushed, PullRequest: 21}},
		{"a link that would be over the limit is not added", []PullRequest{pr(18, "a", bot, head), pr(19, "b", bot, head)}, []PullRequest{pr(21, branch, bot, head)}, head, Verification{Failure: FailureTooManyLinks, PullRequest: 21}},
		{"a linked pull request passes at the limit", []PullRequest{pr(19, "b", bot, head), pr(21, branch, bot, head)}, []PullRequest{pr(21, branch, bot, head)}, head, Verification{Passed: true, PullRequest: 21}},
		{"an unknown local head never matches", nil, []PullRequest{pr(21, branch, bot, head)}, "", Verification{Failure: FailureHeadNotPushed, PullRequest: 21}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := VerifyDone(SubIssue{Number: 10, PullRequests: tt.linked}, branch, tt.onBranch, bot, tt.localHead, 2)
			if got != tt.want {
				t.Errorf("VerifyDone = %+v, want %+v", got, tt.want)
			}
		})
	}
	if got := VerifyDone(SubIssue{}, branch, []PullRequest{pr(21, branch, "", head)}, "", head, 2); got.Passed {
		t.Error("an empty implementer login matched an empty author")
	}
	if got := VerifyDone(SubIssue{}, "", []PullRequest{pr(21, "", bot, head)}, bot, head, 2); got.Passed {
		t.Error("an empty branch matched a pull request without a branch")
	}
	for _, f := range []VerificationFailure{FailureNone, FailureNoOpenPullRequest, FailureAuthorMismatch, FailureHeadNotPushed, FailureTooManyLinks} {
		if s := f.String(); s == "" || strings.HasPrefix(s, "VerificationFailure(") {
			t.Errorf("%d has no name", int(f))
		}
	}
}

// I11 (issue-states.md): the cumin/status/* and risk/* labels of an open
// pull request that closes a sub-issue become those of the issue. Its other
// labels stay, and equal labels give no action.
func TestDecide_I11(t *testing.T) {
	snapshotOf := func(issue []string, prs ...PullRequest) Snapshot {
		return Snapshot{RequirementIssues: []RequirementIssue{{
			Number: 6, Labels: []string{LabelRequirement, LabelImplementing},
			SubIssues: []SubIssue{{Number: 10, Labels: issue, PullRequests: prs}},
		}}}
	}
	checks := []string{LabelAwaitingChecks, "risk/medium"}

	tests := []struct {
		name     string
		snapshot Snapshot
		want     []Action
	}{
		{
			name:     "a pull request without labels gets the status and the risk",
			snapshot: snapshotOf(checks, PullRequest{Number: 21}),
			want:     []Action{CopyLabels{Issue: 10, PullRequest: 21, Labels: []string{LabelAwaitingChecks, "risk/medium"}}},
		},
		{
			name:     "equal labels in another order give no action",
			snapshot: snapshotOf(checks, PullRequest{Number: 21, Labels: []string{"risk/medium", "docs", LabelAwaitingChecks}}),
		},
		{
			name:     "an old status and an old risk are replaced, and other labels stay",
			snapshot: snapshotOf(checks, PullRequest{Number: 21, Labels: []string{"docs", LabelImplementing, "risk/low"}}),
			want:     []Action{CopyLabels{Issue: 10, PullRequest: 21, Labels: []string{"docs", LabelAwaitingChecks, "risk/medium"}}},
		},
		{
			name:     "a status that the Owner added to the pull request is removed",
			snapshot: snapshotOf(checks, PullRequest{Number: 21, Labels: []string{LabelAwaitingChecks, LabelReady, "risk/medium"}}),
			want:     []Action{CopyLabels{Issue: 10, PullRequest: 21, Labels: []string{LabelAwaitingChecks, "risk/medium"}}},
		},
		{
			name:     "an issue without a risk label removes the risk of the pull request",
			snapshot: snapshotOf([]string{LabelAwaitingChecks}, PullRequest{Number: 21, Labels: []string{LabelAwaitingChecks, "risk/low"}}),
			want:     []Action{CopyLabels{Issue: 10, PullRequest: 21, Labels: []string{LabelAwaitingChecks}}},
		},
		{
			name:     "labels of the issue that are not copied stay off the pull request",
			snapshot: snapshotOf([]string{LabelOwnerTask, LabelReady, "risk/low"}, PullRequest{Number: 21, Labels: []string{LabelReady, "risk/low"}}),
		},
		{
			name: "each open pull request that closes the issue is made equal",
			snapshot: snapshotOf(checks,
				PullRequest{Number: 21, Labels: []string{LabelAwaitingChecks, "risk/medium"}},
				PullRequest{Number: 22}),
			want: []Action{CopyLabels{Issue: 10, PullRequest: 22, Labels: []string{LabelAwaitingChecks, "risk/medium"}}},
		},
		{
			name: "a pull request that closes two issues follows the lower number, in any order",
			snapshot: Snapshot{RequirementIssues: []RequirementIssue{{
				Number: 6, Labels: []string{LabelRequirement, LabelImplementing},
				SubIssues: []SubIssue{
					{Number: 11, Labels: []string{LabelReviewing, "risk/low"}, PullRequests: []PullRequest{{Number: 21}}},
					{Number: 10, Labels: checks, PullRequests: []PullRequest{{Number: 21}}},
				},
			}}},
			want: []Action{CopyLabels{Issue: 10, PullRequest: 21, Labels: []string{LabelAwaitingChecks, "risk/medium"}}},
		},
		{
			name: "the actions are in the order of the pull request numbers",
			snapshot: Snapshot{RequirementIssues: []RequirementIssue{{
				Number: 6, Labels: []string{LabelRequirement, LabelImplementing},
				SubIssues: []SubIssue{
					{Number: 10, Labels: checks, PullRequests: []PullRequest{{Number: 23}}},
					{Number: 11, Labels: checks, PullRequests: []PullRequest{{Number: 22}}},
				},
			}}},
			want: []Action{
				CopyLabels{Issue: 11, PullRequest: 22, Labels: []string{LabelAwaitingChecks, "risk/medium"}},
				CopyLabels{Issue: 10, PullRequest: 23, Labels: []string{LabelAwaitingChecks, "risk/medium"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []Action
			for _, action := range Decide(tt.snapshot, 1, nil, nil, time.Time{}, 0) {
				if _, ok := action.(CopyLabels); ok {
					got = append(got, action)
				}
			}
			if !slices.EqualFunc(got, tt.want, func(a, b Action) bool {
				ca, okA := a.(CopyLabels)
				cb, okB := b.(CopyLabels)
				return okA && okB && ca.Issue == cb.Issue && ca.PullRequest == cb.PullRequest && slices.Equal(ca.Labels, cb.Labels)
			}) {
				t.Errorf("Decide = %#v, want %#v", got, tt.want)
			}
		})
	}
}

// The labels of a pull request decide nothing (issue-states.md, principle 5):
// a pull request with cumin/status/ready does not make its issue claimable,
// and a pull request without it does not stop a ready issue.
func TestDecide_TheLabelsOfAPullRequestDecideNothing(t *testing.T) {
	sub := func(labels []string, prLabels []string) Snapshot {
		return Snapshot{RequirementIssues: []RequirementIssue{{
			Number: 6, Labels: []string{LabelRequirement, LabelImplementing},
			SubIssues: []SubIssue{{Number: 10, Labels: labels, PullRequests: []PullRequest{{Number: 21, Labels: prLabels}}}},
		}}}
	}
	claims := func(actions []Action) int {
		n := 0
		for _, action := range actions {
			if _, ok := action.(Claim); ok {
				n++
			}
		}
		return n
	}
	if n := claims(Decide(sub([]string{LabelAwaitingOwnerReview, "risk/low"}, []string{LabelReady, "risk/low"}), 1, nil, nil, time.Time{}, 0)); n != 0 {
		t.Errorf("%d claims for a ready pull request of an issue in review, want 0", n)
	}
	if n := claims(Decide(sub([]string{LabelReady, "risk/low"}, []string{LabelAwaitingOwnerDecision}), 1, nil, nil, time.Time{}, 0)); n != 1 {
		t.Errorf("%d claims for a ready issue, want 1", n)
	}
}

// While cumin stops after its runs, the actions that ask an agent for new work are held
// back, and the ones that need no agent stay, in their order.
func TestWithoutNewWork_KeepsOnlyTheActionsThatNeedNoAgent(t *testing.T) {
	t.Parallel()
	all := []Action{
		StartRequirement{Number: 1},
		ReviewRemaining{Number: 2},
		Accept{Number: 3},
		StartReview{Number: 10, PullRequest: 20},
		FixChecks{Number: 11, PullRequest: 21},
		ResolveConflict{Number: 15, PullRequest: 25},
		StopForUnreportedChecks{Number: 16, PullRequest: 26},
		Plan{Number: 4},
		CheckAcceptance{Number: 5},
		Claim{Number: 12, RequirementIssue: 1},
		MergeOwnerApproval{Number: 13, PullRequest: 23},
		FixOwnerReview{Number: 14, PullRequest: 24},
		CopyLabels{Issue: 10, PullRequest: 20},
	}
	want := []Action{
		StartRequirement{Number: 1},
		ReviewRemaining{Number: 2},
		Accept{Number: 3},
		StopForUnreportedChecks{Number: 16, PullRequest: 26},
		MergeOwnerApproval{Number: 13, PullRequest: 23},
		CopyLabels{Issue: 10, PullRequest: 20},
	}
	if got := WithoutNewWork(all); !reflect.DeepEqual(got, want) {
		t.Errorf("WithoutNewWork = %#v, want %#v", got, want)
	}
	if got := WithoutNewWork(nil); len(got) != 0 {
		t.Errorf("WithoutNewWork(nil) = %#v, want none", got)
	}
}

// Q4 (issue-states.md, the table under Q4): which states mean that cumin
// moves an issue on without the Owner. One case for each row of the table.
func TestSnapshot_MovesWithoutOwner(t *testing.T) {
	sub := func(number int, labels ...string) SubIssue { return SubIssue{Number: number, Labels: labels} }
	requirement := func(status string, subs ...SubIssue) Snapshot {
		labels := []string{LabelRequirement}
		if status != "" {
			labels = append(labels, status)
		}
		return Snapshot{RequirementIssues: []RequirementIssue{{Number: 6, Labels: labels, SubIssues: subs}}}
	}
	open, closed := []BlockedBy{{Number: 9}}, []BlockedBy{{Number: 9, Closed: true}}
	blocked := func(by []BlockedBy) SubIssue {
		return SubIssue{Number: 10, Labels: []string{LabelReady}, BlockedBy: by}
	}

	tests := []struct {
		name     string
		snapshot Snapshot
		want     bool
	}{
		{"no issue", Snapshot{}, false},
		{"awaiting-checks counts", requirement(LabelImplementing, sub(10, LabelAwaitingChecks)), true},
		{"a closed issue in awaiting-checks does not count", requirement(LabelImplementing,
			SubIssue{Number: 10, Closed: true, Labels: []string{LabelAwaitingChecks}}), false},
		{"a ready sub-issue that waits for room under the limit counts", requirement(LabelImplementing,
			sub(10, LabelImplementing), sub(11, LabelReady)), true},
		{"a ready sub-issue whose blocked-by issues are closed counts", requirement(LabelImplementing, blocked(closed)), true},
		{"a ready requirement issue counts", requirement(LabelReady), true},
		{"a ready sub-issue with an open blocked-by issue does not count", requirement(LabelImplementing, blocked(open)), false},
		{"a ready requirement issue with an open blocked-by issue does not count", Snapshot{RequirementIssues: []RequirementIssue{
			{Number: 6, Labels: []string{LabelRequirement, LabelReady}, BlockedBy: open}}}, false},
		{"planning without an agent does not count", requirement(LabelPlanning), false},
		{"implementing without an agent does not count", requirement(LabelImplementing, sub(10, LabelImplementing)), false},
		{"reviewing without an agent does not count", requirement(LabelImplementing, sub(10, LabelReviewing)), false},
		{"awaiting-owner-review does not count", requirement(LabelAwaitingOwnerReview, sub(10, LabelAwaitingOwnerReview)), false},
		{"awaiting-owner-decision does not count", requirement(LabelImplementing, sub(10, LabelAwaitingOwnerDecision)), false},
		{"a ready owner task does not count", requirement(LabelImplementing, sub(10, LabelOwnerTask, LabelReady)), false},
		{"an issue with no status label does not count", requirement("", sub(10, "risk/low")), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.snapshot.MovesWithoutOwner(); got != tt.want {
				t.Errorf("MovesWithoutOwner = %v, want %v", got, tt.want)
			}
		})
	}
}

// The send-back after a request for changes of the Owner (I13) is decided
// from the snapshot alone: an open sub-issue in
// cumin/status/awaiting-owner-review, not running, with a CHANGES_REQUESTED
// review of a person on the head commit. Who of the reviewers is an Owner is
// decided later (OwnerRequestedChanges).
func TestDecide_ARequestForChangesOfAPersonOnTheHeadIsACandidateOfTheSendBack(t *testing.T) {
	t.Parallel()
	const head, old = "2222222222222222222222222222222222222222", "1111111111111111111111111111111111111111"
	review := func(author string, state ReviewState, commit string) Review {
		return Review{Author: author, State: state, Commit: commit}
	}
	for _, tc := range []struct {
		name    string
		labels  []string
		closed  bool
		running bool
		reviews []Review
		want    []Action
	}{
		{name: "a request for changes of a person on the head", labels: []string{LabelAwaitingOwnerReview},
			reviews: []Review{review("owner", ReviewChangesRequested, head), review("other", ReviewApproved, old)},
			want:    []Action{FixOwnerReview{Number: 10, PullRequest: 21, Reviewers: []string{"other", "owner"}}}},
		{name: "a request for changes on an older commit", labels: []string{LabelAwaitingOwnerReview},
			reviews: []Review{review("owner", ReviewChangesRequested, old)}},
		{name: "a request for changes of a bot", labels: []string{LabelAwaitingOwnerReview},
			reviews: []Review{review("app[bot]", ReviewChangesRequested, head)}},
		{name: "a comment-only review", labels: []string{LabelAwaitingOwnerReview},
			reviews: []Review{review("owner", ReviewCommented, head)}},
		{name: "a pull request in another state", labels: []string{LabelReviewing},
			reviews: []Review{review("owner", ReviewChangesRequested, head)}},
		{name: "a closed issue", labels: []string{LabelAwaitingOwnerReview}, closed: true,
			reviews: []Review{review("owner", ReviewChangesRequested, head)}},
		{name: "an issue that runs now", labels: []string{LabelAwaitingOwnerReview}, running: true,
			reviews: []Review{review("owner", ReviewChangesRequested, head)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := Snapshot{
				RequirementIssues: []RequirementIssue{{
					Number: 6, Labels: []string{LabelRequirement, LabelImplementing},
					SubIssues: []SubIssue{{Number: 10, Closed: tc.closed, Labels: tc.labels,
						PullRequests: []PullRequest{{Number: 21, HeadCommit: head, Labels: tc.labels, Reviews: tc.reviews}}}},
				}},
				Running: map[int]bool{10: tc.running},
			}
			if got := Decide(snapshot, 1, nil, nil, time.Time{}, 0); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Decide = %#v, want %#v", got, tc.want)
			}
		})
	}
}
