package workflow_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/core/state"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
	"github.com/cloveclovedev/cumin-works/internal/quota"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

// withMonitorFile gives the service a monitor file in a directory of its
// own, as cumin run does with the state directory.
func withMonitorFile(t *testing.T, service *workflow.Service) string {
	t.Helper()
	service.MonitorPath = filepath.Join(t.TempDir(), state.MonitorFileName)
	return service.MonitorPath
}

// monitorFile is the monitor file as a reader outside cumin parses it.
type monitorFile struct {
	Version  *int `json:"version"`
	LastPoll *struct {
		At     *time.Time `json:"at"`
		Errors *[]struct {
			Repository string `json:"repository"`
			Message    string `json:"message"`
		} `json:"errors"`
	} `json:"last_poll"`
	StopRequested *bool `json:"stop_requested"`
	Quota         *struct {
		State          string     `json:"state"`
		StoppedWindows *[]string  `json:"stopped_windows"`
		NextTryAt      *time.Time `json:"next_try_at"`
	} `json:"quota"`
	Agents *[]state.MonitorAgent `json:"agents"`
}

// readMonitorFile reads the monitor file, and fails the test when a field
// that is always written is missing.
func readMonitorFile(t *testing.T, path string) (monitorFile, string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the monitor file: %v", err)
	}
	var read monitorFile
	if err := json.Unmarshal(raw, &read); err != nil {
		t.Fatalf("the monitor file is not JSON: %v\n%s", err, raw)
	}
	if read.Version == nil || read.LastPoll == nil || read.LastPoll.At == nil || read.LastPoll.Errors == nil ||
		read.StopRequested == nil || read.Quota == nil || read.Quota.StoppedWindows == nil || read.Agents == nil {
		t.Fatalf("the monitor file misses a field that is always written:\n%s", raw)
	}
	return read, string(raw)
}

// After a poll, the monitor file holds the fields of the design note
// (status-menu-bar.md, the table of the fields), and its directory holds no
// temporary file.
func TestMonitorFile_APollWritesTheFieldsOfTheDesignNote(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := sc.service()
	path := withMonitorFile(t, service)
	sc.pollAndWait(t, service)

	read, raw := readMonitorFile(t, path)
	if *read.Version != 1 {
		t.Errorf("version = %d, want 1", *read.Version)
	}
	if !read.LastPoll.At.Equal(sceneNow) || !strings.Contains(raw, `"at": "`+sceneNow.UTC().Format(time.RFC3339)+`"`) {
		t.Errorf("last_poll.at is not the time of the poll %s in UTC:\n%s", sceneNow.UTC().Format(time.RFC3339), raw)
	}
	if len(*read.LastPoll.Errors) != 0 {
		t.Errorf("last_poll.errors = %v, want none", *read.LastPoll.Errors)
	}
	if *read.StopRequested {
		t.Error("stop_requested = true, want false")
	}
	if read.Quota.State != "open" || len(*read.Quota.StoppedWindows) != 0 || read.Quota.NextTryAt != nil {
		t.Errorf("quota is not open with no window and no next try:\n%s", raw)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != state.MonitorFileName {
		t.Errorf("the directory holds %v, want only %s", entries, state.MonitorFileName)
	}
}

// A failed poll writes the reason on one line and the time of the poll. A
// later poll that succeeds writes no error and its own time.
func TestMonitorFile_AFailedPollWritesItsErrorAndTheTimeOfThePoll(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	sc.fake.SetFile(sc.repo, ".cumin/config.toml", wrongFile(`work_dir = "/tmp/elsewhere"`))
	service := sc.service()
	path := withMonitorFile(t, service)
	pollTimes(t, service, 1, true)

	read, raw := readMonitorFile(t, path)
	if !read.LastPoll.At.Equal(sceneNow) {
		t.Errorf("last_poll.at = %s, want the time of the failed poll %s", read.LastPoll.At, sceneNow)
	}
	failed := *read.LastPoll.Errors
	if len(failed) != 1 || failed[0].Repository != "example-org/example-repo" ||
		!strings.Contains(failed[0].Message, "work_dir") || strings.Contains(failed[0].Message, "\n") {
		t.Fatalf("last_poll.errors does not name the repository and the reason on one line:\n%s", raw)
	}

	later := sceneNow.Add(time.Minute)
	sc.clock.Set(later)
	sc.fake.SetFile(sc.repo, ".cumin/config.toml", githubtest.File{Content: "max_review_rounds = 2\n"})
	sc.pollAndWait(t, service)
	read, raw = readMonitorFile(t, path)
	if len(*read.LastPoll.Errors) != 0 || !read.LastPoll.At.Equal(later) {
		t.Errorf("after a poll that succeeded, the file still holds the error or the old time:\n%s", raw)
	}
}

// The monitor file names the window that stops the agent starts and the
// next try time, and holds no usage number: no utilization, no limit.
func TestMonitorFile_HoldsNoUsageNumber(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	reset := sceneNow.Add(2 * time.Hour)
	sc.setQuota(t, 0.90, reset, 0.10, sceneNow.Add(time.Hour))
	service := sc.service()
	withState(t, service)
	path := withMonitorFile(t, service)
	sc.pollAndWait(t, service)
	if n := sc.agentRuns(t); n != 0 {
		t.Fatalf("%d agent runs, want the 5h window to stop the start", n)
	}

	read, raw := readMonitorFile(t, path)
	if read.Quota.State != "stopped" || !slices.Equal(*read.Quota.StoppedWindows, []string{"5h"}) ||
		read.Quota.NextTryAt == nil || !read.Quota.NextTryAt.Equal(reset) {
		t.Errorf("quota is not stopped by the 5h window with the next try at %s:\n%s", reset.UTC().Format(time.RFC3339), raw)
	}
	// Every number of the file, outside of the times, is the version. The
	// usage is 0.90 and 0.10, and the limit is 85.
	var fields map[string]any
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields["last_poll"].(map[string]any), "at")
	delete(fields["quota"].(map[string]any), "next_try_at")
	delete(fields, "version")
	rest, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	numbers := strings.ReplaceAll(string(rest), `"5h"`, "")
	if strings.ContainsAny(numbers, "0123456789") || strings.Contains(raw, "utilization") || strings.Contains(raw, "resets") {
		t.Errorf("the monitor file holds a number outside of the version, the times, and the name of the 5h window:\n%s", raw)
	}
}

