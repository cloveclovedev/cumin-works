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

// reviewerScene puts issue #10 in cumin/status/checking with the risk
// label, one passed required check, and a Reviewer run that holds and then
// submits the review. The next poll applies I3.
func reviewerScene(t *testing.T, opts cliOptions, risk string) (*scene, *workflow.Service) {
	t.Helper()
	opts.holds = true
	sc := newScene(t, opts)
	sc.awaitingChecks(t, []string{"ci"}, []githubtest.Check{{Name: "ci", Conclusion: "SUCCESS"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{
		Number: 10, Parent: 6, Title: subIssueTitle,
		Labels: []string{"cumin/status/checking", risk},
	})
	return sc, sc.serviceWithSession(t)
}

// endHeldRun waits for the agent run that holds, sets the failure of the
// fake GitHub, and lets the run end. The failure meets the step after the
// run, and no call before it.
func endHeldRun(t *testing.T, sc *scene, service *workflow.Service, fail func()) {
	t.Helper()
	waitForAgentRun(t, sc)
	fail()
	sc.release(t)
	service.Wait()
}

// keptReview polls once, so that the Reviewer of #10 runs, and ends the run
// while the fake GitHub answers as fail sets it. The issue then keeps
// cumin/status/reviewing with no agent.
func keptReview(t *testing.T, sc *scene, service *workflow.Service, fail func()) {
	t.Helper()
	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	endHeldRun(t, sc, service, fail)
}

// failEveryRead makes every try of the next read of the issue fail.
func failEveryRead(sc *scene) func() {
	return func() { sc.fake.FailTimes(http.MethodPost, "/graphql", 0, everyTry, http.StatusBadGateway) }
}

// assertStillReviewing checks that nothing changed on #10 after the
// Reviewer run: the label stays, no comment is written, one agent ran, and
// cumin keeps no step for the issue.
func assertStillReviewing(t *testing.T, sc *scene, service *workflow.Service, risk string) {
	t.Helper()
	want := []string{risk, workflow.LabelReviewing}
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
	if messages := sc.messagesExceptQ4(); len(messages) != 0 {
		t.Errorf("notifications = %v, want none", messages)
	}
}

// restartedWith returns a new service on the scene with the state file of
// the service that stopped, as a restart of cumin gives it: it holds no run
// and no memory of the first one.
func (sc *scene) restartedWith(stopped *workflow.Service) *workflow.Service {
	service := sc.service()
	service.State = stopped.State
	return service
}

// A restart of cumin in cumin/status/reviewing after a change request: the
// read after the Reviewer run failed, so the issue kept its label. The poll
// of the new cumin finds REQUEST_CHANGES on the head commit, and sends
// exactly one fix request.
func TestReviewing_ARestartAfterAChangeRequestSendsExactlyOneFixRequest(t *testing.T) {
	sc, stopped := reviewerScene(t, cliOptions{reviews: []string{"REQUEST_CHANGES"}}, "risk/low")
	keptReview(t, sc, stopped, failEveryRead(sc))
	assertStillReviewing(t, sc, stopped, "risk/low")

	// The fix request runs outside the poll: the poll returns while the
	// Implementer holds, and the issue stays in work.
	restarted := sc.restartedWith(stopped)
	sc.clock.Set(sceneNow.Add(time.Minute))
	if err := restarted.Poll(context.Background()); err != nil {
		t.Fatalf("Poll after the restart: %v", err)
	}
	waitForAgentRun(t, sc)
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelImplementing}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/implementing during the fix", got)
	}
	// A poll during the fix sends no second request.
	sc.clock.Set(sceneNow.Add(2 * time.Minute))
	if err := restarted.Poll(context.Background()); err != nil {
		t.Fatalf("Poll during the fix: %v", err)
	}
	sc.release(t)
	restarted.Wait()

	if n := sc.agentRuns(t); n != 2 {
		t.Fatalf("%d agent runs, want the review and one fix", n)
	}
	args := sc.record(t, "agent.args")
	if got := argumentOf(t, args, "--resume"); got != "implementer-session" {
		t.Errorf("--resume = %q, want the Implementer session", got)
	}
	if text := promptOf(t, args); !strings.Contains(text, "Request: review fix") {
		t.Errorf("the request text is no review fix:\n%s", text)
	}
	if n := strings.Count(sc.logs.String(), `"msg":"I5: requested the work"`); n != 1 {
		t.Errorf("%d fix requests in the log, want 1", n)
	}
}

