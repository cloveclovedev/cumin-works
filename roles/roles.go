// Package roles holds the instructions that cumin passes to an agent for
// each role. The files are English Markdown, embedded in the binary. The
// instruction of a role is its file, followed by the writing rules
// (package templates). The other templates reach the agent as skills
// (skills.go). docs/ja/designs/agent-run.md, the topic on the start of
// Claude Code, records this composition.
//
// The files of the Chief Engineer and the Reviewer are placeholders until
// the requirement of each role writes the full instruction.
package roles

import (
	"embed"
	"fmt"
	"strings"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/templates"
)

//go:embed *.md
var files embed.FS

// roleTemplates names the templates that follow the role file, in order.
// Only the writing rules go here: they apply to every text that the agent
// writes. The templates of one action are skills (skills.go). A role
// without an entry gets none.
var roleTemplates = map[config.Role][]string{
	config.RoleImplementer: {"writing-rules.md"},
}

// Instruction returns the instruction of the role: the role file, then
// the templates of the role.
//
// The role is matched against the three agent roles before the file is
// read. The embedded directory holds other Markdown as well (the built-in
// risk criteria), and a name that is not a role must never come back as an
// instruction.
func Instruction(role config.Role) (string, error) {
	switch role {
	case config.RoleChiefEngineer, config.RoleImplementer, config.RoleReviewer:
	default:
		return "", fmt.Errorf("no role instruction for %q", role)
	}
	data, err := files.ReadFile(string(role) + ".md")
	if err != nil {
		return "", fmt.Errorf("no role instruction for %q", role)
	}
	var b strings.Builder
	b.Write(data)
	for _, name := range roleTemplates[role] {
		text, err := templates.Read(name)
		if err != nil {
			return "", fmt.Errorf("instruction for %s: %w", role, err)
		}
		b.WriteString("\n---\n\n")
		b.WriteString(text)
	}
	return b.String(), nil
}