// A read of the usage that failed in the poll is the quota state "unread".
func TestMonitorFile_AnUnreadUsageIsItsOwnQuotaState(t *testing.T) {
	sc := newScene(t)
	sc.failQuota(t)
	service := sc.service()
	path := withMonitorFile(t, service)
	sc.pollAndWait(t, service)

	read, raw := readMonitorFile(t, path)
	if read.Quota.State != "unread" || len(*read.Quota.StoppedWindows) != 0 || read.Quota.NextTryAt != nil {
		t.Errorf("quota is not unread with no window and no next try:\n%s", raw)
	}
}

// A stop after the current runs is in the file from the poll that read the
// request.
func TestMonitorFile_SaysThatCuminStopsAfterTheCurrentRuns(t *testing.T) {
	sc := newScene(t)
	service := sc.service()
	path := withMonitorFile(t, service)
	service.StopRequestPath = filepath.Join(t.TempDir(), state.StopRequestFileName)
	requestStop(t, service.StopRequestPath)
	sc.pollAndWait(t, service)

	if read, raw := readMonitorFile(t, path); !*read.StopRequested {
		t.Errorf("stop_requested = false after a stop request:\n%s", raw)
	}
}

// A monitor file that cannot be written does not fail the poll: the poll
// starts the agent as usual, and a warning names the path.
func TestMonitorFile_AWriteThatFailsDoesNotFailThePoll(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := sc.service()
	// The directory of the file is a regular file, so no user can write
	// below it.
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	service.MonitorPath = filepath.Join(blocked, state.MonitorFileName)
	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	service.Wait()

	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1", n)
	}
	if !strings.Contains(sc.logs.String(), `"level":"WARN","msg":"the monitor file was not written; cumin goes on as usual"`) {
		t.Errorf("no warning about the monitor file:\n%s", sc.logs.String())
	}
}

