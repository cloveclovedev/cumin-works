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
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
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

// RiskCriteria returns the built-in risk criteria of the discipline: the
// text that the Planner and the Reviewer receive word for word when a file
// of the Host or of the target repository does not replace it
// (docs/ja/requirements/cumin-core.md, the topic on settings).
//
// The discipline is a parameter, although v0.1 always passes Default, so
// that a second discipline needs no change here.
func RiskCriteria(discipline string) (string, error) {
	if !isName(discipline) {
		return "", fmt.Errorf("not a discipline name: %q", discipline)
	}
	data, err := files.ReadFile(path.Join(discipline, riskCriteriaFile))
	if err != nil {
		return "", fmt.Errorf("read the built-in risk criteria of %s: %w", discipline, err)
	}
	return string(data), nil
}

// Role returns the file of the role in the discipline: the standards of
// the craft that the instruction of that role carries after the contract
// with cumin (package roles). The second value is false when the
// discipline has no file for the role. A discipline need not have one for
// every role, and a role without one keeps the contract and the writing
// rules.
//
// The discipline is a parameter, although v0.1 always passes Default, so
// that a second discipline needs no change here.
func Role(discipline, role string) (string, bool, error) {
	return roleFile(files, discipline, role)
}

// roleFile reads the file of the role in one discipline. It takes the file
// system, so that a test covers both a discipline that has a file for the
// role and one that has none, while only one discipline ships with cumin,
// and so that a discipline from outside the binary needs no second reader.
func roleFile(fsys fs.FS, discipline, role string) (string, bool, error) {
	if !isName(discipline) {
		return "", false, fmt.Errorf("not a discipline name: %q", discipline)
	}
	if !isName(role) {
		return "", false, fmt.Errorf("not a role name: %q", role)
	}
	data, err := fs.ReadFile(fsys, path.Join(discipline, role+".md"))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("read the %s file of %s: %w", role, discipline, err)
	}
	return string(data), true, nil
}

// isName reports whether s names one directory or one file of a
// discipline: no path of its own, and no way out of the directory.
func isName(s string) bool {
	return s != "" && !strings.ContainsAny(s, `/\`) && !strings.Contains(s, "..")
}
