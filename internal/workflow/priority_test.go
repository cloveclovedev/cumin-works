package workflow

import (
	"slices"
	"testing"
	"time"
)

// The order of the starts (issue-states.md): highest priority first, then
// lowest issue number first. An issue without a priority label takes the
// priority of its requirement issue, and comes after every label when that
// has none either. The priority never passes an open blocked-by issue or
// the limit of issues in progress.
func TestDecide_StartsTheHighestPriorityFirst(t *testing.T) {
	t.Parallel()
	priority := []string{"priority/P0", "priority/P1", "priority/P2"}
	implementing := []string{LabelRequirement, LabelImplementing}
	ready := func(number int, labels ...string) SubIssue {
		return SubIssue{Number: number, Labels: append([]string{LabelReady, "risk/low"}, labels...)}
	}
	requirement := func(number int, labels []string, subs ...SubIssue) RequirementIssue {
		return RequirementIssue{Number: number, Labels: labels, SubIssues: subs}
	}
	claim := func(number, parent int) Action { return Claim{Number: number, RequirementIssue: parent} }

	tests := []struct {
		name     string
		snapshot Snapshot
		room     int
		priority []string
		want     []Action
	}{
		{
			name: "a higher priority starts before a lower number",
			snapshot: Snapshot{RequirementIssues: []RequirementIssue{
				requirement(6, implementing, ready(10, "priority/P2"), ready(11, "priority/P0"), ready(12, "priority/P1")),
			}},
			room: 3, priority: priority,
			want: []Action{claim(11, 6), claim(12, 6), claim(10, 6)},
		},
		{
			name: "the same priority keeps the lowest number first",
			snapshot: Snapshot{RequirementIssues: []RequirementIssue{
				requirement(6, implementing, ready(12, "priority/P1"), ready(10, "priority/P1"), ready(11, "priority/P1")),
			}},
			room: 2, priority: priority,
			want: []Action{claim(10, 6), claim(11, 6)},
		},
		{
			name: "an issue without a priority label comes after every label",
			snapshot: Snapshot{RequirementIssues: []RequirementIssue{
				requirement(6, implementing, ready(10), ready(11, "priority/P2")),
			}},
			room: 1, priority: priority,
			want: []Action{claim(11, 6)},
		},
		{
			name: "a sub-issue without a label takes the label of its requirement issue",
			snapshot: Snapshot{RequirementIssues: []RequirementIssue{
				requirement(6, implementing, ready(10, "priority/P1")),
				requirement(7, append(slices.Clone(implementing), "priority/P0"), ready(20)),
			}},
			room: 1, priority: priority,
			want: []Action{claim(20, 7)},
		},
		{
			name: "the label of the sub-issue wins over the label of its requirement issue",
			snapshot: Snapshot{RequirementIssues: []RequirementIssue{
				requirement(6, implementing, ready(10, "priority/P1")),
				requirement(7, append(slices.Clone(implementing), "priority/P0"), ready(20, "priority/P2")),
			}},
			room: 1, priority: priority,
			want: []Action{claim(10, 6)},
		},
		{
			name: "an issue with two priority labels has the higher one",
			snapshot: Snapshot{RequirementIssues: []RequirementIssue{
				requirement(6, implementing, ready(10, "priority/P1"), ready(11, "priority/P2", "priority/P0")),
			}},
			room: 1, priority: priority,
			want: []Action{claim(11, 6)},
		},
		{
			name: "a split and a claim share the order",
			snapshot: Snapshot{RequirementIssues: []RequirementIssue{
				requirement(6, implementing, ready(10, "priority/P1")),
				requirement(30, []string{LabelRequirement, LabelReady, "priority/P0"}),
			}},
			room: 2, priority: priority,
			want: []Action{Plan{Number: 30}, claim(10, 6)},
		},
		{
			name: "the priority does not pass an open blocked-by issue",
			snapshot: Snapshot{RequirementIssues: []RequirementIssue{
				requirement(6, implementing,
					SubIssue{Number: 10, Labels: []string{LabelReady, "priority/P0"}, BlockedBy: []BlockedBy{{Number: 9}}},
					ready(11, "priority/P2")),
			}},
			room: 1, priority: priority,
			want: []Action{claim(11, 6)},
		},
		{
			name: "the priority does not pass the limit of issues in progress",
			snapshot: Snapshot{RequirementIssues: []RequirementIssue{
				requirement(6, implementing, ready(10, "priority/P0"), SubIssue{Number: 11, Labels: []string{LabelImplementing}}),
			}},
			room: 1, priority: priority,
		},
		{
			name: "the label names ignore case, as on GitHub",
			snapshot: Snapshot{RequirementIssues: []RequirementIssue{
				requirement(6, implementing, ready(10), ready(11, "Priority/p0")),
			}},
			room: 1, priority: priority,
			want: []Action{claim(11, 6)},
		},
		{
			name: "the setting replaces the default names",
			snapshot: Snapshot{RequirementIssues: []RequirementIssue{
				requirement(6, implementing, ready(10, "cumin/priority/P0"), ready(11, "urgent")),
			}},
			room: 1, priority: []string{"urgent"},
			want: []Action{claim(11, 6)},
		},
		{
			name: "without priority labels the lowest number starts first",
			snapshot: Snapshot{RequirementIssues: []RequirementIssue{
				requirement(6, implementing, ready(11, "priority/P0"), ready(10)),
			}},
			room: 1,
			want: []Action{claim(10, 6)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := decideReadyOfOwner(tt.snapshot, tt.room, nil, tt.priority, time.Time{}, 0); !slices.Equal(got, tt.want) {
				t.Errorf("Decide = %+v, want %+v", got, tt.want)
			}
			// The same snapshot in another order gives the same actions.
			if again := decideReadyOfOwner(shuffle(tt.snapshot), tt.room, nil, tt.priority, time.Time{}, 0); !slices.Equal(again, tt.want) {
				t.Errorf("Decide on the shuffled snapshot = %+v, want %+v", again, tt.want)
			}
		})
	}
}
