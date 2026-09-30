package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/state"
)

// quotaHome makes a home directory for the test, with the latest usage that
// cumin run kept, when reset is not zero. It returns the state directory.
func quotaHome(t *testing.T, reset time.Time) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".local", "state", "cumin")
	if !reset.IsZero() {
		store := state.Open(filepath.Join(dir, stateFileName), nil)
		if err := store.SetQuota(state.Quota{
			FiveHour: state.QuotaWindow{Utilization: 0.9, ResetsAt: reset},
			Weekly:   state.QuotaWindow{Utilization: 0.1, ResetsAt: reset.Add(48 * time.Hour)},
		}); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func fixedNow(t *testing.T, at time.Time) {
	t.Helper()
	saved := now
	now = func() time.Time { return at }
	t.Cleanup(func() { now = saved })
}

// Q2: cumin quota allow writes the reset time of the current 5h window,
// and says that the weekly limit still applies.
func TestQuotaAllowWritesTheResetOfTheCurrentFiveHourWindow(t *testing.T) {
	at := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	reset := at.Add(3 * time.Hour)
	dir := quotaHome(t, reset)
	fixedNow(t, at)

	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"quota", "allow"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	got, err := state.ReadAllowance(filepath.Join(dir, state.AllowanceFileName))
	if err != nil || !got.FiveHourUntil.Equal(reset) {
		t.Errorf("allowance = %+v, %v, want until %v", got, err, reset)
	}
	if !strings.Contains(stdout.String(), "weekly pace limit still applies") {
		t.Errorf("stdout = %q", stdout.String())
	}
	// The usage is the Owner's to see in cumin status, not here.
	if strings.Contains(stdout.String(), "0.9") || strings.Contains(stdout.String(), "90") {
		t.Errorf("stdout holds the usage: %q", stdout.String())
	}
}

// Without a known current 5h window, nothing is allowed.
func TestQuotaAllowWithoutACurrentFiveHourWindowAllowsNothing(t *testing.T) {
	at := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	for name, reset := range map[string]time.Time{
		"no usage kept":     {},
		"the window passed": at.Add(-time.Minute),
	} {
		t.Run(name, func(t *testing.T) {
			dir := quotaHome(t, reset)
			fixedNow(t, at)
			var stdout, stderr bytes.Buffer
			if code := runCLI([]string{"quota", "allow"}, &stdout, &stderr); code != exitFailure {
				t.Errorf("exit code = %d, want %d", code, exitFailure)
			}
			if !strings.Contains(stderr.String(), "Nothing was allowed") {
				t.Errorf("stderr = %q", stderr.String())
			}
			if got, _ := state.ReadAllowance(filepath.Join(dir, state.AllowanceFileName)); !got.FiveHourUntil.IsZero() {
				t.Errorf("an allowance was written: %+v", got)
			}
		})
	}
}

func TestQuotaAllowRejectsAnExtraArgument(t *testing.T) {
	quotaHome(t, time.Time{})
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"quota", "allow", "now"}, &stdout, &stderr); code != exitBadUsage {
		t.Errorf("exit code = %d, want %d", code, exitBadUsage)
	}
}
