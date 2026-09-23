// Package templates holds the forms of the text that agents leave on
// GitHub: issues, pull request descriptions, reviews, replies, and
// decision requests. The files are English Markdown.
// docs/ja/requirements/policies/writing-templates.md says what each form
// is for. The writing rules are part of every role instruction, which
// internal/agent composes; the other templates reach an agent as skills.
// The Markdown files stay the one source of the text.
package templates

import (
	"embed"
	"fmt"
)

//go:embed *.md
var files embed.FS

// Read returns the template with the file name, such as "pull-request.md".
func Read(name string) (string, error) {
	data, err := files.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("no template %q", name)
	}
	return string(data), nil
}

// writingRulesFile holds the rules of plain English. They apply to every
// text that an agent writes, so they are part of the instruction of every
// role and not a skill of one action.
const writingRulesFile = "writing-rules.md"

// WritingRules returns the rules of plain English.
func WritingRules() (string, error) {
	return Read(writingRulesFile)
}
