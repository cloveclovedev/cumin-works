package workflow_test

import (
	"bytes"
	"context"
	"net/http"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/state"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

// idlePollService is the service of the scene with the default intervals:
// a poll every minute, and every five minutes for a repository with no
// issue in work. The tests move the clock of the scene by one poll interval
// and call Poll, as Run does at each tick.
func idlePollService(sc *scene) *workflow.Service {
	service := sc.service()
	service.PollInterval = time.Minute
	service.IdlePollInterval = 5 * time.Minute
	return service
}

// queries counts the GraphQL queries that the fake GitHub received for the
// repository with the name.
func queries(sc *scene, name string) int {
	n := 0
	for _, request := range sc.fake.Requests() {
		if request.Method == http.MethodPost && request.Path == "/graphql" && bytes.Contains(request.Body, []byte(`"`+name+`"`)) {
			n++
		}
	}
	return n
}

// pollAt moves the clock to that many minutes after the start of the scene,
// polls, and returns how many GraphQL queries the poll sent for the
// repository with the name.
func pollAt(t *testing.T, sc *scene, service *workflow.Service, minutes int, name string) int {
	t.Helper()
	sc.clock.Set(sceneNow.Add(time.Duration(minutes) * time.Minute))
	before := queries(sc, name)
	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("Poll at minute %d: %v", minutes, err)
	}
	return queries(sc, name) - before
}

// waitsForOwner makes the sub-issue of the scene wait for the Owner, so
// that the repository has no issue in work.
func waitsForOwner(t *testing.T, sc *scene) {
	t.Helper()
	if err := sc.fake.SetLabels(sc.repo, 10, []string{workflow.LabelAwaitingOwnerDecision, "risk/low"}); err != nil {
		t.Fatal(err)
	}
}

// Core-26 (cumin-core.md): a repository with an issue in work is polled at
// every poll interval.
func TestIdlePoll_ARepositoryInWorkIsPolledAtEveryPollInterval(t *testing.T) {
	sc := newScene(t)
	// The issue waits for its required checks: cumin moves it on, and no
	// agent runs.
	if err := sc.fake.SetLabels(sc.repo, 10, []string{workflow.LabelAwaitingChecks, "risk/low"}); err != nil {
		t.Fatal(err)
	}
	service := idlePollService(sc)

	for minute := range 7 {
		if n := pollAt(t, sc, service, minute, "example-repo"); n == 0 {
			t.Errorf("minute %d: the poll sent no GraphQL query, want a poll at every poll interval", minute)
		}
	}
}

// Core-26: a repository with no issue in work is polled once in each idle
// poll interval, and sends no GraphQL query between.
func TestIdlePoll_ARepositoryWithNoIssueInWorkIsPolledOnceInEachIdlePollInterval(t *testing.T) {
	sc := newScene(t)
	waitsForOwner(t, sc)
	service := idlePollService(sc)

	for minute := range 11 {
		n := pollAt(t, sc, service, minute, "example-repo")
		if polled := minute%5 == 0; polled && n == 0 {
			t.Errorf("minute %d: the poll sent no GraphQL query, want the poll of the idle poll interval", minute)
		} else if !polled && n != 0 {
			t.Errorf("minute %d: the poll sent %d GraphQL queries, want none before the idle poll interval is over", minute, n)
		}
	}
}

// Core-26: the Owner adds cumin/status/ready to an issue of an idle
// repository. cumin reads it at the poll of the idle poll interval, and
// the next poll comes after the poll interval.
func TestIdlePoll_AfterANewReadyTheNextPollComesAfterThePollInterval(t *testing.T) {
	sc := newScene(t, cliOptions{sleeps: true})
	waitsForOwner(t, sc)
	service := idlePollService(sc)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sc.clock.Set(sceneNow)
	if err := service.Poll(ctx); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	if err := sc.fake.SetLabels(sc.repo, 10, []string{workflow.LabelReady, "risk/low"}); err != nil {
		t.Fatal(err)
	}
	for minute := 1; minute < 5; minute++ {
		if n := pollAt(t, sc, service, minute, "example-repo"); n != 0 {
			t.Fatalf("minute %d: the poll sent %d GraphQL queries, want none", minute, n)
		}
	}
	sc.clock.Set(sceneNow.Add(5 * time.Minute))
	if err := service.Poll(ctx); err != nil {
		t.Fatalf("Poll at minute 5: %v", err)
	}
	waitForAgentRun(t, sc)
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelImplementing) {
		t.Fatalf("labels of #10 = %v, want the claim at the poll of the idle poll interval", got)
	}

	sc.clock.Set(sceneNow.Add(6 * time.Minute))
	before := queries(sc, "example-repo")
	if err := service.Poll(ctx); err != nil {
		t.Fatalf("Poll at minute 6: %v", err)
	}
	if queries(sc, "example-repo") == before {
		t.Error("minute 6: the poll sent no GraphQL query, want a poll one poll interval after the new ready")
	}
	cancel()
	service.Wait()
}

