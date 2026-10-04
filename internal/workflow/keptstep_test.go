package workflow_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

// keptVerifyDone runs the Implementer of #10 to a done result while the
// fake GitHub answers as fail sets it, and returns the service after the
// run. The run holds until the failure is set, so that the failure meets
// the read of verify done and no call before it.
func keptVerifyDone(t *testing.T, sc *scene, fail func()) *workflow.Service {
	t.Helper()
	// A required check that does not report keeps the issue in
	// cumin/status/checking after verify done, so that the later
	// polls start no review.
	sc.repo.DefaultBranch = "main"
	sc.repo.RequiredChecks = append(sc.repo.RequiredChecks, githubtest.RequiredCheck{Name: "ci"})
	service := sc.service()
	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	waitForAgentRun(t, sc)
	fail()
	sc.release(t)
	service.Wait()
	return service
}

// pollAtMinute moves the clock to that many minutes after the start of the
// scene, and polls. It returns the error of the poll.
func pollAtMinute(sc *scene, service *workflow.Service, minutes int) error {
	sc.clock.Set(sceneNow.Add(time.Duration(minutes) * time.Minute))
	err := service.Poll(context.Background())
	service.Wait()
	return err
}

// assertVerifyDoneIsKept checks that #10 waits with a kept step: the label
// stays, no comment is written, no second agent started, and the issue
// counts as in work.
func assertVerifyDoneIsKept(t *testing.T, sc *scene, service *workflow.Service) {
	t.Helper()
	want := []string{"risk/low", workflow.LabelImplementing}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, want) {
		t.Errorf("labels of #10 = %v, want %v while verify done is kept", got, want)
	}
	if comments := sc.fake.Comments(sc.repo, 10); len(comments) != 0 {
		t.Errorf("%d comments on #10, want none while verify done is kept: %+v", len(comments), comments)
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1: no agent starts while verify done is kept", n)
	}
	if got := workflow.InProgressIssues(service); !slices.Equal(got, []string{"example-org/example-repo#10"}) {
		t.Errorf("issues in work = %v, want #10 while verify done is kept", got)
	}
}

// Core-28 (cumin-core.md): the fake GitHub fails every try of the read
// after done. cumin keeps verify done: the label stays, and no comment is
// written. A poll before the delay does not run the step. The poll after
// the delay runs it, and the issue moves as if the step had not failed.
func TestKeptStep_VerifyDoneRunsAgainAtALaterPollAfterATemporaryFailure(t *testing.T) {
	sc := newScene(t, cliOptions{holds: true})
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := keptVerifyDone(t, sc, func() {
		sc.fake.FailTimes(http.MethodPost, "/graphql", 0, everyTry, http.StatusBadGateway)
	})

	assertVerifyDoneIsKept(t, sc, service)
	logs := sc.logs.String()
	if n := strings.Count(logs, `"msg":"kept verify done after a temporary failure: a later poll runs it again"`); n != 1 {
		t.Errorf("%d log lines for the kept step, want 1:\n%s", n, logs)
	}
	for _, want := range []string{`"level":"WARN"`, `"reason":"`, `"next_try":"` + sceneNow.Add(5*time.Minute).UTC().Format(time.RFC3339) + `"`} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log of the kept step does not hold %s:\n%s", want, logs)
		}
	}

	// A poll before the delay: the step does not run, and the issue holds
	// its slot.
	reads := sc.issueReads(10)
	if err := pollAtMinute(sc, service, 4); err != nil {
		t.Fatalf("Poll at minute 4: %v", err)
	}
	if n := sc.issueReads(10); n != reads {
		t.Errorf("%d reads of #10 at minute 4, want none before the delay", n-reads)
	}
	assertVerifyDoneIsKept(t, sc, service)

	if err := pollAtMinute(sc, service, 5); err != nil {
		t.Fatalf("Poll at minute 5: %v", err)
	}
	want := []string{"risk/low", workflow.LabelChecking}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, want) {
		t.Errorf("labels of #10 = %v, want %v after the kept step ran", got, want)
	}
	if !strings.Contains(sc.logs.String(), `"msg":"I2: verified the pull request"`) {
		t.Errorf("the log does not say that I2 verified the pull request:\n%s", sc.logs.String())
	}
	if got := workflow.InProgressIssues(service); len(got) != 0 {
		t.Errorf("issues in work = %v, want none after the kept step ended", got)
	}
}

