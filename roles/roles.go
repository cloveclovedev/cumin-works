// Package roles holds the contract of each role with cumin: one English
// Markdown file for each role, embedded in the binary.
//
// The package reads its own Markdown and nothing else. The instruction
// that reaches an agent is composed in internal/agent, from this file, the
// file of the role in its discipline (package disciplines), the writing
// rules (package templates), and the risk criteria of the target
// repository. docs/ja/requirements/agents/common.md, the section on the
// composition of the instruction, records the order.
//
// The files of the Planner and the Reviewer are placeholders until the
// requirement of each role writes the full instruction.
package roles

import (
	"embed"
	"fmt"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
)

//go:embed *.md
var files embed.FS

// File returns the role file of the role: the contract between cumin and
// the agent.
//
// The role is matched against the three agent roles before the file is
// read, so that a name which is not a role, and a name which holds a path,
// never come back as a role file.
func File(role config.Role) (string, error) {
	switch role {
	case config.RolePlanner, config.RoleImplementer, config.RoleReviewer:
	default:
		return "", fmt.Errorf("no role file for %q", role)
	}
	data, err := files.ReadFile(string(role) + ".md")
	if err != nil {
		return "", fmt.Errorf("no role file for %q", role)
	}
	return string(data), nil
}
