// Package disciplines holds the standards of one field of work. A role is
// the box that cumin drives, and a discipline is the field of work that
// fills it: the same role runs software engineering today and another
// field later. The files are English Markdown, embedded in the binary, and
// reach an agent as part of the instruction of its role (package roles).
//
// v0.1 ships one discipline and no way to choose another. The backlog item
// on the split of the role and the discipline holds that plan.
package disciplines

import (
	"embed"
	"fmt"
	"path"
)

// Default is the discipline that every role works in. This is the one
// place in the code that names a discipline.
const Default = "software-engineering"

// riskCriteriaFile is the built-in risk criteria of a discipline. A risk
// criterion says which change is risk/high, which is the judgment of one
// field of work.
const riskCriteriaFile = "risk-criteria.md"

// The directory of each discipline holds the files of that discipline. The
// pattern names no discipline, so that Default stays the one place that
// does.
//
//go:embed */*.md
var files embed.FS

// RiskCriteria returns the built-in risk criteria of the default
// discipline: the text that the Chief Engineer and the Reviewer receive
// word for word when a file of the Host or of the target repository does
// not replace it (docs/ja/requirements/cumin-core.md, the topic on
// settings).
func RiskCriteria() (string, error) {
	data, err := files.ReadFile(path.Join(Default, riskCriteriaFile))
	if err != nil {
		return "", fmt.Errorf("read the built-in risk criteria of %s: %w", Default, err)
	}
	return string(data), nil
}
