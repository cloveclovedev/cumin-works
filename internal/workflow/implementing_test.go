package workflow_test

import (
	"context"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/agent"
	"github.com/cloveclovedev/cumin-works/internal/core/state"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

// runWithAFailedReadAfterDone runs the Implementer of #10 to a done result
// while the fake GitHub fails every try of the read after the run, and
// returns the service after the run. The run holds until the failure is
// set, so that the failure meets the read after done and no call before it.
func runWithAFailedReadAfterDone(t *testing.T, sc *scene) *workflow.Service {
	t.Helper()
	// A required check that does not report keeps the issue in
	// cumin/status/checking, so that the later polls start no review.
	sc.repo.DefaultBranch = "main"
	sc.repo.RequiredChecks = append(sc.repo.RequiredChecks, githubtest.RequiredCheck{Name: "ci"})
	service := sc.service()
	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	waitForAgentRun(t, sc)
	sc.fake.FailTimes(http.MethodPost, "/graphql", 0, everyTry, http.StatusBadGateway)
	sc.release(t)
	service.Wait()
	return service
}

// assertStillImplementing checks that nothing changed on #10 after the
// run: the label stays, no comment is written, one agent ran, and no step
// is kept for the issue.
func assertStillImplementing(t *testing.T, sc *scene, service *workflow.Service) {
	t.Helper()
	want := []string{"risk/low", workflow.LabelImplementing}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, want) {
		t.Errorf("labels of #10 = %v, want %v", got, want)
	}
	if comments := sc.fake.Comments(sc.repo, 10); len(comments) != 0 {
		t.Errorf("%d comments on #10, want none: %+v", len(comments), comments)
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1", n)
	}
	if got := workflow.InProgressIssues(service); len(got) != 0 {
		t.Errorf("issues in work = %v, want none: cumin keeps no step after the run", got)
	}
}

// assertWaitsForTheChecks checks that #10 moved to cumin/status/checking
// with no second request.
func assertWaitsForTheChecks(t *testing.T, sc *scene) {
	t.Helper()
	want := []string{"risk/low", workflow.LabelChecking}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, want) {
		t.Errorf("labels of #10 = %v, want %v", got, want)
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1: a verified pull request needs no request", n)
	}
	if comments := sc.fake.Comments(sc.repo, 10); len(comments) != 0 {
		t.Errorf("%d comments on #10, want none: %+v", len(comments), comments)
	}
}

// A read that fails after the run changes nothing, and cumin keeps no step.
// The next poll makes the decision from the same facts on GitHub: the pull
// request is verified, so the issue waits for the checks.
func TestImplementing_AReadThatFailsAfterTheRunChangesNothingAndTheNextPollDecides(t *testing.T) {
	sc := newScene(t, cliOptions{holds: true})
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := runWithAFailedReadAfterDone(t, sc)

	assertStillImplementing(t, sc, service)

	if err := pollAtMinute(sc, service, 1); err != nil {
		t.Fatalf("Poll at minute 1: %v", err)
	}
	assertWaitsForTheChecks(t, sc)
	if !strings.Contains(sc.logs.String(), `"msg":"wait for the checks: verified the pull request"`) {
		t.Errorf("the log does not say that the pull request was verified:\n%s", sc.logs.String())
	}
}

// A restart of cumin in cumin/status/implementing with a verified pull
// request: the first poll of the new cumin moves the issue to
// cumin/status/checking, and sends no request.
func TestImplementing_ARestartWithAVerifiedPullRequestWaitsForTheChecksWithNoRequest(t *testing.T) {
	sc := newScene(t, cliOptions{holds: true})
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	stopped := runWithAFailedReadAfterDone(t, sc)
	assertStillImplementing(t, sc, stopped)

	// The new cumin holds no run and no memory of the first one.
	restarted := sc.service()
	if err := pollAtMinute(sc, restarted, 1); err != nil {
		t.Fatalf("Poll after the restart: %v", err)
	}

	assertWaitsForTheChecks(t, sc)
}

// implementingWithoutAnAgent puts #10 in cumin/status/implementing with no
// agent, as a restart of cumin leaves it. event is the newest event of the
// label.
func implementingWithoutAnAgent(t *testing.T, event githubtest.LabelEvent) *scene {
	t.Helper()
	sc := newScene(t)
	sc.repo.Issues[10].Labels = []string{"risk/low", workflow.LabelImplementing}
	sc.repo.Issues[10].LabelEvents = []githubtest.LabelEvent{event}
	return sc
}