// Core-26: an idle repository and a repository in work are targets at the
// same time. The idle one does not slow the polls of the other one.
func TestIdlePoll_AnIdleRepositoryDoesNotSlowAnotherRepository(t *testing.T) {
	sc := newScene(t)
	if err := sc.fake.SetLabels(sc.repo, 10, []string{workflow.LabelAwaitingChecks, "risk/low"}); err != nil {
		t.Fatal(err)
	}
	other := sc.fake.AddRepository("example-org", "other-repo")
	sc.fake.AddIssue(other, &githubtest.Issue{Number: 1, Labels: []string{githubtest.RequirementLabel}})
	service := idlePollService(sc)
	// The idle repository comes first, so that its skip must not end the poll.
	service.Targets = append([]workflow.Target{target(sc, "other-repo")}, service.Targets...)

	for minute := range 6 {
		sc.clock.Set(sceneNow.Add(time.Duration(minute) * time.Minute))
		inWork, idle := queries(sc, "example-repo"), queries(sc, "other-repo")
		if err := service.Poll(context.Background()); err != nil {
			t.Fatalf("Poll at minute %d: %v", minute, err)
		}
		inWork, idle = queries(sc, "example-repo")-inWork, queries(sc, "other-repo")-idle
		if inWork == 0 {
			t.Errorf("minute %d: no GraphQL query for the repository in work", minute)
		}
		if polled := minute%5 == 0; polled != (idle != 0) {
			t.Errorf("minute %d: %d GraphQL queries for the idle repository, want a poll only at minutes 0 and 5", minute, idle)
		}
	}
}

// A poll that took an action keeps the repository in work for one poll
// interval: the next poll reads what the agent run left. The issue then
// waits for the Owner, and the repository is idle.
func TestIdlePoll_ThePollAfterAnActionReadsWhatTheRunLeft(t *testing.T) {
	// The run returns blocked, so the issue waits for the Owner after it.
	sc := newScene(t, cliOptions{fixture: "blocked.jsonl"})
	service := idlePollService(sc)
	sc.clock.Set(sceneNow)
	sc.pollAndWait(t, service)
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelAwaitingOwnerDecision) {
		t.Fatalf("labels of #10 = %v, want cumin/status/awaiting-owner-decision after the blocked run", got)
	}

	if n := pollAt(t, sc, service, 1, "example-repo"); n == 0 {
		t.Error("minute 1: the poll sent no GraphQL query, want a poll one poll interval after the claim")
	}
	if n := pollAt(t, sc, service, 2, "example-repo"); n != 0 {
		t.Errorf("minute 2: the poll sent %d GraphQL queries, want none: the repository has no issue in work", n)
	}
}

// A stop after the runs behaves as before with an idle repository: every
// poll reads the repository, and cumin exits after a poll with no run.
func TestIdlePoll_AStopAfterTheRunsStillReadsAnIdleRepository(t *testing.T) {
	sc := newScene(t)
	waitsForOwner(t, sc)
	service := sc.service()
	service.PollInterval = 10 * time.Millisecond
	service.IdlePollInterval = time.Hour
	service.StopRequestPath = filepath.Join(t.TempDir(), state.StopRequestFileName)
	returned := make(chan error, 1)
	go func() { returned <- service.Run(context.Background()) }()
	// The first poll leaves the repository idle.
	if !sc.fake.WaitForRequests(http.MethodPost, "/graphql", 1, hangGuard) {
		t.Fatalf("no poll ran:\n%s", sc.logs.String())
	}
	before := queries(sc, "example-repo")
	requestStop(t, service.StopRequestPath)

	select {
	case err := <-returned:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(hangGuard):
		t.Fatalf("cumin did not stop after the stop request:\n%s", sc.logs.String())
	}
	if queries(sc, "example-repo") == before {
		t.Error("the last poll sent no GraphQL query, want a read of the idle repository before the stop")
	}
}
