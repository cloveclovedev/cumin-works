// Package templates holds the forms of the text that agents leave on
// GitHub: issues, pull request descriptions, reviews, replies, and
// decision requests. The files are English Markdown.
// docs/ja/requirements/policies/writing-templates.md says what each form
// is for. cumin passes the templates of a role to the agent as part of the
// role instruction (package roles), so the Markdown files stay the one
// source of the text.
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
