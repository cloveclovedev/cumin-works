package agent

import (
	"testing"
	"time"
)

func TestFactsBlock_NamesTheTimeLimitAndTheEndTimeInUTC(t *testing.T) {
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
				"- Time limit of the run: 50m0s\n" +
				"- End time of the run: 2026-10-03T02:50:00Z\n",
		},
		{
			name:  "more than one hour",
			facts: runFacts{TimeLimit: 90 * time.Minute, End: time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)},
			want: "Facts of this run (data from cumin):\n" +
				"- Time limit of the run: 1h30m0s\n" +
				"- End time of the run: 2026-12-31T23:59:59Z\n",
		},
		{
			name:  "a time in another location is written in UTC",
			facts: runFacts{TimeLimit: time.Minute, End: time.Date(2026, 10, 3, 9, 1, 0, 0, tokyo)},
			want: "Facts of this run (data from cumin):\n" +
				"- Time limit of the run: 1m0s\n" +
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

func TestRequestWithFacts_PutsTheBlockBeforeTheRequestText(t *testing.T) {
	facts := runFacts{TimeLimit: time.Minute, End: time.Date(2026, 10, 3, 0, 1, 0, 0, time.UTC)}
	want := factsBlock(facts) + "\nImplement issue 12."
	if got := requestWithFacts(facts, "Implement issue 12."); got != want {
		t.Errorf("requestWithFacts = %q, want %q", got, want)
	}
}
