package cuminmaintainer

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// This file tests the skill host: its script with a fake gh on PATH and a
// fake replace-binary.sh in a checkout of the test, and the fixed texts of
// its SKILL.md.

// fakeMergeGh answers the read number n of the script from the file
// "answer.<n>" of $FAKE_DIR, or from the file "answer" when that file is
// missing. The file holds the text after the jq filter, because the fake
// runs no jq. A file "fail" makes every read fail with its text, and a file
// "sleep" makes every read last that many seconds. A file "delay" makes every
// read answer after that many seconds. A missing file gives an empty read
// with the exit code 0. The fake records its arguments in the
// file "gh-calls".
const fakeMergeGh = `#!/bin/sh
echo "$*" >>"$FAKE_DIR/gh-calls"
n=$(grep -c '' "$FAKE_DIR/gh-calls")
if [ -f "$FAKE_DIR/sleep" ]; then
	exec sleep "$(cat "$FAKE_DIR/sleep")"
fi
if [ -f "$FAKE_DIR/delay" ]; then
	sleep "$(cat "$FAKE_DIR/delay")"
fi
if [ -f "$FAKE_DIR/fail" ]; then
	cat "$FAKE_DIR/fail" >&2
	exit 1
fi
if [ -f "$FAKE_DIR/answer.$n" ]; then
	cat "$FAKE_DIR/answer.$n"
elif [ -f "$FAKE_DIR/answer" ]; then
	cat "$FAKE_DIR/answer"
fi
exit 0
`

// fakeReplaceBinary records one line for each call in the file "tool-calls"
// of $FAKE_DIR, and ends with the exit code of the file "tool-exit".
const fakeReplaceBinary = `#!/bin/sh
echo "called $*" >>"$FAKE_DIR/tool-calls"
echo "step 1 of 5: check the checkout"
if [ -f "$FAKE_DIR/tool-exit" ]; then
	exit "$(cat "$FAKE_DIR/tool-exit")"
fi
exit 0
`

// mergeWait is one directory with a fake gh, a checkout that holds a fake
// replace-binary.sh, and the files of the two fakes.
type mergeWait struct {
	t      *testing.T
	bin    string
	source string
	data   string
}

