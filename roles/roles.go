// Package roles holds the instructions that cumin passes to an agent for
// each role. The files are English Markdown, embedded in the binary. The
// instruction of a role is its file, followed by the templates that the
// role writes with (package templates). docs/ja/designs/agent-run.md, the
// topic on the start of Claude Code, records this composition.
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

// roleTemplates names the templates of each role, in the order that they
// follow the role file. A role without an entry gets none.
var roleTemplates = map[config.Role][]string{
	config.RoleImplementer: {"writing-rules.md", "pull-request.md", "review-reply.md", "decision-request.md"},
}

// templatesHeading opens the part of the instruction that holds the
// templates.
const templatesHeading = `---

# Templates

The templates below are the forms of the text that you leave on GitHub. Use them as they are. Keep their section headings exactly as written. Write "None" under a section that has no content.
`

// Instruction returns the instruction of the role: the role file, then
// the templates of the role.
func Instruction(role config.Role) (string, error) {
	data, err := files.ReadFile(string(role) + ".md")
	if err != nil {
		return "", fmt.Errorf("no role instruction for %q", role)
	}
	names := roleTemplates[role]
	if len(names) == 0 {
		return string(data), nil
	}
	var b strings.Builder
	b.Write(data)
	b.WriteString("\n")
	b.WriteString(templatesHeading)
	for _, name := range names {
		text, err := templates.Read(name)
		if err != nil {
			return "", fmt.Errorf("instruction for %s: %w", role, err)
		}
		b.WriteString("\n---\n\n")
		b.WriteString(text)
	}
	return b.String(), nil
}
