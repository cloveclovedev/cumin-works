package workflow_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/state"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

// stopAfterRunsScene runs the service of the scene with a short poll interval and a
// stop request file, and returns the file and the channel of the result of
// Run.
func stopAfterRunsScene(t *testing.T, sc *scene, service *workflow.Service) (string, <-chan error) {
	t.Helper()
	service.PollInterval = 10 * time.Millisecond
	service.StopRequestPath = filepath.Join(t.TempDir(), state.StopRequestFileName)
	returned := make(chan error, 1)
	go func() { returned <- service.Run(context.Background()) }()
	return service.StopRequestPath, returned
}

// requestStop writes the stop request, as `cumin stop
// --after-current-runs` does.
func requestStop(t *testing.T, path string) {
	t.Helper()
	if err := state.WriteStopRequest(path, state.StopRequest{RequestedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

// waitForPolls waits until the fake GitHub has received that many more
// snapshot reads. Two more reads mean that one poll began after the call.
func waitForPolls(t *testing.T, sc *scene, more int) {
	t.Helper()
	want := sc.fake.CountRequests(http.MethodPost, "/graphql") + more
	if !sc.fake.WaitForRequests(http.MethodPost, "/graphql", want, hangGuard) {
		t.Fatalf("the polls did not go on:\n%s", sc.logs.String())
	}
}

// stopRequestExists reports whether a stop request is there.
func stopRequestExists(t *testing.T, path string) bool {
	t.Helper()
	_, found, err := state.ReadStopRequest(path)
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// A stop request with one running agent: cumin starts nothing new, lets the run
// end, applies the state that follows it, and then exits with nil, so that
// the command exits with 0.
func TestStopAfterRuns_LetsTheRunEndAppliesItsNextStateAndStartsNothingNew(t *testing.T) {
	sc := newScene(t, cliOptions{holds: true})
	sc.repo.DefaultBranch = "main"
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := sc.service()
	// Room for a second issue, so that only the stop request holds it back.
	service.Settings.MaxIssuesInProgress = 2
	path, returned := stopAfterRunsScene(t, sc, service)
	waitForAgentRun(t, sc)

	requestStop(t, path)
	waitForPolls(t, sc, 2)
	// A second issue becomes ready while cumin stops after its runs.
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 11, Parent: 6, Title: "Add the logout screen", Labels: []string{"cumin/status/ready", "risk/low"}})
	waitForPolls(t, sc, 2)

	select {
	case err := <-returned:
		t.Fatalf("Run returned %v while the agent run was going on:\n%s", err, sc.logs.String())
	default:
	}
	if got := sc.fake.Issue(sc.repo, 11).Labels; !slices.Contains(got, "cumin/status/ready") {
		t.Errorf("labels of #11 = %v, want cumin/status/ready untouched", got)
	}

	sc.release(t)
	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("Run returned %v, want nil so that the command exits with 0", err)
		}
	case <-time.After(hangGuard):
		t.Fatalf("Run did not return after the run ended:\n%s", sc.logs.String())
	}

	// The end of the run was verified (verify done), and the last poll gave
	// the pull request the labels of the issue.
	want := []string{"risk/low", workflow.LabelAwaitingChecks}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, want) {
		t.Errorf("labels of #10 = %v, want %v", got, want)
	}
	if got := sc.fake.PullRequestLabels(sc.repo, 21); !slices.Contains(got, workflow.LabelAwaitingChecks) {
		t.Errorf("labels of the pull request = %v, want cumin/status/awaiting-checks copied from the issue", got)
	}
	// No required check: without the stop request, the review would start at once.
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1: the stop request starts no review and no second issue", n)
	}
	if got := sc.fake.Issue(sc.repo, 11).Labels; !slices.Contains(got, "cumin/status/ready") {
		t.Errorf("labels of #11 = %v, want cumin/status/ready untouched", got)
	}
	if stopRequestExists(t, path) {
		t.Error("the stop request is still there after the exit")
	}
	logs := sc.logs.String()
	for _, want := range []string{
		`"msg":"stop after the current runs: no new work starts; cumin exits when the agent runs have ended"`,
		`"in_progress":["example-org/example-repo#10"]`,
		`"msg":"stop after the current runs: new work is held back"`,
		`"msg":"I2: verified the pull request"`,
		`"msg":"stopped","reason":"the agent runs have ended after a stop request","in_progress":[]`,
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log has no %s:\n%s", want, logs)
		}
	}
	// A stop after the runs is not "nothing to do": the Owner asked for it.
	if messages := sc.webhook.messagesSent(); len(messages) != 0 {
		t.Errorf("notifications = %v, want none", messages)
	}
}

