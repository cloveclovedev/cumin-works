package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// scripts/setup-repo.sh reads priority_labels of .cumin/config.toml with a
// shell function, because it needs only gh and standard tools. The function
// must read the same labels that cumin reads from the same file, or the
// script would ask about other labels than the ones that order the starts.
func TestSetupRepoScript_ReadsThePriorityLabelsThatCuminReads(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "..", "..", "scripts", "setup-repo.sh"))
	if err != nil {
		t.Fatal(err)
	}
	function := regexp.MustCompile(`(?ms)^priority_labels_of\(\) \{\n.*?^\}\n`).Find(script)
	if function == nil {
		t.Fatal("scripts/setup-repo.sh has no function priority_labels_of")
	}
	starter, err := os.ReadFile(filepath.Join("..", "..", "..", "scripts", "setup-repo", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name, file string
	}{
		{"one line", "priority_labels = [\"priority/P0\", \"priority/P1\"]\n"},
		{"more lines, with comments and a trailing comma",
			"max_review_rounds = 2\npriority_labels = [\n  \"priority/P0\",  # the top\n  \"priority/P1\",\n]\nprotected_paths = [\".cumin/\"]\n"},
		{"names with a space, a colon, and a number sign", "priority_labels = [\"priority: high\", 'P#1', \"low\"]\n"},
		{"a key that is only in a comment", "# priority_labels = [\"no\"]\nprotected_paths = [\".cumin/\"]\n"},
		{"the starter file", string(starter)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := withRepository(t, hostSettings(t, ""), tt.file)
			want := settings.PriorityLabels

			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tt.file), 0o600); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command("sh", "-c", string(function)+"priority_labels_of \"$1\"", "sh", path).CombinedOutput()
			if err != nil {
				t.Fatalf("the function failed: %v\n%s", err, out)
			}
			var got []string
			if text := strings.TrimSuffix(string(out), "\n"); text != "" {
				got = strings.Split(text, "\n")
			}
			if !slices.Equal(got, want) {
				t.Errorf("the script reads %q, cumin reads %q", got, want)
			}
		})
	}
}
