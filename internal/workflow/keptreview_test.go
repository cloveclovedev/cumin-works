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

// reviewerScene puts issue #10 in cumin/status/awaiting-checks with the risk
// label, one passed required check, and a Reviewer run that holds and then
// submits the review. The next poll applies I3.
func reviewerScene(t *testing.T, opts cliOptions, risk string) (*scene, *workflow.Service) {
	t.Helper()
	opts.holds = true
	sc := newScene(t, opts)
	sc.awaitingChecks(t, []string{"ci"}, []githubtest.Check{{Name: "ci", Conclusion: "SUCCESS"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{
		Number: 10, Parent: 6, Title: subIssueTitle,
		Labels: []string{"cumin/status/awaiting-checks", risk},
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
// while the fake GitHub answers as fail sets it.
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

// assertReviewStepIsKept checks that #10 waits with a kept step after the
// Reviewer run: the label stays, no comment is written, no second agent
// started, and the issue counts as in work.
func assertReviewStepIsKept(t *testing.T, sc *scene, service *workflow.Service, risk, step string) {
	t.Helper()
	want := []string{risk, workflow.LabelReviewing}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, want) {
		t.Errorf("labels of #10 = %v, want %v while the step is kept", got, want)
	}
	if comments := sc.fake.Comments(sc.repo, 10); len(comments) != 0 {
		t.Errorf("%d comments on #10, want none while the step is kept: %+v", len(comments), comments)
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1: no agent starts while the step is kept", n)
	}
	if got := workflow.InProgressIssues(service); !slices.Equal(got, []string{"example-org/example-repo#10"}) {
		t.Errorf("issues in work = %v, want #10 while the step is kept", got)
	}
	if n := strings.Count(sc.logs.String(), `"msg":"kept `+step+` after a temporary failure: a later poll runs it again"`); n != 1 {
		t.Errorf("%d log lines for the kept step %q, want 1:\n%s", n, step, sc.logs.String())
	}
}

// Core-28 (cumin-core.md), I5: the fake GitHub fails every try of the read
// after the Reviewer returned done. cumin keeps the check of the review:
// the label stays cumin/status/reviewing. A poll before the delay does not
// run the step. The poll after the delay runs it, and the review with
// REQUEST_CHANGES sends one fix request.
func TestKeptStep_TheCheckOfTheReviewRunsAgainAndSendsOneFixRequest(t *testing.T) {
	sc, service := reviewerScene(t, cliOptions{reviews: []string{"REQUEST_CHANGES"}}, "risk/low")
	keptReview(t, sc, service, failEveryRead(sc))
	assertReviewStepIsKept(t, sc, service, "risk/low", "the check of the review")

	reads := sc.issueReads(10)
	if err := pollAtMinute(sc, service, 4); err != nil {
		t.Fatalf("Poll at minute 4: %v", err)
	}
	if n := sc.issueReads(10); n != reads {
		t.Errorf("%d reads of #10 at minute 4, want none before the delay", n-reads)
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs at minute 4, want 1", n)
	}

	// The fix request runs outside the poll: the poll returns while the
	// Implementer holds, and the issue stays in work.
	sc.clock.Set(sceneNow.Add(5 * time.Minute))
	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("Poll at minute 5: %v", err)
	}
	waitForAgentRun(t, sc)
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelImplementing}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/implementing during the fix", got)
	}
	if got := workflow.InProgressIssues(service); !slices.Equal(got, []string{"example-org/example-repo#10"}) {
		t.Errorf("issues in work = %v, want #10 during the fix", got)
	}
	sc.release(t)
	service.Wait()

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
	// The fix is pushed (the fake CLI adds no commit), so I2 passes.
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelAwaitingChecks}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/awaiting-checks after the fix", got)
	}
	if got := workflow.InProgressIssues(service); len(got) != 0 {
		t.Errorf("issues in work = %v, want none after the fix ended", got)
	}
}

