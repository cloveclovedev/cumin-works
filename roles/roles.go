// Package roles holds the instructions that cumin passes to an agent for
// each role. The files are English Markdown, embedded in the binary. A
// role file holds the contract between cumin and the agent.
//
// The instruction of a role is its file, then the file of that role in the
// default discipline (package disciplines), then the writing rules
// (package templates). The discipline holds the standards of the field of
// work, and a role whose discipline has no file gets the other two parts.
// The other templates reach the agent as skills (skills.go).
// docs/ja/requirements/agents/common.md, the topic on the composition of
// the instruction, and docs/ja/designs/agent-run.md, the topic on the
// start of Claude Code, record this composition.
//
// The files of the Planner and the Reviewer are placeholders until
// the requirement of each role writes the full instruction.
package roles

import (
	"embed"
	"fmt"
	"strings"

	"github.com/cloveclovedev/cumin-works/disciplines"
	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/templates"
)

//go:embed *.md
var files embed.FS

// writingRules is the last part of every instruction. The rules apply to
// every text that the agent writes, so they go into the instruction
// itself. The templates of one action are skills instead (skills.go).
const writingRules = "writing-rules.md"

// partSeparator stands between the parts of an instruction.
const partSeparator = "\n---\n\n"

// Instruction returns the instruction of the role: the role file, then
// the file of the role in the default discipline when that discipline has
// one, then the writing rules.
//
// The role is matched against the three agent roles before the file is
// read, so that a name which is not a role, and a name which holds a path,
// never come back as an instruction.
func Instruction(role config.Role) (string, error) {
	switch role {
	case config.RolePlanner, config.RoleImplementer, config.RoleReviewer:
	default:
		return "", fmt.Errorf("no role instruction for %q", role)
	}
	data, err := files.ReadFile(string(role) + ".md")
	if err != nil {
		return "", fmt.Errorf("no role instruction for %q", role)
	}
	parts := []string{string(data)}
	discipline, ok, err := disciplines.Role(string(role))
	if err != nil {
		return "", fmt.Errorf("instruction for %s: %w", role, err)
	}
	if ok {
		parts = append(parts, discipline)
	}
	rules, err := templates.Read(writingRules)
	if err != nil {
		return "", fmt.Errorf("instruction for %s: %w", role, err)
	}
	parts = append(parts, rules)
	return strings.Join(parts, partSeparator), nil
}
