package workflow

import (
	"math/rand/v2"
	"slices"
	"testing"
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
			name:          "ready on a requirement issue gives no claim (R1 is another rule)",
			snapshot:      Snapshot{RequirementIssues: []RequirementIssue{requirement(6, []string{LabelReady})}},
			maxInProgress: 1,
		},
		{
			name:          "an empty snapshot gives no action",
			snapshot:      Snapshot{},
			maxInProgress: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Decide(tt.snapshot, tt.maxInProgress)
			if !slices.Equal(got, tt.want) {
				t.Errorf("Decide = %+v, want %+v", got, tt.want)
			}
			// The same snapshot in another order gives the same actions.
			shuffled := shuffle(tt.snapshot)
			if again := Decide(shuffled, tt.maxInProgress); !slices.Equal(again, tt.want) {
				t.Errorf("Decide on the shuffled snapshot = %+v, want %+v", again, tt.want)
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