// A step that fails again waits for the delay once more.
func TestKeptStep_AStepThatFailsAgainWaitsOnceMore(t *testing.T) {
	sc := newScene(t, cliOptions{holds: true})
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := keptVerifyDone(t, sc, func() {
		sc.fake.FailTimes(http.MethodPost, "/graphql", 0, everyTry, http.StatusBadGateway)
	})

	sc.fake.FailTimes(http.MethodPost, "/graphql", 0, everyTry, http.StatusBadGateway)
	// The poll itself reads after the kept step, so it succeeds.
	if err := pollAtMinute(sc, service, 5); err != nil {
		t.Fatalf("Poll at minute 5: %v", err)
	}
	assertVerifyDoneIsKept(t, sc, service)
	reads := sc.issueReads(10)
	if err := pollAtMinute(sc, service, 9); err != nil {
		t.Fatalf("Poll at minute 9: %v", err)
	}
	if n := sc.issueReads(10); n != reads {
		t.Errorf("%d reads of #10 at minute 9, want none before the second delay", n-reads)
	}

	if err := pollAtMinute(sc, service, 10); err != nil {
		t.Fatalf("Poll at minute 10: %v", err)
	}
	want := []string{"risk/low", workflow.LabelChecking}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, want) {
		t.Errorf("labels of #10 = %v, want %v after the second try", got, want)
	}
}

// Core-28 (cumin-core.md): after a full rate limit, the client sends
// nothing before the reset time, so the kept step does not read the issue
// at the poll after the delay. It runs at the first poll after the reset.
func TestKeptStep_AfterARateLimitVerifyDoneRunsAfterTheResetTime(t *testing.T) {
	sc := newScene(t, cliOptions{holds: true})
	sc.client.SetNow(sc.clock.Now)
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := keptVerifyDone(t, sc, func() {
		sc.fake.LimitTimes(http.MethodPost, "/graphql", 1, http.StatusOK, sceneNow.Add(7*time.Minute))
	})
	assertVerifyDoneIsKept(t, sc, service)

	// Minute 5 is before the reset: the step is due, and the client sends
	// no call. The poll of the repository fails for the same reason.
	if err := pollAtMinute(sc, service, 5); err == nil {
		t.Error("Poll at minute 5 returned no error, want the full rate limit")
	}
	if n := sc.issueReads(10); n != 1 {
		t.Errorf("%d reads of #10 before the reset time, want 1 (the read that met the limit)", n)
	}
	assertVerifyDoneIsKept(t, sc, service)

	if err := pollAtMinute(sc, service, 10); err != nil {
		t.Fatalf("Poll at minute 10: %v", err)
	}
	want := []string{"risk/low", workflow.LabelChecking}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, want) {
		t.Errorf("labels of #10 = %v, want %v after the reset time", got, want)
	}
}

// A failure that is not temporary is not kept: a kept step that runs again
// and finds no pull request stops the issue for the Owner (I2).
func TestKeptStep_AVerificationThatFailsStillStopsTheIssueForTheOwner(t *testing.T) {
	sc := newScene(t, cliOptions{holds: true})
	service := keptVerifyDone(t, sc, func() {
		sc.fake.FailTimes(http.MethodPost, "/graphql", 0, everyTry, http.StatusBadGateway)
	})
	assertVerifyDoneIsKept(t, sc, service)

	if err := pollAtMinute(sc, service, 5); err != nil {
		t.Fatalf("Poll at minute 5: %v", err)
	}
	want := []string{"risk/low", workflow.LabelAwaitingDecision}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, want) {
		t.Errorf("labels of #10 = %v, want %v", got, want)
	}
	reason := workflow.VerificationReason(workflow.FailureNoOpenPullRequest)
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 || !strings.Contains(comments[0].Body, "Reason: "+reason) {
		t.Errorf("comments on #10 = %+v, want one stop note with the reason %q", comments, reason)
	}
	// The poll after the stop may add the notice that nothing can go on.
	if messages := sc.messagesExceptQ4(); len(messages) != 1 || !strings.Contains(messages[0], reason) {
		t.Errorf("notifications = %v, want one with the reason %q", messages, reason)
	}
	if got := workflow.InProgressIssues(service); len(got) != 0 {
		t.Errorf("issues in work = %v, want none after the issue stopped", got)
	}
}

// The label change of verify done reached GitHub, and its answer was lost.
// The kept step reads the issue first and sees the new label: it changes
// nothing, and the issue leaves the set of issues in work.
func TestKeptStep_AWriteThatAlreadyHappenedIsNotMadeTwice(t *testing.T) {
	sc := newScene(t, cliOptions{holds: true})
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := keptVerifyDone(t, sc, func() {
		sc.fake.CloseTimes(http.MethodPut, putLabelsPath, 1)
	})
	assertVerifyDoneIsKept(t, sc, service)
	labels := []string{"risk/low", workflow.LabelChecking}
	if err := sc.fake.SetLabels(sc.repo, 10, labels); err != nil {
		t.Fatal(err)
	}
	writes := sc.fake.CountRequests(http.MethodPut, putLabelsPath)

	if err := pollAtMinute(sc, service, 5); err != nil {
		t.Fatalf("Poll at minute 5: %v", err)
	}

	if !strings.Contains(sc.logs.String(), "while verify done was kept; nothing changes") {
		t.Errorf("the log does not say that the kept step changed nothing:\n%s", sc.logs.String())
	}
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != writes {
		t.Errorf("%d label changes of #10 by the kept step, want none", n-writes)
	}
	if got := workflow.InProgressIssues(service); len(got) != 0 {
		t.Errorf("issues in work = %v, want none", got)
	}
}