// Core-28 (cumin-core.md), I6 and I7: after a failed read, the kept step
// finds the approval at a later poll and reaches the merge decision. A
// risk/low pull request is merged once; a risk/medium one waits for the
// Owner, with one notification.
func TestKeptStep_TheCheckOfTheReviewRunsAgainAndReachesTheMergeDecision(t *testing.T) {
	t.Run("risk/low is merged", func(t *testing.T) {
		sc, service := reviewerScene(t, cliOptions{reviews: []string{"APPROVE"}}, "risk/low")
		keptReview(t, sc, service, failEveryRead(sc))
		assertReviewStepIsKept(t, sc, service, "risk/low", "the check of the review")
		if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
			t.Errorf("%d merge requests while the step is kept, want none", n)
		}

		if err := pollAtMinute(sc, service, 5); err != nil {
			t.Fatalf("Poll at minute 5: %v", err)
		}
		for _, want := range []string{`"msg":"I6: decided on the approved pull request"`, `"decision":"` + workflow.MergeNow.String() + `"`} {
			if !strings.Contains(sc.logs.String(), want) {
				t.Errorf("the log of the merge decision does not hold %s:\n%s", want, sc.logs.String())
			}
		}
		if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 1 {
			t.Errorf("%d merge requests, want 1", n)
		}
		if got := workflow.InProgressIssues(service); len(got) != 0 {
			t.Errorf("issues in work = %v, want none after the merge step", got)
		}
	})
	t.Run("risk/medium waits for the Owner", func(t *testing.T) {
		sc, service := reviewerScene(t, cliOptions{reviews: []string{"APPROVE"}}, "risk/medium")
		keptReview(t, sc, service, failEveryRead(sc))
		assertReviewStepIsKept(t, sc, service, "risk/medium", "the check of the review")
		if messages := sc.messagesExceptQ4(); len(messages) != 0 {
			t.Errorf("notifications while the step is kept = %v, want none", messages)
		}

		if err := pollAtMinute(sc, service, 5); err != nil {
			t.Fatalf("Poll at minute 5: %v", err)
		}
		if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/medium", workflow.LabelAwaitingOwnerReview}) {
			t.Errorf("labels of #10 = %v, want risk/medium and cumin/status/awaiting-owner-review", got)
		}
		if messages := sc.messagesExceptQ4(); len(messages) != 1 || !strings.Contains(messages[0], "the merge needs a decision") {
			t.Errorf("notifications = %v, want one of I7", messages)
		}
		if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
			t.Errorf("%d merge requests, want none", n)
		}
		if n := sc.agentRuns(t); n != 1 {
			t.Errorf("%d agent runs, want 1", n)
		}
	})
}

// A failed read of the required checks after an approval keeps the step in
// the same way: the first read of the issue succeeds, and every try of the
// read of the rules of the default branch fails.
func TestKeptStep_AFailedReadOfTheRequiredChecksAfterAnApprovalKeepsTheStep(t *testing.T) {
	sc, service := reviewerScene(t, cliOptions{reviews: []string{"APPROVE"}}, "risk/medium")
	keptReview(t, sc, service, func() {
		sc.fake.FailTimes(http.MethodGet, branchRulesPath, 0, everyTry, http.StatusBadGateway)
	})
	assertReviewStepIsKept(t, sc, service, "risk/medium", "the check of the review")
	if !strings.Contains(sc.logs.String(), `"msg":"I6: the required checks were not read; the issue keeps its label"`) {
		t.Errorf("the log does not say that the required checks were not read:\n%s", sc.logs.String())
	}
	if messages := sc.messagesExceptQ4(); len(messages) != 0 {
		t.Errorf("notifications while the step is kept = %v, want none", messages)
	}

	if err := pollAtMinute(sc, service, 5); err != nil {
		t.Fatalf("Poll at minute 5: %v", err)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/medium", workflow.LabelAwaitingOwnerReview}) {
		t.Errorf("labels of #10 = %v, want risk/medium and cumin/status/awaiting-owner-review", got)
	}
	if messages := sc.messagesExceptQ4(); len(messages) != 1 {
		t.Errorf("notifications = %v, want one of I7", messages)
	}
	if got := workflow.InProgressIssues(service); len(got) != 0 {
		t.Errorf("issues in work = %v, want none after the kept step ended", got)
	}
}

// Core-28 (cumin-core.md), I10: the Reviewer returned blocked, and every
// try of the read of the issue fails. cumin keeps the stop and writes
// nothing. At a later poll the issue reaches
// cumin/status/awaiting-owner-decision with one comment.
func TestKeptStep_TheStopAfterABlockedReviewRunsAgainWithOneComment(t *testing.T) {
	sc, service := reviewerScene(t, cliOptions{fixture: "blocked.jsonl"}, "risk/low")
	keptReview(t, sc, service, failEveryRead(sc))
	assertReviewStepIsKept(t, sc, service, "risk/low", "the stop after a blocked review")
	if messages := sc.messagesExceptQ4(); len(messages) != 0 {
		t.Errorf("notifications while the stop is kept = %v, want none", messages)
	}

	if err := pollAtMinute(sc, service, 5); err != nil {
		t.Fatalf("Poll at minute 5: %v", err)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelAwaitingOwnerDecision}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/awaiting-owner-decision", got)
	}
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 || !strings.HasPrefix(comments[0].Body, "## Decision needed") {
		t.Errorf("comments on #10 = %+v, want the blocked_reason of the Reviewer once", comments)
	}
	if messages := sc.messagesExceptQ4(); len(messages) != 1 {
		t.Errorf("notifications = %v, want one of I10", messages)
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1: a blocked result is not retried", n)
	}
	if got := workflow.InProgressIssues(service); len(got) != 0 {
		t.Errorf("issues in work = %v, want none after the issue stopped", got)
	}

	// A later poll finds no kept step: nothing is written twice.
	if err := pollAtMinute(sc, service, 10); err != nil {
		t.Fatalf("Poll at minute 10: %v", err)
	}
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 1 {
		t.Errorf("%d comments on #10 after a later poll, want 1", n)
	}
}

