package workflow

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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

func TestSetupRepoScript_MapsEachOldStatusLabelToTheNewOne(t *testing.T) {
	function := setupRepoFunctions(t, "new_status_label")
	tests := []struct {
		name, old, requirement, total, closed, want string
	}{
		{"awaiting checks", LabelAwaitingChecks, "no", "0", "0", LabelChecking},
		{"awaiting the decision of the Owner", LabelAwaitingOwnerDecision, "no", "0", "0", LabelAwaitingDecision},
		{"awaiting the decision of the Owner on a requirement issue", LabelAwaitingOwnerDecision, "yes", "2", "2", LabelAwaitingDecision},
		{"awaiting the review of the Owner on an implementation issue", LabelAwaitingOwnerReview, "no", "0", "0", LabelAwaitingMergeDecision},
		{"awaiting the review of the Owner on a requirement issue with all sub-issues closed", LabelAwaitingOwnerReview, "yes", "3", "3", LabelAwaitingAcceptance},
		{"awaiting the review of the Owner on a requirement issue with an open sub-issue", LabelAwaitingOwnerReview, "yes", "3", "2", LabelAwaitingPlanReview},
		{"awaiting the review of the Owner on a requirement issue without sub-issues", LabelAwaitingOwnerReview, "yes", "0", "0", LabelAwaitingPlanReview},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := exec.Command("sh", "-c", function+`new_status_label "$@"`, "sh", tt.old, tt.requirement, tt.total, tt.closed).CombinedOutput()
			if err != nil {
				t.Fatalf("the function failed: %v\n%s", err, out)
			}
			if got := strings.TrimSuffix(string(out), "\n"); got != tt.want {
				t.Errorf("new label = %q, want %q", got, tt.want)
			}
		})
	}
	t.Run("a label that is not an old status label", func(t *testing.T) {
		out, err := exec.Command("sh", "-c", function+`new_status_label "$@"`, "sh", LabelReady, "no", "0", "0").CombinedOutput()
		if err == nil || len(out) != 0 {
			t.Errorf("the function printed %q and returned %v, want nothing and a failure", out, err)
		}
	})
}

// fakeGh is a gh that keeps the labels of a repository and of its issues in
// files, and writes each call that changes something to the file "changes".
// The list of the issues is the output after --jq of the script:
// number, kind, requirement issue, sub-issues, closed sub-issues.
const fakeGh = `#!/bin/sh
case "$*" in
  *"-X POST repos/o/r/labels"*)
    echo "$*" >>"$GH_STATE/changes"
    for argument in "$@"; do
      case "$argument" in name=*) echo "${argument#name=}" >>"$GH_STATE/labels" ;; esac
    done ;;
  *"-X POST repos/o/r/issues/"*)
    echo "$*" >>"$GH_STATE/changes" ;;
  *"-X DELETE repos/o/r/issues/"*)
    echo "$*" >>"$GH_STATE/changes"
    # The old label leaves every list of the fake.
    : >"$GH_STATE/issues" ;;
  *"repos/o/r/labels --jq"*) cat "$GH_STATE/labels" ;;
  *"repos/o/r/issues -f state=open -f labels=cumin/status/awaiting-owner-review"*) cat "$GH_STATE/issues" ;;
  *"repos/o/r/issues -f state=open"*) ;;
  *) echo "unexpected call: $*" >&2; exit 1 ;;
esac
`

// runLabelStep runs the label step of the script against the fake gh, with
// the given answer on the standard input, and returns what it printed.
func runLabelStep(t *testing.T, state, answer, dryRun string) string {
	t.Helper()
	functions := setupRepoFunctions(t, "die", "repository_labels", "new_status_label", "create_repository_labels", "move_status_labels")
	command := exec.Command("sh", "-c", "set -eu\n"+functions+"repo=o/r\nwork=\"$GH_STATE/work\"\ndry_run="+dryRun+"\ncreate_repository_labels\nmove_status_labels\n")
	command.Env = append(os.Environ(), "GH_STATE="+state, "PATH="+state+string(os.PathListSeparator)+os.Getenv("PATH"))
	command.Stdin = strings.NewReader(answer)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("the label step failed: %v\n%s", err, out)
	}
	return string(out)
}

