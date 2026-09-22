package disciplines

import (
	"os"
	"path/filepath"
	"testing"
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