// implementingByCumin is the event of cumin/status/implementing that
// cumin-core added.
func implementingByCumin() githubtest.LabelEvent {
	return githubtest.LabelEvent{Label: workflow.LabelImplementing, At: sceneNow.Add(-time.Hour), Actor: cuminSlug, ActorType: "Bot"}
}

// A restart of cumin in cumin/status/implementing with no pull request: the
// poll sends one second request and counts it in the state file. That run
// leaves no pull request either, so the issue goes to
// cumin/status/awaiting-decision with the reason. The polls that follow
// send nothing more.
func TestImplementing_ARestartWithNoPullRequestSendsOneSecondRequestThenStops(t *testing.T) {
	sc := implementingWithoutAnAgent(t, implementingByCumin())
	service := sc.service()
	service.State = state.Open(filepath.Join(t.TempDir(), "state.json"), nil)

	for range 3 {
		sc.pollAndWait(t, service)
	}

	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs, want one second request", n)
	}
	if !strings.Contains(sc.logs.String(), "the implementation is requested again") {
		t.Errorf("the log does not say that the implementation is requested again:\n%s", sc.logs.String())
	}
	want := []string{"risk/low", workflow.LabelAwaitingDecision}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, want) {
		t.Errorf("labels of #10 = %v, want %v", got, want)
	}
	reason := workflow.VerificationReason(workflow.FailureNoOpenPullRequest)
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 {
		t.Fatalf("%d comments on #10, want one stop note: %+v", len(comments), comments)
	}
	for _, want := range []string{"## Stopped for a Maintainer", "Reason: " + reason, "Retried: once"} {
		if !strings.Contains(comments[0].Body, want) {
			t.Errorf("the comment has no %q:\n%s", want, comments[0].Body)
		}
	}
	if messages := sc.messagesExceptWaiting(); len(messages) != 1 || !strings.Contains(messages[0], reason) {
		t.Errorf("notifications = %v, want one with the reason %q", messages, reason)
	}
}

// A question of the Implementer after the label: the poll moves the issue
// to cumin/status/awaiting-decision, sends no request, and writes no
// comment of its own.
func TestImplementing_AQuestionAfterTheLabelStopsWithNoRequest(t *testing.T) {
	sc := implementingWithoutAnAgent(t, implementingByCumin())
	sc.fake.AddComment(sc.repo, 10, githubtest.Comment{
		Author: implementerSlug, AuthorIsBot: true, At: sceneNow.Add(-time.Minute),
		Body: workflow.DecisionRequestHeading + ": which table holds the setting?\n",
	})
	service := sc.service()

	for range 2 {
		sc.pollAndWait(t, service)
	}

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none", n)
	}
	want := []string{"risk/low", workflow.LabelAwaitingDecision}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, want) {
		t.Errorf("labels of #10 = %v, want %v", got, want)
	}
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 1 {
		t.Errorf("%d comments on #10, want only the question", n)
	}
	if messages := sc.messagesExceptWaiting(); len(messages) != 1 || !strings.Contains(messages[0], "asked a question") {
		t.Errorf("notifications = %v, want one about the question", messages)
	}
}

// While the Implementer runs, a poll changes nothing on the issue and reads
// nothing more for it.
func TestImplementing_APollChangesNothingWhileTheImplementerRuns(t *testing.T) {
	sc := newScene(t, cliOptions{holds: true})
	service := sc.service()
	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	waitForAgentRun(t, sc)
	changes, reads := sc.labelChanges(), sc.issueReads(10)

	for i := range 2 {
		if err := service.Poll(context.Background()); err != nil {
			t.Fatalf("poll %d while the Implementer runs: %v", i+1, err)
		}
	}

	if n := sc.labelChanges(); n != changes {
		t.Errorf("%d label changes while the Implementer runs, want none", n-changes)
	}
	if n := sc.issueReads(10); n != reads {
		t.Errorf("%d reads of #10 while the Implementer runs, want none", n-reads)
	}
	want := []string{"risk/low", workflow.LabelImplementing}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, want) {
		t.Errorf("labels of #10 = %v, want %v", got, want)
	}
	if comments := sc.fake.Comments(sc.repo, 10); len(comments) != 0 {
		t.Errorf("%d comments on #10, want none", len(comments))
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1", n)
	}
	sc.release(t)
	service.Wait()
}

