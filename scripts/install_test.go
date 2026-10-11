package scripts

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

// replace is a Host that the test builds in a temporary directory, to
// replace the binary there: fake git, cumin, launchctl, plutil, and go on
// PATH, and the files of the LaunchAgent under a temporary home.
type replace struct {
	t *testing.T
	// health writes the monitor file and the plist of the Host.
	health      *health
	bin         string
	calls       string
	prefix      string
	stopRequest string
	env         []string
}

// replaceTools are the tools of macOS that scripts/install.sh and
// scripts/cumin-health.sh may call. Every run of the tests has only
// these and the five fakes on PATH, so a call of another tool fails the
// tests.
var replaceTools = []string{"date", "sed", "awk", "sleep", "tr", "dirname", "id", "mktemp", "mkdir", "install", "rm"}

// The fakes write every call to the file CALLS, one line for each call.
// The fake plutil records the call and then runs the real plutil, because
// scripts/cumin-health.sh reads the monitor file with it.
var replaceFakes = map[string]string{
	"git": `#!/bin/sh
[ "$1" = "-C" ] && shift 2
echo "git $*" >>"$CALLS"
case "$*" in
  "symbolic-ref --short refs/remotes/origin/HEAD") echo origin/main ;;
  "symbolic-ref --short HEAD") echo "${FAKE_BRANCH:-main}" ;;
  "status --porcelain") printf '%s' "${FAKE_CHANGES:-}" ;;
  "fetch --quiet origin main") ;;
  "rev-parse HEAD") echo 1111 ;;
  "rev-parse origin/main") echo "${FAKE_REMOTE_COMMIT:-1111}" ;;
  *) echo "fake git: unexpected call: $*" >&2; exit 1 ;;
esac
`,
	// cumin stop writes the stop request, as the real command does. The
	// test removes it, as cumin run does when it has ended.
	"cumin": `#!/bin/sh
echo "cumin $*" >>"$CALLS"
[ "$*" = "stop --after-current-runs" ] || { echo "fake cumin: unexpected call: $*" >&2; exit 1; }
mkdir -p "$HOME/.local/state/cumin"
echo '{}' >"$HOME/.local/state/cumin/stop-request.json"
`,
	"launchctl": `#!/bin/sh
echo "launchctl $*" >>"$CALLS"
[ "$1" != "print" ] || exit "${FAKE_NOT_LOADED:-0}"
`,
	"go": `#!/bin/sh
echo "go $*" >>"$CALLS"
[ "$1 $2" = "build -o" ] || { echo "fake go: unexpected call: $*" >&2; exit 1; }
echo "the new binary" >"$3"
`,
	"plutil": `#!/bin/sh
echo "plutil $*" >>"$CALLS"
exec REAL_PLUTIL "$@"
`,
}