// newMergeWait returns a mergeWait whose fake gh gives the answers of files.
func newMergeWait(t *testing.T, files map[string]string) *mergeWait {
	t.Helper()
	dir := t.TempDir()
	m := &mergeWait{t: t, bin: filepath.Join(dir, "bin"), source: filepath.Join(dir, "source"), data: filepath.Join(dir, "data")}
	for _, d := range []string{m.bin, filepath.Join(m.source, "scripts"), m.data} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(m.bin, "gh"), []byte(fakeMergeGh), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.source, "scripts", "replace-binary.sh"), []byte(fakeReplaceBinary), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(m.data, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

// env returns the environment of the script: the fake gh first on PATH, the
// directory of the fakes, and source as CUMIN_SOURCE_DIR. An empty source
// leaves the variable unset.
func (m *mergeWait) env(source string) []string {
	var env []string
	for _, pair := range os.Environ() {
		if !strings.HasPrefix(pair, "CUMIN_SOURCE_DIR=") {
			env = append(env, pair)
		}
	}
	env = append(env, "PATH="+m.bin+string(os.PathListSeparator)+os.Getenv("PATH"), "FAKE_DIR="+m.data)
	if source != "" {
		env = append(env, "CUMIN_SOURCE_DIR="+source)
	}
	return env
}

// run runs the script for pull request 12 with an interval of one second and
// the arguments of args. It returns the output and the exit code.
func (m *mergeWait) run(args ...string) (string, int) {
	m.t.Helper()
	return m.runWith(m.env(m.source), append([]string{"some-owner/some-repo", "12", "--interval", "1"}, args...)...)
}

// runWith runs the script with the environment env and the arguments args.
func (m *mergeWait) runWith(env []string, args ...string) (string, int) {
	m.t.Helper()
	cmd := exec.Command(filepath.Join("skills", "host", "wait-for-merge.sh"), args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0
	case errors.As(err, &exit):
		return string(out), exit.ExitCode()
	}
	m.t.Fatalf("the script did not run: %v\n%s", err, out)
	return "", 0
}

// lines returns the lines of a file of the fakes, or nil when it is missing.
func (m *mergeWait) lines(name string) []string {
	data, err := os.ReadFile(filepath.Join(m.data, name))
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func TestWaitForMerge_CallsTheToolOnceForAMergedPullRequest(t *testing.T) {
	t.Parallel()
	m := newMergeWait(t, map[string]string{"answer": "closed true\n"})
	out, code := m.run("--timeout", "5")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	if calls := m.lines("tool-calls"); len(calls) != 1 || calls[0] != "called" {
		t.Errorf("the calls of replace-binary.sh = %q, want exactly one call with no option", calls)
	}
	if !strings.Contains(out, "the pull request 12 of some-owner/some-repo is merged.") || !strings.Contains(out, "step 1 of 5") {
		t.Errorf("the output lacks the merge or the output of the tool\n%s", out)
	}
	// The script only reads GitHub: every call is "gh api" without a method
	// and without a field, which gh sends as GET.
	calls := m.lines("gh-calls")
	if len(calls) != 1 {
		t.Errorf("gh ran %d times, want 1\n%q", len(calls), calls)
	}
	for _, line := range calls {
		words := strings.Fields(line)
		if words[0] != "api" || words[1] != "repos/some-owner/some-repo/pulls/12" {
			t.Errorf("gh ran %q, want gh api for the pull request", line)
		}
		for _, word := range words {
			switch word {
			case "-X", "--method", "-f", "-F", "--field", "--raw-field", "--input":
				t.Errorf("gh ran %q, which can write", line)
			}
		}
	}
}

// The wait goes on over an empty read, a failed state, and an open pull
// request, and it calls the tool one time when the merge shows.
func TestWaitForMerge_WaitsUntilTheMergeShows(t *testing.T) {
	t.Parallel()
	m := newMergeWait(t, map[string]string{"answer.1": "", "answer.2": "open false\n", "answer": "closed true\n"})
	out, code := m.run("--timeout", "30")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	if calls := m.lines("tool-calls"); len(calls) != 1 {
		t.Errorf("replace-binary.sh ran %d times, want 1", len(calls))
	}
	if calls := m.lines("gh-calls"); len(calls) != 3 {
		t.Errorf("gh ran %d times, want 3", len(calls))
	}
}

// No read but "closed" with "merged: true" is a merge. At the limit, the
// script says so with the last read, and it replaces nothing.
func TestWaitForMerge_EndsAtTheTimeLimitWithNoCallOfTheTool(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"an empty answer of gh", nil, "the last read: gh returned nothing"},
		{"a failed gh", map[string]string{"fail": "gh: Server Error (HTTP 502)\n"}, "the last read: gh could not read the pull request: gh: Server Error (HTTP 502)"},
		{"an open pull request", map[string]string{"answer": "open false\n"}, "the last read: the pull request is open"},
		{"an answer that is not a state", map[string]string{"answer": "true\n"}, "the last read: gh returned 'true', which is not a state of a pull request"},
		{"an open pull request that says merged", map[string]string{"answer": "open true\n"}, "which is not a state of a pull request"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := newMergeWait(t, tt.files)
			// The limit is far above a slow start of the fake on a busy
			// machine, so the wait holds at least two reads.
			out, code := m.run("--timeout", "10")
			if code != exitTimeLimit {
				t.Fatalf("exit code = %d, want %d\n%s", code, exitTimeLimit, out)
			}
			for _, want := range []string{"time limit", tt.want, "Nothing is replaced."} {
				if !strings.Contains(out, want) {
					t.Errorf("the output lacks %q\n%s", want, out)
				}
			}
			if calls := m.lines("tool-calls"); calls != nil {
				t.Errorf("replace-binary.sh ran before the limit: %q", calls)
			}
			if calls := m.lines("gh-calls"); len(calls) < 2 {
				t.Errorf("gh ran %d times, want a read again after the interval", len(calls))
			}
		})
	}
}

func TestWaitForMerge_FailsForAPullRequestClosedWithoutAMerge(t *testing.T) {
	t.Parallel()
	m := newMergeWait(t, map[string]string{"answer": "closed false\n"})
	start := time.Now()
	out, code := m.run("--timeout", "60")
	if code != 1 || !strings.Contains(out, "error: the pull request 12 of some-owner/some-repo is closed without a merge. Nothing is replaced.") {
		t.Errorf("exit code = %d, want 1 and the closed pull request\n%s", code, out)
	}
	if calls := m.lines("tool-calls"); calls != nil {
		t.Errorf("replace-binary.sh ran for a pull request without a merge: %q", calls)
	}
	if took := time.Since(start); took > 30*time.Second {
		t.Errorf("the script took %s, want the end at the first read", took)
	}
}

