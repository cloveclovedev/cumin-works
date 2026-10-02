package agent

// This file renders the facts of one run that cumin gives to the agent: a
// block of labelled lines that stands before the request text. The facts
// change with each run, so they are part of the request and not of the
// instruction of the role (docs/ja/requirements/agents/common.md, the
// section on the process and the session). The file is pure: Start reads
// the clock and the settings, and passes the values in.

import (
	"strings"
	"time"
)

// runFacts are the facts of one run that the agent receives.
type runFacts struct {
	// TimeLimit is the time limit of the run: the setting
	// roles.<role>.time_limit of the settings that the run uses.
	TimeLimit time.Duration
	// End is the time at which the run ends: the start of the run plus the
	// time limit.
	End time.Time
}

// factsBlock returns the block of labelled lines of the facts. The first
// line says that the lines are data from cumin. The end time is in UTC as
// RFC 3339, whatever the location of the time is.
func factsBlock(facts runFacts) string {
	var b strings.Builder
	b.WriteString("Facts of this run (data from cumin):\n")
	b.WriteString("- Time limit of the run: " + facts.TimeLimit.String() + "\n")
	b.WriteString("- End time of the run: " + facts.End.UTC().Format(time.RFC3339) + "\n")
	return b.String()
}

// requestWithFacts puts the block of facts before the request text, with
// one empty line between them.
func requestWithFacts(facts runFacts, text string) string {
	return factsBlock(facts) + "\n" + text
}
