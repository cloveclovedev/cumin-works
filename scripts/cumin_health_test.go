// Package scripts holds the tests of the general tools of the Host, the
// shell scripts of this directory.
package scripts

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The golden monitor file of the acceptance tests of cumin: its last poll
// is at 2026-10-04T07:00:05Z and has one error, and two agents are at work.
const (
	goldenMonitorFile = "../internal/workflow/testdata/monitor-file.json"
	freshNow          = "2026-10-04T07:01:00Z"
)

// health is one run of scripts/cumin-health.sh on a Host that the test
// builds in a temporary directory.
type health struct {
	t       *testing.T
	dir     string
	monitor string
	plist   string
	now     string
}

// newHealth returns a Host with no monitor file, no plist, and no log.
// The script needs plutil and the date of macOS, so the test skips elsewhere.
func newHealth(t *testing.T) *health {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("scripts/cumin-health.sh needs plutil, which is macOS only")
	}
	dir := t.TempDir()
	return &health{
		t:       t,
		dir:     dir,
		monitor: filepath.Join(dir, "monitor.json"),
		plist:   filepath.Join(dir, "dev.cloveclove.cumin.plist"),
		now:     freshNow,
	}
}

// writeGolden puts the golden monitor file on the Host.
func (h *health) writeGolden() {
	h.t.Helper()
	golden, err := os.ReadFile(goldenMonitorFile)
	if err != nil {
		h.t.Fatal(err)
	}
	h.write(h.monitor, string(golden))
}

// writePoll puts a monitor file with one poll on the Host, as cumin run
// does: a temporary file in the same directory, then a rename.
func (h *health) writePoll(at string, pollErrors string) {
	h.t.Helper()
	temporary := h.monitor + ".tmp"
	h.write(temporary, fmt.Sprintf(
		`{"version":1,"last_poll":{"at":%q,"errors":[%s]},"stop_requested":false,"quota":{"state":"open","stopped_windows":[]},"agents":[],"waiting":[]}`,
		at, pollErrors))
	if err := os.Rename(temporary, h.monitor); err != nil {
		h.t.Fatal(err)
	}
}

// writeLog puts the plist of the LaunchAgent and the log that it names on
// the Host.
func (h *health) writeLog(lines ...string) {
	h.t.Helper()
	log := filepath.Join(h.dir, "cumin.log")
	h.write(log, strings.Join(lines, "\n")+"\n")
	h.write(h.plist, `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>StandardOutPath</key>
	<string>`+log+`</string>
</dict>
</plist>
`)
}

func (h *health) write(path, content string) {
	h.t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

// standardTools are the tools of macOS that the script may call. Every run
// of the tests has only these on PATH, so a call of another tool (jq, gh,
// python3) fails the tests.
var standardTools = []string{"plutil", "date", "sed", "awk", "sleep"}

// command returns the script with the files of the Host, a fixed "now",
// and a PATH that holds only the standard tools.
func (h *health) command(args ...string) *exec.Cmd {
	h.t.Helper()
	bin := filepath.Join(h.dir, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		h.t.Fatal(err)
	}
	for _, tool := range standardTools {
		link := filepath.Join(bin, tool)
		if _, err := os.Lstat(link); err == nil {
			continue
		}
		path, err := exec.LookPath(tool)
		if err != nil {
			h.t.Fatal(err)
		}
		if err := os.Symlink(path, link); err != nil {
			h.t.Fatal(err)
		}
	}
	command := exec.Command("./cumin-health.sh", append([]string{"--monitor", h.monitor, "--plist", h.plist}, args...)...)
	command.Env = []string{"PATH=" + bin, "HOME=" + h.dir, "CUMIN_HEALTH_NOW=" + h.now, "CUMIN_HEALTH_INTERVAL=0.05"}
	return command
}

// run runs the script to its end, and returns its output and its exit code.
func (h *health) run(args ...string) (string, int) {
	h.t.Helper()
	out, err := h.command(args...).CombinedOutput()
	return string(out), exitCode(h.t, err)
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("the script did not run: %v", err)
	}
	return exit.ExitCode()
}

func wantLines(t *testing.T, out string, lines ...string) {
	t.Helper()
	for _, line := range lines {
		if !strings.Contains(out, line+"\n") {
			t.Errorf("the output has no line %q:\n%s", line, out)
		}
	}
}