// The label change of I5 reached GitHub, and its answer was lost. The kept
// step reads the issue first and sees cumin/status/implementing: it does
// not write the label again, and it sends the fix request once.
func TestKeptStep_ALostLabelChangeOfTheFixRequestIsNotMadeTwice(t *testing.T) {
	sc, service := reviewerScene(t, cliOptions{reviews: []string{"REQUEST_CHANGES"}}, "risk/low")
	keptReview(t, sc, service, func() { sc.fake.CloseTimes(http.MethodPut, putLabelsPath, 1) })
	assertReviewStepIsKept(t, sc, service, "risk/low", "the check of the review")
	implementing := []string{"risk/low", workflow.LabelImplementing}
	if err := sc.fake.SetLabels(sc.repo, 10, implementing); err != nil {
		t.Fatal(err)
	}
	writes := sc.fake.CountRequests(http.MethodPut, putLabelsPath)

	sc.clock.Set(sceneNow.Add(5 * time.Minute))
	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("Poll at minute 5: %v", err)
	}
	waitForAgentRun(t, sc)
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != writes {
		t.Errorf("%d label changes of #10 before the fix request, want none", n-writes)
	}
	sc.release(t)
	service.Wait()

	if n := sc.agentRuns(t); n != 2 {
		t.Errorf("%d agent runs, want the review and one fix", n)
	}
	if n := strings.Count(sc.logs.String(), `"msg":"I5: requested the work"`); n != 1 {
		t.Errorf("%d fix requests in the log, want 1", n)
	}
}

// The Owner moved the issue while the step was kept. The kept step reads
// the issue first, finds it out of cumin/status/reviewing, and changes
// nothing: no request is sent.
func TestKeptStep_AnIssueThatLeftTheReviewIsLeftAsItIs(t *testing.T) {
	sc, service := reviewerScene(t, cliOptions{reviews: []string{"REQUEST_CHANGES"}}, "risk/low")
	keptReview(t, sc, service, failEveryRead(sc))
	assertReviewStepIsKept(t, sc, service, "risk/low", "the check of the review")
	labels := []string{"risk/low", workflow.LabelAwaitingOwnerDecision}
	if err := sc.fake.SetLabels(sc.repo, 10, labels); err != nil {
		t.Fatal(err)
	}
	writes := sc.fake.CountRequests(http.MethodPut, putLabelsPath)

	if err := pollAtMinute(sc, service, 5); err != nil {
		t.Fatalf("Poll at minute 5: %v", err)
	}
	if !strings.Contains(sc.logs.String(), "while the check of the review was kept; nothing changes") {
		t.Errorf("the log does not say that the kept step changed nothing:\n%s", sc.logs.String())
	}
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != writes {
		t.Errorf("%d label changes of #10 by the kept step, want none", n-writes)
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1: nothing is requested", n)
	}
	if got := workflow.InProgressIssues(service); len(got) != 0 {
		t.Errorf("issues in work = %v, want none", got)
	}
}

// The label change after a head that moved reached GitHub, and its answer
// was lost: the issue has cumin/status/awaiting-checks with passed checks
// while the step is kept. A poll before the delay starts no agent for the
// issue in work. The poll after the delay ends the step: it writes nothing,
// and no second agent run starts.
func TestKeptStep_AnIssueInWorkThatWaitsForTheChecksStartsNoSecondAgent(t *testing.T) {
	sc, service := reviewerScene(t, cliOptions{reviews: []string{"APPROVE"}, movesHeadOnRun: 1}, "risk/low")
	keptReview(t, sc, service, func() { sc.fake.CloseTimes(http.MethodPut, putLabelsPath, 1) })
	assertReviewStepIsKept(t, sc, service, "risk/low", "the check of the review")
	labels := []string{"risk/low", workflow.LabelAwaitingChecks}
	if err := sc.fake.SetLabels(sc.repo, 10, labels); err != nil {
		t.Fatal(err)
	}
	writes := sc.fake.CountRequests(http.MethodPut, putLabelsPath)

	if err := pollAtMinute(sc, service, 1); err != nil {
		t.Fatalf("Poll at minute 1: %v", err)
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs at minute 1, want 1: no agent starts for an issue in work", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, labels) {
		t.Errorf("labels of #10 at minute 1 = %v, want %v", got, labels)
	}
	if got := workflow.InProgressIssues(service); !slices.Equal(got, []string{"example-org/example-repo#10"}) {
		t.Errorf("issues in work at minute 1 = %v, want #10", got)
	}

	// The required check has not reported on the new head, so that the poll
	// that ends the step starts no review of its own.
	sc.repo.PullRequests[21].Checks = nil
	if err := pollAtMinute(sc, service, 5); err != nil {
		t.Fatalf("Poll at minute 5: %v", err)
	}
	if !strings.Contains(sc.logs.String(), "while the check of the review was kept; nothing changes") {
		t.Errorf("the log does not say that the kept step changed nothing:\n%s", sc.logs.String())
	}
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != writes {
		t.Errorf("%d label changes of #10 after the lost one, want none", n-writes)
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1: no second run", n)
	}
	if got := workflow.InProgressIssues(service); len(got) != 0 {
		t.Errorf("issues in work = %v, want none after the kept step ended", got)
	}
}
