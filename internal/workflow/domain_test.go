package workflow

import (
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// "request the implementation": claim an open sub-issue with cumin/status/ready
// whose blocked-by issues are all closed, lowest number first, within the
// limit of issues in progress.
func TestDecide_RequestTheImplementation(t *testing.T) {
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
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, implementing, withStatus(10, LabelAwaitingMergeDecision))}},
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
			name:          "an checking sub-issue fills limit 1",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, implementing, withStatus(10, LabelChecking), ready(11))}},
			maxInProgress: 1,
		},
		{
			name:          "a reviewing sub-issue fills limit 1",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, implementing, withStatus(10, LabelReviewing), ready(11))}},
			maxInProgress: 1,
		},
		{
			name:          "a sub-issue that waits for the Maintainer does not fill the limit",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, implementing, withStatus(10, LabelAwaitingDecision), ready(11))}},
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
			name: "a requirement issue in accepting fills limit 1",
			snapshot: Snapshot{RequirementIssues: []RequirementIssue{
				requirement(6, implementing, ready(10)),
				requirement(7, []string{LabelAccepting}),
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
			got := decideReadyOfMaintainer(tt.snapshot, tt.maxInProgress, nil, nil, time.Time{}, 0)
			if !slices.Equal(got, tt.want) {
				t.Errorf("Decide = %+v, want %+v", got, tt.want)
			}
			// The same snapshot in another order gives the same actions.
			shuffled := shuffle(tt.snapshot)
			if again := decideReadyOfMaintainer(shuffled, tt.maxInProgress, nil, nil, time.Time{}, 0); !slices.Equal(again, tt.want) {
				t.Errorf("Decide on the shuffled snapshot = %+v, want %+v", again, tt.want)
			}
		})
	}
}

// "request the split": plan an open requirement issue with
// cumin/status/ready whose blocked-by issues are all closed, with or
// without sub-issues. "request the split" and "request the implementation"
// share the limit of issues in progress.
func TestDecide_RequestTheSplit(t *testing.T) {
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
			name:          "awaiting-decision without ready gives no plan",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, LabelAwaitingDecision, nil)}},
			maxInProgress: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decideReadyOfMaintainer(tt.snapshot, tt.maxInProgress, nil, nil, time.Time{}, 0)
			if !slices.Equal(got, tt.want) {
				t.Errorf("Decide = %+v, want %+v", got, tt.want)
			}
			if again := decideReadyOfMaintainer(shuffle(tt.snapshot), tt.maxInProgress, nil, nil, time.Time{}, 0); !slices.Equal(again, tt.want) {
				t.Errorf("Decide on the shuffled snapshot = %+v, want %+v", again, tt.want)
			}
		})
	}
}