// A read at the end of the wait has its full time, so the message at the
// limit names what that read saw, and not a read that the script cut.
func TestWaitForMerge_NamesTheLastReadOfASlowGhAtTheTimeLimit(t *testing.T) {
	t.Parallel()
	m := newMergeWait(t, map[string]string{"delay": "2", "answer": "open false\n"})
	out, code := m.run("--timeout", "5")
	if code != exitTimeLimit || !strings.Contains(out, "time limit") || !strings.Contains(out, "(the last read: the pull request is open)") {
		t.Errorf("exit code = %d, want %d and the open pull request as the last read\n%s", code, exitTimeLimit, out)
	}
	if strings.Contains(out, "gh did not end") {
		t.Errorf("the script cut a read before its limit\n%s", out)
	}
	if calls := m.lines("tool-calls"); calls != nil {
		t.Errorf("replace-binary.sh ran: %q", calls)
	}
}

// A gh that hangs does not hold the script: each read ends at the limit of
// one read, which the test sets to one second.
func TestWaitForMerge_EndsAtTheTimeLimitWhenGhHangs(t *testing.T) {
	t.Parallel()
	m := newMergeWait(t, map[string]string{"sleep": "60"})
	start := time.Now()
	out, code := m.runWith(append(m.env(m.source), "CUMIN_MERGE_READ_LIMIT=1"), "some-owner/some-repo", "12", "--interval", "1", "--timeout", "1")
	if code != exitTimeLimit || !strings.Contains(out, "time limit") || !strings.Contains(out, "gh did not end in 1 seconds") {
		t.Errorf("exit code = %d, want %d and the read that did not end\n%s", code, exitTimeLimit, out)
	}
	if calls := m.lines("tool-calls"); calls != nil {
		t.Errorf("replace-binary.sh ran: %q", calls)
	}
	if took := time.Since(start); took > 30*time.Second {
		t.Errorf("the script took %s with a time limit of 1 second", took)
	}
}

func TestWaitForMerge_ExitsOneWhenTheToolFails(t *testing.T) {
	t.Parallel()
	m := newMergeWait(t, map[string]string{"answer": "closed true\n", "tool-exit": "2\n"})
	out, code := m.run("--timeout", "5")
	if code != 1 || !strings.Contains(out, "error: replace-binary.sh ended with the exit code 2.") {
		t.Errorf("exit code = %d, want 1 and the exit code of the tool\n%s", code, out)
	}
	if calls := m.lines("tool-calls"); len(calls) != 1 {
		t.Errorf("replace-binary.sh ran %d times, want 1", len(calls))
	}
}

// A wrong call ends before the first read of GitHub.
func TestWaitForMerge_RefusesAWrongCall(t *testing.T) {
	t.Parallel()
	m := newMergeWait(t, map[string]string{"answer": "closed true\n"})
	missing := filepath.Join(m.source, "elsewhere")
	tests := []struct {
		name   string
		source string
		args   []string
		want   string
	}{
		{"no argument", m.source, nil, "usage: wait-for-merge.sh"},
		{"no number", m.source, []string{"some-owner/some-repo"}, "usage: wait-for-merge.sh"},
		{"a repository without an owner", m.source, []string{"some-repo", "12"}, "the repository must be <owner>/<repo>"},
		{"a number that is a word", m.source, []string{"some-owner/some-repo", "twelve"}, "the number of the pull request must be a number"},
		{"a third argument", m.source, []string{"some-owner/some-repo", "12", "13"}, "unknown argument '13'"},
		{"an unknown option", m.source, []string{"some-owner/some-repo", "12", "--force"}, "unknown argument '--force'"},
		{"a time limit without a value", m.source, []string{"some-owner/some-repo", "12", "--timeout"}, "--timeout needs a value"},
		{"a time limit of zero", m.source, []string{"some-owner/some-repo", "12", "--timeout", "0"}, "--timeout must be a number of seconds"},
		{"no CUMIN_SOURCE_DIR", "", []string{"some-owner/some-repo", "12"}, "CUMIN_SOURCE_DIR is not set"},
		{"a CUMIN_SOURCE_DIR without the tool", missing, []string{"some-owner/some-repo", "12"}, "scripts/replace-binary.sh' is not a file that the script can run"},
	}
	for _, tt := range tests {
		out, code := m.runWith(m.env(tt.source), tt.args...)
		if code != 2 || !strings.Contains(out, tt.want) {
			t.Errorf("%s: exit code = %d, want 2 and %q\n%s", tt.name, code, tt.want, out)
		}
	}
	if calls := m.lines("gh-calls"); calls != nil {
		t.Errorf("gh ran after a wrong call: %q", calls)
	}
	if calls := m.lines("tool-calls"); calls != nil {
		t.Errorf("replace-binary.sh ran after a wrong call: %q", calls)
	}
}

