package cuminmaintainer

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// This file tests the skill watch: its script with a monitor file of the
// test, and the fixed texts of its SKILL.md.

// exitTimeLimit is the exit code of the script when the time limit ended
// with no new entry.
const exitTimeLimit = 124

// The two entries of the golden monitor file, as the script prints them.
const (
	goldenMergeDecision = "merge-decision\texample/tool\t9\tfix(api): return 404 for a missing item\thttps://github.com/example/tool/pull/14\n"
	goldenDecision      = "decision\texample/app\t31\tShow the history of an item\thttps://github.com/example/app/issues/31\n"
)

// goldenMonitorFile returns the monitor file that the acceptance test of
// cumin writes and that the menu bar app reads.
func goldenMonitorFile(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "internal", "workflow", "testdata", "monitor-file.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// monitorFileWith returns a monitor file whose last poll ended at the time
// at, with the entries of waiting.
func monitorFileWith(at time.Time, waiting string) []byte {
	return []byte(`{"version": 1, "last_poll": {"at": "` + at.UTC().Format(time.RFC3339) + `", "errors": []}, "waiting": [` + waiting + `]}`)
}

// watch is one directory with a monitor file and a --seen file.
type watch struct {
	t     *testing.T
	file  string
	seen  string
	calls string
}

// newWatch returns a watch whose monitor file holds content. A nil content
// leaves the monitor file missing.
func newWatch(t *testing.T, content []byte) *watch {
	t.Helper()
	dir := t.TempDir()
	w := &watch{t: t, file: filepath.Join(dir, "monitor.json"), seen: filepath.Join(dir, "seen"), calls: filepath.Join(dir, "calls")}
	if content != nil {
		w.write(content)
	}
	// A fake gh and a fake curl stand first on PATH and record a call, so
	// that a test sees a read of GitHub or of the network.
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gh", "curl"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\necho \""+name+" $*\" >>\"$FAKE_CALLS\"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

// write replaces the monitor file in one step, as cumin does.
func (w *watch) write(content []byte) {
	w.t.Helper()
	tmp := w.file + ".tmp"
	if err := os.WriteFile(tmp, content, 0o600); err != nil {
		w.t.Fatal(err)
	}
	if err := os.Rename(tmp, w.file); err != nil {
		w.t.Fatal(err)
	}
}

// command returns the script with the monitor file and the --seen file of w,
// an interval of one second, and the arguments of args.
func (w *watch) command(args ...string) (*exec.Cmd, *bytes.Buffer, *bytes.Buffer) {
	args = append([]string{"--file", w.file, "--seen", w.seen, "--interval", "1"}, args...)
	cmd := exec.Command(filepath.Join("skills", "watch", "wait-for-waiting.sh"), args...)
	cmd.Env = append(os.Environ(), "PATH="+filepath.Join(filepath.Dir(w.file), "bin")+string(os.PathListSeparator)+os.Getenv("PATH"), "FAKE_CALLS="+w.calls)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	return cmd, &stdout, &stderr
}

// exitCode returns the exit code of a command that ended with err.
func (w *watch) exitCode(err error) int {
	w.t.Helper()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.ExitCode()
	}
	w.t.Fatalf("the script did not run: %v", err)
	return 0
}

// run runs the script to its end, and returns the entries that it printed,
// its messages, and its exit code.
func (w *watch) run(args ...string) (string, string, int) {
	w.t.Helper()
	cmd, stdout, stderr := w.command(args...)
	code := w.exitCode(cmd.Run())
	return stdout.String(), stderr.String(), code
}

// seenText returns the text of the --seen file, or "" when it is missing.
func (w *watch) seenText() string {
	data, _ := os.ReadFile(w.seen)
	return string(data)
}

func TestWaitForWaiting_PrintsTheEntriesOfTheGoldenFileOnce(t *testing.T) {
	t.Parallel()
	w := newWatch(t, goldenMonitorFile(t))

	entries, messages, code := w.run("--timeout", "5")
	if code != 0 || entries != goldenMergeDecision+goldenDecision {
		t.Fatalf("the first call: exit code = %d, entries = %q, want 0 and both entries of the golden file\n%s", code, entries, messages)
	}

	entries, messages, code = w.run("--timeout", "2")
	if entries != "" {
		t.Errorf("the second call with the same --seen file printed %q, want nothing", entries)
	}
	if code != exitTimeLimit || !strings.Contains(messages, "time limit") {
		t.Errorf("the second call: exit code = %d, messages = %q, want %d and \"time limit\"", code, messages, exitTimeLimit)
	}
	// The last poll of the golden file is of 2026-10-04.
	if !strings.Contains(messages, "last_poll.at is 2026-10-04T07:00:05Z") || !strings.Contains(messages, "cumin may not run") {
		t.Errorf("the message at the limit does not name the old last_poll.at:\n%s", messages)
	}

	// The script reads one local file.
	if calls, err := os.ReadFile(w.calls); err == nil {
		t.Errorf("the script called gh or the network:\n%s", calls)
	}
	script := readText(filepath.Join("skills", "watch"), "wait-for-waiting.sh")
	code2 := regexp.MustCompile(`(?m)^\s*#.*$`).ReplaceAllString(script, "")
	if found := regexp.MustCompile(`\b(gh|curl|wget|nc|ssh|jq|python3?)\b`).FindString(code2); found != "" {
		t.Errorf("the script names %q, which is a read of the network or no standard tool of macOS", found)
	}
}

func TestWaitForWaiting_PrintsAloneAnEntryAddedDuringTheWait(t *testing.T) {
	t.Parallel()
	first := `{"repository": "example/tool", "issue": 9, "kind": "merge-decision", "title": "fix(api): return 404", "url": "https://example.test/pull/14"}`
	added := `{"repository": "example/app", "issue": 31, "kind": "plan-review", "title": "Show the history", "url": "https://example.test/issues/31"}`
	w := newWatch(t, monitorFileWith(time.Now(), first))
	if entries, messages, code := w.run("--timeout", "5"); code != 0 || !strings.HasPrefix(entries, "merge-decision\texample/tool\t9\t") {
		t.Fatalf("the first call: exit code = %d, entries = %q\n%s", code, entries, messages)
	}

	cmd, stdout, stderr := w.command("--timeout", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// The script reads the file at its start and then every second, so it
	// waits when the entry arrives.
	time.Sleep(1500 * time.Millisecond)
	w.write(monitorFileWith(time.Now(), first+","+added))
	code := w.exitCode(cmd.Wait())
	want := "plan-review\texample/app\t31\tShow the history\thttps://example.test/issues/31\n"
	if code != 0 || stdout.String() != want {
		t.Errorf("exit code = %d, entries = %q, want 0 and only the added entry %q\n%s", code, stdout.String(), want, stderr.String())
	}
}

func TestWaitForWaiting_SkipsAReadThatFailsAndKeepsTheSeenFile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content []byte
		reason  string
	}{
		{"an empty file", []byte{}, "the file is empty"},
		{"a missing file", nil, "the file is missing"},
		{"a file that is not JSON", []byte(`{"version": 1, "waiting": [`), "the file is not valid JSON"},
		{"a file of a newer version", []byte(`{"version": 2, "waiting": []}`), "not a monitor file of version 1"},
		{"a file with no list of waiting entries", []byte(`{"version": 1}`), `no list "waiting"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := newWatch(t, goldenMonitorFile(t))
			if _, messages, code := w.run("--timeout", "5"); code != 0 {
				t.Fatalf("the first call: exit code = %d\n%s", code, messages)
			}
			seen := w.seenText()
			if strings.Count(seen, "\n") != 2 {
				t.Fatalf("the --seen file holds %q, want the two entries of the golden file", seen)
			}

			// The good file comes back during the wait, with the same
			// entries: a failed read in between made none of them new.
			if tt.content == nil {
				if err := os.Remove(w.file); err != nil {
					t.Fatal(err)
				}
			} else {
				w.write(tt.content)
			}
			cmd, stdout, stderr := w.command("--timeout", "4")
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			time.Sleep(1500 * time.Millisecond)
			if got := w.seenText(); got != seen {
				t.Errorf("the --seen file changed to %q during the failed reads, want %q", got, seen)
			}
			w.write(goldenMonitorFile(t))
			code := w.exitCode(cmd.Wait())
			if stdout.String() != "" || code != exitTimeLimit {
				t.Errorf("exit code = %d, entries = %q, want %d and no entry\n%s", code, stdout.String(), exitTimeLimit, stderr.String())
			}
			if got := w.seenText(); got != seen {
				t.Errorf("the --seen file holds %q after the wait, want %q", got, seen)
			}

			// With only failed reads, the message at the limit says why.
			if tt.content == nil {
				if err := os.Remove(w.file); err != nil {
					t.Fatal(err)
				}
			} else {
				w.write(tt.content)
			}
			entries, messages, code := w.run("--timeout", "1")
			if entries != "" || code != exitTimeLimit {
				t.Errorf("exit code = %d, entries = %q, want %d and no entry", code, entries, exitTimeLimit)
			}
			for _, want := range []string{"time limit", tt.reason, "nothing is known about the issues that wait"} {
				if !strings.Contains(messages, want) {
					t.Errorf("the message at the limit lacks %q:\n%s", want, messages)
				}
			}
			if got := w.seenText(); got != seen {
				t.Errorf("the --seen file holds %q after only failed reads, want %q", got, seen)
			}
		})
	}
}

func TestWaitForWaiting_ReportsAgainAnEntryThatLeftAndCameBack(t *testing.T) {
	t.Parallel()
	entry := `{"repository": "example/app", "issue": 31, "kind": "decision", "title": "Show the history", "url": "https://example.test/issues/31"}`
	w := newWatch(t, monitorFileWith(time.Now(), entry))
	if _, messages, code := w.run("--timeout", "5"); code != 0 {
		t.Fatalf("the first call: exit code = %d\n%s", code, messages)
	}

	// A good read with no entry is the only read that empties the --seen file.
	w.write(monitorFileWith(time.Now(), ""))
	entries, messages, code := w.run("--timeout", "1")
	if entries != "" || code != exitTimeLimit || w.seenText() != "" {
		t.Fatalf("exit code = %d, entries = %q, --seen = %q, want %d, no entry, and an empty --seen file\n%s", code, entries, w.seenText(), exitTimeLimit, messages)
	}
	// The last poll is of now, so the message names no old last_poll.at.
	if strings.Contains(messages, "last_poll.at") || strings.Contains(messages, "failed") {
		t.Errorf("the message at the limit names a problem of a fresh file:\n%s", messages)
	}

	w.write(monitorFileWith(time.Now(), entry))
	if entries, messages, code := w.run("--timeout", "5"); code != 0 || !strings.HasPrefix(entries, "decision\texample/app\t31\t") {
		t.Errorf("exit code = %d, entries = %q, want 0 and the entry that came back\n%s", code, entries, messages)
	}
}

func TestWaitForWaiting_RefusesAWrongArgument(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"a time limit that is not a number", []string{"--timeout", "soon"}, "--timeout must be a number of seconds"},
		{"an interval of zero", []string{"--interval", "0"}, "--interval must be a number of seconds"},
		{"an unknown argument", []string{"--forever"}, "unknown argument '--forever'"},
		{"an argument with no value", []string{"--timeout"}, "--timeout needs a value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w := newWatch(t, goldenMonitorFile(t))
			entries, messages, code := w.run(tt.args...)
			if code != 2 || entries != "" || !strings.Contains(messages, tt.want) {
				t.Errorf("exit code = %d, entries = %q, messages = %q, want 2, no entry, and %q", code, entries, messages, tt.want)
			}
			if w.seenText() != "" {
				t.Errorf("the --seen file holds %q after a wrong argument", w.seenText())
			}
		})
	}

	out, err := exec.Command(filepath.Join("skills", "watch", "wait-for-waiting.sh"), "--timeout", "1").CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 || !strings.Contains(string(out), "--seen <path> is missing") {
		t.Errorf("a call with no --seen file: %v, %q, want the exit code 2 and \"--seen <path> is missing\"", err, out)
	}
}

// The skill starts the script and names the skill of each kind of the
// monitor file. The kinds are those of docs/ja/designs/status-menu-bar.md.
func TestWatchSkill_StartsTheScriptAndNamesTheSkillOfEachKind(t *testing.T) {
	text := readText(filepath.Join("skills", "watch"), "SKILL.md")
	for _, want := range []string{
		"Start `${CLAUDE_SKILL_DIR}/wait-for-waiting.sh --seen ~/.local/state/cumin/watch-seen` in the background",
		"Empty output never means \"nothing waits\".",
		"| `plan-review` | A plan of the Planner waits for the review | `plan-review` |",
		"| `merge-decision` | A pull request waits for the merge decision | `merge-decision` |",
		"| `decision` | A stopped issue waits for an answer | `decision-request` |",
		"| `acceptance` | A requirement issue waits for the acceptance |",
		"| 124 | The time limit ended with no new entry.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("SKILL.md lacks %q", want)
		}
	}
	// Each skill that the table names is a skill of the plugin.
	table, _ := section(text, "The skill of each kind")
	for _, m := range regexp.MustCompile("(?m)^\\| `[a-z-]+` \\|[^|]+\\|[^`|]*`([a-z-]+)`").FindAllStringSubmatch(table, -1) {
		if readText(filepath.Join("skills", m[1]), "SKILL.md") == "" {
			t.Errorf("SKILL.md names the skill %q, which the plugin does not hold", m[1])
		}
	}
	commands, _ := section(text, "Commands")
	if strings.Contains(commands, "`gh ") || strings.Contains(commands, "Recorded by GitHub, so ask first") {
		t.Errorf("the skill watch names a command of gh:\n%s", commands)
	}
}