// A restart of cumin in cumin/status/reviewing after an approval with
// risk/medium: the poll of the new cumin moves the issue to
// cumin/status/awaiting-merge-decision with one notification. The polls
// that follow send nothing more.
func TestReviewing_ARestartAfterAnApprovalWithRiskMediumAsksTheOwnerOnce(t *testing.T) {
	sc, stopped := reviewerScene(t, cliOptions{reviews: []string{"APPROVE"}}, "risk/medium")
	keptReview(t, sc, stopped, failEveryRead(sc))
	assertStillReviewing(t, sc, stopped, "risk/medium")

	restarted := sc.restartedWith(stopped)
	for minute := 1; minute <= 2; minute++ {
		if err := pollAtMinute(sc, restarted, minute); err != nil {
			t.Fatalf("Poll at minute %d: %v", minute, err)
		}
	}

	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/medium", workflow.LabelAwaitingMergeDecision}) {
		t.Errorf("labels of #10 = %v, want risk/medium and cumin/status/awaiting-merge-decision", got)
	}
	if messages := sc.messagesExceptQ4(); len(messages) != 1 || !strings.Contains(messages[0], "the merge needs a decision") {
		t.Errorf("notifications = %v, want one that asks for the merge decision", messages)
	}
	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
		t.Errorf("%d merge requests, want none", n)
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1", n)
	}
}

// A failed read of the required checks after an approval changes nothing
// either, and the next poll decides the same.
func TestReviewing_AFailedReadOfTheRequiredChecksChangesNothingAndTheNextPollDecides(t *testing.T) {
	sc, service := reviewerScene(t, cliOptions{reviews: []string{"APPROVE"}}, "risk/medium")
	keptReview(t, sc, service, func() {
		sc.fake.FailTimes(http.MethodGet, branchRulesPath, 0, everyTry, http.StatusBadGateway)
	})
	assertStillReviewing(t, sc, service, "risk/medium")

	if err := pollAtMinute(sc, service, 1); err != nil {
		t.Fatalf("Poll at minute 1: %v", err)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/medium", workflow.LabelAwaitingMergeDecision}) {
		t.Errorf("labels of #10 = %v, want risk/medium and cumin/status/awaiting-merge-decision", got)
	}
	if messages := sc.messagesExceptQ4(); len(messages) != 1 {
		t.Errorf("notifications = %v, want one", messages)
	}
}

