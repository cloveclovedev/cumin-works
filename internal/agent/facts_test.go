package agent

import (
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
)

func TestFactsBlock_NamesTheIssueTheProtectedPathsTheTimeLimitAndTheEndTimeInUTC(t *testing.T) {
	tokyo := time.FixedZone("UTC+9", 9*60*60)
	tests := []struct {
		name  string
		facts runFacts
		want  string
	}{
		{
			name:  "minutes",
			facts: runFacts{TimeLimit: 50 * time.Minute, End: time.Date(2026, 10, 3, 2, 50, 0, 0, time.UTC)},
			want: "Facts of this run (data from cumin):\n" +
				"- Time limit of the run: 50m\n" +
				"- End time of the run: 2026-10-03T02:50:00Z\n",
		},
		{
			name:  "more than one hour",
			facts: runFacts{TimeLimit: 90 * time.Minute, End: time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)},
			want: "Facts of this run (data from cumin):\n" +
				"- Time limit of the run: 1h30m\n" +
				"- End time of the run: 2026-12-31T23:59:59Z\n",
		},
		{
			name: "a requirement issue",
			facts: runFacts{Facts: Facts{IssueNumber: 7, IssueKind: IssueKindRequirement},
				TimeLimit: 20 * time.Minute, End: time.Date(2026, 10, 3, 1, 20, 0, 0, time.UTC)},
			want: "Facts of this run (data from cumin):\n" +
				"- Issue of the run: #7 (requirement issue)\n" +
				"- Login of the Owner: there is no Owner login\n" +
				"- Time limit of the run: 20m\n" +
				"- End time of the run: 2026-10-03T01:20:00Z\n",
		},
		{
			name: "an implementation issue",
			facts: runFacts{Facts: Facts{IssueNumber: 12, IssueKind: IssueKindImplementation, OwnerLogin: "example-owner"},
				TimeLimit: 50 * time.Minute, End: time.Date(2026, 10, 3, 1, 50, 0, 0, time.UTC)},
			want: "Facts of this run (data from cumin):\n" +
				"- Issue of the run: #12 (implementation issue)\n" +
				"- Login of the Owner: example-owner\n" +
				"- Time limit of the run: 50m\n" +
				"- End time of the run: 2026-10-03T01:50:00Z\n",
		},
		{
			name: "the protected paths and the rules of matching",
			facts: runFacts{Facts: Facts{IssueNumber: 12, IssueKind: IssueKindImplementation,
				ProtectedPaths: []string{".cumin/", "CLAUDE.md", "/docs/requirements/"}},
				TimeLimit: 50 * time.Minute, End: time.Date(2026, 10, 3, 1, 50, 0, 0, time.UTC)},
			want: "Facts of this run (data from cumin):\n" +
				"- Issue of the run: #12 (implementation issue)\n" +
				"- Login of the Owner: there is no Owner login\n" +
				"- Protected paths (agents keep these paths unchanged):\n" +
				"  - `.cumin/`\n" +
				"  - `CLAUDE.md`\n" +
				"  - `/docs/requirements/`\n" +
				"- Rules of matching of the protected paths:\n" +
				"  - An entry with no \"/\" other than a trailing \"/\" matches at any depth. \"CLAUDE.md\" also matches \"sub/CLAUDE.md\".\n" +
				"  - An entry with a leading \"/\" or an inner \"/\" matches only at that position from the top of the repository.\n" +
				"  - An entry with a trailing \"/\" is a directory. It matches everything below that directory.\n" +
				"  - Wildcards do not work.\n" +
				"  - Upper case and lower case are the same. \"claude.md\" matches \"CLAUDE.md\".\n" +
				"- Time limit of the run: 50m\n" +
				"- End time of the run: 2026-10-03T01:50:00Z\n",
		},
		{
			name: "an empty list of protected paths has no rules",
			facts: runFacts{Facts: Facts{ProtectedPaths: []string{}},
				TimeLimit: time.Minute, End: time.Date(2026, 10, 3, 0, 1, 0, 0, time.UTC)},
			want: "Facts of this run (data from cumin):\n" +
				"- Protected paths (agents keep these paths unchanged): none\n" +
				"- Time limit of the run: 1m\n" +
				"- End time of the run: 2026-10-03T00:01:00Z\n",
		},
		{
			name: "the Planner receives the time limits of the Implementer and the Reviewer",
			facts: runFacts{Facts: Facts{IssueNumber: 7, IssueKind: IssueKindRequirement},
				TimeLimit: 20 * time.Minute, End: time.Date(2026, 10, 3, 1, 20, 0, 0, time.UTC),
				Role: config.RolePlanner, ImplementerTimeLimit: 50 * time.Minute, ReviewerTimeLimit: 30 * time.Minute},
			want: "Facts of this run (data from cumin):\n" +
				"- Issue of the run: #7 (requirement issue)\n" +
				"- Login of the Owner: there is no Owner login\n" +
				"- Time limit of the run: 20m\n" +
				"- End time of the run: 2026-10-03T01:20:00Z\n" +
				"- Time limit of the Implementer: 50m\n" +
				"- Time limit of the Reviewer: 30m\n",
		},
		{
			name: "the Implementer and the Reviewer do not receive the time limits of the other roles",
			facts: runFacts{TimeLimit: 50 * time.Minute, End: time.Date(2026, 10, 3, 1, 50, 0, 0, time.UTC),
				Role: config.RoleImplementer, ImplementerTimeLimit: 50 * time.Minute, ReviewerTimeLimit: 30 * time.Minute},
			want: "Facts of this run (data from cumin):\n" +
				"- Time limit of the run: 50m\n" +
				"- End time of the run: 2026-10-03T01:50:00Z\n",
		},
		{
			name:  "a time in another location is written in UTC",
			facts: runFacts{TimeLimit: time.Minute, End: time.Date(2026, 10, 3, 9, 1, 0, 0, tokyo)},
			want: "Facts of this run (data from cumin):\n" +
				"- Time limit of the run: 1m\n" +
				"- End time of the run: 2026-10-03T00:01:00Z\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := factsBlock(tt.facts); got != tt.want {
				t.Errorf("factsBlock = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLimitText_LeavesOutThePartsThatAreZeroAtTheEnd(t *testing.T) {
	tests := []struct {
		limit time.Duration
		want  string
	}{
		{50 * time.Minute, "50m"},
		{10 * time.Minute, "10m"},
		{90 * time.Minute, "1h30m"},
		{time.Hour, "1h"},
		{10 * time.Hour, "10h"},
		{90 * time.Second, "1m30s"},
		{time.Hour + 30*time.Second, "1h0m30s"},
		{30 * time.Second, "30s"},
		{0, "0s"},
	}
	for _, tt := range tests {
		if got := limitText(tt.limit); got != tt.want {
			t.Errorf("limitText(%v) = %q, want %q", tt.limit, got, tt.want)
		}
	}
}

func TestRequestWithFacts_PutsTheBlockBeforeTheRequestText(t *testing.T) {
	facts := runFacts{TimeLimit: time.Minute, End: time.Date(2026, 10, 3, 0, 1, 0, 0, time.UTC)}
	want := factsBlock(facts) + "\nImplement issue 12."
	if got := requestWithFacts(facts, "Implement issue 12."); got != want {
		t.Errorf("requestWithFacts = %q, want %q", got, want)
	}
}
