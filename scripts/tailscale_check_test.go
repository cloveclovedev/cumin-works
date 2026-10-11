package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// tailscaleCheck is one run of scripts/tailscale-check.sh on a device that
// the test builds in a temporary directory: fake nc and tailscale on PATH.
type tailscaleCheck struct {
	t     *testing.T
	bin   string
	calls string
	// answers are the ports that answer the fake nc, as "<address>:<port>".
	answers []string
	// status is what the fake tailscale prints for "status --json".
	status string
}

// tailscaleCheckTools are the tools of macOS that the script may call.
// Every run of the tests has only these and the two fakes on PATH, so a
// call of another tool fails the tests.
var tailscaleCheckTools = []string{"plutil", "sed", "tr"}

// The fakes write every call to the file CALLS, one line for each call.
// The fake nc answers only on the ports of FAKE_ANSWERS. The fake
// tailscale knows "status --json" only, so "tailscale ping" fails.
var tailscaleCheckFakes = map[string]string{
	"nc": `#!/bin/sh
echo "nc $*" >>"$CALLS"
while [ $# -gt 2 ]; do shift; done
for answer in $FAKE_ANSWERS; do
  [ "$answer" != "$1:$2" ] || exit 0
done
exit 1
`,
	"tailscale": `#!/bin/sh
echo "tailscale $*" >>"$CALLS"
[ "$*" = "status --json" ] || { echo "fake tailscale: unexpected call: $*" >&2; exit 1; }
printf '%s' "$FAKE_STATUS"
`,
}

// Two online devices and one offline device, as "tailscale status --json"
// prints them. The addresses are names, so that no real address is here.
const twoOnlineDevices = `{"Self":{"DNSName":"host.example.","Online":true},"Peer":{
"nodekey:aa":{"DNSName":"first.example.","TailscaleIPs":["first-address","first-other"],"Online":true},
"nodekey:bb":{"DNSName":"second.example.","TailscaleIPs":["second-address"],"Online":true},
"nodekey:cc":{"DNSName":"third.example.","TailscaleIPs":["third-address"],"Online":false}}}`