func TestCuminHealth_ShowsTheLastPollItsErrorAndBothAgents(t *testing.T) {
	h := newHealth(t)
	h.writeGolden()

	out, code := h.run()

	wantLines(t, out,
		"last poll: 2026-10-04T07:00:05Z (55s ago)",
		"errors of the last poll: 1",
		"  example/app: read the snapshot: GitHub returned 502",
		"agents at work: 2",
		"  example/app#31 planner (acceptance check): Show the history of an item",
		"  example/tool#12 implementer (implement): feat(api): add the list endpoint",
		"waiting issues: 2",
		"error: the last poll has 1 error(s)",
	)
	if code != 1 {
		t.Errorf("exit code = %d, want 1: the last poll has an error\n%s", code, out)
	}
}

func TestCuminHealth_AFreshPollWithoutAnErrorIsHealthy(t *testing.T) {
	h := newHealth(t)
	h.writePoll("2026-10-04T07:00:05Z", "")

	out, code := h.run()

	if code != 0 {
		t.Errorf("exit code = %d, want 0\n%s", code, out)
	}
	wantLines(t, out, "errors of the last poll: 0", "agents at work: 0", "cumin is running: the last poll is fresh and has no error")
}

func TestCuminHealth_AnOldLastPollFails(t *testing.T) {
	h := newHealth(t)
	h.writePoll("2026-10-04T07:00:05Z", "")

	// 180 seconds is the last age that is fresh by default.
	h.now = "2026-10-04T07:03:05Z"
	if out, code := h.run(); code != 0 {
		t.Errorf("at 180s: exit code = %d, want 0\n%s", code, out)
	}
	h.now = "2026-10-04T07:03:06Z"
	out, code := h.run()
	if code != 1 {
		t.Errorf("at 181s: exit code = %d, want 1\n%s", code, out)
	}
	wantLines(t, out, "error: the last poll is 181s old, over the limit of 180s. cumin stopped, or the Host slept")

	// The option changes the limit in both directions.
	if out, code := h.run("--stale-after", "600"); code != 0 {
		t.Errorf("at 181s with --stale-after 600: exit code = %d, want 0\n%s", code, out)
	}
	h.now = freshNow
	if out, code := h.run("--stale-after", "30"); code != 1 {
		t.Errorf("at 55s with --stale-after 30: exit code = %d, want 1\n%s", code, out)
	}
}

func TestCuminHealth_AMissingOrUnreadableMonitorFileFails(t *testing.T) {
	h := newHealth(t)

	out, code := h.run()
	if code != 1 || !strings.Contains(out, "error: the monitor file "+h.monitor+" is missing") {
		t.Errorf("a missing file: exit code = %d, want 1 and the reason\n%s", code, out)
	}

	for name, content := range map[string]string{
		"not JSON":           "cumin",
		"no last poll":       `{"version":1}`,
		"a newer version":    `{"version":2,"last_poll":{"at":"2026-10-04T07:00:05Z","errors":[]}}`,
		"a time with no UTC": `{"version":1,"last_poll":{"at":"2026-10-04 16:00","errors":[]}}`,
	} {
		h.write(h.monitor, content)
		out, code := h.run()
		if code != 1 || !strings.Contains(out, "error: the monitor file "+h.monitor) {
			t.Errorf("%s: exit code = %d, want 1 and the reason\n%s", name, code, out)
		}
	}
}

func TestCuminHealth_ShowsTheErrorsSinceTheStart(t *testing.T) {
	h := newHealth(t)
	h.writePoll("2026-10-04T07:00:05Z", "")
	h.writeLog(
		`{"time":"2026-10-04T05:00:00Z","level":"INFO","msg":"cumin run starts","repositories":["example/app"]}`,
		`{"time":"2026-10-04T05:30:00Z","level":"ERROR","msg":"an error before the last start"}`,
		`{"time":"2026-10-04T06:00:00Z","level":"INFO","msg":"cumin run starts","repositories":["example/app"]}`,
		`{"time":"2026-10-04T06:10:00Z","level":"WARN","msg":"a warning"}`,
		`{"time":"2026-10-04T06:20:00Z","level":"ERROR","msg":"the poll failed","repository":"example/app","used_percent":93}`,
		`{"time":"2026-10-04T06:30:00Z","level":"ERROR","msg":"the run \"implement\" failed","issue":12}`,
	)

	out, code := h.run()

	wantLines(t, out,
		"errors since the start: 2 (log: "+filepath.Join(h.dir, "cumin.log")+")",
		"  2026-10-04T06:20:00Z the poll failed",
		`  2026-10-04T06:30:00Z the run \"implement\" failed`,
	)
	for _, absent := range []string{"before the last start", "a warning", "93"} {
		if strings.Contains(out, absent) {
			t.Errorf("the output holds %q:\n%s", absent, out)
		}
	}
	// The errors since the start are shown only: the last poll decides.
	if code != 0 {
		t.Errorf("exit code = %d, want 0\n%s", code, out)
	}
}

