package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/core/state"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

func statusSettings() *config.Settings {
	return &config.Settings{
		Repositories: []config.Repository{{Owner: "example-org", Name: "example-repo"}},
		Quota: config.QuotaSettings{
			FiveHour: config.FiveHourQuota{Threshold: 85},
			Weekly:   config.WeeklyQuota{Target: 85, Lead: 24 * time.Hour},
		},
	}
}

// readFake reads the snapshot from the fake GitHub, as cumin status does
// with the token of cumin-core.
func readFake(t *testing.T) readRepository {
	t.Helper()
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{githubtest.RequirementLabel, "cumin/status/planning"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 7, Labels: []string{githubtest.RequirementLabel, "cumin/status/implementing"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 10, Parent: 7, Title: "a", Labels: []string{"cumin/status/reviewing", "risk/low"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 11, Parent: 7, Title: "b", Labels: []string{"cumin/status/awaiting-owner-decision", "risk/low"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 12, Parent: 7, Title: "c", Labels: []string{"cumin/status/ready", "risk/low"}})
	client := github.NewAppClient(server.URL, server.Client())
	return func(ctx context.Context, r config.Repository) (github.RepositorySnapshot, error) {
		return client.ReadSnapshot(ctx, githubtest.Token, r.Owner, r.Name)
	}
}

// cumin status lists the issues under the right heading, and the stored
// usage with its time and the limits of now.
func TestStatusShowsTheWorkTheWaitingIssuesAndTheQuota(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 1, 10, 0, 0, 0, time.Local)
	store := state.Open(filepath.Join(dir, stateFileName), nil)
	if err := store.SetQuota(state.Quota{
		FiveHour: state.QuotaWindow{Utilization: 0.9, ResetsAt: at.Add(2 * time.Hour)},
		Weekly:   state.QuotaWindow{Utilization: 0.2, ResetsAt: at.Add(time.Hour)},
		ReadAt:   at.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := writeStatus(t.Context(), &out, statusSettings(), dir, at, readFake(t)); err != nil {
		t.Fatalf("writeStatus: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"read at " + stamp(at.Add(-time.Minute)),
		"5h window:     90.0% used, limit 85.0%",
		"weekly window: 20.0% used, pace limit 85.0%",
		"new starts: stopped by the 5h window, next try at " + stamp(at.Add(2*time.Hour)),
		"Agents at work (from the labels on GitHub):\n  example-org/example-repo #6 cumin/status/planning\n  example-org/example-repo #10 cumin/status/reviewing\n",
		"Waiting for the Owner:\n  example-org/example-repo #11 cumin/status/awaiting-owner-decision\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the report has no %q:\n%s", want, text)
		}
	}
	// A requirement issue in implementing has no agent of its own (R3).
	if strings.Contains(text, "#7 ") {
		t.Errorf("the report lists #7:\n%s", text)
	}
}

// With no usage kept, the report says so and still lists the issues. An
// allowance shows its end.
func TestStatusWithoutAUsageStillListsTheIssues(t *testing.T) {
	var out bytes.Buffer
	if err := writeStatus(t.Context(), &out, statusSettings(), t.TempDir(), time.Now(), readFake(t)); err != nil {
		t.Fatalf("writeStatus: %v", err)
	}
	if !strings.Contains(out.String(), "not read yet") || !strings.Contains(out.String(), "#11 cumin/status/awaiting-owner-decision") {
		t.Errorf("the report:\n%s", out.String())
	}
}

func TestStatusShowsTheAllowance(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 1, 10, 0, 0, 0, time.Local)
	until := at.Add(2 * time.Hour)
	store := state.Open(filepath.Join(dir, stateFileName), nil)
	if err := store.SetQuota(state.Quota{
		FiveHour: state.QuotaWindow{Utilization: 0.9, ResetsAt: until},
		Weekly:   state.QuotaWindow{Utilization: 0.2, ResetsAt: at.Add(time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteAllowance(filepath.Join(dir, state.AllowanceFileName), state.Allowance{FiveHourUntil: until}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := writeStatus(t.Context(), &out, statusSettings(), dir, at, readFake(t)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"limit 100.0%", "allowance: the 5h limit is 100% until " + stamp(until), "new starts: go on"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the report has no %q:\n%s", want, out.String())
		}
	}
}

// A repository that cannot be read is named, and the report is not
// complete.
func TestStatusNamesARepositoryThatWasNotRead(t *testing.T) {
	var out bytes.Buffer
	fail := func(context.Context, config.Repository) (github.RepositorySnapshot, error) {
		return github.RepositorySnapshot{}, errors.New("no token")
	}
	err := writeStatus(t.Context(), &out, statusSettings(), t.TempDir(), time.Now(), fail)
	if err == nil || !strings.Contains(out.String(), "example-org/example-repo: not read: no token") {
		t.Errorf("err = %v, report:\n%s", err, out.String())
	}
}

func TestVersionPrintsAVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"--version"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	if !strings.HasPrefix(stdout.String(), "cumin ") || len(strings.TrimSpace(stdout.String())) <= len("cumin") {
		t.Errorf("stdout = %q, want a version", stdout.String())
	}
	if code := runCLI([]string{"--version", "status"}, &stdout, &stderr); code != exitBadUsage {
		t.Errorf("--version with a command: exit code = %d, want %d", code, exitBadUsage)
	}
}

func TestStatusRejectsAnExtraArgument(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"status", "now"}, &stdout, &stderr); code != exitBadUsage {
		t.Errorf("exit code = %d, want %d", code, exitBadUsage)
	}
}
