package disciplines

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// The built-in risk criteria comes from the directory of the default
// discipline. The test reads the file from disk, so that a move of the
// file fails here and not only where an agent would read the text.
func TestRiskCriteria_ComesFromTheDirectoryOfTheDefaultDiscipline(t *testing.T) {
	text, err := RiskCriteria()
	if err != nil {
		t.Fatal(err)
	}
	onDisk, err := os.ReadFile(filepath.Join(Default, "risk-criteria.md"))
	if err != nil {
		t.Fatalf("read the file of the discipline: %v", err)
	}
	if text != string(onDisk) {
		t.Errorf("RiskCriteria() is not %s/risk-criteria.md", Default)
	}
	if len(text) == 0 {
		t.Error("the built-in risk criteria is empty")
	}
}

// A discipline has a file for some roles and none for others. A role
// without one is not an error: the instruction of that role is then the
// role file and the writing rules (package roles).
func TestRole_TellsAFileOfTheRoleFromNoFile(t *testing.T) {
	fsys := fstest.MapFS{
		"a-discipline/implementer.md": {Data: []byte("# The craft of the Implementer\n")},
	}
	text, ok, err := roleFile(fsys, "a-discipline", "implementer")
	if err != nil || !ok {
		t.Fatalf("roleFile(implementer) = %q, %v, %v; want the file", text, ok, err)
	}
	if text != "# The craft of the Implementer\n" {
		t.Errorf("text = %q, want the file as it is", text)
	}
	if text, ok, err := roleFile(fsys, "a-discipline", "reviewer"); err != nil || ok || text != "" {
		t.Errorf("roleFile(reviewer) = %q, %v, %v; want no file and no error", text, ok, err)
	}
	if _, _, err := roleFile(fsys, "a-discipline", "../a-discipline/implementer"); err == nil {
		t.Error("roleFile with a path succeeded, want an error")
	}
	if _, _, err := roleFile(fsys, "a-discipline", ""); err == nil {
		t.Error("roleFile with an empty role succeeded, want an error")
	}
}

// Every role that cumin drives is asked for, so that a typo in a file name
// of the default discipline cannot pass unseen.
func TestRole_AnswersForEveryRoleOfTheDefaultDiscipline(t *testing.T) {
	for _, role := range []string{"planner", "implementer", "reviewer"} {
		text, ok, err := Role(role)
		if err != nil {
			t.Errorf("Role(%s): %v", role, err)
			continue
		}
		if ok && text == "" {
			t.Errorf("Role(%s) found an empty file", role)
		}
		t.Logf("%s: has a file in %s = %v", role, Default, ok)
	}
}