// newReplace returns a Host where every check passes: the checkout is on
// main with no local change and equal to the remote, the LaunchAgent is
// loaded and runs <prefix>/cumin, and the monitor file holds one poll.
func newReplace(t *testing.T) *replace {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("scripts/install.sh needs launchctl and plutil, which are macOS only")
	}
	dir := t.TempDir()
	r := &replace{
		t: t,
		health: &health{
			t:       t,
			dir:     dir,
			monitor: filepath.Join(dir, ".local", "state", "cumin", "monitor.json"),
			plist:   filepath.Join(dir, "Library", "LaunchAgents", "dev.cloveclove.cumin.plist"),
			now:     freshNow,
		},
		bin:         filepath.Join(dir, "bin"),
		calls:       filepath.Join(dir, "calls"),
		prefix:      filepath.Join(dir, "prefix"),
		stopRequest: filepath.Join(dir, ".local", "state", "cumin", "stop-request.json"),
	}
	for _, d := range []string{r.bin, filepath.Dir(r.health.monitor), filepath.Dir(r.health.plist)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, tool := range replaceTools {
		path, err := exec.LookPath(tool)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(path, filepath.Join(r.bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	plutil, err := exec.LookPath("plutil")
	if err != nil {
		t.Fatal(err)
	}
	for name, script := range replaceFakes {
		script = strings.ReplaceAll(script, "REAL_PLUTIL", plutil)
		if err := os.WriteFile(filepath.Join(r.bin, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	r.health.write(r.calls, "")
	r.writePlist(filepath.Join(r.prefix, "cumin"))
	r.health.writePoll("2026-10-04T06:58:00Z", "")
	return r
}

// writePlist puts the plist of a LaunchAgent that runs the program on the Host.
func (r *replace) writePlist(program string) {
	r.t.Helper()
	r.health.write(r.health.plist, `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>ProgramArguments</key>
	<array>
		<string>`+program+`</string>
		<string>run</string>
	</array>
</dict>
</plist>
`)
}

// endCumin does what cumin run does when it has ended after a stop
// request: it removes the request. The script has printed that it waits.
func (r *replace) endCumin(run *waitRun) {
	r.t.Helper()
	run.until("waiting until cumin has ended")
	if err := os.Remove(r.stopRequest); err != nil {
		r.t.Fatal(err)
	}
}

// called returns the calls of the fakes, one line for each call.
func (r *replace) called() string {
	r.t.Helper()
	calls, err := os.ReadFile(r.calls)
	if err != nil {
		r.t.Fatal(err)
	}
	return string(calls)
}

// wantNoCall fails when a fake was called with one of the prefixes.
func (r *replace) wantNoCall(prefixes ...string) {
	r.t.Helper()
	calls := r.called()
	for _, prefix := range prefixes {
		if strings.HasPrefix(calls, prefix) || strings.Contains(calls, "\n"+prefix) {
			r.t.Errorf("the script called %q:\n%s", prefix, calls)
		}
	}
}

// install is one run of scripts/install.sh on the Host of replace.
type install struct {
	*replace
}

func newInstall(t *testing.T) *install {
	t.Helper()
	return &install{replace: newReplace(t)}
}

// start starts the script, with a short wait between two reads.
func (i *install) start(args ...string) *waitRun {
	i.t.Helper()
	command := exec.Command("./install.sh", append([]string{"--prefix", i.prefix}, args...)...)
	command.Env = append([]string{
		"PATH=" + i.bin, "HOME=" + i.health.dir, "CALLS=" + i.calls,
		"CUMIN_INSTALL_INTERVAL=0.05", "CUMIN_HEALTH_INTERVAL=0.05", "CUMIN_HEALTH_NOW=" + i.health.now,
	}, i.env...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		i.t.Fatal(err)
	}
	command.Stderr = command.Stdout
	if err := command.Start(); err != nil {
		i.t.Fatal(err)
	}
	return &waitRun{t: i.t, command: command, lines: bufio.NewScanner(stdout)}
}

// writeOldBinary puts the binary that the Host runs today in the prefix.
func (i *install) writeOldBinary() {
	i.t.Helper()
	if err := os.MkdirAll(i.prefix, 0o700); err != nil {
		i.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(i.prefix, "cumin"), []byte("the old binary\n"), 0o700); err != nil {
		i.t.Fatal(err)
	}
}

// wantBinary fails when the binary in the prefix is not the content.
func (i *install) wantBinary(want string) {
	i.t.Helper()
	if installed, err := os.ReadFile(filepath.Join(i.prefix, "cumin")); err != nil || string(installed) != want {
		i.t.Errorf("the binary in the prefix = %q, %v, want %q", installed, err, want)
	}
}

func TestInstall_AfterCurrentRunsRunsTheSixStepsInOrder(t *testing.T) {
	i := newInstall(t)
	i.writeOldBinary()

	run := i.start("--after-current-runs")
	i.endCumin(run)
	run.until("waiting for 2 poll(s) after 2026-10-04T06:58:00Z")
	i.health.writePoll("2026-10-04T06:59:00Z", "")
	run.until("poll 1 of 2")
	i.health.writePoll("2026-10-04T07:00:00Z", "")
	out, code := run.end()

	if code != 0 {
		t.Errorf("exit code = %d, want 0\n%s", code, out)
	}
	wantLines(t, out,
		"cumin has ended",
		"installed: "+i.prefix+"/cumin",
		"poll 2 of 2: 2026-10-04T07:00:00Z, no error",
		"replaced: "+i.prefix+"/cumin runs, and two polls have no error",
	)
	// One call of each step, in the order of the steps.
	calls := i.called()
	rest := calls
	for _, call := range []string{
		"git symbolic-ref --short HEAD\n",
		"git status --porcelain\n",
		"git fetch --quiet origin main\n",
		"git rev-parse origin/main\n",
		"launchctl print gui/",
		"plutil -extract ProgramArguments.0 raw",
		"go build -o ",
		"cumin stop --after-current-runs\n",
		"launchctl kickstart -k gui/",
		"plutil -extract last_poll.at raw",
	} {
		_, after, found := strings.Cut(rest, call)
		if !found {
			t.Fatalf("no call %q after the calls before it:\n%s", call, calls)
		}
		rest = after
	}
	i.wantBinary("the new binary\n")
}

func TestInstall_AWrongCheckoutLaunchAgentOrBuildEndsBeforeTheStop(t *testing.T) {
	for name, c := range map[string]struct {
		env       string
		program   string
		failBuild bool
		want      string
	}{
		"a wrong branch":       {env: "FAKE_BRANCH=feature", want: "error: the checkout is on the branch feature, not on the default branch main"},
		"a local change":       {env: "FAKE_CHANGES= M README.md", want: "error: the checkout has a local change"},
		"behind the remote":    {env: "FAKE_REMOTE_COMMIT=2222", want: "error: the checkout is at 1111, and origin/main is at 2222"},
		"a job not loaded":     {env: "FAKE_NOT_LOADED=1", want: "error: the LaunchAgent is not loaded"},
		"a job of another bin": {program: "/elsewhere/cumin", want: "error: the LaunchAgent runs /elsewhere/cumin, not "},
		"a failed build":       {failBuild: true, want: "error: the build failed. Nothing was installed"},
	} {
		t.Run(name, func(t *testing.T) {
			i := newInstall(t)
			i.writeOldBinary()
			if c.env != "" {
				i.env = []string{c.env}
			}
			if c.program != "" {
				i.writePlist(c.program)
			}
			if c.failBuild {
				failed := "#!/bin/sh\necho \"go $*\" >>\"$CALLS\"\nexit 1\n"
				if err := os.WriteFile(filepath.Join(i.bin, "go"), []byte(failed), 0o700); err != nil {
					t.Fatal(err)
				}
			}

			out, code := i.start("--after-current-runs").end()

			if code != 1 || !strings.Contains(out, c.want) {
				t.Errorf("exit code = %d, want 1 and %q\n%s", code, c.want, out)
			}
			i.wantNoCall("cumin ", "launchctl kickstart")
			if !c.failBuild {
				i.wantNoCall("go ")
			}
			i.wantBinary("the old binary\n")
		})
	}
}

func TestInstall_AfterCurrentRunsStopsWithTimeLimitAndInstallsNothing(t *testing.T) {
	i := newInstall(t)
	i.writeOldBinary()

	// cumin does not end: nobody removes the stop request.
	out, code := i.start("--after-current-runs", "--stop-timeout", "1").end()

	if code != 1 {
		t.Errorf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "error: time limit: cumin has not ended in 1s. Nothing was installed, and cumin is left as it is") {
		t.Errorf("the output does not say \"time limit\":\n%s", out)
	}
	i.wantNoCall("launchctl kickstart")
	i.wantBinary("the old binary\n")
}

func TestInstall_ASignalDuringTheWaitOfTheStopEndsTheScript(t *testing.T) {
	i := newInstall(t)
	i.writeOldBinary()

	run := i.start("--after-current-runs")
	run.until("waiting until cumin has ended")
	if err := run.command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	// cumin ends after the signal: a script that continued would install.
	if err := os.Remove(i.stopRequest); err != nil {
		t.Fatal(err)
	}
	out, code := run.end()

	if code != 1 {
		t.Errorf("exit code = %d, want 1\n%s", code, out)
	}
	if strings.Contains(out, "\ncumin has ended\n") {
		t.Errorf("the script continued after the signal:\n%s", out)
	}
	i.wantNoCall("launchctl kickstart")
	i.wantBinary("the old binary\n")
}

func TestInstall_HelpPrintsTheHeaderCommentOnly(t *testing.T) {
	i := newInstall(t)

	out, code := i.start("--help").end()

	if code != 2 {
		t.Errorf("exit code = %d, want 2\n%s", code, out)
	}
	if !strings.HasSuffix(out, "value of --stop-timeout, and 1 otherwise.\n") || strings.Contains(out, "set -eu") {
		t.Errorf("the usage text does not end with the header comment:\n%s", out)
	}
}

func TestInstall_AfterCurrentRunsFailsWithTheErrorsOfThePolls(t *testing.T) {
	i := newInstall(t)

	run := i.start("--after-current-runs")
	i.endCumin(run)
	run.until("waiting for 2 poll(s)")
	i.health.writePoll("2026-10-04T06:59:00Z", `{"repository":"example/app","message":"read the snapshot: GitHub returned 502"}`)
	out, code := run.end()

	if code != 1 {
		t.Errorf("exit code = %d, want 1\n%s", code, out)
	}
	wantLines(t, out,
		"poll 1 of 2: 2026-10-04T06:59:00Z, 1 error(s)",
		"  example/app: read the snapshot: GitHub returned 502",
		"error: the poll at 2026-10-04T06:59:00Z has 1 error(s)",
		"error: the new binary is installed and cumin was started, but the check of the polls failed. Read the errors above",
	)
	i.wantBinary("the new binary\n")
}

func TestInstall_AWrongOptionGivesExitCode2(t *testing.T) {
	i := newInstall(t)

	for _, args := range [][]string{
		{"--no-such-option"},
		{"--after-current-runs", "--stop-timeout"},
		{"--after-current-runs", "--stop-timeout", "1h"},
		{"--after-current-runs", "--stop-timeout", ""},
		{"--stop-timeout", "-1"},
	} {
		if out, code := i.start(args...).end(); code != 2 {
			t.Errorf("%v: exit code = %d, want 2\n%s", args, code, out)
		}
	}
	i.wantNoCall("git ", "cumin ", "go ", "launchctl ")
}

// Without --after-current-runs, the script calls what it called before the
// option: no git, no cumin, and no check of the polls.
func TestInstall_WithoutAfterCurrentRunsTheCallsAreAsBefore(t *testing.T) {
	for name, c := range map[string]struct {
		args []string
		want []string
	}{
		"no option": {want: []string{"go build -o "}},
		"--restart": {args: []string{"--restart"}, want: []string{
			"go build -o ", "launchctl print gui/", "plutil -extract ProgramArguments.0 raw", "launchctl kickstart -k gui/",
		}},
	} {
		t.Run(name, func(t *testing.T) {
			i := newInstall(t)

			out, code := i.start(c.args...).end()

			if code != 0 {
				t.Errorf("exit code = %d, want 0\n%s", code, out)
			}
			calls := strings.Split(strings.TrimSuffix(i.called(), "\n"), "\n")
			if len(calls) != len(c.want) {
				t.Fatalf("the calls = %q, want %d call(s) %q", calls, len(c.want), c.want)
			}
			for n, want := range c.want {
				if !strings.HasPrefix(calls[n], want) {
					t.Errorf("call %d = %q, want the prefix %q", n+1, calls[n], want)
				}
			}
			i.wantBinary("the new binary\n")
		})
	}
}
