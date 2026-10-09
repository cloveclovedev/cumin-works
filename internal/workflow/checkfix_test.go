package workflow_test

import (
	"context"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/core/state"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

// failingCheck makes the required check "ci" of the pull request of #10
// fail, with an annotation and a job log that FailedCheckContent reads, and
// gives the scene a state file with the session of an earlier run and
// count check fix requests. It returns the path of the state file.
func (sc *scene) failingCheck(t *testing.T, service *workflow.Service, count int) string {
	t.Helper()
	sc.awaitingChecks(t, []string{"ci"}, []githubtest.Check{{Name: "ci", Conclusion: "FAILURE"}})
	sc.fake.AddCheckRun(sc.repo, sc.remoteHead, githubtest.CheckRun{
		ID: 7, Name: "ci", Conclusion: "failure", JobID: 42,
		Annotations: []githubtest.Annotation{{Path: "login.go", Level: "failure", Message: "login_test.go:12: want 2, got 1"}},
		JobLog:      "step 1\nFAIL\texample/login\n",
	})
	path := filepath.Join(t.TempDir(), "state.json")
	service.State = state.Open(path, nil)
	if err := service.State.Set("example-org/example-repo", 10, state.Issue{SessionID: "earlier-session", CheckFixRequests: count}); err != nil {
		t.Fatal(err)
	}
	return path
}

// "request a check fix": a failed required check moves the issue back to
// cumin/status/implementing and sends one request of the kind "check fix",
// in the session of the last run, with what the failed check says. The
// label changes first, so the polls that follow send nothing more.
func TestAFailedCheckGivesOneFixRequestInTheSameSession(t *testing.T) {
	sc := newScene(t)
	service := sc.service()
	path := sc.failingCheck(t, service, 0)
	ctx := context.Background()

	for i := range 3 {
		if err := service.Poll(ctx); err != nil {
			t.Fatalf("poll %d: %v", i+1, err)
		}
	}
	service.Wait()

	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1", n)
	}
	args := sc.record(t, "agent.args")
	if got := argumentOf(t, args, "--resume"); got != "earlier-session" {
		t.Errorf("--resume = %q, want the session of the last run", got)
	}
	text := promptOf(t, args)
	requireIssueOfTheRun(t, text, 10, "implementation issue")
	for _, want := range []string{"Request: check fix", "Pull request: #21", "Branch: " + wantBranch,
		`Check "ci" failed.`, "login.go: login_test.go:12: want 2, got 1", "FAIL\texample/login", "Do not open a new pull request"} {
		if !strings.Contains(text, want) {
			t.Errorf("the request text has no %q:\n%s", want, text)
		}
	}
	// The worktree of the issue is on the branch of the pull request.
	dir := filepath.Join(sc.workRoot, "example-org", "example-repo", "10-implementer")
	if got := branchOf(t, dir); got != wantBranch {
		t.Errorf("branch of the worktree = %q, want %q", got, wantBranch)
	}
	if got := state.Open(path, nil).Issue("example-org/example-repo", 10).CheckFixRequests; got != 1 {
		t.Errorf("check fix requests after a restart = %d, want 1", got)
	}
	logs := sc.logs.String()
	for _, want := range []string{`"msg":"request a check fix: a required check failed; the issue goes back to the Implementer"`,
		`"msg":"request a check fix: requested the work"`, `"kind":"check fix"`, `"resumed":true`} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log has no %s", want)
		}
	}
}

// "request a check fix" after a rerun: the check failed at the poll, and a
// rerun of it passed before cumin read the content. GitHub then lists only
// the rerun by default. The request still holds the content of the attempt
// that failed, and nothing of the attempt that passed.
func TestACheckFixRequestHoldsTheFailedAttemptAfterARerunPassed(t *testing.T) {
	sc := newScene(t)
	service := sc.service()
	sc.failingCheck(t, service, 0)
	sc.fake.AddCheckRun(sc.repo, sc.remoteHead, githubtest.CheckRun{
		ID: 9, Name: "ci", Conclusion: "success", JobID: 43, JobLog: "ok\texample/login\n",
	})

	if err := service.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	service.Wait()

	text := promptOf(t, sc.record(t, "agent.args"))
	for _, want := range []string{`Check "ci" failed.`, "login.go: login_test.go:12: want 2, got 1", "FAIL\texample/login"} {
		if !strings.Contains(text, want) {
			t.Errorf("the request text has no %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "ok\texample/login") {
		t.Errorf("the request text holds the log of the attempt that passed:\n%s", text)
	}
}

// "request a check fix", then "wait for the checks": a fix that ends with done is verified
// again, and a verified pull request returns to cumin/status/checking.
func TestADoneCheckFixIsVerifiedAgain(t *testing.T) {
	sc := newScene(t)
	service := sc.service()
	sc.failingCheck(t, service, 1)

	sc.pollAndWait(t, service)

	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelChecking}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/checking", got)
	}
	// The claim and the wait for the checks: two label changes; the stop was not used.
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != 2 {
		t.Errorf("%d label changes, want 2 (the check fix request and the wait for the checks)", n)
	}
	got := service.State.Issue("example-org/example-repo", 10)
	if got.CheckFixRequests != 2 {
		t.Errorf("check fix requests = %d, want 2", got.CheckFixRequests)
	}
	if got.SessionID != "11111111-2222-4333-8444-555555555555" {
		t.Errorf("session = %q, want the session of the fix run", got.SessionID)
	}
}

