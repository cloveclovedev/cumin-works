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

// statusZone is the time zone of the report in these tests. Its offset is
// not a full hour.
var statusZone = time.FixedZone("UTC-03:30", -(3*3600 + 30*60))

// statusAt is the time of a report whose time does not matter.
var statusAt = time.Date(2026, 10, 1, 10, 0, 0, 0, statusZone)

// readFake reads the snapshot from the fake GitHub, as cumin status does
// with the token of cumin-core.
func readFake(t *testing.T) readRepository {
	t.Helper()
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{githubtest.RequirementLabel, "cumin/status/planning"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 7, Labels: []string{githubtest.RequirementLabel, "cumin/status/implementing"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 10, Parent: 7, Title: "a", Labels: []string{"cumin/status/reviewing", "risk/low"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 11, Parent: 7, Title: "b", Labels: []string{"cumin/status/awaiting-decision", "risk/low"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 12, Parent: 7, Title: "c", Labels: []string{"cumin/status/ready", "risk/low"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 13, Parent: 7, Title: "d", Labels: []string{"cumin/status/awaiting-merge-decision", "risk/medium"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 8, Labels: []string{githubtest.RequirementLabel, "cumin/status/awaiting-plan-review"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 9, Labels: []string{githubtest.RequirementLabel, "cumin/status/awaiting-acceptance"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 14, Labels: []string{githubtest.RequirementLabel, "cumin/status/accepting"}})
	client := github.NewAppClient(server.URL, server.Client())
	return func(ctx context.Context, r config.Repository) (github.RepositorySnapshot, error) {
		return client.ReadSnapshot(ctx, githubtest.Token, r.Owner, r.Name)
	}
}

// cumin status lists the issues under the right heading, and the stored
// usage with its time and the limits of now.
func TestStatusShowsTheWorkTheWaitingIssuesAndTheQuota(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 1, 10, 0, 0, 0, statusZone)
	store := state.Open(filepath.Join(dir, stateFileName), nil)
	if err := store.SetQuota(state.Quota{
		FiveHour: state.QuotaWindow{Utilization: 0.9, ResetsAt: at.Add(2 * time.Hour)},
		Weekly:   state.QuotaWindow{Utilization: 0.2, ResetsAt: at.Add(time.Hour)},
		ReadAt:   at.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := writeStatus(t.Context(), &out, statusSettings(), dir, at, statusZone, readFake(t)); err != nil {
		t.Fatalf("writeStatus: %v", err)
	}
	text := out.String()
	for _, want := range []string{
		"read at " + stamp(at.Add(-time.Minute), statusZone),
		"5h window:     90.0% used, limit 85.0%",
		"weekly window: 20.0% used, pace limit 85.0%",
		"agent starts: stopped by the 5h window, next try at " + stamp(at.Add(2*time.Hour), statusZone),
		"Agents at work (from the labels on GitHub):\n" + agentsAtWorkNote + "\n  example-org/example-repo #6 cumin/status/planning\n  example-org/example-repo #10 cumin/status/reviewing\n  example-org/example-repo #14 cumin/status/accepting\n",
		"Waiting for a Maintainer:\n",
		"  example-org/example-repo #11 cumin/status/awaiting-decision\n",
		"  example-org/example-repo #13 cumin/status/awaiting-merge-decision\n",
		"  example-org/example-repo #8 cumin/status/awaiting-plan-review\n",
		"  example-org/example-repo #9 cumin/status/awaiting-acceptance\n",
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

// With no usage kept, the report says that the usage was not read yet, with
// no line on the agent starts, and still lists the issues.
func TestStatusWithoutAUsageStillListsTheIssues(t *testing.T) {
	var out bytes.Buffer
	if err := writeStatus(t.Context(), &out, statusSettings(), t.TempDir(), statusAt, statusZone, readFake(t)); err != nil {
		t.Fatalf("writeStatus: %v", err)
	}
	text := out.String()
	if !strings.HasPrefix(text, "Quota:\n  not read yet: cumin run reads the usage before its first start\n\n") || !strings.Contains(text, "#11 cumin/status/awaiting-decision") {
		t.Errorf("the report:\n%s", text)
	}
	if strings.Contains(text, "agent starts:") {
		t.Errorf("the report has a line on the agent starts without a usage:\n%s", text)
	}
}

// The note line follows the short heading of the agents at work, also when
// no agent works, and names both causes of a wait with no agent.
func TestStatusNotesUnderTheAgentsAtWorkThatAnIssueCanWaitWithNoAgent(t *testing.T) {
	for _, cause := range []string{"agent starts are stopped", "a stop after the current runs is requested"} {
		if !strings.Contains(agentsAtWorkNote, cause) {
			t.Errorf("the note line does not name %q: %s", cause, agentsAtWorkNote)
		}
	}
	none := func(context.Context, config.Repository) (github.RepositorySnapshot, error) {
		return github.RepositorySnapshot{}, nil
	}
	for name, read := range map[string]readRepository{"with agents at work": readFake(t), "with no agent at work": none} {
		var out bytes.Buffer
		if err := writeStatus(t.Context(), &out, statusSettings(), t.TempDir(), statusAt, statusZone, read); err != nil {
			t.Fatalf("%s: writeStatus: %v", name, err)
		}
		if !strings.Contains(out.String(), "\nAgents at work (from the labels on GitHub):\n"+agentsAtWorkNote+"\n  ") {
			t.Errorf("%s: the report has no note line under the heading:\n%s", name, out.String())
		}
	}
	var out bytes.Buffer
	if err := writeStatus(t.Context(), &out, statusSettings(), t.TempDir(), statusAt, statusZone, none); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), agentsAtWorkNote+"\n  none\n") {
		t.Errorf("the empty list is not under the note line:\n%s", out.String())
	}
}

func TestStatusShowsTheAllowance(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 10, 1, 10, 0, 0, 0, statusZone)
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
	if err := writeStatus(t.Context(), &out, statusSettings(), dir, at, statusZone, readFake(t)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"limit 100.0%", "allowance: the 5h limit is 100% until " + stamp(until, statusZone), "agent starts: go on"} {
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
	err := writeStatus(t.Context(), &out, statusSettings(), t.TempDir(), statusAt, statusZone, fail)
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

// cumin status needs only the cumin-core App: a missing role App does not
// matter, and a missing cumin-core App names the key.
func TestCoreClientIDNeedsOnlyTheCuminCoreApp(t *testing.T) {
	settings := statusSettings()
	settings.GitHubApps = map[string]map[string]string{"Example-Org": {config.AppCuminCore: "Iv23liCORE"}}
	if id, err := coreClientID(settings, "example-org"); err != nil || id != "Iv23liCORE" {
		t.Errorf("coreClientID = %q, %v, want the cumin-core ID", id, err)
	}
	if _, err := coreClientID(settings, "other-org"); err == nil || !strings.Contains(err.Error(), "github_apps.other-org.cumin-core") {
		t.Errorf("err = %v, want one that names the key", err)
	}
}
