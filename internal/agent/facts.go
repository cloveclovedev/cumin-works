package agent

// This file renders the facts of one run that cumin gives to the agent: a
// block of labelled lines that stands before the request text. The facts
// change with each run, so they are part of the request and not of the
// instruction of the role (docs/ja/requirements/agents/common.md, the
// sections on the facts of the start request, and on the process and the
// session). The file is pure: Start reads the clock and the settings, the
// caller names the issue, and Start passes the values in.

import (
	"strconv"
	"strings"
	"time"
)

// IssueKind is the kind of the issue that a run works on.
type IssueKind string

const (
	IssueKindRequirement    IssueKind = "requirement issue"
	IssueKindImplementation IssueKind = "implementation issue"
)

// Facts are the facts of one run that the caller of Start knows.
type Facts struct {
	// IssueNumber and IssueKind name the issue that the run works on. An
	// agent that returns blocked stops this issue. A zero number leaves
	// the line out: a run without an issue behind it (a test).
	IssueNumber int
	IssueKind   IssueKind
	// OwnerLogin is the login of the Owner of the issue: the account that
	// added the newest cumin/status/ready, when that account is the Owner.
	// Empty says that there is no Owner login. The line stands with the
	// line of the issue.
	OwnerLogin string
}

// runFacts are the facts of one run that the agent receives.
type runFacts struct {
	// Facts are the facts that the caller of Start gave.
	Facts
	// TimeLimit is the time limit of the run: the setting
	// roles.<role>.time_limit of the settings that the run uses.
	TimeLimit time.Duration
	// End is the time at which the run ends: the start of the run plus the
	// time limit.
	End time.Time
}

// factsBlock returns the block of labelled lines of the facts. The first
// line says that the lines are data from cumin. The issue of the run and
// the login of the Owner come next. The end time is in UTC as RFC 3339, whatever the location of the
// time is.
func factsBlock(facts runFacts) string {
	var b strings.Builder
	b.WriteString("Facts of this run (data from cumin):\n")
	if facts.IssueNumber != 0 {
		b.WriteString("- Issue of the run: #" + strconv.Itoa(facts.IssueNumber) + " (" + string(facts.IssueKind) + ")\n")
		if facts.OwnerLogin != "" {
			b.WriteString("- Login of the Owner: " + facts.OwnerLogin + "\n")
		} else {
			b.WriteString("- Login of the Owner: there is no Owner login\n")
		}
	}
	b.WriteString("- Time limit of the run: " + facts.TimeLimit.String() + "\n")
	b.WriteString("- End time of the run: " + facts.End.UTC().Format(time.RFC3339) + "\n")
	return b.String()
}

// requestWithFacts puts the block of facts before the request text, with
// one empty line between them.
func requestWithFacts(facts runFacts, text string) string {
	return factsBlock(facts) + "\n" + text
}