// The skill calls the three general tools and its script, and it sends the
// reader to the guide for the options of the tools.
func TestHostSkill_NamesEveryCommandAndLinksToTheGuide(t *testing.T) {
	text := readText(filepath.Join("skills", "host"), "SKILL.md")
	for _, want := range []string{
		"The skill adds no step of its own",
		"When `CUMIN_SOURCE_DIR` is not set, ask the Maintainer for the path",
		// The shell expands "$CUMIN_SOURCE_DIR" before an assignment in front
		// of the command, so the skill sets the variable first.
		"set the variable first in the same command: `export CUMIN_SOURCE_DIR=<path>; <command>`",
		"| `replace-binary.sh` | A merge changed code outside the tests, and cumin must run the new binary | Yes.",
		"| `live-scenario.sh` | The Maintainer asks for the live scenario E2E-1 | Yes.",
		"## The new binary before the next approval\n",
		"replace the binary before the session approves the next pull request",
		"| 124 | The time limit ended before a read showed the merge. The message holds \"time limit\" |",
		"Only the exit code 0 is a success.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("SKILL.md lacks %q", want)
		}
	}

	if strings.Contains(text, "`CUMIN_SOURCE_DIR=<path>") {
		t.Error("SKILL.md gives the assignment in front of the command, which the shell applies after it expands the command")
	}

	commands, _ := section(text, "Commands")
	if !strings.Contains(commands, "`$CUMIN_SOURCE_DIR/docs/ja/guides/host-tools.md`") {
		t.Error("the section \"## Commands\" does not link to host-tools.md for the options of the tools")
	}
	read, changes, ok := strings.Cut(commands, "Changes the Host, so ask first:")
	if !ok {
		t.Fatal("the section \"## Commands\" does not mark the commands that change the Host")
	}
	if !strings.Contains(read, "- `\"$CUMIN_SOURCE_DIR\"/scripts/cumin-health.sh`") {
		t.Error("cumin-health.sh must stand under the commands that only read")
	}
	for _, command := range []string{
		"- `\"$CUMIN_SOURCE_DIR\"/scripts/replace-binary.sh`",
		"- `\"$CUMIN_SOURCE_DIR\"/scripts/live-scenario.sh --repo <owner>/<sandbox> --config <file>`",
		"- `${CLAUDE_SKILL_DIR}/wait-for-merge.sh <owner>/<repository> <number>`",
	} {
		if strings.Contains(read, command) || !strings.Contains(changes, command) {
			t.Errorf("%q must stand only under the commands that change the Host", command)
		}
	}
	// Every call of a tool, of the script, and of git in the text of the skill
	// stands under "## Commands".
	for _, command := range regexp.MustCompile("`((?:\"\\$CUMIN_SOURCE_DIR\"|\\$\\{CLAUDE_SKILL_DIR\\}|git )[^`]*)`").FindAllStringSubmatch(text, -1) {
		if !strings.Contains(commands, "- `"+command[1]+"`") {
			t.Errorf("the section \"## Commands\" lacks %q", command[1])
		}
	}
	// Each tool that the skill calls is a tool of the repository.
	for _, tool := range []string{"cumin-health.sh", "replace-binary.sh", "live-scenario.sh"} {
		if _, err := os.Stat(filepath.Join("..", "..", "scripts", tool)); err != nil {
			t.Errorf("the skill calls %s, which scripts/ does not hold: %v", tool, err)
		}
	}
}