// A restart of cumin in cumin/status/reviewing with no review on the head
// commit: the poll sends one second request and counts it in the state
// file. That run leaves no review either, so the issue goes to
// cumin/status/awaiting-decision with the reason. The poll that follows
// sends nothing more.
func TestReviewing_ARestartWithNoReviewSendsOneSecondRequestThenStops(t *testing.T) {
	sc, stopped := reviewerScene(t, cliOptions{}, "risk/low")
	keptReview(t, sc, stopped, failEveryRead(sc))
	assertStillReviewing(t, sc, stopped, "risk/low")

	restarted := sc.restartedWith(stopped)
	sc.clock.Set(sceneNow.Add(time.Minute))
	if err := restarted.Poll(context.Background()); err != nil {
		t.Fatalf("Poll after the restart: %v", err)
	}
	waitForAgentRun(t, sc)
	// The new cumin cannot know that the session of the state file got the
	// request of this stay, so the second request is the whole review
	// request, and not the short text for a session that holds it.
	text := promptOf(t, sc.record(t, "agent.args"))
	for _, want := range []string{"Request: review\n", "Implementation issue: #10", "Pull request: #21", "Work directory: "} {
		if !strings.Contains(text, want) {
			t.Errorf("the second request after the restart does not hold %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "cumin found no review of yours") {
		t.Errorf("the second request after the restart is the short text for a resumed session:\n%s", text)
	}
	sc.release(t)
	restarted.Wait()
	if err := pollAtMinute(sc, restarted, 2); err != nil {
		t.Fatalf("Poll at minute 2: %v", err)
	}

	if n := sc.agentRuns(t); n != 2 {
		t.Errorf("%d agent runs, want the review and one second request", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelAwaitingDecision}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/awaiting-decision", got)
	}
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 || !strings.Contains(comments[0].Body, workflow.MissingReviewReason) {
		t.Errorf("comments on #10 = %+v, want the reason once", comments)
	}
	if messages := sc.messagesExceptQ4(); len(messages) != 1 {
		t.Errorf("notifications = %v, want one", messages)
	}
}

// A restart of cumin after the head commit moved during the review: the
// state file holds the head commit of the request, so the poll sends the
// issue back to cumin/status/checking, and requests no review of a head
// commit whose checks did not run.
func TestReviewing_ARestartAfterTheHeadMovedGoesBackToTheChecks(t *testing.T) {
	sc, stopped := reviewerScene(t, cliOptions{reviews: []string{"APPROVE"}, movesHeadOnRun: 1}, "risk/low")
	keptReview(t, sc, stopped, failEveryRead(sc))
	assertStillReviewing(t, sc, stopped, "risk/low")
	// The required check has not reported on the new head, so that the
	// issue stays in cumin/status/checking.
	sc.repo.PullRequests[21].Checks = nil

	restarted := sc.restartedWith(stopped)
	for minute := 1; minute <= 2; minute++ {
		if err := pollAtMinute(sc, restarted, minute); err != nil {
			t.Fatalf("Poll at minute %d: %v", minute, err)
		}
	}

	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelChecking}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/checking", got)
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want only the review of the old head", n)
	}
	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
		t.Errorf("%d merge requests, want none", n)
	}
}

// While the Reviewer runs, a poll changes nothing on the issue, and reads
// nothing more for it.
func TestReviewing_APollChangesNothingWhileTheReviewerRuns(t *testing.T) {
	sc, service := reviewerScene(t, cliOptions{reviews: []string{"APPROVE"}}, "risk/medium")
	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	waitForAgentRun(t, sc)
	changes, reads := sc.fake.CountRequests(http.MethodPut, putLabelsPath), sc.issueReads(10)

	sc.clock.Set(sceneNow.Add(time.Minute))
	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("Poll during the review: %v", err)
	}

	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/medium", workflow.LabelReviewing}) {
		t.Errorf("labels of #10 = %v, want risk/medium and cumin/status/reviewing", got)
	}
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != changes {
		t.Errorf("%d label changes of #10 during the review, want none", n-changes)
	}
	if n := sc.issueReads(10); n != reads {
		t.Errorf("%d reads of #10 during the review, want none", n-reads)
	}
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 0 {
		t.Errorf("%d comments on #10, want none", n)
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1", n)
	}
	sc.release(t)
	service.Wait()
}

// A cumin/status/reviewing that an account with only triage permission
// added is not a state (issue-states.md, the account that added a status
// label): no agent starts, nothing is merged, no label changes, and the
// Owner is told once.
func TestReviewing_ALabelOfAnAccountWithTriagePermissionDoesNothing(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	sc.repo.Issues[10].Labels = []string{"risk/low", workflow.LabelReviewing}
	sc.repo.Issues[10].LabelEvents = []githubtest.LabelEvent{statusBy(workflow.LabelReviewing, "a-triager")}
	sc.fake.SetPermission("a-triager", "triage", "User")
	service := sc.service()

	for range 3 {
		sc.pollAndWait(t, service)
	}

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none", n)
	}
	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
		t.Errorf("%d merge requests, want none", n)
	}
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != 0 {
		t.Errorf("%d label changes of #10, want none", n)
	}
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 0 {
		t.Errorf("%d comments on #10, want none", n)
	}
	if n := strings.Count(sc.logs.String(), "is not of cumin-core or of an Owner"); n != 1 {
		t.Errorf("%d log lines for one label event across three polls, want 1:\n%s", n, sc.logs.String())
	}
}