func newTailscaleCheck(t *testing.T) *tailscaleCheck {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("scripts/tailscale-check.sh needs plutil, which is macOS only")
	}
	dir := t.TempDir()
	c := &tailscaleCheck{t: t, bin: filepath.Join(dir, "bin"), calls: filepath.Join(dir, "calls"), status: twoOnlineDevices}
	if err := os.MkdirAll(c.bin, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tool := range tailscaleCheckTools {
		path, err := exec.LookPath(tool)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(path, filepath.Join(c.bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	for name, script := range tailscaleCheckFakes {
		if err := os.WriteFile(filepath.Join(c.bin, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(c.calls, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return c
}

// run runs the script to its end, and returns its output and its exit code.
func (c *tailscaleCheck) run(args ...string) (string, int) {
	c.t.Helper()
	command := exec.Command("./tailscale-check.sh", args...)
	command.Env = []string{
		"PATH=" + c.bin,
		"CALLS=" + c.calls,
		"FAKE_ANSWERS=" + strings.Join(c.answers, " "),
		"FAKE_STATUS=" + c.status,
	}
	out, err := command.CombinedOutput()
	return string(out), exitCode(c.t, err)
}

// wantCalls fails when the calls of the fakes are not exactly these.
func (c *tailscaleCheck) wantCalls(want ...string) {
	c.t.Helper()
	calls, err := os.ReadFile(c.calls)
	if err != nil {
		c.t.Fatal(err)
	}
	var wanted strings.Builder
	for _, call := range want {
		wanted.WriteString(call + "\n")
	}
	if got := string(calls); got != wanted.String() {
		c.t.Errorf("the calls are:\n%swant:\n%s", got, wanted.String())
	}
}

func TestTailscaleCheck_MemberSeesOnlyTheTwoPortsOfTheHost(t *testing.T) {
	c := newTailscaleCheck(t)
	c.answers = []string{"host.example:22", "host.example:5900"}

	out, code := c.run("member", "host.example")

	if want := "ok: host.example port 22: expected answer, got answer\n" +
		"ok: host.example port 5900: expected answer, got answer\n" +
		"ok: host.example port 445: expected no answer, got no answer\n"; out != want {
		t.Errorf("the output is:\n%swant:\n%s", out, want)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0: every check is ok\n%s", code, out)
	}
	// Every connection attempt has a limit, and nothing but nc runs: no
	// tailscale ping, and no command that changes the Host.
	c.wantCalls(
		"nc -z -G 5 -w 5 host.example 22",
		"nc -z -G 5 -w 5 host.example 5900",
		"nc -z -G 5 -w 5 host.example 445",
	)
}

func TestTailscaleCheck_MemberFailsWhenTheOtherPortAnswers(t *testing.T) {
	c := newTailscaleCheck(t)
	c.answers = []string{"host.example:22", "host.example:5900", "host.example:8080"}

	out, code := c.run("member", "host.example", "--other-port", "8080", "--timeout", "2")

	wantLines(t, out,
		"ok: host.example port 22: expected answer, got answer",
		"ok: host.example port 5900: expected answer, got answer",
		"fail: host.example port 8080: expected no answer, got answer",
	)
	if code != 1 {
		t.Errorf("exit code = %d, want 1: the other port answers\n%s", code, out)
	}
	c.wantCalls(
		"nc -z -G 2 -w 2 host.example 22",
		"nc -z -G 2 -w 2 host.example 5900",
		"nc -z -G 2 -w 2 host.example 8080",
	)
}

func TestTailscaleCheck_MemberFailsWhenTheSSHPortDoesNotAnswer(t *testing.T) {
	c := newTailscaleCheck(t)
	c.answers = []string{"host.example:5900"}

	out, code := c.run("member", "host.example")

	wantLines(t, out,
		"fail: host.example port 22: expected answer, got no answer",
		"ok: host.example port 5900: expected answer, got answer",
		"ok: host.example port 445: expected no answer, got no answer",
	)
	if code != 1 {
		t.Errorf("exit code = %d, want 1: the SSH port does not answer\n%s", code, out)
	}
}

func TestTailscaleCheck_HostReachesNoPortOfTheOnlineDevices(t *testing.T) {
	c := newTailscaleCheck(t)

	out, code := c.run("host")

	if want := "ok: first.example port 22: expected no answer, got no answer\n" +
		"ok: first.example port 5900: expected no answer, got no answer\n" +
		"ok: second.example port 22: expected no answer, got no answer\n" +
		"ok: second.example port 5900: expected no answer, got no answer\n"; out != want {
		t.Errorf("the output is:\n%swant:\n%s", out, want)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0: every check is ok\n%s", code, out)
	}
	// The script reads the devices once, and never calls tailscale ping.
	// The offline device gets no connection attempt.
	c.wantCalls(
		"tailscale status --json",
		"nc -z -G 5 -w 5 first-address 22",
		"nc -z -G 5 -w 5 first-address 5900",
		"nc -z -G 5 -w 5 second-address 22",
		"nc -z -G 5 -w 5 second-address 5900",
	)
}

func TestTailscaleCheck_HostFailsWhenADeviceAnswersOnTheSSHPort(t *testing.T) {
	c := newTailscaleCheck(t)
	c.answers = []string{"second-address:22"}

	out, code := c.run("host")

	wantLines(t, out,
		"ok: first.example port 22: expected no answer, got no answer",
		"ok: first.example port 5900: expected no answer, got no answer",
		"fail: second.example port 22: expected no answer, got answer",
		"ok: second.example port 5900: expected no answer, got no answer",
	)
	if code != 1 {
		t.Errorf("exit code = %d, want 1: a device answers on the SSH port\n%s", code, out)
	}
}

// A run that checks nothing must not look like a run where every check is ok.
func TestTailscaleCheck_HostFailsWhenNoOtherDeviceIsOnline(t *testing.T) {
	c := newTailscaleCheck(t)
	c.status = `{"Self":{"DNSName":"host.example.","Online":true}}`

	out, code := c.run("host")

	wantLines(t, out, "error: tailscale status --json lists no other device as online, so the script checked nothing")
	if code != 1 {
		t.Errorf("exit code = %d, want 1: the script checked nothing\n%s", code, out)
	}
	c.wantCalls("tailscale status --json")
}

func TestTailscaleCheck_WrongArgumentsExitWithTwo(t *testing.T) {
	for name, args := range map[string][]string{
		"no mode":                     {},
		"unknown mode":                {"both"},
		"member without a host":       {"member"},
		"unknown option":              {"member", "host.example", "--port", "80"},
		"option without a value":      {"member", "host.example", "--other-port"},
		"other port is not a number":  {"member", "host.example", "--other-port", "ssh"},
		"other port is the SSH port":  {"member", "host.example", "--other-port", "22"},
		"timeout is not a number":     {"host", "--timeout", "soon"},
		"timeout is zero":             {"host", "--timeout", "0"},
		"other port in the host mode": {"host", "--other-port", "80"},
	} {
		t.Run(name, func(t *testing.T) {
			c := newTailscaleCheck(t)

			out, code := c.run(args...)

			if code != 2 {
				t.Errorf("exit code = %d, want 2: wrong arguments\n%s", code, out)
			}
			c.wantCalls()
		})
	}
}