// After a run ends and the issue left cumin/status/implementing, nothing
// changes, and the log line starts with the action of the request in work
// ("request the implementation"), not with a transition that this path
// does not take.
func TestImplementing_TheLogOfAnIssueThatLeftStartsWithTheActionOfTheRequest(t *testing.T) {
	sc := newScene(t, cliOptions{holds: true})
	service := sc.service()
	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	waitForAgentRun(t, sc)
	left := []string{"risk/low", workflow.LabelAwaitingDecision}
	if err := sc.fake.SetLabels(sc.repo, 10, left); err != nil {
		t.Fatal(err)
	}
	changes := sc.labelChanges()
	sc.release(t)
	service.Wait()

	want := `"msg":"` + string(workflow.ActionRequestTheImplementation) + `: the issue is not in cumin/status/implementing; nothing changes"`
	if !strings.Contains(sc.logs.String(), want) {
		t.Errorf("the log does not hold %s:\n%s", want, sc.logs.String())
	}
	if strings.Contains(sc.logs.String(), string(workflow.ActionWaitForTheChecks)+":") {
		t.Errorf("the log names \"wait for the checks\", a transition that this path does not take:\n%s", sc.logs.String())
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, left) {
		t.Errorf("labels of #10 = %v, want %v", got, left)
	}
	if n := sc.labelChanges(); n != changes {
		t.Errorf("%d label changes after the run, want none", n-changes)
	}
}

// A cumin/status/implementing that an account with only triage permission
// added is not a state (issue-states.md, the account that added a status
// label): no agent starts, no label changes, and the notification goes out once.
func TestImplementing_ALabelOfAnAccountWithTriagePermissionDoesNothing(t *testing.T) {
	sc := implementingWithoutAnAgent(t, statusBy(workflow.LabelImplementing, "a-triager"))
	sc.fake.SetPermission("a-triager", "triage", "User")
	service := sc.service()

	for range 3 {
		sc.pollAndWait(t, service)
	}

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none", n)
	}
	if n := sc.labelChanges(); n != 0 {
		t.Errorf("%d label changes, want none", n)
	}
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 0 {
		t.Errorf("%d comments on #10, want none", n)
	}
	if n := strings.Count(sc.logs.String(), "is not of cumin-core or of a Maintainer"); n != 1 {
		t.Errorf("%d log lines for one label event across three polls, want 1:\n%s", n, sc.logs.String())
	}
}

// withoutARemote returns a service on the scene whose remote repository
// does not exist, so that no work directory can be prepared, with the state
// file at path.
func (sc *scene) withoutARemote(t *testing.T, path string) *workflow.Service {
	t.Helper()
	service := sc.service()
	service.State = state.Open(path, nil)
	service.Targets[0].RemoteURL = filepath.Join(t.TempDir(), "no-remote.git")
	return service
}

// restarted returns a new service on the scene with the state file at path,
// as a restart of cumin gives it.
func (sc *scene) restarted(path string) *workflow.Service {
	service := sc.service()
	service.State = state.Open(path, nil)
	return service
}

// The start of a stay is written where the label changes. A check fix
// changes the label while the state file holds the second request of the
// earlier stay, and cumin stops before the Implementer starts (here: the
// work directory is not prepared). The new cumin still sends the one second
// request of the new stay, and does not stop the issue.
func TestImplementing_ARestartRightAfterACheckFixStillSendsOneSecondRequest(t *testing.T) {
	sc := newScene(t)
	path := filepath.Join(t.TempDir(), "state.json")
	stopped := sc.withoutARemote(t, path)
	sc.failingCheck(t, stopped, 0)
	stopped.State = state.Open(path, nil)
	if err := stopped.State.Set("example-org/example-repo", 10, state.Issue{SessionID: "earlier-session", ImplementationRequests: 1}); err != nil {
		t.Fatal(err)
	}
	sc.pollAndWait(t, stopped)
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelImplementing) {
		t.Fatalf("labels of #10 = %v, want cumin/status/implementing after the check fix", got)
	}
	if n := sc.agentRuns(t); n != 0 {
		t.Fatalf("%d agent runs before the restart, want none", n)
	}

	sc.pollAndWait(t, sc.restarted(path))

	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs after the restart, want one second request", n)
	}
	if !strings.Contains(sc.logs.String(), "the implementation is requested again") {
		t.Errorf("the log does not say that the implementation is requested again:\n%s", sc.logs.String())
	}
	if comments := sc.fake.Comments(sc.repo, 10); len(comments) != 0 {
		t.Errorf("%d comments on #10, want none: %+v", len(comments), comments)
	}
}

