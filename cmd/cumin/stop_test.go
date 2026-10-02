package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/state"
)

// cumin stop --after-current-runs writes the drain request with the time of
// the request, and a second call changes nothing.
func TestStopAfterCurrentRunsWritesTheDrainRequestOnce(t *testing.T) {
	dir := quotaHome(t, time.Time{})
	at := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	fixedNow(t, at)

	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"stop", "--after-current-runs"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	path := filepath.Join(dir, state.DrainFileName)
	got, found, err := state.ReadDrain(path)
	if err != nil || !found || !got.RequestedAt.Equal(at) {
		t.Errorf("drain request = %+v, %v, %v, want one requested at %v", got, found, err, at)
	}
	if !strings.Contains(stdout.String(), "starts no new work") {
		t.Errorf("stdout = %q", stdout.String())
	}

	fixedNow(t, at.Add(time.Hour))
	stdout.Reset()
	if code := runCLI([]string{"stop", "--after-current-runs"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("second call: exit code = %d, stderr = %s", code, stderr.String())
	}
	if got, _, _ := state.ReadDrain(path); !got.RequestedAt.Equal(at) {
		t.Errorf("the second call changed the time of the request to %v", got.RequestedAt)
	}
	if !strings.Contains(stdout.String(), "already requested") {
		t.Errorf("stdout of the second call = %q", stdout.String())
	}
}

// cumin stop alone does nothing: the flag says what the command does, and a
// stop at once stays SIGTERM.
func TestStopNeedsTheFlag(t *testing.T) {
	dir := quotaHome(t, time.Time{})
	for _, args := range [][]string{{"stop"}, {"stop", "now"}, {"stop", "--after-current-runs", "now"}} {
		var stdout, stderr bytes.Buffer
		if code := runCLI(args, &stdout, &stderr); code != exitBadUsage {
			t.Errorf("%v: exit code = %d, want %d", args, code, exitBadUsage)
		}
		if !strings.Contains(stderr.String(), "usage: cumin stop --after-current-runs") {
			t.Errorf("%v: stderr = %q, want the usage", args, stderr.String())
		}
	}
	if _, found, _ := state.ReadDrain(filepath.Join(dir, state.DrainFileName)); found {
		t.Error("a drain request was written")
	}
}

// cumin status says that cumin drains while the request is there, above the
// agents at work that the drain waits for.
func TestStatusShowsTheDrain(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	read := readFake(t)

	var out bytes.Buffer
	if err := writeStatus(t.Context(), &out, statusSettings(), dir, at, statusZone, read); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Stop:") {
		t.Errorf("the report names a stop without a request:\n%s", out.String())
	}

	if err := state.WriteDrain(filepath.Join(dir, state.DrainFileName), state.Drain{RequestedAt: at}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := writeStatus(t.Context(), &out, statusSettings(), dir, at, statusZone, read); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	want := "Stop:\n  draining, requested at " + stamp(at, statusZone) + ": cumin starts no new work and exits when the agents at work have ended\n"
	drain, agents := strings.Index(text, want), strings.Index(text, "Agents at work")
	if drain < 0 || agents < drain || !strings.Contains(text, "#10 cumin/status/reviewing") {
		t.Errorf("the report does not show the drain above the agents at work:\n%s", text)
	}
}
