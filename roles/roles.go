// Package roles holds the instructions that cumin passes to an agent for
// each role. The files are English Markdown, embedded in the binary.
//
// The files are placeholders until the requirement of each role writes
// the full instruction.
package roles

import (
	"embed"
	"fmt"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
)

//go:embed *.md
var files embed.FS

// Instruction returns the instruction of the role.
func Instruction(role config.Role) (string, error) {
	data, err := files.ReadFile(string(role) + ".md")
	if err != nil {
		return "", fmt.Errorf("no role instruction for %q", role)
	}
	return string(data), nil
}
