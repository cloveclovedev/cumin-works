package scripts

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// replace is one run of scripts/replace-binary.sh on a Host that the test
// builds in a temporary directory: fake git, cumin, launchctl, plutil, and
// go on PATH, and the files of the LaunchAgent under a temporary home.
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

// replaceTools are the tools of macOS that the script, scripts/install.sh,
// and scripts/cumin-health.sh may call. Every run of the tests has only
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
		t.Skip("scripts/replace-binary.sh needs launchctl and plutil, which are macOS only")
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

// start starts the script, with a short wait between two reads.
func (r *replace) start(args ...string) *waitRun {
	r.t.Helper()
	command := exec.Command("./replace-binary.sh", append([]string{"--prefix", r.prefix}, args...)...)
	command.Env = append([]string{
		"PATH=" + r.bin, "HOME=" + r.health.dir, "CALLS=" + r.calls,
		"CUMIN_REPLACE_INTERVAL=0.05", "CUMIN_HEALTH_INTERVAL=0.05", "CUMIN_HEALTH_NOW=" + r.health.now,
	}, r.env...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		r.t.Fatal(err)
	}
	command.Stderr = command.Stdout
	if err := command.Start(); err != nil {
		r.t.Fatal(err)
	}
	return &waitRun{t: r.t, command: command, lines: bufio.NewScanner(stdout)}
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

func TestReplaceBinary_RunsTheStepsInOrder(t *testing.T) {
	r := newReplace(t)

	run := r.start()
	r.endCumin(run)
	run.until("waiting for 2 poll(s) after 2026-10-04T06:58:00Z")
	r.health.writePoll("2026-10-04T06:59:00Z", "")
	run.until("poll 1 of 2")
	r.health.writePoll("2026-10-04T07:00:00Z", "")
	out, code := run.end()

	if code != 0 {
		t.Errorf("exit code = %d, want 0\n%s", code, out)
	}
	wantLines(t, out,
		"cumin has ended",
		"installed: "+r.prefix+"/cumin",
		"poll 2 of 2: 2026-10-04T07:00:00Z, no error",
		"replaced: "+r.prefix+"/cumin runs, and two polls have no error",
	)
	// One call of each step, in the order of the steps.
	calls := r.called()
	rest := calls
	for _, call := range []string{
		"git symbolic-ref --short HEAD\n",
		"git status --porcelain\n",
		"git fetch --quiet origin main\n",
		"git rev-parse origin/main\n",
		"launchctl print gui/",
		"plutil -extract ProgramArguments.0 raw",
		"cumin stop --after-current-runs\n",
		"go build -o ",
		"launchctl kickstart -k gui/",
		"plutil -extract last_poll.at raw",
	} {
		_, after, found := strings.Cut(rest, call)
		if !found {
			t.Fatalf("no call %q after the calls before it:\n%s", call, calls)
		}
		rest = after
	}
	if installed, err := os.ReadFile(filepath.Join(r.prefix, "cumin")); err != nil || string(installed) != "the new binary\n" {
		t.Errorf("the new binary is not installed: %q, %v", installed, err)
	}
}

func TestReplaceBinary_AWrongCheckoutOrTargetEndsBeforeTheStop(t *testing.T) {
	for name, c := range map[string]struct {
		env     string
		program string
		want    string
	}{
		"a wrong branch":       {env: "FAKE_BRANCH=feature", want: "error: the checkout is on the branch feature, not on the default branch main"},
		"a local change":       {env: "FAKE_CHANGES= M README.md", want: "error: the checkout has a local change"},
		"behind the remote":    {env: "FAKE_REMOTE_COMMIT=2222", want: "error: the checkout is at 1111, and origin/main is at 2222"},
		"a job not loaded":     {env: "FAKE_NOT_LOADED=1", want: "error: the LaunchAgent is not loaded"},
		"a job of another bin": {program: "/elsewhere/cumin", want: "error: the LaunchAgent runs /elsewhere/cumin, not "},
	} {
		t.Run(name, func(t *testing.T) {
			r := newReplace(t)
			if c.env != "" {
				r.env = []string{c.env}
			}
			if c.program != "" {
				r.writePlist(c.program)
			}

			out, code := r.start().end()

			if code != 1 || !strings.Contains(out, c.want) {
				t.Errorf("exit code = %d, want 1 and %q\n%s", code, c.want, out)
			}
			r.wantNoCall("cumin ", "go ", "launchctl kickstart")
		})
	}
}

func TestReplaceBinary_StopsWithTimeLimitAndInstallsNothing(t *testing.T) {
	r := newReplace(t)

	// cumin does not end: nobody removes the stop request.
	out, code := r.start("--stop-timeout", "1").end()

	if code != 1 {
		t.Errorf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "error: time limit: cumin has not ended in 1s. Nothing was installed, and cumin is left as it is") {
		t.Errorf("the output does not say \"time limit\":\n%s", out)
	}
	r.wantNoCall("go ", "launchctl kickstart")
	if _, err := os.Stat(filepath.Join(r.prefix, "cumin")); !os.IsNotExist(err) {
		t.Errorf("a binary is installed after the time limit: %v", err)
	}
}

func TestReplaceBinary_FailsWithTheErrorsOfThePolls(t *testing.T) {
	r := newReplace(t)

	run := r.start()
	r.endCumin(run)
	run.until("waiting for 2 poll(s)")
	r.health.writePoll("2026-10-04T06:59:00Z", `{"repository":"example/app","message":"read the snapshot: GitHub returned 502"}`)
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
}

func TestReplaceBinary_DryRunPrintsTheStepsAndChangesNothing(t *testing.T) {
	r := newReplace(t)

	out, code := r.start("--dry-run", "--stop-timeout", "90").end()

	if code != 0 {
		t.Errorf("exit code = %d, want 0\n%s", code, out)
	}
	wantLines(t, out,
		`dry run: would run "git fetch origin main", and compare HEAD with origin/main`,
		`dry run: step 3 of 5: would run "cumin stop --after-current-runs", and wait at most 90s until cumin has ended`,
		"dry run: nothing changed",
	)
	for _, step := range []string{"step 1 of 5", "step 2 of 5", "step 4 of 5", "step 5 of 5"} {
		if !strings.Contains(out, step) {
			t.Errorf("the output does not name %q:\n%s", step, out)
		}
	}
	// Only the calls that read are left: the checkout and the target.
	r.wantNoCall("git fetch", "cumin ", "go ", "launchctl kickstart")
	if _, err := os.Stat(r.stopRequest); !os.IsNotExist(err) {
		t.Errorf("a stop request is written: %v", err)
	}

	// A check that fails is a failure of the dry run too.
	r.env = []string{"FAKE_BRANCH=feature"}
	if out, code := r.start("--dry-run").end(); code != 1 {
		t.Errorf("a wrong branch: exit code = %d, want 1\n%s", code, out)
	}
}

func TestReplaceBinary_AWrongOptionGivesExitCode2(t *testing.T) {
	r := newReplace(t)

	for _, args := range [][]string{
		{"--no-such-option"},
		{"--stop-timeout"},
		{"--stop-timeout", "1h"},
		{"--prefix", ""},
	} {
		if out, code := r.start(args...).end(); code != 2 {
			t.Errorf("%v: exit code = %d, want 2\n%s", args, code, out)
		}
	}
	r.wantNoCall("git ", "cumin ", "go ", "launchctl ")
}