// The golden file is one full example of the monitor file. The reader of
// the file tests against the same file.
func TestMonitorFile_MatchesTheGoldenFile(t *testing.T) {
	at := time.Date(2026, 10, 4, 7, 0, 5, 0, time.UTC)
	built := workflow.BuildMonitorFile(workflow.MonitorFacts{
		PollEnded:     at,
		PollErrors:    []state.MonitorPollError{{Repository: "example/app", Message: "read the snapshot: GitHub returned 502"}},
		StopRequested: true,
		UsageKept:     true,
		Usage: quota.Usage{
			FiveHour: quota.Window{Utilization: 0.90, ResetsAt: time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)},
			Weekly:   quota.Window{Utilization: 0.10, ResetsAt: at.Add(time.Hour)},
		},
		QuotaSettings: config.QuotaSettings{
			FiveHour: config.FiveHourQuota{Threshold: 85},
			Weekly:   config.WeeklyQuota{Target: 85, Lead: 24 * time.Hour},
		},
		Location: time.UTC,
		Running: []state.MonitorAgent{
			{Repository: "example/tool", Issue: 12, Role: "implementer", Request: "implement", Title: "feat(api): add the list endpoint", URL: "https://github.com/example/tool/issues/12"},
			{Repository: "example/app", Issue: 31, Role: "planner", Request: "acceptance check", Title: "Show the history of an item", URL: "https://github.com/example/app/issues/31"},
		},
	})
	path := filepath.Join(t.TempDir(), state.MonitorFileName)
	if err := state.WriteMonitorFile(path, built); err != nil {
		t.Fatalf("WriteMonitorFile: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "monitor-file.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("the monitor file is\n%s\nwant testdata/monitor-file.json:\n%s", got, want)
	}
}

// The pure rule of the quota state: no kept usage stops nothing, a read
// that failed in the poll wins over a kept usage, and a window whose reset
// has passed stops nothing.
func TestMonitorFile_TheQuotaStateFollowsTheFactsOfThePoll(t *testing.T) {
	at := time.Date(2026, 10, 4, 7, 0, 5, 0, time.UTC)
	settings := config.QuotaSettings{
		FiveHour: config.FiveHourQuota{Threshold: 85},
		Weekly:   config.WeeklyQuota{Target: 85, Lead: 24 * time.Hour},
	}
	usage := func(fiveHourReset time.Time) quota.Usage {
		return quota.Usage{
			FiveHour: quota.Window{Utilization: 0.90, ResetsAt: fiveHourReset},
			Weekly:   quota.Window{Utilization: 0.10, ResetsAt: at.Add(time.Hour)},
		}
	}
	tests := []struct {
		name  string
		facts workflow.MonitorFacts
		want  string
	}{
		{"no kept usage", workflow.MonitorFacts{}, "open"},
		{"a read that failed in the poll", workflow.MonitorFacts{QuotaUnread: true, UsageKept: true, Usage: usage(at.Add(time.Hour))}, "unread"},
		{"a window at its limit", workflow.MonitorFacts{UsageKept: true, Usage: usage(at.Add(time.Hour))}, "stopped"},
		{"a window whose reset has passed", workflow.MonitorFacts{UsageKept: true, Usage: usage(at.Add(-time.Hour))}, "open"},
		{"an allowance for the window", workflow.MonitorFacts{UsageKept: true, Usage: usage(at.Add(time.Hour)), Allowance: quota.Allowance{FiveHourUntil: at.Add(time.Hour)}}, "open"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.facts.PollEnded, test.facts.QuotaSettings, test.facts.Location = at, settings, time.UTC
			got := workflow.BuildMonitorFile(test.facts).Quota
			if got.State != test.want {
				t.Errorf("quota.state = %q, want %q", got.State, test.want)
			}
			if stopped := test.want == "stopped"; (len(got.StoppedWindows) > 0) != stopped || (got.NextTryAt != nil) != stopped {
				t.Errorf("quota = %+v, want windows and a next try only when stopped", got)
			}
		})
	}
}

