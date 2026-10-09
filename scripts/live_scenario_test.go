package scripts

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// live is one run of scripts/live-scenario.sh on a Host that the test
// builds in a temporary directory: the Host of the tests of
// scripts/replace-binary.sh, with a fake gh, a fake go that stands for
// TestLiveE2E, and the two settings files.
type live struct {
	*replace
	hostConfig    string
	sandboxConfig string
	// duringTest is the copy that the fake go keeps of the Host settings
	// file: what cumin reads while the test runs.
	duringTest string
}

const (
	hostSettings    = "repositories = [\"example/app\"]\n"
	sandboxSettings = "repositories = [\"example/sandbox\"]\n"
)

// liveTools are the tools of macOS that the script calls beside replaceTools.
var liveTools = []string{"cp", "mv"}

var liveFakes = map[string]string{
	// The fake gh answers "gh issue list" with one open issue for the
	// label FAKE_OPEN_LABEL, and with no issue for the other labels.
	"gh": `#!/bin/sh
echo "gh $*" >>"$CALLS"
[ "$1 $2" = "issue list" ] || { echo "fake gh: unexpected call: $*" >&2; exit 1; }
[ -z "${FAKE_GH_FAILS:-}" ] || { echo "fake gh: HTTP 502" >&2; exit 1; }
while [ $# -gt 0 ]; do
  [ "$1" != "--label" ] || label="$2"
  shift
done
[ "$label" != "${FAKE_OPEN_LABEL:-}" ] || echo 12
`,
	// With FAKE_START_FAILS, the start of step 3 fails: "kickstart"
	// without -k. The start of step 5 passes.
	"launchctl": `#!/bin/sh
echo "launchctl $*" >>"$CALLS"
[ "$1" != "kickstart" ] || [ "$2" = "-k" ] || [ -z "${FAKE_START_FAILS:-}" ] || exit 1
`,
	// The fake go stands for TestLiveE2E. With FAKE_TEST=hang it starts a
	// child and waits for it, as the go command waits for the test binary.
	"go": `#!/bin/sh
echo "go $* CUMIN_LIVE=${CUMIN_LIVE:-} CUMIN_LIVE_REPO=${CUMIN_LIVE_REPO:-}" >>"$CALLS"
cp "$HOST_CONFIG" "$DURING_TEST"
case "${FAKE_TEST:-pass}" in
  pass) echo "ok  	example/cmd/cumin" ;;
  fail) echo "FAIL	example/cmd/cumin"; exit 1 ;;
  hang) echo "the test runs"; sleep 60 ;;
esac
`,
}

