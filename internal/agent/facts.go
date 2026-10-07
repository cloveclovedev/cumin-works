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

	"github.com/cloveclovedev/cumin-works/internal/core/config"
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
	// IssueOwnerLogin is the login of the Issue Owner: the account that
	// added the newest cumin/status/ready, when that account is a Maintainer.
	// Empty says that there is no Issue Owner login. The line stands with the
	// line of the issue.
	IssueOwnerLogin string
	// ProtectedPaths is the list of protected paths that applies in the
	// target repository, resolved by the caller: the list of its
	// .cumin/config.toml, or the default list. cumin does not check the
	// entries. Nil leaves the lines out: a run without the settings of a
	// repository behind it (a test). An empty list is a value: the
	// repository protects nothing.
	ProtectedPaths []string
}

// protectedPathRules are the rules of matching of the protected paths, as
// the check cumin-protected-paths applies them (cumin-core.md, the topic on
// settings): any depth, a fixed position, a directory, no wildcard, no
// case.
const protectedPathRules = `- Rules of matching of the protected paths:
  - An entry with no "/" other than a trailing "/" matches at any depth. "CLAUDE.md" also matches "sub/CLAUDE.md".
  - An entry with a leading "/" or an inner "/" matches only at that position from the top of the repository.
  - An entry with a trailing "/" is a directory. It matches everything below that directory.
  - Wildcards do not work.
  - Upper case and lower case are the same. "claude.md" matches "CLAUDE.md".
`

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
	// Role is the role of the run. The Planner alone receives the time
	// limits of the other roles.
	Role config.Role
	// ImplementerTimeLimit and ReviewerTimeLimit are the time limits of
	// one run of the Implementer and of the Reviewer: the settings
	// roles.implementer.time_limit and roles.reviewer.time_limit. The
	// Planner sizes each implementation issue with them.
	ImplementerTimeLimit time.Duration
	ReviewerTimeLimit    time.Duration
}

// factsBlock returns the block of labelled lines of the facts. The first
// line says that the lines are data from cumin. The issue of the run and
// the login of the Issue Owner come next, then the protected paths, one entry
// on each line, with the rules of matching. The end time is in UTC as RFC
// 3339, whatever the location of the time is. A run of the Planner has two
// more lines at the end: the time limits of the Implementer and of the
// Reviewer.
func factsBlock(facts runFacts) string {
	var b strings.Builder
	b.WriteString("Facts of this run (data from cumin):\n")
	if facts.IssueNumber != 0 {
		b.WriteString("- Issue of the run: #" + strconv.Itoa(facts.IssueNumber) + " (" + string(facts.IssueKind) + ")\n")
		if facts.IssueOwnerLogin != "" {
			b.WriteString("- Issue Owner login: " + facts.IssueOwnerLogin + "\n")
		} else {
			b.WriteString("- Issue Owner login: there is no Issue Owner login\n")
		}
	}
	if facts.ProtectedPaths != nil {
		b.WriteString(protectedPathsLines(facts.ProtectedPaths))
	}
	b.WriteString("- Time limit of the run: " + limitText(facts.TimeLimit) + "\n")
	b.WriteString("- End time of the run: " + facts.End.UTC().Format(time.RFC3339) + "\n")
	if facts.Role == config.RolePlanner {
		b.WriteString("- Time limit of the Implementer: " + limitText(facts.ImplementerTimeLimit) + "\n")
		b.WriteString("- Time limit of the Reviewer: " + limitText(facts.ReviewerTimeLimit) + "\n")
	}
	return b.String()
}

// protectedPathsLines returns the lines of the protected paths: each entry
// as the settings hold it, then the rules of matching. An empty list has no
// entry to match, so the rules are left out.
func protectedPathsLines(entries []string) string {
	if len(entries) == 0 {
		return "- Protected paths (agents keep these paths unchanged): none\n"
	}
	var b strings.Builder
	b.WriteString("- Protected paths (agents keep these paths unchanged):\n")
	for _, entry := range entries {
		b.WriteString("  - `" + entry + "`\n")
	}
	b.WriteString(protectedPathRules)
	return b.String()
}

// limitText writes a time limit without the parts that are zero at its
// end: 50m, not 50m0s, and 1h, not 1h0m0s.
func limitText(limit time.Duration) string {
	text := limit.String()
	if strings.HasSuffix(text, "m0s") {
		text = strings.TrimSuffix(text, "0s")
	}
	if strings.HasSuffix(text, "h0m") {
		text = strings.TrimSuffix(text, "0m")
	}
	return text
}

// requestWithFacts puts the block of facts before the request text, with
// one empty line between them.
func requestWithFacts(facts runFacts, text string) string {
	return factsBlock(facts) + "\n" + text
}