func TestCuminHealth_SaysWhyTheErrorsSinceTheStartAreUnknown(t *testing.T) {
	h := newHealth(t)
	h.writePoll("2026-10-04T07:00:05Z", "")

	out, code := h.run()
	wantLines(t, out, "errors since the start: unknown (the plist "+h.plist+" is missing, so the log has no known path)")
	if code != 0 {
		t.Errorf("exit code = %d, want 0\n%s", code, out)
	}

	h.writeLog(`{"time":"2026-10-04T06:20:00Z","level":"ERROR","msg":"the poll failed"}`)
	out, _ = h.run()
	if !strings.Contains(out, `errors since the start: unknown (the log `+filepath.Join(h.dir, "cumin.log")+` has no line "cumin run starts")`) {
		t.Errorf("a log with no start is not reported:\n%s", out)
	}
}

// waitRun is a run with --wait-polls whose output the test reads line by
// line, so that the test writes the next poll only after the script
// reported the one before. No sleep of the test decides the result.
type waitRun struct {
	t       *testing.T
	command *exec.Cmd
	lines   *bufio.Scanner
	out     strings.Builder
}

func (h *health) startWait(args ...string) *waitRun {
	h.t.Helper()
	command := h.command(args...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		h.t.Fatal(err)
	}
	command.Stderr = command.Stdout
	if err := command.Start(); err != nil {
		h.t.Fatal(err)
	}
	return &waitRun{t: h.t, command: command, lines: bufio.NewScanner(stdout)}
}

// until reads the output up to the line that starts with the prefix.
func (w *waitRun) until(prefix string) {
	w.t.Helper()
	for w.lines.Scan() {
		w.out.WriteString(w.lines.Text() + "\n")
		if strings.HasPrefix(w.lines.Text(), prefix) {
			return
		}
	}
	w.t.Fatalf("the script ended before a line %q:\n%s", prefix, w.out.String())
}

// end reads the rest of the output, and returns all of it with the exit code.
func (w *waitRun) end() (string, int) {
	w.t.Helper()
	for w.lines.Scan() {
		w.out.WriteString(w.lines.Text() + "\n")
	}
	return w.out.String(), exitCode(w.t, w.command.Wait())
}

func TestCuminHealth_WaitPollsReturnsAfterTwoNewPollsWithoutAnError(t *testing.T) {
	h := newHealth(t)
	h.writePoll("2026-10-04T06:58:00Z", "")

	run := h.startWait("--wait-polls", "2", "--timeout", "60")
	run.until("waiting for 2 poll(s) after 2026-10-04T06:58:00Z")
	h.writePoll("2026-10-04T06:59:00Z", "")
	run.until("poll 1 of 2: 2026-10-04T06:59:00Z, no error")
	h.writePoll("2026-10-04T07:00:00Z", "")
	out, code := run.end()

	if code != 0 {
		t.Errorf("exit code = %d, want 0\n%s", code, out)
	}
	wantLines(t, out,
		"poll 2 of 2: 2026-10-04T07:00:00Z, no error",
		"last poll: 2026-10-04T07:00:00Z (60s ago)",
		"cumin is running: the last poll is fresh and has no error",
	)
}

func TestCuminHealth_WaitPollsFailsOnAnErrorInOneOfThePolls(t *testing.T) {
	h := newHealth(t)
	h.writePoll("2026-10-04T06:58:00Z", "")

	run := h.startWait("--wait-polls", "2", "--timeout", "60")
	run.until("waiting for 2 poll(s)")
	h.writePoll("2026-10-04T06:59:00Z", `{"repository":"example/app","message":"read the snapshot: GitHub returned 502"}`)
	out, code := run.end()

	if code != 1 {
		t.Errorf("exit code = %d, want 1\n%s", code, out)
	}
	wantLines(t, out,
		"poll 1 of 2: 2026-10-04T06:59:00Z, 1 error(s)",
		"  example/app: read the snapshot: GitHub returned 502",
		"error: the poll at 2026-10-04T06:59:00Z has 1 error(s)",
	)
}

func TestCuminHealth_WaitPollsFailsWithTimeLimitAtTheLimit(t *testing.T) {
	h := newHealth(t)
	h.writePoll("2026-10-04T06:58:00Z", "")

	run := h.startWait("--wait-polls", "2", "--timeout", "1")
	run.until("waiting for 2 poll(s)")
	h.writePoll("2026-10-04T06:59:00Z", "")
	out, code := run.end()

	if code != 1 {
		t.Errorf("exit code = %d, want 1\n%s", code, out)
	}
	wantLines(t, out, "error: time limit: the last poll moved 1 time(s) of 2 in 1s")
}
