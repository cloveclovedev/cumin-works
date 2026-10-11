package scripts

import (
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
// CALLS, and a PATCH or a POST changes LABELS, so that a second run of the
// script reads what the first run wrote.
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
	dryRun bool
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
	s.namePriorityLabels()
	return s
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
	for _, name := range []string{"die", "repository_labels", "default_priority_labels", "update_labels", "apply_repository_labels"} {
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
	command.Env = []string{"PATH=" + s.bin, "WORK=" + s.work, "CALLS=" + s.calls, "LABELS=" + s.labels, "DRY_RUN=" + dryRun}
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
