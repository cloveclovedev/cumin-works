package workflow

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
)

// setupRepoFunctions returns the named shell functions of
// scripts/setup-repo.sh, so that a test runs them without the rest of the
// script.
func setupRepoFunctions(t *testing.T, names ...string) string {
	t.Helper()
	script, err := os.ReadFile(filepath.Join("..", "..", "scripts", "setup-repo.sh"))
	if err != nil {
		t.Fatal(err)
	}
	var functions strings.Builder
	for _, name := range names {
		function := regexp.MustCompile(`(?ms)^` + name + `\(\) \{\n.*?^\}\n`).Find(script)
		if function == nil {
			t.Fatalf("scripts/setup-repo.sh has no function %s", name)
		}
		functions.Write(function)
	}
	return functions.String()
}

// scripts/setup-repo.sh creates the labels of cumin with a list of its own,
// because it needs only gh and standard tools. The list must be the one of
// RepositoryLabels, or the script and cumin would create different labels.
func TestSetupRepoScript_CreatesTheLabelsThatCuminCreates(t *testing.T) {
	out, err := exec.Command("sh", "-c", setupRepoFunctions(t, "repository_labels")+"repository_labels").CombinedOutput()
	if err != nil {
		t.Fatalf("the function failed: %v\n%s", err, out)
	}
	var want strings.Builder
	for _, label := range RepositoryLabels() {
		fmt.Fprintf(&want, "%s|%s|%s\n", label.Name, label.Color, label.Description)
	}
	if string(out) != want.String() {
		t.Errorf("the script creates\n%s\ncumin creates\n%s", out, want.String())
	}
}

// The script updates the default priority labels with a second list of its
// own. The list must be the one of DefaultPriorityLabels, or the script would
// change the labels that cumin creates.
func TestSetupRepoScript_UpdatesTheDefaultPriorityLabelsThatCuminCreates(t *testing.T) {
	out, err := exec.Command("sh", "-c", setupRepoFunctions(t, "default_priority_labels")+"default_priority_labels").CombinedOutput()
	if err != nil {
		t.Fatalf("the function failed: %v\n%s", err, out)
	}
	var want strings.Builder
	for _, label := range DefaultPriorityLabels(config.DefaultPriorityLabels()) {
		fmt.Fprintf(&want, "%s|%s|%s\n", label.Name, label.Color, label.Description)
	}
	if string(out) != want.String() {
		t.Errorf("the script updates\n%s\ncumin creates\n%s", out, want.String())
	}
}
