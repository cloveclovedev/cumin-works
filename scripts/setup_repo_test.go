package scripts

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// setupRepoTools are the standard tools that the labels step of
// scripts/setup-repo.sh may call. Every run of the tests has only these and
// the fake gh on PATH, so a call of another tool fails the tests.
var setupRepoTools = []string{"cat", "tr", "grep", "awk"}

// The fake gh holds the labels of the repository in the file LABELS, one
// line for each label, in the form that the script asks of gh: name, color,
// and description with a tab between them. It writes every call to the file
// CALLS, and a PATCH, a POST, or a DELETE changes LABELS, so that a second
// run of the script reads what the first run wrote. The file ISSUES holds one
// line for each issue or pull request that carries a label: the label, the
// state, and the number, with a tab between them.
const setupRepoFakeGh = `#!/bin/sh
echo "gh $*" >>"$CALLS"
case "$*" in
  "api --paginate repos/acme/app/labels --jq "*) cat "$LABELS" ;;
  "api -X PATCH repos/acme/app/labels/"*)
    NAME="${4#repos/acme/app/labels/}" COLOR="${6#color=}" DESCRIPTION="${8#description=}" awk -F '\t' '
      $1 == ENVIRON["NAME"] { print $1 "\t" ENVIRON["COLOR"] "\t" ENVIRON["DESCRIPTION"]; next }
      { print }
    ' "$LABELS" >"$LABELS.new"
    cat "$LABELS.new" >"$LABELS" ;;
  "api -X POST repos/acme/app/labels "*)
    printf '%s\t%s\t%s\n' "${6#name=}" "${8#color=}" "${10#description=}" >>"$LABELS" ;;
  "api --paginate --method GET repos/acme/app/issues -f labels="*)
    NAME="${7#labels=}" STATE="${9#state=}" awk -F '\t' '
      $1 == ENVIRON["NAME"] && $2 == ENVIRON["STATE"] { print $3 }
    ' "$ISSUES" ;;
  "api -X DELETE repos/acme/app/labels/"*)
    NAME="${4#repos/acme/app/labels/}" awk -F '\t' '
      tolower($1) != ENVIRON["NAME"] { print }
    ' "$LABELS" >"$LABELS.new"
    cat "$LABELS.new" >"$LABELS" ;;
  *) echo "fake gh: unexpected call: $*" >&2; exit 1 ;;
esac
`

// setupRepo is the labels step of scripts/setup-repo.sh on a repository
// that the fake gh holds in a temporary directory.
type setupRepo struct {
	t      *testing.T
	bin    string
	work   string
	calls  string
	labels string
	issues string
	dryRun bool
	// input is what the person who runs the script types. Empty is no input.
	input string
}

