package roles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRiskCriteria(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, RiskCriteriaFile), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func ptr(s string) *string { return &s }

// The three levels of the risk criteria: the text that ships with cumin,
// the file of the Host, and the file of the repository.
func TestRiskCriteria_TakesTheStrongestFileThatExists(t *testing.T) {
	builtIn, err := DefaultRiskCriteria()
	if err != nil {
		t.Fatal(err)
	}
	hostDir := writeRiskCriteria(t, "# Risk criteria of the Host\n")

	tests := []struct {
		name       string
		repository *string
		hostDir    string
		wantText   string
		wantSource RiskCriteriaSource
	}{
		{"no file", nil, t.TempDir(), builtIn, RiskCriteriaFromDefault},
		{"no Host directory", nil, "", builtIn, RiskCriteriaFromDefault},
		{"the file of the Host", nil, hostDir, "# Risk criteria of the Host\n", RiskCriteriaFromHost},
		{"the file of the repository over the Host", ptr("# Of the repository\n"), hostDir, "# Of the repository\n", RiskCriteriaFromRepository},
		{"the file of the repository over the default", ptr("# Of the repository\n"), t.TempDir(), "# Of the repository\n", RiskCriteriaFromRepository},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, source, err := RiskCriteria(tt.repository, tt.hostDir)
			if err != nil {
				t.Fatalf("RiskCriteria: %v", err)
			}
			if text != tt.wantText {
				t.Errorf("text = %q, want %q", text, tt.wantText)
			}
			if source != tt.wantSource {
				t.Errorf("source = %q, want %q", source, tt.wantSource)
			}
		})
	}
}

// The text is returned as it is. cumin does not read it.
func TestRiskCriteria_ReturnsTheTextAsItIs(t *testing.T) {
	const odd = "  # Ours\r\n\ttabs and trailing spaces   \n\n"
	text, _, err := RiskCriteria(ptr(odd), "")
	if err != nil {
		t.Fatal(err)
	}
	if text != odd {
		t.Errorf("text = %q, want the same bytes", text)
	}
}

func TestRiskCriteria_AnEmptyFileIsAnError(t *testing.T) {
	tests := []struct {
		name       string
		repository *string
		hostDir    string
		want       string
	}{
		{"the file of the repository", ptr("  \n\t\n"), "", ".cumin/risk-criteria.md: the risk criteria is empty"},
		{"the file of the Host", nil, writeRiskCriteria(t, "\n"), RiskCriteriaFile + ": the risk criteria is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := RiskCriteria(tt.repository, tt.hostDir)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want an error that names the path", err)
			}
		})
	}
}

// The built-in text is what the requirements rely on: the three levels,
// who decides the merge, and the changes that are high by default. The
// sixth question of the Reviewer (authentication, cryptography, session
// handling) is risk/high in the built-in text.
func TestDefaultRiskCriteria_HoldsWhatTheRequirementsRelyOn(t *testing.T) {
	text, err := DefaultRiskCriteria()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"`risk/low`", "`risk/medium`", "`risk/high`",
		"a revert cannot undo",
		"database migration", "deployment or CI setting",
		"authentication", "cryptography", "session handling", "payments",
		"external service", "public API", "`.cumin/`",
		"When in doubt, take the higher one",
		"The Owner decides the risk",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the built-in risk criteria does not say: %s", want)
		}
	}
	if !strings.HasPrefix(text, "# ") || strings.Contains(text, "**") {
		t.Errorf("the built-in risk criteria must start with a heading and must not use bold text:\n%s", text)
	}
}