// A work directory that cannot be prepared sends no request. The next poll
// requests the implementation again, and the second failure stops the
// implementation for the Maintainer with the reason. The polls that follow send
// nothing more.
func TestImplementing_AWorkDirectoryThatIsNotPreparedTwiceStopsTheIssue(t *testing.T) {
	sc := newScene(t)
	service := sc.withoutARemote(t, filepath.Join(t.TempDir(), "state.json"))

	for range 4 {
		sc.pollAndWait(t, service)
	}

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none", n)
	}
	if n := strings.Count(sc.logs.String(), `: the work directory was not prepared"`); n != 2 {
		t.Errorf("%d tries to prepare the work directory, want 2: no third request", n)
	}
	want := []string{"risk/low", workflow.LabelAwaitingDecision}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, want) {
		t.Errorf("labels of #10 = %v, want %v", got, want)
	}
	reason := workflow.WorkDirectoryReason()
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 {
		t.Fatalf("%d comments on #10, want one stop note: %+v", len(comments), comments)
	}
	for _, want := range []string{"## Stopped for a Maintainer", "Reason: " + reason, "Retried: once"} {
		if !strings.Contains(comments[0].Body, want) {
			t.Errorf("the comment has no %q:\n%s", want, comments[0].Body)
		}
	}
	if messages := sc.messagesExceptWaiting(); len(messages) != 1 || !strings.Contains(messages[0], reason) {
		t.Errorf("notifications = %v, want one with the reason %q", messages, reason)
	}
}

// A restart of cumin during a conflict resolution whose pull request fails
// the check: the state file says that the stay is a conflict resolution, so
// the second request of the poll is the conflict resolution again.
func TestImplementing_ARestartDuringAConflictResolutionRequestsTheResolutionAgain(t *testing.T) {
	sc := conflictingBeforeChecks(t, cliOptions{}, "CONFLICTING")
	path := filepath.Join(t.TempDir(), "state.json")
	sc.pollAndWait(t, sc.withoutARemote(t, path))
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelImplementing) {
		t.Fatalf("labels of #10 = %v, want cumin/status/implementing after the conflict", got)
	}
	if n := sc.agentRuns(t); n != 0 {
		t.Fatalf("%d agent runs before the restart, want none", n)
	}

	sc.pollAndWait(t, sc.restarted(path))

	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs after the restart, want one second request", n)
	}
	wantDir := filepath.Join(sc.workRoot, "example-org", "example-repo", "10-implementer")
	want := workflow.ConflictResolutionRequestText("example-org/example-repo", 10, 21, wantBranch, wantDir, "main")
	if got := promptOf(t, sc.record(t, "agent.args")); !strings.HasSuffix(got, "\n\n"+want) {
		t.Errorf("the request text after the restart = %q, want %q", got, want)
	}
}

// The stop after a second abnormal end names the kind of that end, so that
// the Maintainer knows where to look.
func TestImplementing_TheStopAfterASecondAbnormalEndNamesItsKind(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "is-error.jsonl"})
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 2 {
		t.Fatalf("%d agent runs, want 2", n)
	}
	reason := workflow.AfterAbnormalEndReason(workflow.VerificationReason(workflow.FailureNoOpenPullRequest), "Implementer", agent.EndError)
	if !strings.Contains(reason, "error reported by the CLI") {
		t.Errorf("the reason %q does not name the kind of the end", reason)
	}
	assertStopped(t, sc, reason, 0, "once")
}

// A run that hit the quota limit does not use up the second request: the
// quota decides before the request is counted, so the issue keeps
// cumin/status/implementing with a count of zero.
func TestImplementing_ARunThatHitTheQuotaLimitDoesNotUseUpTheSecondRequest(t *testing.T) {
	sc := newScene(t)
	// The minimal run passes; the agent run reports a weekly usage of 0.51
	// (done.jsonl) against a target of 50, and leaves no pull request.
	sc.setQuota(t, 0.10, sceneNow.Add(time.Hour), 0.10, sceneNow.Add(time.Hour))
	sc.quota.Weekly.Target = 50
	service := sc.service()
	service.State = state.Open(filepath.Join(t.TempDir(), "state.json"), nil)

	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs, want 1: the quota stops the second request", n)
	}
	if n := service.State.Issue("example-org/example-repo", 10).ImplementationRequests; n != 0 {
		t.Errorf("the state file counts %d second requests, want none", n)
	}
	want := []string{"risk/low", workflow.LabelImplementing}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, want) {
		t.Errorf("labels of #10 = %v, want %v", got, want)
	}
	if comments := sc.fake.Comments(sc.repo, 10); len(comments) != 0 {
		t.Errorf("%d comments on #10, want none: %+v", len(comments), comments)
	}
}