// The check of the split: one or more sub-issues, each with exactly one risk
// label. The first failing sub-issue by number is named.
func TestVerifySplit(t *testing.T) {
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

// "mark the requirement as in work" and "ask about the remaining sub-issues":
// a requirement issue follows its sub-issues.
func TestDecide_MarkTheRequirementAsInWorkAndAskAboutTheRemainingSubIssues(t *testing.T) {
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
		{"mark the requirement as in work: ready added after the review label", requirement(LabelAwaitingPlanReview, readyAt(10, t0.Add(time.Minute))), []Action{StartRequirement{Number: 6}}},
		{"mark the requirement as in work: ready from before the review label waits", requirement(LabelAwaitingPlanReview, readyAt(10, t0.Add(-time.Minute))), nil},
		{"mark the requirement as in work: ready added after the acceptance label", requirement(LabelAwaitingAcceptance, closed, readyAt(10, t0.Add(time.Minute))), []Action{StartRequirement{Number: 6}}},
		{"mark the requirement as in work: ready from before the acceptance label waits", requirement(LabelAwaitingAcceptance, closed, readyAt(10, t0.Add(-time.Minute))), nil},
		{"mark the requirement as in work: the label of an implementation issue does not move a requirement issue", requirement(LabelAwaitingMergeDecision, readyAt(10, t0.Add(time.Minute))), nil},
		{"mark the requirement as in work: without the label times nothing moves", func() RequirementIssue {
			r := requirement(LabelAwaitingPlanReview, readyAt(10, t0.Add(time.Minute)))
			r.LabelTimesRead = false
			return r
		}(), nil},
		{"mark the requirement as in work: no status label and a ready sub-issue", requirement("", readyAt(10, time.Time{})), []Action{StartRequirement{Number: 6}}},
		{"mark the requirement as in work: no status label and no ready sub-issue", requirement("", sub(10, "risk/low")), nil},
		{"mark the requirement as in work: a closed sub-issue with ready does not count", requirement("", SubIssue{Number: 10, Closed: true, Labels: []string{LabelReady}}), nil},
		{"ask about the remaining sub-issues: only sub-issues without a status label are open", requirement(LabelImplementing, closed, sub(10, "risk/low")), []Action{ReviewRemaining{Number: 6}}},
		{"ask about the remaining sub-issues: an owner task left alone", requirement(LabelImplementing, closed, sub(10, LabelOwnerTask, "risk/high")), []Action{ReviewRemaining{Number: 6}}},
		{"ask about the remaining sub-issues: an open sub-issue with a status label", requirement(LabelImplementing, sub(10, "risk/low"), sub(11, LabelChecking, "risk/low")), nil},
		{"ask about the remaining sub-issues: every sub-issue closed", requirement(LabelImplementing, closed), nil},
		{"ask about the remaining sub-issues: not in awaiting-plan-review", requirement(LabelAwaitingPlanReview, sub(10, "risk/low")), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decideReadyOfMaintainer(Snapshot{RequirementIssues: []RequirementIssue{tt.requirement}}, 0, nil, nil, time.Time{}, 0)
			if !slices.EqualFunc(got, tt.want, func(a, b Action) bool { return a == b }) {
				t.Errorf("Decide = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// Without the label times, "mark the requirement as in work" cannot be
// judged, and a claim would take
// away the cumin/status/ready that it needs: the sub-issues wait.
func TestDecide_ClaimsWaitForTheLabelTimes(t *testing.T) {
	requirement := RequirementIssue{Number: 6, Labels: []string{LabelRequirement, LabelAwaitingPlanReview},
		SubIssues: []SubIssue{{Number: 10, Labels: []string{LabelReady, "risk/low"}}}}
	if got := decideReadyOfMaintainer(Snapshot{RequirementIssues: []RequirementIssue{requirement}}, 1, nil, nil, time.Time{}, 0); len(got) != 0 {
		t.Errorf("Decide without the label times = %+v, want no action", got)
	}
	requirement.LabelTimesRead = true
	want := []Action{Claim{Number: 10, RequirementIssue: 6}}
	if got := decideReadyOfMaintainer(Snapshot{RequirementIssues: []RequirementIssue{requirement}}, 1, nil, nil, time.Time{}, 0); !slices.EqualFunc(got, want, func(a, b Action) bool { return a == b }) {
		t.Errorf("Decide with the label times = %+v, want %+v", got, want)
	}
}

// "request the acceptance check" and "ask for the acceptance": every
// sub-issue closed; an acceptance check
// comment after the last close moves the requirement issue to the Maintainer,
// otherwise the Planner is asked.
func TestDecide_RequestTheAcceptanceCheckAndAskForTheAcceptance(t *testing.T) {
	closedAt := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	requirement := func(checkAt time.Time, subs ...SubIssue) RequirementIssue {
		return RequirementIssue{Number: 6, Labels: []string{LabelRequirement, LabelImplementing}, SubIssues: subs,
			CommentsRead: true, AcceptanceCheckAt: checkAt, FollowUpsDone: true}
	}
	closed := SubIssue{Number: 10, Closed: true, ClosedAt: closedAt, Labels: []string{"risk/low"}}
	later := SubIssue{Number: 11, Closed: true, ClosedAt: closedAt.Add(2 * time.Hour), Labels: []string{"risk/low"}}
	// accepting is the requirement issue after cumin moved it to
	// cumin/status/accepting: "ask for the acceptance" is decided only there.
	accepting := func(checkAt time.Time, subs ...SubIssue) RequirementIssue {
		r := requirement(checkAt, subs...)
		r.Labels = []string{LabelRequirement, LabelAccepting}
		r.StatusRead, r.StatusCounts = true, true
		return r
	}

	// planReview is the requirement issue in
	// cumin/status/awaiting-plan-review, the second starting state of
	// "request the acceptance check".
	planReview := func(checkAt time.Time, subs ...SubIssue) RequirementIssue {
		r := requirement(checkAt, subs...)
		r.Labels = []string{LabelRequirement, LabelAwaitingPlanReview}
		return r
	}

	tests := []struct {
		name     string
		snapshot Snapshot
		room     int
		want     []Action
	}{
		{"request the acceptance check from awaiting-plan-review: all closed and no comment", Snapshot{RequirementIssues: []RequirementIssue{planReview(time.Time{}, closed)}}, 1, []Action{CheckAcceptance{Number: 6}}},
		{"request the acceptance check from awaiting-plan-review: a comment from before the last close", Snapshot{RequirementIssues: []RequirementIssue{planReview(closedAt.Add(time.Hour), closed, later)}}, 1, []Action{CheckAcceptance{Number: 6}}},
		{"request the acceptance check from awaiting-plan-review: no room", Snapshot{RequirementIssues: []RequirementIssue{planReview(time.Time{}, closed)}}, 0, nil},
		{"request the acceptance check from awaiting-plan-review: no sub-issue", Snapshot{RequirementIssues: []RequirementIssue{planReview(time.Time{})}}, 1, nil},
		{"request the acceptance check from awaiting-plan-review: an open Owner task", Snapshot{RequirementIssues: []RequirementIssue{planReview(time.Time{}, closed, SubIssue{Number: 12, Labels: []string{LabelOwnerTask}})}}, 1, nil},
		{"request the acceptance check from awaiting-plan-review: the comments were not read", Snapshot{RequirementIssues: []RequirementIssue{func() RequirementIssue {
			r := planReview(time.Time{}, closed)
			r.CommentsRead = false
			return r
		}()}}, 1, nil},
		{"request the acceptance check from awaiting-plan-review: a follow-up note is still missing", Snapshot{RequirementIssues: []RequirementIssue{func() RequirementIssue {
			r := planReview(time.Time{}, closed)
			r.FollowUpsDone = false
			return r
		}()}}, 1, nil},
		{"in awaiting-plan-review, a comment after the last close stops the request", Snapshot{RequirementIssues: []RequirementIssue{planReview(closedAt.Add(3*time.Hour), closed, later)}}, 1, nil},
		{"in awaiting-acceptance, every sub-issue closed requests nothing", Snapshot{RequirementIssues: []RequirementIssue{func() RequirementIssue {
			r := requirement(time.Time{}, closed)
			r.Labels = []string{LabelRequirement, LabelAwaitingAcceptance}
			return r
		}()}}, 1, nil},
		{"request the acceptance check: all closed and no comment", Snapshot{RequirementIssues: []RequirementIssue{requirement(time.Time{}, closed)}}, 1, []Action{CheckAcceptance{Number: 6}}},
		{"request the acceptance check: a comment from before the last close", Snapshot{RequirementIssues: []RequirementIssue{requirement(closedAt.Add(time.Hour), closed, later)}}, 1, []Action{CheckAcceptance{Number: 6}}},
		{"request the acceptance check: no room", Snapshot{RequirementIssues: []RequirementIssue{requirement(time.Time{}, closed)}}, 0, nil},
		{"request the acceptance check: the Planner of the requirement issue runs", Snapshot{RequirementIssues: []RequirementIssue{requirement(time.Time{}, closed)}, Running: map[int]bool{6: true}}, 2, nil},
		{"request the acceptance check: no sub-issue", Snapshot{RequirementIssues: []RequirementIssue{requirement(time.Time{})}}, 1, nil},
		{"request the acceptance check: an open sub-issue", Snapshot{RequirementIssues: []RequirementIssue{requirement(time.Time{}, closed, SubIssue{Number: 12, Labels: []string{LabelReviewing}})}}, 1, nil},
		{"request the acceptance check: the comments were not read", Snapshot{RequirementIssues: []RequirementIssue{func() RequirementIssue {
			r := requirement(time.Time{}, closed)
			r.CommentsRead = false
			return r
		}()}}, 1, nil},
		{"request the acceptance check: a follow-up note is still missing", Snapshot{RequirementIssues: []RequirementIssue{func() RequirementIssue {
			r := requirement(time.Time{}, closed)
			r.FollowUpsDone = false
			return r
		}()}}, 1, nil},
		{"ask for the acceptance: a missing follow-up note does not stop it", Snapshot{RequirementIssues: []RequirementIssue{func() RequirementIssue {
			r := accepting(closedAt.Add(3*time.Hour), closed, later)
			r.FollowUpsDone = false
			return r
		}()}}, 0, []Action{Accept{Number: 6}}},
		{"ask for the acceptance: a comment at the same second as the last close", Snapshot{RequirementIssues: []RequirementIssue{accepting(closedAt.Add(2*time.Hour), closed, later)}}, 0, []Action{Accept{Number: 6}}},
		{"ask for the acceptance: a comment after the last close", Snapshot{RequirementIssues: []RequirementIssue{accepting(closedAt.Add(3*time.Hour), closed, later)}}, 0, []Action{Accept{Number: 6}}},
		{"in implementing, a comment after the last close does nothing: no rule requests a check there", Snapshot{RequirementIssues: []RequirementIssue{requirement(closedAt.Add(3*time.Hour), closed, later)}}, 1, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decideReadyOfMaintainer(tt.snapshot, tt.room, nil, nil, time.Time{}, 0)
			if !slices.EqualFunc(got, tt.want, func(a, b Action) bool { return a == b }) {
				t.Errorf("Decide = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// The limit counts only the working labels. Issues with cumin/status/ready
// beyond the limit do not lower the free slots, also when the running set
// still names one of them: with a limit of 2 and one issue in
// cumin/status/implementing, exactly one ready issue starts.
func TestDecide_ReadyIssuesBeyondTheLimitDoNotLowerTheFreeSlots(t *testing.T) {
	ready := func(number int) SubIssue { return SubIssue{Number: number, Labels: []string{LabelReady, "risk/low"}} }
	snapshot := Snapshot{
		RequirementIssues: []RequirementIssue{
			{Number: 6, Labels: []string{LabelRequirement, LabelImplementing}, SubIssues: []SubIssue{
				{Number: 10, Labels: []string{LabelImplementing, "risk/low"}}, ready(11), ready(12), ready(13),
			}},
			{Number: 7, Labels: []string{LabelRequirement, LabelReady}},
		},
		Running: map[int]bool{7: true, 10: true, 13: true},
	}
	starts := 0
	for _, action := range decideReadyOfMaintainer(snapshot, 2, nil, nil, time.Time{}, 0) {
		switch action.(type) {
		case Claim, Plan:
			starts++
		}
	}
	if starts != 1 {
		t.Errorf("%d starts, want 1: one slot of 2 is free, whatever the number of ready issues", starts)
	}
	if got := decideReadyOfMaintainer(snapshot, 1, nil, nil, time.Time{}, 0); len(got) != 0 {
		t.Errorf("Decide with a limit of 1 = %+v, want no action: the test would pass for a wrong reason", got)
	}
}

// A running agent is never touched: with a running agent in each working
// state, the poll decides no action for that issue, so it sends no request
// and changes no label.
func TestDecide_ARunningAgentInEachWorkingStateGetsNoAction(t *testing.T) {
	requirement := func(status string, subs ...SubIssue) RequirementIssue {
		return RequirementIssue{Number: 6, Labels: []string{LabelRequirement, status}, SubIssues: subs,
			CommentsRead: true, StatusRead: true, StatusCounts: true}
	}
	sub := func(status string) SubIssue {
		return SubIssue{Number: 10, Labels: []string{status, "risk/low"}}
	}
	tests := []struct {
		name        string
		requirement RequirementIssue
		running     int
	}{
		{LabelPlanning, requirement(LabelPlanning), 6},
		{LabelAccepting, requirement(LabelAccepting, SubIssue{Number: 10, Closed: true}), 6},
		{LabelImplementing, requirement(LabelImplementing, sub(LabelImplementing)), 10},
		{LabelReviewing, requirement(LabelImplementing, sub(LabelReviewing)), 10},
		{LabelMerging, requirement(LabelImplementing, sub(LabelMerging)), 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := Snapshot{RequirementIssues: []RequirementIssue{tt.requirement}, Running: map[int]bool{tt.running: true}}
			if got := Decide(snapshot, 5, nil, nil, time.Time{}, 0); len(got) != 0 {
				t.Errorf("Decide = %+v, want no action while the agent runs", got)
			}
		})
	}
}

func TestAcceptanceCheckAt(t *testing.T) {
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

// "request the implementation" never claims a sub-issue with
// cumin/type/owner-task. It has no status
// label of work in progress, so it takes no place under the limit.
func TestDecide_RequestTheImplementationSkipsAnOwnerTask(t *testing.T) {
	ownerTask := SubIssue{Number: 10, Labels: []string{LabelOwnerTask, LabelReady, "risk/high"}}
	ready := SubIssue{Number: 11, Labels: []string{LabelReady, "risk/low"}}
	snapshot := Snapshot{RequirementIssues: []RequirementIssue{{Number: 6, Labels: []string{LabelRequirement, LabelImplementing}, SubIssues: []SubIssue{ownerTask, ready}}}}
	want := []Action{Claim{Number: 11, RequirementIssue: 6}}
	if got := decideReadyOfMaintainer(snapshot, 1, nil, nil, time.Time{}, 0); !slices.EqualFunc(got, want, func(a, b Action) bool { return a == b }) {
		t.Errorf("Decide = %+v, want %+v", got, want)
	}

	// An issue that became an owner task while an agent works on it still
	// counts by its status label: an agent may run or start for it ("request a check fix").
	snapshot.RequirementIssues[0].SubIssues[0].Labels = []string{LabelOwnerTask, LabelChecking, "risk/high"}
	if got := decideReadyOfMaintainer(snapshot, 1, nil, nil, time.Time{}, 0); len(got) != 0 {
		t.Errorf("Decide with an owner task in checking = %+v, want no claim", got)
	}
}

func TestNeedsLabelTimes(t *testing.T) {
	ready := SubIssue{Number: 10, Labels: []string{LabelReady}}
	tests := []struct {
		name string
		r    RequirementIssue
		want bool
	}{
		{"review with a ready sub-issue", RequirementIssue{Labels: []string{LabelAwaitingPlanReview}, SubIssues: []SubIssue{ready}}, true},
		{"review without a ready sub-issue", RequirementIssue{Labels: []string{LabelAwaitingPlanReview}, SubIssues: []SubIssue{{Number: 10}}}, false},
		{"acceptance with a ready sub-issue", RequirementIssue{Labels: []string{LabelAwaitingAcceptance}, SubIssues: []SubIssue{ready}}, true},
		{"acceptance without a ready sub-issue", RequirementIssue{Labels: []string{LabelAwaitingAcceptance}, SubIssues: []SubIssue{{Number: 10}}}, false},
		{"implementing with a ready sub-issue", RequirementIssue{Labels: []string{LabelImplementing}, SubIssues: []SubIssue{ready}}, false},
		{"a sub-issue waits for its checks", RequirementIssue{Labels: []string{LabelImplementing}, SubIssues: []SubIssue{{Number: 10, Labels: []string{LabelChecking}}}}, true},
		{"a closed sub-issue in checking", RequirementIssue{Labels: []string{LabelImplementing}, SubIssues: []SubIssue{{Number: 10, Closed: true, Labels: []string{LabelChecking}}}}, false},
		{"a sub-issue that waits for the Maintainer has a request for changes of a person on the head", RequirementIssue{Labels: []string{LabelImplementing}, SubIssues: []SubIssue{{Number: 10, Labels: []string{LabelAwaitingMergeDecision},
			PullRequests: []PullRequest{{Number: 21, HeadCommit: "c2", Reviews: []Review{{Author: "owner", State: ReviewChangesRequested, Commit: "c2"}}}}}}}, true},
		{"a sub-issue that waits for the Maintainer has a request for changes on an older commit", RequirementIssue{Labels: []string{LabelImplementing}, SubIssues: []SubIssue{{Number: 10, Labels: []string{LabelAwaitingMergeDecision},
			PullRequests: []PullRequest{{Number: 21, HeadCommit: "c2", Reviews: []Review{{Author: "owner", State: ReviewChangesRequested, Commit: "c1"}}}}}}}, false},
		{"a sub-issue that waits for the Maintainer has an approval on the head", RequirementIssue{Labels: []string{LabelImplementing}, SubIssues: []SubIssue{{Number: 10, Labels: []string{LabelAwaitingMergeDecision},
			PullRequests: []PullRequest{{Number: 21, HeadCommit: "c2", Reviews: []Review{{Author: "owner", State: ReviewApproved, Commit: "c2"}}}}}}}, false},
	}
	for _, tt := range tests {
		if got := NeedsLabelTimes(tt.r); got != tt.want {
			t.Errorf("%s: NeedsLabelTimes = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// The end of the split: after a pass, an open sub-issue sends the split to
// the Maintainer; every sub-issue closed sends the requirement issue to the
// acceptance check.
func TestSplitStatus(t *testing.T) {
	tests := []struct {
		name string
		subs []SubIssue
		want string
	}{
		{"one open sub-issue", []SubIssue{{Number: 10, Closed: true}, {Number: 11}}, LabelAwaitingPlanReview},
		{"every sub-issue closed", []SubIssue{{Number: 10, Closed: true}, {Number: 11, Closed: true}}, LabelAccepting},
	}
	for _, tt := range tests {
		if got := SplitStatus(RequirementIssue{Number: 6, SubIssues: tt.subs}); got != tt.want {
			t.Errorf("%s: SplitStatus = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// The way out of cumin/status/planning (issue-states.md, the transitions
// of a requirement issue): the same facts always give the same action, at a
// poll and at the end of a Planner run.
func TestSplitEnd(t *testing.T) {
	labeledAt := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	planning := func(change func(*RequirementIssue)) RequirementIssue {
		requirement := RequirementIssue{
			Number: 6, Labels: []string{LabelRequirement, LabelPlanning},
			SubIssues:    []SubIssue{{Number: 10, Labels: []string{"risk/low"}}},
			CommentsRead: true, LabelTimesRead: true, ReviewAt: labeledAt,
		}
		if change != nil {
			change(&requirement)
		}
		return requirement
	}
	noSubIssue := func(r *RequirementIssue) { r.SubIssues = nil }
	tests := []struct {
		name        string
		requirement RequirementIssue
		running     bool
		want        Action
	}{
		{
			name:        "a verified split with an open sub-issue asks the Maintainer to review the plan",
			requirement: planning(nil),
			want:        ReviewPlan{Number: 6},
		},
		{
			name:        "a verified split with every sub-issue closed requests the acceptance check",
			requirement: planning(func(r *RequirementIssue) { r.SubIssues[0].Closed = true }),
			want:        CheckAcceptance{Number: 6},
		},
		{
			name:        "no sub-issue requests the split again",
			requirement: planning(noSubIssue),
			want:        Plan{Number: 6, Again: true},
		},
		{
			name: "no sub-issue after the second request stops for the Maintainer",
			requirement: planning(func(r *RequirementIssue) {
				noSubIssue(r)
				r.SplitRequestedAgain = true
			}),
			want: StopSplit{Number: 6, Reason: SplitReason(SplitVerification{Failure: SplitNoSubIssue})},
		},
		{
			name: "a sub-issue without a risk label after the second request stops for the Maintainer",
			requirement: planning(func(r *RequirementIssue) {
				r.SubIssues[0].Labels = nil
				r.SplitRequestedAgain = true
			}),
			want: StopSplit{Number: 6, Reason: SplitReason(SplitVerification{Failure: SplitNoRiskLabel, SubIssue: 10})},
		},
		{
			name: "a question after the label stops for the Maintainer at once",
			requirement: planning(func(r *RequirementIssue) {
				noSubIssue(r)
				r.QuestionAt = labeledAt.Add(time.Minute)
			}),
			want: StopSplit{Number: 6, Question: true},
		},
		{
			name:        "a question after the label decides before a verified split",
			requirement: planning(func(r *RequirementIssue) { r.QuestionAt = labeledAt.Add(time.Minute) }),
			want:        StopSplit{Number: 6, Question: true},
		},
		{
			name: "a question from before the label belongs to an earlier stay",
			requirement: planning(func(r *RequirementIssue) {
				noSubIssue(r)
				r.QuestionAt = labeledAt.Add(-time.Minute)
			}),
			want: Plan{Number: 6, Again: true},
		},
		{
			name:        "a running Planner gives no action",
			requirement: planning(nil),
			running:     true,
		},
		{
			name:        "comments that were not read give no action",
			requirement: planning(func(r *RequirementIssue) { r.CommentsRead = false }),
		},
		{
			name:        "label times that were not read give no action",
			requirement: planning(func(r *RequirementIssue) { r.LabelTimesRead = false }),
		},
		{
			name:        "another state gives no action",
			requirement: planning(func(r *RequirementIssue) { r.Labels = []string{LabelRequirement, LabelAwaitingPlanReview} }),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// cumin-core or a Maintainer added the status label.
			tt.requirement.StatusRead, tt.requirement.StatusCounts = true, true
			if got := SplitEnd(tt.requirement, tt.running); got != tt.want {
				t.Errorf("SplitEnd = %#v, want %#v", got, tt.want)
			}
			// A label of another account, and one whose account was not
			// read, decide nothing.
			for _, counts := range []struct{ read, counts bool }{{true, false}, {false, false}} {
				other := tt.requirement
				other.StatusRead, other.StatusCounts = counts.read, counts.counts
				if got := SplitEnd(other, tt.running); got != nil {
					t.Errorf("SplitEnd with StatusRead %v = %#v, want nil for a label that does not count", counts.read, got)
				}
			}
			// Decide gives the same action at a poll.
			snapshot := Snapshot{RequirementIssues: []RequirementIssue{tt.requirement}, Running: map[int]bool{6: tt.running}}
			var want []Action
			if tt.want != nil {
				want = []Action{tt.want}
			}
			if got := Decide(snapshot, 1, nil, nil, labeledAt, time.Hour); !slices.Equal(got, want) {
				t.Errorf("Decide = %#v, want %#v", got, want)
			}
		})
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
		LabelReady: true, LabelImplementing: true, LabelAwaitingDecision: true,
		LabelRequirement: false, "risk/low": false, "status": false,
	} {
		if got := IsStatusLabel(name); got != want {
			t.Errorf("IsStatusLabel(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestReplaceStatusLabel(t *testing.T) {
	tests := []struct {
		name   string
		labels []string
		status string
		want   []string
	}{
		{"the other labels stay", []string{"cumin/status/implementing", "risk/low", "question"}, LabelChecking, []string{"risk/low", "question", LabelChecking}},
		{"the type label stays", []string{LabelRequirement, LabelReady}, LabelPlanning, []string{LabelRequirement, LabelPlanning}},
		{"every old status label goes", []string{"cumin/status/awaiting-decision", "risk/low", "cumin/status/ready", "question"}, LabelImplementing, []string{"risk/low", "question", LabelImplementing}},
		{"no label", nil, LabelImplementing, []string{LabelImplementing}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ReplaceStatusLabel(tt.labels, tt.status); !slices.Equal(got, tt.want) {
				t.Errorf("ReplaceStatusLabel = %v, want %v", got, tt.want)
			}
		})
	}
}

// The check of the pull request: after done, an open pull request is on the branch
// that cumin chose, its author is the Implementer App, and the head of the
// worktree is pushed. The issue gets a closing link when it has none.
func TestVerifyDone(t *testing.T) {
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

// "copy the labels to the pull request": the cumin/status/* and risk/* labels of an open
// pull request that closes a sub-issue become those of the issue. Its other
// labels stay, and equal labels give no action.
func TestDecide_CopyTheLabelsToThePullRequest(t *testing.T) {
	snapshotOf := func(issue []string, prs ...PullRequest) Snapshot {
		return Snapshot{RequirementIssues: []RequirementIssue{{
			Number: 6, Labels: []string{LabelRequirement, LabelImplementing},
			SubIssues: []SubIssue{{Number: 10, Labels: issue, PullRequests: prs}},
		}}}
	}
	checks := []string{LabelChecking, "risk/medium"}

	tests := []struct {
		name     string
		snapshot Snapshot
		want     []Action
	}{
		{
			name:     "a pull request without labels gets the status and the risk",
			snapshot: snapshotOf(checks, PullRequest{Number: 21}),
			want:     []Action{CopyLabels{Issue: 10, PullRequest: 21, Labels: []string{LabelChecking, "risk/medium"}}},
		},
		{
			name:     "equal labels in another order give no action",
			snapshot: snapshotOf(checks, PullRequest{Number: 21, Labels: []string{"risk/medium", "docs", LabelChecking}}),
		},
		{
			name:     "an old status and an old risk are replaced, and other labels stay",
			snapshot: snapshotOf(checks, PullRequest{Number: 21, Labels: []string{"docs", LabelImplementing, "risk/low"}}),
			want:     []Action{CopyLabels{Issue: 10, PullRequest: 21, Labels: []string{"docs", LabelChecking, "risk/medium"}}},
		},
		{
			name:     "a status that the Maintainer added to the pull request is removed",
			snapshot: snapshotOf(checks, PullRequest{Number: 21, Labels: []string{LabelChecking, LabelReady, "risk/medium"}}),
			want:     []Action{CopyLabels{Issue: 10, PullRequest: 21, Labels: []string{LabelChecking, "risk/medium"}}},
		},
		{
			name:     "an issue without a risk label removes the risk of the pull request",
			snapshot: snapshotOf([]string{LabelChecking}, PullRequest{Number: 21, Labels: []string{LabelChecking, "risk/low"}}),
			want:     []Action{CopyLabels{Issue: 10, PullRequest: 21, Labels: []string{LabelChecking}}},
		},
		{
			name:     "labels of the issue that are not copied stay off the pull request",
			snapshot: snapshotOf([]string{LabelOwnerTask, LabelReady, "risk/low"}, PullRequest{Number: 21, Labels: []string{LabelReady, "risk/low"}}),
		},
		{
			name: "each open pull request that closes the issue is made equal",
			snapshot: snapshotOf(checks,
				PullRequest{Number: 21, Labels: []string{LabelChecking, "risk/medium"}},
				PullRequest{Number: 22}),
			want: []Action{CopyLabels{Issue: 10, PullRequest: 22, Labels: []string{LabelChecking, "risk/medium"}}},
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
			want: []Action{CopyLabels{Issue: 10, PullRequest: 21, Labels: []string{LabelChecking, "risk/medium"}}},
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
				CopyLabels{Issue: 11, PullRequest: 22, Labels: []string{LabelChecking, "risk/medium"}},
				CopyLabels{Issue: 10, PullRequest: 23, Labels: []string{LabelChecking, "risk/medium"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []Action
			for _, action := range decideReadyOfMaintainer(tt.snapshot, 1, nil, nil, time.Time{}, 0) {
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
	if n := claims(decideReadyOfMaintainer(sub([]string{LabelAwaitingMergeDecision, "risk/low"}, []string{LabelReady, "risk/low"}), 1, nil, nil, time.Time{}, 0)); n != 0 {
		t.Errorf("%d claims for a ready pull request of an issue in review, want 0", n)
	}
	if n := claims(decideReadyOfMaintainer(sub([]string{LabelReady, "risk/low"}, []string{LabelAwaitingDecision}), 1, nil, nil, time.Time{}, 0)); n != 1 {
		t.Errorf("%d claims for a ready issue, want 1", n)
	}
}

// The one check before a start of an agent: while cumin stops after the
// current runs, or while the quota usage stops agent starts, no start gets
// a permit; at any other time every start gets one. The zero value of the
// permit permits nothing.
func TestPermitStart_GivesNoPermitWhileCuminStopsAfterTheCurrentRunsOrAgentStartsAreStopped(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ stopsAfterRuns, quotaAllows bool }{{true, true}, {false, false}, {true, false}} {
		if permit, ok := PermitStart(c.stopsAfterRuns, c.quotaAllows); ok || permit != (StartPermit{}) {
			t.Errorf("PermitStart(%v, %v) = %#v, %v, want no permit", c.stopsAfterRuns, c.quotaAllows, permit, ok)
		}
	}
	permit, ok := PermitStart(false, true)
	if !ok || permit == (StartPermit{}) {
		t.Errorf("PermitStart(false, true) = %#v, %v, want a permit that holds", permit, ok)
	}
}

// "tell that cumin waits" (the table under "tell that cumin waits"): which
// states mean that cumin
// moves an issue on without the Maintainer. One case for each row of the table.
func TestSnapshot_MovesWithoutMaintainer(t *testing.T) {
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
		{"checking counts", requirement(LabelImplementing, sub(10, LabelChecking)), true},
		{"a closed issue in checking does not count", requirement(LabelImplementing,
			SubIssue{Number: 10, Closed: true, Labels: []string{LabelChecking}}), false},
		{"a ready sub-issue that waits for room under the limit counts", requirement(LabelImplementing,
			sub(10, LabelImplementing), sub(11, LabelReady)), true},
		{"a ready sub-issue whose blocked-by issues are closed counts", requirement(LabelImplementing, blocked(closed)), true},
		{"a ready requirement issue counts", requirement(LabelReady), true},
		{"a ready sub-issue with an open blocked-by issue does not count", requirement(LabelImplementing, blocked(open)), false},
		{"a ready requirement issue with an open blocked-by issue does not count", Snapshot{RequirementIssues: []RequirementIssue{
			{Number: 6, Labels: []string{LabelRequirement, LabelReady}, BlockedBy: open}}}, false},
		{"planning without an agent counts: the next poll decides its way out", requirement(LabelPlanning), true},
		{"accepting without an agent counts", requirement(LabelAccepting), true},
		{"implementing without an agent counts", requirement(LabelImplementing, sub(10, LabelImplementing)), true},
		{"reviewing without an agent counts", requirement(LabelImplementing, sub(10, LabelReviewing)), true},
		{"merging without an agent counts", requirement(LabelImplementing, sub(10, LabelMerging)), true},
		{"a closed issue in merging does not count", requirement(LabelImplementing,
			SubIssue{Number: 10, Closed: true, Labels: []string{LabelMerging}}), false},
		{"implementing of another account does not count", requirement(LabelImplementing,
			SubIssue{Number: 10, Labels: []string{LabelImplementing}, Implementing: &ImplementingFacts{}}), false},
		{"reviewing of another account does not count", requirement(LabelImplementing,
			SubIssue{Number: 10, Labels: []string{LabelReviewing}, Reviewing: &ReviewingFacts{}}), false},
		{"merging of another account does not count", requirement(LabelImplementing,
			SubIssue{Number: 10, Labels: []string{LabelMerging}, Merging: &MergingFacts{}}), false},
		{"awaiting-plan-review does not count", requirement(LabelAwaitingPlanReview, sub(10, LabelAwaitingMergeDecision)), false},
		{"awaiting-decision does not count", requirement(LabelImplementing, sub(10, LabelAwaitingDecision)), false},
		{"a ready owner task does not count", requirement(LabelImplementing, sub(10, LabelOwnerTask, LabelReady)), false},
		{"an issue with no status label does not count", requirement("", sub(10, "risk/low")), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.snapshot.MovesWithoutMaintainer(); got != tt.want {
				t.Errorf("MovesWithoutMaintainer = %v, want %v", got, tt.want)
			}
		})
	}
}

// The send-back after a request for changes of the Maintainer ("send back for
// changes") is decided
// from the snapshot alone: an open sub-issue in
// cumin/status/awaiting-merge-decision, not running, with a CHANGES_REQUESTED
// review of a person on the head commit, submitted after
// cumin/status/awaiting-merge-decision was last added to the issue. Who of the
// reviewers is a Maintainer is decided later (MaintainerRequestedChanges).
func TestDecide_ARequestForChangesOfAPersonOnTheHeadIsACandidateOfTheSendBack(t *testing.T) {
	t.Parallel()
	const head, old = "2222222222222222222222222222222222222222", "1111111111111111111111111111111111111111"
	awaitingMaintainerAt := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	review := func(author string, state ReviewState, commit string) Review {
		return Review{Author: author, State: state, Commit: commit, SubmittedAt: awaitingMaintainerAt.Add(time.Minute)}
	}
	answered := review("owner", ReviewChangesRequested, head)
	answered.SubmittedAt = awaitingMaintainerAt.Add(-time.Minute)
	for _, tc := range []struct {
		name    string
		labels  []string
		closed  bool
		running bool
		// timesNotRead leaves the label times of the requirement issue
		// unread.
		timesNotRead bool
		reviews      []Review
		want         []Action
	}{
		{name: "a request for changes of a person on the head", labels: []string{LabelAwaitingMergeDecision},
			reviews: []Review{review("owner", ReviewChangesRequested, head), review("other", ReviewApproved, old)},
			want:    []Action{FixMaintainerReview{Number: 10, PullRequest: 21, Reviewers: []string{"other", "owner"}}}},
		{name: "a request for changes from before the issue last waited for the Maintainer", labels: []string{LabelAwaitingMergeDecision},
			reviews: []Review{answered}},
		{name: "a new request for changes after an answered one", labels: []string{LabelAwaitingMergeDecision},
			reviews: []Review{answered, review("owner", ReviewChangesRequested, head)},
			want:    []Action{FixMaintainerReview{Number: 10, PullRequest: 21, Reviewers: []string{"owner"}}}},
		{name: "the label times are not read", labels: []string{LabelAwaitingMergeDecision}, timesNotRead: true,
			reviews: []Review{review("owner", ReviewChangesRequested, head)}},
		{name: "a request for changes on an older commit", labels: []string{LabelAwaitingMergeDecision},
			reviews: []Review{review("owner", ReviewChangesRequested, old)}},
		{name: "a request for changes of a bot", labels: []string{LabelAwaitingMergeDecision},
			reviews: []Review{review("app[bot]", ReviewChangesRequested, head)}},
		{name: "a comment-only review", labels: []string{LabelAwaitingMergeDecision},
			reviews: []Review{review("owner", ReviewCommented, head)}},
		{name: "a pull request in another state", labels: []string{LabelReviewing},
			reviews: []Review{review("owner", ReviewChangesRequested, head)}},
		{name: "a closed issue", labels: []string{LabelAwaitingMergeDecision}, closed: true,
			reviews: []Review{review("owner", ReviewChangesRequested, head)}},
		{name: "an issue that runs now", labels: []string{LabelAwaitingMergeDecision}, running: true,
			reviews: []Review{review("owner", ReviewChangesRequested, head)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := Snapshot{
				RequirementIssues: []RequirementIssue{{
					Number: 6, Labels: []string{LabelRequirement, LabelImplementing}, LabelTimesRead: !tc.timesNotRead,
					SubIssues: []SubIssue{{Number: 10, Closed: tc.closed, Labels: tc.labels, AwaitingMergeDecisionAt: awaitingMaintainerAt,
						PullRequests: []PullRequest{{Number: 21, HeadCommit: head, Labels: tc.labels, Reviews: tc.reviews}}}},
				}},
				Running: map[int]bool{10: tc.running},
			}
			if got := decideReadyOfMaintainer(snapshot, 1, nil, nil, time.Time{}, 0); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Decide = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// An issue is in work when cumin or an agent moves it on without the Maintainer.
func TestHasIssueInWork(t *testing.T) {
	requirement := func(labels []string, subs ...SubIssue) Snapshot {
		return Snapshot{RequirementIssues: []RequirementIssue{{Number: 6, Labels: labels, SubIssues: subs}}}
	}
	tests := []struct {
		name     string
		snapshot Snapshot
		want     bool
	}{
		{"no requirement issue", Snapshot{}, false},
		{"requirement issue with no status label", requirement(nil), false},
		{"ready requirement issue", requirement([]string{LabelReady}), true},
		{"planning requirement issue", requirement([]string{LabelPlanning}), true},
		{"implementing requirement issue with no sub-issue in work", requirement([]string{LabelImplementing},
			SubIssue{Number: 10, Labels: []string{LabelAwaitingMergeDecision}}), false},
		{"requirement issue that waits for the Maintainer", requirement([]string{LabelAwaitingPlanReview}), false},
		{"ready sub-issue", requirement(nil, SubIssue{Number: 10, Labels: []string{LabelReady}}), true},
		{"implementing sub-issue", requirement(nil, SubIssue{Number: 10, Labels: []string{LabelImplementing}}), true},
		{"sub-issue that waits for the checks", requirement(nil, SubIssue{Number: 10, Labels: []string{LabelChecking}}), true},
		{"reviewing sub-issue", requirement(nil, SubIssue{Number: 10, Labels: []string{LabelReviewing}}), true},
		{"sub-issue that waits for the Maintainer", requirement(nil, SubIssue{Number: 10, Labels: []string{LabelAwaitingDecision}}), false},
		{"closed sub-issue", requirement(nil, SubIssue{Number: 10, Closed: true, Labels: []string{LabelImplementing}}), false},
	}
	for _, tt := range tests {
		if got := tt.snapshot.HasIssueInWork(); got != tt.want {
			t.Errorf("%s: HasIssueInWork = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// A repository is in work until a poll that succeeded found nothing to do.
func TestRepositoryInWork(t *testing.T) {
	tests := []struct {
		name     string
		last     LastPoll
		agentRun bool
		want     bool
	}{
		{"no poll ran yet", LastPoll{}, false, true},
		{"the last poll failed", LastPoll{Ran: true, Failed: true}, false, true},
		{"the last poll took an action", LastPoll{Ran: true, Acted: true}, false, true},
		{"an issue is in work", LastPoll{Ran: true, IssueInWork: true}, false, true},
		{"an agent run", LastPoll{Ran: true}, true, true},
		{"nothing to do", LastPoll{Ran: true}, false, false},
	}
	for _, tt := range tests {
		if got := RepositoryInWork(tt.last, tt.agentRun); got != tt.want {
			t.Errorf("%s: RepositoryInWork = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// A repository that is not in work is polled at the tick nearest to the
// idle poll interval after its last poll.
func TestPollIsDue(t *testing.T) {
	const poll, idle = time.Minute, 5 * time.Minute
	tests := []struct {
		name   string
		inWork bool
		since  time.Duration
		want   bool
	}{
		{"in work, one tick later", true, time.Minute, true},
		{"idle, one tick later", false, time.Minute, false},
		{"idle, one tick before the idle poll interval", false, 4*time.Minute + time.Second, false},
		{"idle, a tick that comes a moment early", false, 5*time.Minute - time.Millisecond, true},
		{"idle, the idle poll interval is over", false, 5 * time.Minute, true},
	}
	for _, tt := range tests {
		if got := PollIsDue(tt.inWork, tt.since, poll, idle); got != tt.want {
			t.Errorf("%s: PollIsDue = %v, want %v", tt.name, got, tt.want)
		}
	}
	if !PollIsDue(false, time.Minute, time.Minute, time.Minute) {
		t.Error("equal intervals: PollIsDue = false, want a poll at every tick")
	}
}

// The way out of cumin/status/accepting is a pure function of the facts of
// the requirement issue and of whether its Planner runs.
func TestAcceptanceEnd(t *testing.T) {
	closedAt := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	labeledAt := closedAt.Add(time.Minute)
	accepting := func(change func(*RequirementIssue)) RequirementIssue {
		requirement := RequirementIssue{
			Number: 6, Labels: []string{LabelRequirement, LabelAccepting},
			SubIssues:    []SubIssue{{Number: 10, Closed: true, ClosedAt: closedAt}},
			CommentsRead: true, LabelTimesRead: true, ReviewAt: labeledAt,
		}
		if change != nil {
			change(&requirement)
		}
		return requirement
	}
	tests := []struct {
		name        string
		requirement RequirementIssue
		running     bool
		want        Action
	}{
		{
			name:        "no comment requests the acceptance check again",
			requirement: accepting(nil),
			want:        CheckAcceptance{Number: 6, Again: true},
		},
		{
			name:        "no comment after the second request stops for the Maintainer",
			requirement: accepting(func(r *RequirementIssue) { r.AcceptanceRequestedAgain = true }),
			want:        StopAcceptance{Number: 6},
		},
		{
			name:        "the comment after the last close asks the Maintainer to accept",
			requirement: accepting(func(r *RequirementIssue) { r.AcceptanceCheckAt = closedAt.Add(time.Hour) }),
			want:        Accept{Number: 6},
		},
		{
			name:        "a comment from before the last close belongs to an earlier round",
			requirement: accepting(func(r *RequirementIssue) { r.AcceptanceCheckAt = closedAt.Add(-time.Hour) }),
			want:        CheckAcceptance{Number: 6, Again: true},
		},
		{
			name:        "a question after the label stops for the Maintainer at once",
			requirement: accepting(func(r *RequirementIssue) { r.QuestionAt = labeledAt.Add(time.Minute) }),
			want:        StopAcceptance{Number: 6, Question: true},
		},
		{
			name:        "a question from before the label belongs to an earlier stay",
			requirement: accepting(func(r *RequirementIssue) { r.QuestionAt = labeledAt.Add(-time.Second) }),
			want:        CheckAcceptance{Number: 6, Again: true},
		},
		{
			name:        "a running Planner changes nothing",
			requirement: accepting(func(r *RequirementIssue) { r.AcceptanceCheckAt = closedAt.Add(time.Hour) }),
			running:     true,
		},
		{
			name:        "comments that were not read change nothing",
			requirement: accepting(func(r *RequirementIssue) { r.CommentsRead = false }),
		},
		{
			name:        "label times that were not read request nothing",
			requirement: accepting(func(r *RequirementIssue) { r.LabelTimesRead = false }),
		},
		{
			name:        "another state is not decided here",
			requirement: accepting(func(r *RequirementIssue) { r.Labels = []string{LabelRequirement, LabelImplementing} }),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// cumin-core or a Maintainer added the status label.
			tt.requirement.StatusRead, tt.requirement.StatusCounts = true, true
			if got := AcceptanceEnd(tt.requirement, tt.running); got != tt.want {
				t.Errorf("AcceptanceEnd = %#v, want %#v", got, tt.want)
			}
			// A label of another account, and one whose account was not
			// read, decide nothing.
			for _, counts := range []struct{ read, counts bool }{{true, false}, {false, false}} {
				other := tt.requirement
				other.StatusRead, other.StatusCounts = counts.read, counts.counts
				if got := AcceptanceEnd(other, tt.running); got != nil {
					t.Errorf("AcceptanceEnd with StatusRead %v = %#v, want nil for a label that does not count", counts.read, got)
				}
			}
			// The poll decides the same from the snapshot and the running set.
			snapshot := Snapshot{RequirementIssues: []RequirementIssue{tt.requirement}, Running: map[int]bool{6: tt.running}}
			var want []Action
			if tt.want != nil {
				want = []Action{tt.want}
			}
			if got := Decide(snapshot, 1, nil, nil, closedAt, time.Hour); !reflect.DeepEqual(got, want) {
				t.Errorf("Decide = %#v, want %#v", got, want)
			}
		})
	}
}

// ImplementationEnd decides the way out of cumin/status/implementing from
// the facts on GitHub, and decides nothing while the Implementer runs,
// without the facts, and from a label that does not count.
func TestImplementationEnd(t *testing.T) {
	labeledAt := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	const branch, implementer, head = "cumin/10-add-the-login-screen", "example-implementer[bot]", "abc"
	verified := PullRequest{Number: 21, NodeID: "PR_21", HeadCommit: head, HeadBranch: branch, Author: implementer}
	implementing := func(change func(*SubIssue)) SubIssue {
		sub := SubIssue{
			Number: 10, Labels: []string{"risk/low", LabelImplementing},
			PullRequests: []PullRequest{{Number: 21, HeadCommit: head, HeadBranch: branch, Author: implementer, HeadCommittedAt: labeledAt.Add(time.Minute)}},
			Implementing: &ImplementingFacts{
				StatusCounts: true, ImplementingAt: labeledAt, Branch: branch, OnBranch: []PullRequest{verified},
				Implementer: implementer, LocalHead: head, MaxLinks: 2,
			},
		}
		if change != nil {
			change(&sub)
		}
		return sub
	}
	noPullRequest := func(s *SubIssue) { s.PullRequests, s.Implementing.OnBranch = nil, nil }
	tests := []struct {
		name    string
		sub     SubIssue
		running bool
		want    Action
	}{
		{
			name: "a verified pull request waits for the checks",
			sub:  implementing(nil),
			want: WaitForChecks{Number: 10, PullRequest: 21, PullRequestNodeID: "PR_21"},
		},
		{
			name: "a verified pull request without a closing link gets the link",
			sub:  implementing(func(s *SubIssue) { s.PullRequests = nil }),
			want: WaitForChecks{Number: 10, PullRequest: 21, PullRequestNodeID: "PR_21", AddLink: true},
		},
		{
			name: "no pull request requests the implementation again",
			sub:  implementing(noPullRequest),
			want: RequestImplementationAgain{Number: 10},
		},
		{
			name: "no pull request after the second request stops the implementation with the reason",
			sub: implementing(func(s *SubIssue) {
				noPullRequest(s)
				s.Implementing.RequestedAgain = true
			}),
			want: StopImplementation{Number: 10, Reason: VerificationReason(FailureNoOpenPullRequest), Retried: true},
		},
		{
			name: "a question after the label stops the implementation before every other check",
			sub:  implementing(func(s *SubIssue) { s.Implementing.QuestionAt = labeledAt.Add(time.Minute) }),
			want: StopImplementation{Number: 10, Question: true},
		},
		{
			name: "a question before the label does not count",
			sub:  implementing(func(s *SubIssue) { s.Implementing.QuestionAt = labeledAt.Add(-time.Minute) }),
			want: WaitForChecks{Number: 10, PullRequest: 21, PullRequestNodeID: "PR_21"},
		},
		{
			name: "a head commit older than the label after a conflict request stops the implementation",
			sub: implementing(func(s *SubIssue) {
				s.Implementing.ConflictRequested = true
				s.PullRequests[0].HeadCommittedAt = labeledAt.Add(-time.Minute)
			}),
			want: StopImplementation{Number: 10, Reason: ConflictNotResolvedReason(21), PullRequest: 21},
		},
		{
			name: "a head commit newer than the label after a conflict request waits for the checks",
			sub:  implementing(func(s *SubIssue) { s.Implementing.ConflictRequested = true }),
			want: WaitForChecks{Number: 10, PullRequest: 21, PullRequestNodeID: "PR_21"},
		},
		{
			name: "a head commit older than the label after another request waits for the checks",
			sub:  implementing(func(s *SubIssue) { s.PullRequests[0].HeadCommittedAt = labeledAt.Add(-time.Minute) }),
			want: WaitForChecks{Number: 10, PullRequest: 21, PullRequestNodeID: "PR_21"},
		},
		{name: "a running Implementer decides nothing", sub: implementing(nil), running: true},
		{name: "facts that were not read decide nothing", sub: implementing(func(s *SubIssue) { s.Implementing = nil })},
		{name: "a label of another account decides nothing", sub: implementing(func(s *SubIssue) { s.Implementing.StatusCounts = false })},
		{name: "another state decides nothing", sub: implementing(func(s *SubIssue) { s.Labels = []string{"risk/low", LabelChecking} })},
		{name: "a closed issue decides nothing", sub: implementing(func(s *SubIssue) { s.Closed = true })},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ImplementationEnd(tt.sub, tt.running); got != tt.want {
				t.Errorf("ImplementationEnd = %#v, want %#v", got, tt.want)
			}
			// Decide returns the same action for the issue in a snapshot.
			snapshot := Snapshot{RequirementIssues: []RequirementIssue{{Number: 6, Labels: []string{LabelRequirement, LabelImplementing}, SubIssues: []SubIssue{tt.sub}}},
				Running: map[int]bool{10: tt.running}}
			found := false
			for _, action := range Decide(snapshot, 1, nil, nil, labeledAt, time.Hour) {
				found = found || tt.want != nil && action == tt.want
			}
			if tt.want != nil && !found {
				t.Errorf("Decide does not return %#v", tt.want)
			}
		})
	}
}

// reviewingSub is an implementation issue in cumin/status/reviewing with
// the pull request #21 at the head commit "new", no Reviewer, and the
// given reviews of the Reviewer.
func reviewingSub(facts ReviewingFacts, reviews ...Review) SubIssue {
	at := time.Date(2026, 1, 2, 3, 0, 0, 0, time.UTC)
	facts.StatusCounts, facts.ReviewingAt, facts.Reviewer, facts.Limit = true, at, "reviewer[bot]", 2
	for i := range reviews {
		reviews[i].Author = "reviewer[bot]"
		reviews[i].SubmittedAt = at.Add(time.Duration(i+1) * time.Minute)
	}
	return SubIssue{
		Number:       10,
		Labels:       []string{"risk/medium", LabelReviewing},
		PullRequests: []PullRequest{{Number: 21, HeadCommit: "new", Reviews: reviews}},
		Reviewing:    &facts,
	}
}

func TestReviewEnd_DecidesFromTheFacts(t *testing.T) {
	changes := Review{State: ReviewChangesRequested, Commit: "new"}
	explanation := Comment{Author: "reviewer[bot]", Body: DecisionRequestHeading}
	tests := []struct {
		name string
		sub  SubIssue
		want Action
	}{
		{"changes requested below the limit", reviewingSub(ReviewingFacts{RequestedHead: "new"}, changes),
			RequestReviewFix{Number: 10, PullRequest: 21, Round: 1}},
		{"changes requested at the limit", reviewingSub(ReviewingFacts{}, Review{State: ReviewChangesRequested, Commit: "old"}, changes),
			RequestCause{Number: 10, PullRequest: 21}},
		{"the cause is explained", reviewingSub(ReviewingFacts{Explained: true, Explanation: explanation}, Review{State: ReviewChangesRequested, Commit: "old"}, changes),
			StopAtRoundLimit{Number: 10, Explanation: explanation}},
		{"the request of the cause left no explanation", reviewingSub(ReviewingFacts{CauseRequested: true}, Review{State: ReviewChangesRequested, Commit: "old"}, changes),
			StopReview{Number: 10, Action: ActionStopAtTheRoundLimit, Reason: MissingExplanationReason, PullRequest: 21}},
		{"the second request of the cause left no explanation", reviewingSub(ReviewingFacts{CauseRequestedAgain: true}, Review{State: ReviewChangesRequested, Commit: "old"}, changes),
			StopReview{Number: 10, Action: ActionStopAtTheRoundLimit, Reason: MissingCauseReason, PullRequest: 21, Retried: true}},
		{"the second request of the cause returned done and left no explanation", reviewingSub(ReviewingFacts{CauseRequested: true, CauseRequestedAgain: true}, Review{State: ReviewChangesRequested, Commit: "old"}, changes),
			StopReview{Number: 10, Action: ActionStopAtTheRoundLimit, Reason: MissingCauseReason, PullRequest: 21, Retried: true}},
		{"approved with risk/medium", reviewingSub(ReviewingFacts{}, Review{State: ReviewApproved, Commit: "new"}),
			AskMaintainerToMerge{Number: 10, PullRequest: 21}},
		{"approved, a required check does not pass", reviewingSub(ReviewingFacts{Required: []RequiredCheck{{Name: "ci"}}}, Review{State: ReviewApproved, Commit: "new"}),
			BackToChecks{Number: 10}},
		{"the head commit moved", reviewingSub(ReviewingFacts{RequestedHead: "old"}, Review{State: ReviewChangesRequested, Commit: "old"}),
			BackToChecks{Number: 10, HeadMoved: true}},
		{"no review on the head commit", reviewingSub(ReviewingFacts{}),
			RequestReviewAgain{Number: 10, PullRequest: 21}},
		{"no review after the second request", reviewingSub(ReviewingFacts{RequestedAgain: true}),
			StopReview{Number: 10, Action: ActionStopTheReview, Reason: MissingReviewReason, PullRequest: 21, Retried: true}},
		{"a question after the label", reviewingSub(ReviewingFacts{QuestionAt: time.Date(2026, 1, 2, 4, 0, 0, 0, time.UTC)}, changes),
			StopReview{Number: 10, Question: true, Action: ActionStopTheReview, PullRequest: 21}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ReviewEnd(tt.sub, false)
			// The review of the action is the latest one of the scene.
			switch a := got.(type) {
			case RequestReviewFix:
				a.Review = Review{}
				got = a
			case RequestCause:
				a.Review = Review{}
				got = a
			}
			if got != tt.want {
				t.Errorf("ReviewEnd = %#v, want %#v", got, tt.want)
			}
			if running := ReviewEnd(tt.sub, true); running != nil {
				t.Errorf("ReviewEnd while the Reviewer runs = %#v, want nil", running)
			}
		})
	}
}

// Without an open pull request, the review stops for a Maintainer. A
// question comes first, then the risk label, then the pull request.
func TestReviewEnd_NoOpenPullRequestStopsTheReview(t *testing.T) {
	question := ReviewingFacts{QuestionAt: time.Date(2026, 1, 2, 4, 0, 0, 0, time.UTC)}
	tests := []struct {
		name   string
		facts  ReviewingFacts
		labels []string
		want   StopReview
	}{
		{"one risk label", ReviewingFacts{RequestedHead: "new", RequestedAgain: true}, []string{"risk/medium", LabelReviewing},
			StopReview{Number: 10, Action: ActionStopTheReview, Reason: NoOpenPullRequestReason}},
		{"no risk label", ReviewingFacts{}, []string{LabelReviewing},
			StopReview{Number: 10, Action: ActionStopTheReview, Reason: RiskLabelReason(MergeNoRiskLabel)}},
		{"two risk labels", ReviewingFacts{}, []string{"risk/low", "risk/high", LabelReviewing},
			StopReview{Number: 10, Action: ActionStopTheReview, Reason: RiskLabelReason(MergeTwoRiskLabels)}},
		{"a question after the label", question, []string{LabelReviewing},
			StopReview{Number: 10, Question: true, Action: ActionStopTheReview}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub := reviewingSub(tt.facts)
			sub.Labels, sub.PullRequests = tt.labels, nil
			if got := ReviewEnd(sub, false); got != tt.want {
				t.Errorf("ReviewEnd = %#v, want %#v", got, tt.want)
			}
			if running := ReviewEnd(sub, true); running != nil {
				t.Errorf("ReviewEnd while the Reviewer runs = %#v, want nil", running)
			}
		})
	}
}

// A review of the old head commit stays on GitHub after the head commit
// moved, and counts as a round.
func TestReviewEnd_AReviewOfTheOldHeadStillCountsAsARound(t *testing.T) {
	sub := reviewingSub(ReviewingFacts{RequestedHead: "old"}, Review{State: ReviewChangesRequested, Commit: "old"})
	if got, want := ReviewEnd(sub, false), (BackToChecks{Number: 10, HeadMoved: true}); got != want {
		t.Fatalf("ReviewEnd = %#v, want %#v", got, want)
	}
	if rounds := ReviewRounds(sub.PullRequests[0].Reviews, "reviewer[bot]", time.Time{}); rounds != 1 {
		t.Errorf("%d rounds, want 1: the review of the old head commit counts", rounds)
	}
}

// The steps in cumin/status/merging (issue-states.md, what cumin does inside
// merging): the same facts always give the same step. A pull request that
// GitHub reports as conflicting gets a conflict resolution request and no
// merge; MERGEABLE and UNKNOWN still send the merge.
func TestMergeEnd(t *testing.T) {
	const reviewer = "cumin-reviewer[bot]"
	facts := func() *MergingFacts {
		return &MergingFacts{StatusCounts: true, Reviewer: reviewer, Required: []RequiredCheck{{Name: "ci"}}}
	}
	pull := func(mergeable MergeableState, conclusion CheckConclusion) PullRequest {
		return PullRequest{
			Number: 21, HeadCommit: "head", Mergeable: mergeable,
			Checks:  []CheckResult{{Name: "ci", Conclusion: conclusion}},
			Reviews: []Review{{Author: reviewer, State: ReviewApproved, Commit: "head", SubmittedAt: time.Unix(1, 0)}},
		}
	}
	merging := func(facts *MergingFacts, prs ...PullRequest) SubIssue {
		return SubIssue{Number: 10, Labels: []string{"risk/low", LabelMerging}, PullRequests: prs, Merging: facts}
	}
	send := SendMerge{Number: 10, PullRequest: 21, HeadCommit: "head"}
	tests := []struct {
		name    string
		sub     SubIssue
		running bool
		want    Action
	}{
		{name: "MERGEABLE sends the merge", sub: merging(facts(), pull(Mergeable, CheckPassed)), want: send},
		{name: "UNKNOWN sends the merge", sub: merging(facts(), pull(MergeableUnknown, CheckPassed)), want: send},
		{name: "CONFLICTING requests a conflict resolution, with no merge",
			sub: merging(facts(), pull(Conflicting, CheckPassed)), want: ResolveMergeConflict{Number: 10, PullRequest: 21}},
		{name: "CONFLICTING with conditions that do not hold goes back to the checks",
			sub: merging(facts(), pull(Conflicting, CheckFailed)), want: LeaveMerge{Number: 10}},
		{name: "conditions that do not hold go back to the checks",
			sub: merging(facts(), pull(Mergeable, CheckFailed)), want: LeaveMerge{Number: 10}},
		{name: "a merged pull request closes the issue",
			sub: merging(&MergingFacts{StatusCounts: true, Merged: 21}), want: CloseMergedIssue{Number: 10, PullRequest: 21}},
		{name: "no pull request goes back to the checks", sub: merging(facts()), want: LeaveMerge{Number: 10}},
		{name: "CONFLICTING while a step of the issue runs decides nothing",
			sub: merging(facts(), pull(Conflicting, CheckPassed)), running: true},
		{name: "CONFLICTING without the facts decides nothing", sub: merging(nil, pull(Conflicting, CheckPassed))},
		{name: "CONFLICTING under a label that does not count decides nothing",
			sub: merging(&MergingFacts{Reviewer: reviewer, Required: []RequiredCheck{{Name: "ci"}}}, pull(Conflicting, CheckPassed))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MergeEnd(tt.sub, tt.running); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MergeEnd = %#v, want %#v", got, tt.want)
			}
		})
	}
}