// A stop request with no running agent exits at the next poll.
func TestStopAfterRuns_WithNoRunningAgentExitsAtOnce(t *testing.T) {
	sc := newScene(t)
	// Nothing is ready, so no agent runs when the request comes.
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Labels: []string{"risk/low"}})
	service := sc.service()
	path, returned := stopAfterRunsScene(t, sc, service)
	waitForPolls(t, sc, 1)

	requestStop(t, path)
	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("Run returned %v, want nil", err)
		}
	case <-time.After(hangGuard):
		t.Fatalf("Run did not return:\n%s", sc.logs.String())
	}
	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none", n)
	}
	if stopRequestExists(t, path) {
		t.Error("the stop request is still there after the exit")
	}
	if !strings.Contains(sc.logs.String(), `"msg":"stopped","reason":"the agent runs have ended after a stop request"`) {
		t.Errorf("the log has no stop line of the stop request:\n%s", sc.logs.String())
	}
}

// A stop request survives nothing: a request from before the start is removed, and
// cumin run works as usual.
func TestStopAfterRuns_ARequestFromBeforeTheStartIsDropped(t *testing.T) {
	// The run sleeps, so that the test sees it start; the cancel ends it.
	sc := newScene(t, cliOptions{sleeps: true})
	service := sc.service()
	service.PollInterval = 10 * time.Millisecond
	service.StopRequestPath = filepath.Join(t.TempDir(), state.StopRequestFileName)
	requestStop(t, service.StopRequestPath)

	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() { returned <- service.Run(ctx) }()
	waitForAgentRun(t, sc)
	cancel()
	if err := <-returned; err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stopRequestExists(t, service.StopRequestPath) {
		t.Error("the stop request of before the start is still there")
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelImplementing) {
		t.Errorf("labels of #10 = %v, want the claim of a usual start", got)
	}
}

// A stop signal after a stop request stops at once, as without one.
func TestStopAfterRuns_TheStopSignalStillStopsAtOnce(t *testing.T) {
	sc := newScene(t, cliOptions{sleeps: true})
	service := sc.service()
	service.PollInterval = 10 * time.Millisecond
	service.StopGrace = 30 * time.Second
	service.StopRequestPath = filepath.Join(t.TempDir(), state.StopRequestFileName)
	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() { returned <- service.Run(ctx) }()
	waitForAgentRun(t, sc)
	requestStop(t, service.StopRequestPath)
	waitForPolls(t, sc, 2)

	cancel()
	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(hangGuard):
		t.Fatalf("Run did not return after the stop signal:\n%s", sc.logs.String())
	}
	logs := sc.logs.String()
	if !strings.Contains(logs, `"msg":"stopped","reason":"context canceled","in_progress":["example-org/example-repo#10"]`) {
		t.Errorf("the log has no stop line of the signal that names #10:\n%s", logs)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelImplementing) {
		t.Errorf("labels of #10 = %v, want cumin/status/implementing", got)
	}
	if stopRequestExists(t, service.StopRequestPath) {
		t.Error("the stop request is still there after the stop")
	}
}

// A request of before the start that cannot be removed does not stop the
// new process either: every start would otherwise exit at once, and launchd
// leaves a process that exits with 0 stopped.
func TestStopAfterRuns_ARequestThatCannotBeRemovedAtTheStartIsPassedOver(t *testing.T) {
	sc := newScene(t, cliOptions{sleeps: true})
	service := sc.service()
	service.PollInterval = 10 * time.Millisecond
	dir := t.TempDir()
	service.StopRequestPath = filepath.Join(dir, state.StopRequestFileName)
	requestStop(t, service.StopRequestPath)
	// The directory cannot be written, so the file cannot be removed.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() { returned <- service.Run(ctx) }()
	// The claim and the agent run show that the polls went on as usual.
	waitForAgentRun(t, sc)
	waitForPolls(t, sc, 2)
	select {
	case err := <-returned:
		t.Fatalf("Run returned %v on the request of before the start:\n%s", err, sc.logs.String())
	default:
	}
	cancel()
	if err := <-returned; err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !stopRequestExists(t, service.StopRequestPath) {
		t.Fatal("the request was removed; the test did not reach the case")
	}
	logs := sc.logs.String()
	if !strings.Contains(logs, `"msg":"the stop request of an earlier start was not removed"`) {
		t.Errorf("the log has no warning for the request that stays:\n%s", logs)
	}
	if strings.Contains(logs, `"msg":"stop after the current runs: no new work starts`) {
		t.Errorf("cumin took the request of before the start:\n%s", logs)
	}
}

// A request that is not older than the start is for the process that is
// starting: the start keeps it, and the first poll takes it.
func TestStopAfterRuns_ARequestOfTheStartIsKept(t *testing.T) {
	sc := newScene(t)
	service := sc.service()
	service.PollInterval = 10 * time.Millisecond
	service.StopRequestPath = filepath.Join(t.TempDir(), state.StopRequestFileName)
	// The time of a request that comes just after the start.
	if err := state.WriteStopRequest(service.StopRequestPath, state.StopRequest{RequestedAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}

	if err := service.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none: the first poll takes it", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/ready") {
		t.Errorf("labels of #10 = %v, want cumin/status/ready untouched", got)
	}
	if stopRequestExists(t, service.StopRequestPath) {
		t.Error("the stop request is still there after the exit")
	}
}