// newLabelState prepares a repository with only the label "bug", and one
// implementation issue, one requirement issue, and one pull request in
// cumin/status/awaiting-owner-review.
func newLabelState(t *testing.T) string {
	t.Helper()
	state := t.TempDir()
	files := map[string]string{
		"gh":      fakeGh,
		"labels":  "bug\n",
		"issues":  "7\tissue\tno\t0\t0\n8\tissue\tyes\t2\t2\n9\tpull-request\tno\t0\t0\n",
		"changes": "",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(state, name), []byte(content), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(state, "work"), 0o700); err != nil {
		t.Fatal(err)
	}
	return state
}

func changesOf(t *testing.T, state string) string {
	t.Helper()
	changes, err := os.ReadFile(filepath.Join(state, "changes"))
	if err != nil {
		t.Fatal(err)
	}
	return string(changes)
}

func TestSetupRepoScript_ReplacesTheOldStatusLabelsOnlyAfterTheAnswerYes(t *testing.T) {
	for _, answer := range []string{"", "n\n", "\n"} {
		state := newLabelState(t)
		out := runLabelStep(t, state, answer, "0")
		if changes := changesOf(t, state); strings.Contains(changes, "issues/") {
			t.Errorf("answer %q: the script changed a label of an issue:\n%s", answer, changes)
		}
		if !strings.Contains(out, "#8 (issue): cumin/status/awaiting-owner-review -> cumin/status/awaiting-acceptance") ||
			!strings.Contains(out, "kept       no label of an issue was replaced") {
			t.Errorf("answer %q: the script printed\n%s", answer, out)
		}
	}

	state := newLabelState(t)
	out := runLabelStep(t, state, "y\n", "0")
	changes := changesOf(t, state)
	// The new label is added before the old one is removed.
	want := []string{
		"-X POST repos/o/r/issues/7/labels -f labels[]=cumin/status/awaiting-merge-decision",
		"-X DELETE repos/o/r/issues/7/labels/cumin%2Fstatus%2Fawaiting-owner-review",
		"-X POST repos/o/r/issues/8/labels -f labels[]=cumin/status/awaiting-acceptance",
		"-X DELETE repos/o/r/issues/8/labels/cumin%2Fstatus%2Fawaiting-owner-review",
		"-X POST repos/o/r/issues/9/labels -f labels[]=cumin/status/awaiting-merge-decision",
		"-X DELETE repos/o/r/issues/9/labels/cumin%2Fstatus%2Fawaiting-owner-review",
	}
	at := 0
	for _, call := range want {
		next := strings.Index(changes[at:], call)
		if next < 0 {
			t.Fatalf("the calls lack %q after position %d:\n%s", call, at, changes)
		}
		at += next
	}
	if got := strings.Count(changes, "-X POST repos/o/r/labels"); got != len(RepositoryLabels()) {
		t.Errorf("the script created %d labels, want %d\n%s", got, len(RepositoryLabels()), out)
	}
}

func TestSetupRepoScript_ASecondRunChangesNothing(t *testing.T) {
	state := newLabelState(t)
	runLabelStep(t, state, "y\n", "0")
	if err := os.WriteFile(filepath.Join(state, "changes"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	out := runLabelStep(t, state, "y\n", "0")
	if changes := changesOf(t, state); changes != "" {
		t.Errorf("the second run changed:\n%s", changes)
	}
	want := "unchanged  the labels of cumin exist\nunchanged  no open issue or pull request carries an old status label\n"
	if out != want {
		t.Errorf("the second run printed\n%s\nwant\n%s", out, want)
	}
}

func TestSetupRepoScript_ADryRunChangesNoLabel(t *testing.T) {
	state := newLabelState(t)
	out := runLabelStep(t, state, "y\n", "1")
	if changes := changesOf(t, state); changes != "" {
		t.Errorf("the dry run changed:\n%s", changes)
	}
	if !strings.Contains(out, "would create the label cumin/status/checking") || !strings.Contains(out, "would ask  whether to replace them") {
		t.Errorf("the dry run printed\n%s", out)
	}
}