// "stop for failed checks": at max_check_fix_requests, cumin sends no request.
// The issue gets one comment, cumin/status/awaiting-decision, and one
// notification, with the step "stop for failed checks". After the Maintainer
// adds cumin/status/ready,
// the next request starts a new session and the count starts at zero.
func TestTheLimitOfCheckFixRequestsStopsTheIssueForTheMaintainer(t *testing.T) {
	sc := newScene(t)
	service := sc.service()
	sc.failingCheck(t, service, 3)

	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none at the limit", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelAwaitingDecision}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/awaiting-decision", got)
	}
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 {
		t.Fatalf("%d comments, want 1", len(comments))
	}
	for _, want := range []string{"Step: stop for failed checks", "(ci)", "after 3 check fix requests", "Pull request: #21"} {
		if !strings.Contains(comments[0].Body, want) {
			t.Errorf("the comment has no %q:\n%s", want, comments[0].Body)
		}
	}
	if n := len(sc.webhook.messagesSent()); n != 1 {
		t.Errorf("%d notifications, want 1", n)
	}

	// The Maintainer answers and adds cumin/status/ready.
	if err := sc.fake.SetLabels(sc.repo, 10, []string{workflow.LabelReady, "risk/low"}); err != nil {
		t.Fatal(err)
	}
	sc.pollAndWait(t, service)
	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs after cumin/status/ready, want 1", n)
	}
	if args := sc.record(t, "agent.args"); strings.Contains(args, "--resume") {
		t.Error("the request after cumin/status/ready resumed a session; it must start a new one")
	}
	if got := service.State.Issue("example-org/example-repo", 10).CheckFixRequests; got != 0 {
		t.Errorf("check fix requests after cumin/status/ready = %d, want 0", got)
	}
}

// "request a check fix": a label that cannot change starts no request, and the count goes
// back, so that failed writes never use up the limit.
func TestAFailedLabelChangeKeepsTheCountOfCheckFixRequests(t *testing.T) {
	sc := newScene(t)
	service := sc.service()
	sc.failingCheck(t, service, 1)
	sc.fake.FailNext(http.MethodPut, putLabelsPath, http.StatusInternalServerError)

	if err := service.Poll(context.Background()); err == nil {
		t.Error("the poll hid the failed label change")
	}
	service.Wait()

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none", n)
	}
	if got := service.State.Issue("example-org/example-repo", 10).CheckFixRequests; got != 1 {
		t.Errorf("check fix requests = %d, want 1 (set back)", got)
	}
}

// "stop for failed checks": at the limit, the label changes first. When it
// cannot change, cumin
// posts no comment and sends no notification, so that the polls that
// follow do not repeat them.
func TestAStopForFailedChecksWhoseLabelFailsWritesNothingElse(t *testing.T) {
	sc := newScene(t)
	service := sc.service()
	sc.failingCheck(t, service, 3)
	sc.fake.FailNext(http.MethodPut, putLabelsPath, http.StatusInternalServerError)

	if err := service.Poll(context.Background()); err == nil {
		t.Error("the poll hid the failed label change")
	}
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 0 {
		t.Errorf("%d comments, want none", n)
	}
	if n := len(sc.webhook.messagesSent()); n != 0 {
		t.Errorf("%d notifications, want none", n)
	}

	// The next poll stops the issue once.
	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 1 {
		t.Errorf("%d comments after the label changed, want 1", n)
	}
	if n := len(sc.webhook.messagesSent()); n != 1 {
		t.Errorf("%d notifications after the label changed, want 1", n)
	}
}