// While an agent runs, the monitor file lists the issue with its role, its
// request kind, its title, and its URL. The request kind is "continue",
// because an open pull request already closes the issue. After the run ends, the file no
// longer lists the issue, with no poll in between: last_poll.at stays the
// time of the poll.
func TestMonitorFile_ListsARunningAgentUntilItsRunEnds(t *testing.T) {
	sc := newScene(t, cliOptions{holds: true})
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := sc.service()
	path := withMonitorFile(t, service)
	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	waitForAgentRun(t, sc)

	read, raw := readMonitorFile(t, path)
	want := []state.MonitorAgent{{
		Repository: "example-org/example-repo", Issue: 10, Role: "implementer", Request: "continue",
		Title: subIssueTitle, URL: "https://github.com/example-org/example-repo/issues/10",
	}}
	if !slices.Equal(*read.Agents, want) {
		t.Fatalf("agents = %+v, want %+v:\n%s", *read.Agents, want, raw)
	}

	// The clock moves, so that a write of a poll would show in last_poll.at.
	sc.clock.Set(sceneNow.Add(time.Minute))
	sc.release(t)
	service.Wait()
	read, raw = readMonitorFile(t, path)
	if len(*read.Agents) != 0 || !strings.Contains(raw, `"agents": []`) {
		t.Errorf("the file still lists the agent after the end of its run:\n%s", raw)
	}
	if !read.LastPoll.At.Equal(sceneNow) {
		t.Errorf("last_poll.at = %s, want the time of the one poll %s", read.LastPoll.At, sceneNow)
	}
	if n := sc.pollQueries(); n != 1 {
		t.Errorf("%d poll queries, want 1: the end of the run writes the file, not a poll", n)
	}
}

// A Planner run is listed with the requirement issue, its title, and the
// request kind of the acceptance check.
func TestMonitorFile_ListsAPlannerRunWithItsRequestKind(t *testing.T) {
	sc, _ := newAcceptanceScene(t, cliOptions{fixture: "planner-done.jsonl", holds: true})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 6, Title: "Add the login", Labels: []string{githubtest.RequirementLabel, "cumin/status/implementing"}})
	service := sc.service()
	path := withMonitorFile(t, service)
	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	waitForAgentRun(t, sc)

	read, raw := readMonitorFile(t, path)
	want := []state.MonitorAgent{{
		Repository: "example-org/example-repo", Issue: 6, Role: "planner", Request: "acceptance check",
		Title: "Add the login", URL: "https://github.com/example-org/example-repo/issues/6",
	}}
	if !slices.Equal(*read.Agents, want) {
		t.Errorf("agents = %+v, want %+v:\n%s", *read.Agents, want, raw)
	}
	// The run leaves no acceptance check comment, so the same step requests
	// the acceptance check again: the issue stays in the list until the
	// second run ends.
	sc.release(t)
	waitForAgentRun(t, sc)
	sc.release(t)
	service.Wait()
	if read, raw := readMonitorFile(t, path); len(*read.Agents) != 0 {
		t.Errorf("the file still lists the agent after the end of its run:\n%s", raw)
	}
}

// A Reviewer run is listed with the implementation issue and the request
// kind of the review.
func TestMonitorFile_ListsAReviewerRunWithItsRequestKind(t *testing.T) {
	sc := newScene(t, cliOptions{reviews: []string{"APPROVE"}, holds: true})
	sc.awaitingChecks(t, []string{"ci"}, []githubtest.Check{{Name: "ci", Conclusion: "SUCCESS"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Labels: []string{"cumin/status/checking", "risk/low"}})
	service := sc.service()
	path := withMonitorFile(t, service)
	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	waitForAgentRun(t, sc)

	read, raw := readMonitorFile(t, path)
	want := []state.MonitorAgent{{
		Repository: "example-org/example-repo", Issue: 10, Role: "reviewer", Request: "review",
		Title: subIssueTitle, URL: "https://github.com/example-org/example-repo/issues/10",
	}}
	if !slices.Equal(*read.Agents, want) {
		t.Errorf("agents = %+v, want %+v:\n%s", *read.Agents, want, raw)
	}
	sc.release(t)
	service.Wait()
	if read, raw := readMonitorFile(t, path); len(*read.Agents) != 0 {
		t.Errorf("the file still lists the agent after the end of its run:\n%s", raw)
	}
}

// The list of the running agents has a fixed order, whatever order the
// facts have: by the repository, then by the number of the issue.
func TestMonitorFile_TheRunningAgentsHaveAFixedOrder(t *testing.T) {
	agents := []state.MonitorAgent{
		{Repository: "example/tool", Issue: 12},
		{Repository: "example/app", Issue: 31},
		{Repository: "example/tool", Issue: 9},
		{Repository: "example/app", Issue: 4},
	}
	want := []state.MonitorAgent{agents[3], agents[1], agents[2], agents[0]}
	for range 3 {
		got := workflow.BuildMonitorFile(workflow.MonitorFacts{Running: agents, Location: time.UTC}).Agents
		if !slices.Equal(got, want) {
			t.Fatalf("agents = %+v, want %+v", got, want)
		}
		slices.Reverse(agents)
	}
}