// newLive returns a Host where the scenario can run: the sandbox is clean,
// the LaunchAgent is loaded, and its plist names the Host settings file.
func newLive(t *testing.T) *live {
	t.Helper()
	r := newReplace(t)
	dir := r.health.dir
	l := &live{
		replace:       r,
		hostConfig:    filepath.Join(dir, "config.toml"),
		sandboxConfig: filepath.Join(dir, "sandbox.toml"),
		duringTest:    filepath.Join(dir, "config-during-the-test.toml"),
	}
	for _, tool := range liveTools {
		path, err := exec.LookPath(tool)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(path, filepath.Join(r.bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	for name, script := range liveFakes {
		if err := os.WriteFile(filepath.Join(r.bin, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	r.health.write(l.hostConfig, hostSettings)
	r.health.write(l.sandboxConfig, sandboxSettings)
	r.health.write(r.health.plist, `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>ProgramArguments</key>
	<array>
		<string>`+filepath.Join(r.prefix, "cumin")+`</string>
		<string>run</string>
		<string>--config</string>
		<string>`+l.hostConfig+`</string>
	</array>
</dict>
</plist>
`)
	return l
}

// start starts the script for the sandbox example/sandbox, with a short
// wait between two reads.
func (l *live) start(args ...string) *waitRun {
	l.t.Helper()
	command := exec.Command("./live-scenario.sh", args...)
	command.Env = append([]string{
		"PATH=" + l.bin, "HOME=" + l.health.dir, "CALLS=" + l.calls,
		"HOST_CONFIG=" + l.hostConfig, "DURING_TEST=" + l.duringTest,
		"CUMIN_LIVE_SCENARIO_INTERVAL=0.05", "CUMIN_HEALTH_INTERVAL=0.05", "CUMIN_HEALTH_NOW=" + l.health.now,
	}, l.env...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		l.t.Fatal(err)
	}
	command.Stderr = command.Stdout
	if err := command.Start(); err != nil {
		l.t.Fatal(err)
	}
	return &waitRun{t: l.t, command: command, lines: bufio.NewScanner(stdout)}
}

// scenario starts the script with the two arguments that every run needs.
func (l *live) scenario(args ...string) *waitRun {
	l.t.Helper()
	return l.start(append([]string{"--repo", "example/sandbox", "--config", l.sandboxConfig}, args...)...)
}

// twoPolls does what the cumin that step 5 started does: two polls
// without an error.
func (l *live) twoPolls(run *waitRun) {
	l.t.Helper()
	run.until("waiting for 2 poll(s)")
	l.health.writePoll("2026-10-04T06:59:00Z", "")
	run.until("poll 1 of 2")
	l.health.writePoll("2026-10-04T07:00:00Z", "")
}

// wantHostSettings fails when the Host settings file differs from its
// first content, or when the copy of step 3 is left.
func (l *live) wantHostSettings() {
	l.t.Helper()
	if got, err := os.ReadFile(l.hostConfig); err != nil || string(got) != hostSettings {
		l.t.Errorf("the Host settings file = %q, %v, want its first content %q", got, err, hostSettings)
	}
	if _, err := os.Stat(l.hostConfig + ".before-live-scenario"); !os.IsNotExist(err) {
		l.t.Errorf("the copy of the Host settings is left: %v", err)
	}
}

// wantSandboxDuringTest fails when cumin did not have the settings of the
// sandbox while the test ran.
func (l *live) wantSandboxDuringTest() {
	l.t.Helper()
	if got, err := os.ReadFile(l.duringTest); err != nil || string(got) != sandboxSettings {
		l.t.Errorf("the Host settings file during the test = %q, %v, want the settings of the sandbox", got, err)
	}
}

// lastLines returns the last two lines of the output.
func lastLines(out string) string {
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) > 2 {
		lines = lines[len(lines)-2:]
	}
	return strings.Join(lines, "\n")
}

const hostIsBack = "host: the Host settings are back, cumin runs, and two polls have no error"

func TestLiveScenario_RunsTheStepsInOrderAndPutsTheHostBack(t *testing.T) {
	l := newLive(t)

	run := l.scenario()
	l.endCumin(run)
	l.twoPolls(run)
	out, code := run.end()

	if code != 0 {
		t.Errorf("exit code = %d, want 0\n%s", code, out)
	}
	// The two results are the last two lines, one line for each.
	if got, want := lastLines(out), "test: passed\n"+hostIsBack; got != want {
		t.Errorf("the last two lines = %q, want %q\n%s", got, want, out)
	}
	l.wantSandboxDuringTest()
	l.wantHostSettings()
	// One call of each step, in the order of the steps.
	calls := l.called()
	rest := calls
	for _, call := range []string{
		"gh issue list --repo example/sandbox --state open --label cumin/status/ready ",
		"gh issue list --repo example/sandbox --state open --label cumin/status/planning ",
		"gh issue list --repo example/sandbox --state open --label cumin/status/implementing ",
		"gh issue list --repo example/sandbox --state open --label cumin/status/checking ",
		"gh issue list --repo example/sandbox --state open --label cumin/status/reviewing ",
		"cumin stop --after-current-runs\n",
		"launchctl kickstart gui/",
		"go test -count=1 -timeout 0 -run TestLiveE2E -v ./cmd/cumin/ CUMIN_LIVE=1 CUMIN_LIVE_REPO=example/sandbox\n",
		"launchctl kickstart -k gui/",
		"plutil -extract last_poll.at raw",
	} {
		_, after, found := strings.Cut(rest, call)
		if !found {
			t.Fatalf("no call %q after the calls before it:\n%s", call, calls)
		}
		rest = after
	}
}

func TestLiveScenario_ASandboxThatIsNotCleanEndsBeforeTheStop(t *testing.T) {
	for name, c := range map[string]struct {
		env  string
		want string
	}{
		"an open ready issue":     {env: "FAKE_OPEN_LABEL=cumin/status/ready", want: "error: the sandbox example/sandbox is not clean: an open issue has the label cumin/status/ready (#12)"},
		"an open reviewing issue": {env: "FAKE_OPEN_LABEL=cumin/status/reviewing", want: "error: the sandbox example/sandbox is not clean: an open issue has the label cumin/status/reviewing (#12)"},
		"a failed read":           {env: "FAKE_GH_FAILS=1", want: `error: cannot read the open issues of example/sandbox with the label cumin/status/ready. A failed read is not "clean"`},
	} {
		t.Run(name, func(t *testing.T) {
			l := newLive(t)
			l.env = []string{c.env}

			out, code := l.scenario().end()

			if code != 1 || !strings.Contains(out, c.want) {
				t.Errorf("exit code = %d, want 1 and %q\n%s", code, c.want, out)
			}
			l.wantNoCall("cumin ", "go ", "launchctl ")
			l.wantHostSettings()
		})
	}
}

func TestLiveScenario_AFailedTestPutsTheHostBack(t *testing.T) {
	l := newLive(t)
	l.env = []string{"FAKE_TEST=fail"}

	run := l.scenario()
	l.endCumin(run)
	l.twoPolls(run)
	out, code := run.end()

	if code != 1 {
		t.Errorf("exit code = %d, want 1\n%s", code, out)
	}
	if got, want := lastLines(out), "test: failed (exit code 1)\n"+hostIsBack; got != want {
		t.Errorf("the last two lines = %q, want %q\n%s", got, want, out)
	}
	l.wantSandboxDuringTest()
	l.wantHostSettings()
}

func TestLiveScenario_AFailedStartOfStep3PutsTheHostBack(t *testing.T) {
	l := newLive(t)
	l.env = []string{"FAKE_START_FAILS=1"}

	run := l.scenario()
	l.endCumin(run)
	l.twoPolls(run)
	out, code := run.end()

	if code != 1 {
		t.Errorf("exit code = %d, want 1\n%s", code, out)
	}
	if got, want := lastLines(out), "test: not run\n"+hostIsBack; got != want {
		t.Errorf("the last two lines = %q, want %q\n%s", got, want, out)
	}
	l.wantNoCall("go ")
	l.wantHostSettings()
}

func TestLiveScenario_ASignalDuringTheStopSaysThatCuminStops(t *testing.T) {
	l := newLive(t)

	run := l.scenario()
	run.until("waiting until cumin has ended")
	if err := run.command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	out, code := run.end()

	if code != 1 || !strings.Contains(out, "error: signal: the Host settings are not changed, and cumin is left as it is: it still stops after the current runs") {
		t.Errorf("exit code = %d, want 1 and the advice on the signal\n%s", code, out)
	}
	l.wantNoCall("go ", "launchctl kickstart")
	l.wantHostSettings()
}

func TestLiveScenario_ASignalDuringTheTestPutsTheHostBack(t *testing.T) {
	l := newLive(t)
	l.env = []string{"FAKE_TEST=hang"}

	run := l.scenario()
	l.endCumin(run)
	run.until("the test runs")
	if err := run.command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	l.twoPolls(run)
	// The end of the output also proves that the child of the fake go has
	// ended: it holds the output of the script open while it runs.
	out, code := run.end()

	if code != 1 {
		t.Errorf("exit code = %d, want 1\n%s", code, out)
	}
	if got, want := lastLines(out), "test: stopped by a signal\n"+hostIsBack; got != want {
		t.Errorf("the last two lines = %q, want %q\n%s", got, want, out)
	}
	l.wantSandboxDuringTest()
	l.wantHostSettings()
	if !strings.Contains(l.called(), "launchctl kickstart -k gui/") {
		t.Errorf("cumin was not started again:\n%s", l.called())
	}
}

func TestLiveScenario_StopsTheTestWithTimeLimitAndPutsTheHostBack(t *testing.T) {
	l := newLive(t)
	l.env = []string{"FAKE_TEST=hang"}

	run := l.scenario("--test-timeout", "1")
	l.endCumin(run)
	l.twoPolls(run)
	out, code := run.end()

	if code != 1 {
		t.Errorf("exit code = %d, want 1\n%s", code, out)
	}
	if got, want := lastLines(out), "test: time limit: the test has not ended in 1s, and the script stopped it\n"+hostIsBack; got != want {
		t.Errorf("the last two lines = %q, want %q\n%s", got, want, out)
	}
	l.wantHostSettings()
}

func TestLiveScenario_StopsWithTimeLimitAndChangesNoSetting(t *testing.T) {
	l := newLive(t)

	// cumin does not end: nobody removes the stop request.
	out, code := l.scenario("--stop-timeout", "1").end()

	if code != 1 {
		t.Errorf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "error: time limit: cumin has not ended in 1s. The Host settings are not changed, and cumin is left as it is") {
		t.Errorf("the output does not say \"time limit\":\n%s", out)
	}
	l.wantNoCall("go ", "launchctl kickstart")
	l.wantHostSettings()
}

func TestLiveScenario_FailedPollsAreTheResultOfTheHost(t *testing.T) {
	l := newLive(t)

	run := l.scenario()
	l.endCumin(run)
	run.until("waiting for 2 poll(s)")
	l.health.writePoll("2026-10-04T06:59:00Z", `{"repository":"example/app","message":"read the snapshot: GitHub returned 502"}`)
	out, code := run.end()

	if code != 1 {
		t.Errorf("exit code = %d, want 1\n%s", code, out)
	}
	if got, want := lastLines(out), "test: passed\nhost: failed: the Host settings are back and cumin was started, but the check of the polls failed. Read the errors above"; got != want {
		t.Errorf("the last two lines = %q, want %q\n%s", got, want, out)
	}
	l.wantHostSettings()
}

func TestLiveScenario_ACopyOfAnEarlierRunEndsBeforeTheStop(t *testing.T) {
	l := newLive(t)
	copyOfEarlierRun := l.hostConfig + ".before-live-scenario"
	l.health.write(copyOfEarlierRun, hostSettings)

	out, code := l.scenario().end()

	if code != 1 || !strings.Contains(out, "an earlier run did not put the Host settings back") {
		t.Errorf("exit code = %d, want 1 and the copy of the earlier run\n%s", code, out)
	}
	l.wantNoCall("cumin ", "go ", "launchctl kickstart")
	if got, err := os.ReadFile(copyOfEarlierRun); err != nil || string(got) != hostSettings {
		t.Errorf("the copy of the earlier run = %q, %v, want it unchanged", got, err)
	}
}

func TestLiveScenario_AWrongOptionGivesExitCode2(t *testing.T) {
	l := newLive(t)

	for _, args := range [][]string{
		{"--no-such-option"},
		{"--repo", "example/sandbox"},
		{"--config", l.sandboxConfig},
		{"--repo", "sandbox", "--config", l.sandboxConfig},
		{"--repo", "example/sandbox", "--config", l.sandboxConfig, "--stop-timeout", "1h"},
		{"--repo", "example/sandbox", "--config", l.sandboxConfig, "--test-timeout"},
	} {
		if out, code := l.start(args...).end(); code != 2 {
			t.Errorf("%v: exit code = %d, want 2\n%s", args, code, out)
		}
	}
	l.wantNoCall("gh ", "cumin ", "go ", "launchctl ")
}

// The script takes the sandbox from its arguments: its text names no
// repository.
func TestLiveScenario_HoldsNoNameOfARepository(t *testing.T) {
	script, err := os.ReadFile("live-scenario.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cloveclovedev", "cumin-works"} {
		if strings.Contains(string(script), name) {
			t.Errorf("the script holds %q", name)
		}
	}
}