// newSetupRepo returns a repository with the given labels, each one as
// "name|color|description", whose .cumin/config.toml names no priority label.
func newSetupRepo(t *testing.T, labels ...string) *setupRepo {
	t.Helper()
	dir := t.TempDir()
	s := &setupRepo{
		t:      t,
		bin:    filepath.Join(dir, "bin"),
		work:   filepath.Join(dir, "work"),
		calls:  filepath.Join(dir, "calls"),
		labels: filepath.Join(dir, "labels"),
		issues: filepath.Join(dir, "issues"),
	}
	for _, d := range []string{s.bin, s.work} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, tool := range setupRepoTools {
		path, err := exec.LookPath(tool)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(path, filepath.Join(s.bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(s.bin, "gh"), []byte(setupRepoFakeGh), 0o700); err != nil {
		t.Fatal(err)
	}
	var lines strings.Builder
	for _, label := range labels {
		lines.WriteString(strings.Replace(label, "|", "\t", 2) + "\n")
	}
	s.write(s.labels, lines.String())
	s.write(s.calls, "")
	s.write(s.issues, "")
	s.namePriorityLabels()
	return s
}

// carry puts the label on that many open and closed issues and pull requests.
func (s *setupRepo) carry(label string, open, closed int) {
	s.t.Helper()
	var lines strings.Builder
	for i := range open + closed {
		state := "open"
		if i >= open {
			state = "closed"
		}
		fmt.Fprintf(&lines, "%s\t%s\t%d\n", label, state, i+1)
	}
	s.write(s.issues, lines.String())
}

func (s *setupRepo) write(path, content string) {
	s.t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		s.t.Fatal(err)
	}
}

// namePriorityLabels puts the names that the script read from
// priority_labels of .cumin/config.toml, as the step before the labels step
// does.
func (s *setupRepo) namePriorityLabels(names ...string) {
	s.t.Helper()
	var lines strings.Builder
	for _, name := range names {
		lines.WriteString(name + "\n")
	}
	s.write(filepath.Join(s.work, "priority-labels"), lines.String())
}

// run runs the labels step of the script alone, with the settings that the
// script has at that step, and returns what it printed.
func (s *setupRepo) run() string {
	s.t.Helper()
	script, err := os.ReadFile("setup-repo.sh")
	if err != nil {
		s.t.Fatal(err)
	}
	program := "set -eu\nrepo=acme/app\nconfig_path=.cumin/config.toml\nwork=\"$WORK\"\ndry_run=\"$DRY_RUN\"\n"
	for _, name := range []string{"die", "repository_labels", "default_priority_labels", "update_labels", "retired_labels", "count_carriers", "delete_retired_labels", "apply_repository_labels"} {
		function := regexp.MustCompile(`(?ms)^` + name + `\(\) \{\n.*?^\}\n`).Find(script)
		if function == nil {
			s.t.Fatalf("scripts/setup-repo.sh has no function %s", name)
		}
		program += string(function)
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		s.t.Fatal(err)
	}
	dryRun := "0"
	if s.dryRun {
		dryRun = "1"
	}
	command := exec.Command(sh, "-c", program+"apply_repository_labels")
	command.Env = []string{"PATH=" + s.bin, "WORK=" + s.work, "CALLS=" + s.calls, "LABELS=" + s.labels, "ISSUES=" + s.issues, "DRY_RUN=" + dryRun}
	command.Stdin = strings.NewReader(s.input)
	out, err := command.CombinedOutput()
	if err != nil {
		s.t.Fatalf("the labels step failed: %v\n%s", err, out)
	}
	return string(out)
}

// writes returns the calls of the fake gh that change the repository, one
// line for each call, and forgets them.
func (s *setupRepo) writes() []string {
	s.t.Helper()
	calls, err := os.ReadFile(s.calls)
	if err != nil {
		s.t.Fatal(err)
	}
	s.write(s.calls, "")
	var writes []string
	for _, call := range strings.Split(strings.TrimSpace(string(calls)), "\n") {
		if call != "" && !strings.HasPrefix(call, "gh api --paginate ") {
			writes = append(writes, call)
		}
	}
	return writes
}

// hasLine reports whether the output holds exactly this line.
func hasLine(out, line string) bool {
	for _, l := range strings.Split(out, "\n") {
		if l == line {
			return true
		}
	}
	return false
}

// labelsOfCumin returns the labels of cumin as the repository holds them
// after a run of the script: the lists of the script itself.
func labelsOfCumin(t *testing.T, functions ...string) []string {
	t.Helper()
	script, err := os.ReadFile("setup-repo.sh")
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, name := range functions {
		list := regexp.MustCompile(`(?ms)^` + name + `\(\) \{\n  cat <<'LABELS'\n(.*?)^LABELS\n\}\n`).FindSubmatch(script)
		if list == nil {
			t.Fatalf("scripts/setup-repo.sh has no list %s", name)
		}
		labels = append(labels, strings.Split(strings.TrimSpace(string(list[1])), "\n")...)
	}
	return labels
}

const priorityLabel = "cumin/priority/P1|D4C5F9|A Maintainer says: start this before a lower priority"

func TestSetupRepo_UpdatesALabelWhoseColorOrDescriptionDiffers(t *testing.T) {
	for name, label := range map[string]string{
		"another description": "cumin/status/ready|0E8A16|The Owner says: this issue can start",
		"another color":       "cumin/status/ready|FFFFFF|A Maintainer says: this issue can start",
		"no description":      "cumin/status/ready|0E8A16|",
	} {
		t.Run(name, func(t *testing.T) {
			s := newSetupRepo(t, label)
			out := s.run()
			if !hasLine(out, "updated    label cumin/status/ready") {
				t.Errorf("the script does not say that it updated the label:\n%s", out)
			}
			want := "gh api -X PATCH repos/acme/app/labels/cumin/status/ready -f color=0E8A16 -f description=A Maintainer says: this issue can start"
			var patches []string
			for _, call := range s.writes() {
				if strings.HasPrefix(call, "gh api -X PATCH ") {
					patches = append(patches, call)
				}
			}
			if len(patches) != 1 || patches[0] != want {
				t.Errorf("the updates are\n%s\nwant one:\n%s", strings.Join(patches, "\n"), want)
			}
		})
	}
}

func TestSetupRepo_LeavesALabelThatEqualsTheList(t *testing.T) {
	// The repository has every label of cumin but the first one. GitHub
	// ignores the case of a name, and a color in lower case is the same color.
	labels := labelsOfCumin(t, "repository_labels")[1:]
	for i, label := range labels {
		if strings.HasPrefix(label, "risk/low|") {
			labels[i] = "Risk/Low|c2e0c6|A few lines with an obvious effect; cumin merges"
		}
	}
	s := newSetupRepo(t, labels...)
	out := s.run()
	for _, line := range []string{"unchanged  label cumin/status/ready", "unchanged  label risk/low", "created    label cumin/type/requirement"} {
		if !hasLine(out, line) {
			t.Errorf("the output has no line %q:\n%s", line, out)
		}
	}
	if strings.Contains(out, "updated") {
		t.Errorf("the script updated a label that equals the list:\n%s", out)
	}
	writes := s.writes()
	if len(writes) != 1 || !strings.HasPrefix(writes[0], "gh api -X POST repos/acme/app/labels -f name=cumin/type/requirement ") {
		t.Errorf("the writes are %q, want only the creation of the missing label", writes)
	}
}

func TestSetupRepo_SecondRunWritesNothing(t *testing.T) {
	s := newSetupRepo(t,
		"cumin/status/ready|FFFFFF|The Owner says: this issue can start",
		"cumin/priority/P1|000000|old",
	)
	s.run()
	if writes := s.writes(); len(writes) == 0 {
		t.Fatal("the first run wrote nothing")
	}
	out := s.run()
	if writes := s.writes(); len(writes) != 0 {
		t.Errorf("the second run wrote %q", writes)
	}
	for _, label := range append(labelsOfCumin(t, "repository_labels"), priorityLabel) {
		if line := "unchanged  label " + strings.SplitN(label, "|", 2)[0]; !hasLine(out, line) {
			t.Errorf("the second run has no line %q:\n%s", line, out)
		}
	}
}

func TestSetupRepo_UpdatesADefaultPriorityLabelAndCreatesNone(t *testing.T) {
	s := newSetupRepo(t, append(labelsOfCumin(t, "repository_labels"), "cumin/priority/P1|000000|old", strings.Replace(priorityLabel, "P1", "P2", 1))...)
	out := s.run()
	for _, line := range []string{"updated    label cumin/priority/P1", "unchanged  label cumin/priority/P2"} {
		if !hasLine(out, line) {
			t.Errorf("the output has no line %q:\n%s", line, out)
		}
	}
	want := "gh api -X PATCH repos/acme/app/labels/cumin/priority/P1 -f color=D4C5F9 -f description=A Maintainer says: start this before a lower priority"
	if writes := s.writes(); len(writes) != 1 || writes[0] != want {
		t.Errorf("the writes are %q, want only\n%s", writes, want)
	}
}

func TestSetupRepo_NeverChangesALabelWhenTheSettingsNamePriorityLabels(t *testing.T) {
	s := newSetupRepo(t, append(labelsOfCumin(t, "repository_labels")[1:],
		"cumin/priority/P1|000000|old",
		"Cumin/Type/Requirement|000000|the priority label of the organization",
		"priority/high|000000|another priority label of the organization",
	)...)
	s.namePriorityLabels("cumin/type/requirement", "priority/high")
	out := s.run()
	if writes := s.writes(); len(writes) != 0 {
		t.Errorf("the script wrote %q", writes)
	}
	if strings.Contains(out, "cumin/priority/") || strings.Contains(out, "priority/high") {
		t.Errorf("the script worked on a priority label:\n%s", out)
	}
	if want := "kept       label cumin/type/requirement (.cumin/config.toml names it as a priority label)"; !hasLine(out, want) {
		t.Errorf("the output has no line %q:\n%s", want, out)
	}
}

func TestSetupRepo_DryRunChangesNoLabel(t *testing.T) {
	s := newSetupRepo(t, "cumin/status/ready|FFFFFF|old", "cumin/priority/P1|000000|old", "risk/low|C2E0C6|A few lines with an obvious effect; cumin merges")
	s.dryRun = true
	out := s.run()
	for _, line := range []string{
		"would update the label cumin/status/ready",
		"would update the label cumin/priority/P1",
		"would create the label cumin/type/requirement",
		"unchanged  label risk/low",
	} {
		if !hasLine(out, line) {
			t.Errorf("the output has no line %q:\n%s", line, out)
		}
	}
	if writes := s.writes(); len(writes) != 0 {
		t.Errorf("the dry run wrote %q", writes)
	}
}

const retiredLabel = "cumin/status/awaiting-checks"

// newSetupRepoWithRetiredLabel returns a repository that holds every label
// of cumin, one retired label, and one label outside both lists.
func newSetupRepoWithRetiredLabel(t *testing.T) *setupRepo {
	t.Helper()
	return newSetupRepo(t, append(labelsOfCumin(t, "repository_labels"),
		"Cumin/Status/Awaiting-Checks|BFD4F2|GitHub runs the required checks",
		"bug|D73A4A|Something does not work",
	)...)
}

func TestSetupRepo_DeletesARetiredLabelAfterTheAnswerY(t *testing.T) {
	s := newSetupRepoWithRetiredLabel(t)
	s.carry(retiredLabel, 0, 2)
	s.input = "y\n"
	out := s.run()
	if line := "retired    label " + retiredLabel + ": 0 open and 2 closed issues and pull requests carry it"; !hasLine(out, line) {
		t.Errorf("the output has no line %q:\n%s", line, out)
	}
	// The answer comes from a file, so the question and the next line share one line.
	if !strings.Contains(out, "removes the label from every closed issue and pull request. [y/N] deleted    label "+retiredLabel+"\n") {
		t.Errorf("the script does not ask what the question must say, or does not say that it deleted the label:\n%s", out)
	}
	want := "gh api -X DELETE repos/acme/app/labels/" + retiredLabel
	if writes := s.writes(); len(writes) != 1 || writes[0] != want {
		t.Errorf("the writes are %q, want only\n%s", writes, want)
	}
	// The label is gone, so a second run asks nothing and writes nothing.
	out = s.run()
	if writes := s.writes(); len(writes) != 0 || strings.Contains(out, retiredLabel) {
		t.Errorf("the second run wrote %q and printed:\n%s", writes, out)
	}
}

func TestSetupRepo_KeepsARetiredLabelWithoutTheAnswerY(t *testing.T) {
	for name, input := range map[string]string{
		"the answer n":    "n\n",
		"the answer yes":  "yes\n",
		"an empty answer": "\n",
		"no input":        "",
	} {
		t.Run(name, func(t *testing.T) {
			s := newSetupRepoWithRetiredLabel(t)
			s.carry(retiredLabel, 0, 2)
			s.input = input
			out := s.run()
			if !strings.Contains(out, "Delete the label "+retiredLabel) {
				t.Errorf("the script did not ask:\n%s", out)
			}
			if writes := s.writes(); len(writes) != 0 {
				t.Errorf("the script wrote %q", writes)
			}
			if strings.Contains(out, "deleted") {
				t.Errorf("the script says that it deleted a label:\n%s", out)
			}
		})
	}
}

func TestSetupRepo_NeverDeletesARetiredLabelThatAnOpenIssueCarries(t *testing.T) {
	s := newSetupRepoWithRetiredLabel(t)
	s.carry(retiredLabel, 1, 2)
	s.input = "y\n"
	out := s.run()
	if want := "kept       label " + retiredLabel + ": 1 open issues and pull requests carry it"; !hasLine(out, want) {
		t.Errorf("the output has no line %q:\n%s", want, out)
	}
	if strings.Contains(out, "Delete the label") {
		t.Errorf("the script asked:\n%s", out)
	}
	if writes := s.writes(); len(writes) != 0 {
		t.Errorf("the script wrote %q", writes)
	}
}

func TestSetupRepo_LeavesALabelOutsideTheListsAndAMissingRetiredLabel(t *testing.T) {
	// The repository holds no retired label, and one label outside both lists.
	s := newSetupRepo(t, append(labelsOfCumin(t, "repository_labels"), "bug|D73A4A|Something does not work")...)
	s.input = "y\n"
	out := s.run()
	if strings.Contains(out, "Delete the label") || strings.Contains(out, "retired") || strings.Contains(out, "bug") {
		t.Errorf("the script worked on a label outside the lists, or on a label that the repository does not hold:\n%s", out)
	}
	if writes := s.writes(); len(writes) != 0 {
		t.Errorf("the script wrote %q", writes)
	}
}

func TestSetupRepo_NeverDeletesARetiredLabelThatTheSettingsNameAsAPriorityLabel(t *testing.T) {
	s := newSetupRepoWithRetiredLabel(t)
	s.namePriorityLabels(retiredLabel)
	s.carry(retiredLabel, 0, 2)
	s.input = "y\n"
	out := s.run()
	if want := "kept       label " + retiredLabel + " (.cumin/config.toml names it as a priority label)"; !hasLine(out, want) {
		t.Errorf("the output has no line %q:\n%s", want, out)
	}
	if writes := s.writes(); len(writes) != 0 {
		t.Errorf("the script wrote %q", writes)
	}
}

func TestSetupRepo_DryRunAsksNothingAndDeletesNoRetiredLabel(t *testing.T) {
	s := newSetupRepoWithRetiredLabel(t)
	s.carry(retiredLabel, 0, 2)
	s.dryRun = true
	s.input = "y\n"
	out := s.run()
	for _, line := range []string{
		"retired    label " + retiredLabel + ": 0 open and 2 closed issues and pull requests carry it",
		"would ask  whether to delete the label " + retiredLabel,
	} {
		if !hasLine(out, line) {
			t.Errorf("the output has no line %q:\n%s", line, out)
		}
	}
	if writes := s.writes(); len(writes) != 0 {
		t.Errorf("the dry run wrote %q", writes)
	}
}
