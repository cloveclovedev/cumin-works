package workflow_test

import (
	"context"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

const putRequirementLabelsPath = "/repos/example-org/example-repo/issues/6/labels"

// newPlanScene is the scene of R1: the requirement issue #6 carries
// cumin/status/ready, and its sub-issue #10 carries no status label, so
// that I1 does not start it. The fake CLI answers as a Planner that
// returned done.
func newPlanScene(t *testing.T) *scene {
	t.Helper()
	sc := newScene(t, cliOptions{fixture: "planner-done.jsonl"})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 6, Labels: []string{githubtest.RequirementLabel, "cumin/status/ready"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Labels: []string{"risk/low"}})
	return sc
}

// R1 (issue-states.md): a ready requirement issue moves to
// cumin/status/planning, and only then the Planner starts once, with the
// request "plan", in a new session and a detached checkout of the default
// branch.
func TestR1_AReadyRequirementIssueIsPlannedOnce(t *testing.T) {
	sc := newPlanScene(t)
	service := sc.service()
	for i := range 3 {
		if err := service.Poll(context.Background()); err != nil {
			t.Fatalf("poll %d: %v", i+1, err)
		}
	}
	service.Wait()

	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1", n)
	}
	// Two label changes: planning before the request (R1), and the review
	// of the Owner after the run (R2).
	if n := sc.fake.CountRequests(http.MethodPut, putRequirementLabelsPath); n != 2 {
		t.Errorf("%d label changes of #6, want 2", n)
	}
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != 0 {
		t.Errorf("%d label changes of #10, want none", n)
	}

	// The work directory is the one of the issue and the role, detached at
	// the head of the default branch; no branch is made for it.
	// The work directory is gone after the run, so its path is resolved
	// from the parent, which stays.
	wantDir := filepath.Join(realPath(t, filepath.Join(sc.workRoot, "example-org", "example-repo")), "6-planner")
	if got := strings.TrimSpace(sc.record(t, "agent.cwd")); got != wantDir {
		t.Errorf("the CLI ran in %q, want %q", got, wantDir)
	}
	if got := strings.TrimSpace(sc.record(t, "agent.ref")); got != "HEAD" {
		t.Errorf("the work directory is on the branch %q, want a detached HEAD", got)
	}
	if got := strings.TrimSpace(sc.record(t, "agent.head")); got != sc.remoteHead {
		t.Errorf("the work directory is at %s, want the head of main %s", got, sc.remoteHead)
	}
	if branches := strings.TrimSpace(sc.record(t, "agent.branches")); branches != "" {
		t.Errorf("a branch was made for the Planner: %q", branches)
	}

	args := sc.record(t, "agent.args")
	text := promptOf(t, args)
	requireIssueOfTheRun(t, text, 6, "requirement issue")
	for _, want := range []string{"Request: plan", "example-org/example-repo", "Requirement issue: #6"} {
		if !strings.Contains(text, want) {
			t.Errorf("the request text has no %q:\n%s", want, text)
		}
	}
	if strings.Contains(args, "--resume") {
		t.Error("the Planner resumed a session, want a new one")
	}
	instruction := systemPromptOf(t, args)
	for _, want := range []string{"# Planner", "# Software engineering for the Planner"} {
		if !strings.Contains(instruction, want) {
			t.Errorf("the instruction does not hold %q", want)
		}
	}

	logs := sc.logs.String()
	for _, want := range []string{`"msg":"R1: moved the requirement issue to planning"`, `"cumin/status/planning"`,
		`"msg":"R1: requested the Planner"`, `"kind":"plan"`, `"role":"planner"`,
		`"msg":"the agent run ended"`, `"result":"done"`} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log has no %s", want)
		}
	}
}

// R1 with Core-8 (cumin-core.md): after a restart, the requirement issue
// in cumin/status/planning is not requested again.
func TestR1_RestartDoesNotRequestTwice(t *testing.T) {
	sc := newPlanScene(t)
	sc.pollAndWait(t, sc.service())
	sc.pollAndWait(t, sc.service())

	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1", n)
	}
}

// R1 waits while a blocked-by issue of the requirement issue is open, and
// starts after it closes.
func TestR1_AnOpenBlockedByIssueWaits(t *testing.T) {
	sc := newPlanScene(t)
	blocker := &githubtest.Issue{Number: 3, Title: "Another requirement"}
	sc.fake.AddIssue(sc.repo, blocker)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 6, Labels: []string{githubtest.RequirementLabel, "cumin/status/ready"}, BlockedBy: []int{3}})
	service := sc.service()

	sc.pollAndWait(t, service)
	if n := sc.agentRuns(t); n != 0 {
		t.Fatalf("%d agent runs while #3 is open, want none", n)
	}
	if got := sc.fake.Issue(sc.repo, 6).Labels; !slices.Contains(got, "cumin/status/ready") {
		t.Errorf("labels of #6 = %v, want cumin/status/ready kept", got)
	}

	blocker.Closed = true
	sc.pollAndWait(t, service)
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs after #3 closed, want 1", n)
	}
}

// A requirement issue in cumin/status/planning, whose Planner is still
// running, counts against the limit of issues in progress
// (max_issues_in_progress is 1 in the scene), so a ready sub-issue of
// another requirement issue waits.
func TestR1_PlanningFillsTheLimit(t *testing.T) {
	sc := newPlanScene(t)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 6, Labels: []string{githubtest.RequirementLabel, "cumin/status/planning"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 7, Labels: []string{githubtest.RequirementLabel, "cumin/status/implementing"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 11, Parent: 7, Title: "Another sub-issue", Labels: []string{"cumin/status/ready", "risk/low"}})
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none", n)
	}
	if got := sc.fake.Issue(sc.repo, 11).Labels; !slices.Contains(got, "cumin/status/ready") {
		t.Errorf("labels of #11 = %v, want cumin/status/ready while #6 is planning", got)
	}
}

// The start request of the Planner carries the risk criteria of the
// repository, which the instruction ends with.
func TestR1_TheInstructionEndsWithTheRiskCriteriaOfTheRepository(t *testing.T) {
	const criteria = "# Risk criteria of this repository\n\nEvery change is risk/high.\n"
	sc := newPlanScene(t)
	sc.fake.SetFile(sc.repo, ".cumin/risk-criteria.md", githubtest.File{Content: criteria})
	sc.pollAndWait(t, sc.service())

	instruction := systemPromptOf(t, sc.record(t, "agent.args"))
	if !strings.HasSuffix(instruction, criteria) {
		t.Errorf("the instruction does not end with the risk criteria of the repository:\n%s", instruction)
	}
}

// assertStoppedForTheOwner checks the stop step of R2 on the requirement
// issue #6: one comment that holds each of body, the label
// cumin/status/awaiting-decision, and one notification that holds
// each of message.
func assertStoppedForTheOwner(t *testing.T, sc *scene, body, message []string) {
	t.Helper()
	assertStoppedWithMessages(t, sc, sc.webhook.messagesSent(), body, message)
}

// assertStoppedForTheOwnerAfterAPoll is assertStoppedForTheOwner for a
// test that polls after the stop: that poll may add the notice that
// nothing can go on (Q4), which does not count.
func assertStoppedForTheOwnerAfterAPoll(t *testing.T, sc *scene, body, message []string) {
	t.Helper()
	assertStoppedWithMessages(t, sc, sc.messagesExceptQ4(), body, message)
}

func assertStoppedWithMessages(t *testing.T, sc *scene, messages, body, message []string) {
	t.Helper()
	comments := sc.fake.Comments(sc.repo, 6)
	if len(comments) != 1 {
		t.Fatalf("%d comments on #6, want 1: %+v", len(comments), comments)
	}
	for _, want := range body {
		if !strings.Contains(comments[0].Body, want) {
			t.Errorf("the comment has no %q:\n%s", want, comments[0].Body)
		}
	}
	want := []string{githubtest.RequirementLabel, "cumin/status/awaiting-decision"}
	if got := sc.fake.Issue(sc.repo, 6).Labels; !slices.Equal(got, want) {
		t.Errorf("labels of #6 = %v, want %v", got, want)
	}
	if len(messages) != 1 {
		t.Fatalf("%d notifications, want 1: %v", len(messages), messages)
	}
	for _, want := range append([]string{"R2", "example-org/example-repo", "issue #6"}, message...) {
		if !strings.Contains(messages[0], want) {
			t.Errorf("the notification has no %q:\n%s", want, messages[0])
		}
	}
}

// R2 (issue-states.md): after done, the requirement issue has one or more
// sub-issues, each with exactly one risk label. Then it moves to
// cumin/status/awaiting-plan-review, and the Owner is notified once.
func TestR2_ASplitWithOneRiskLabelEachGoesToTheOwner(t *testing.T) {
	sc := newPlanScene(t)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 11, Parent: 6, Title: "Second", Labels: []string{"risk/high"}})
	sc.pollAndWait(t, sc.service())

	want := []string{githubtest.RequirementLabel, "cumin/status/awaiting-plan-review"}
	if got := sc.fake.Issue(sc.repo, 6).Labels; !slices.Equal(got, want) {
		t.Errorf("labels of #6 = %v, want %v", got, want)
	}
	if n := len(sc.fake.Comments(sc.repo, 6)); n != 0 {
		t.Errorf("%d comments on #6, want none on the success path", n)
	}
	messages := sc.webhook.messagesSent()
	if len(messages) != 1 {
		t.Fatalf("%d notifications, want 1: %v", len(messages), messages)
	}
	for _, want := range []string{"R2", "needs a review", "issue #6", "/issues/6"} {
		if !strings.Contains(messages[0], want) {
			t.Errorf("the notification has no %q:\n%s", want, messages[0])
		}
	}
	// One poll, the read of the login of the Owner before the start, and
	// one read again after the run: R2 judges on the facts of that moment.
	if n := sc.fake.CountRequests(http.MethodPost, "/graphql"); n != 3 {
		t.Errorf("%d GraphQL requests, want 3", n)
	}
	if !strings.Contains(sc.logs.String(), `"msg":"R2: the split waits for the Owner"`) {
		t.Error("the log does not say that the split waits for the Owner")
	}
}

func TestR2_NoSubIssueStopsForTheOwner(t *testing.T) {
	sc := newPlanScene(t)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Title: subIssueTitle})
	sc.pollAndWait(t, sc.service())

	reason := "this requirement issue has no sub-issue"
	assertStoppedForTheOwner(t, sc, []string{"## Stopped for the Owner", "Row: R2", reason, "Retried: no"}, []string{reason})
}

func TestR2_ARiskLabelMissingOrTwiceStopsForTheOwner(t *testing.T) {
	tests := []struct {
		name   string
		labels []string
		reason string
	}{
		{"no risk label", nil, "the sub-issue #10 has no risk label"},
		{"two risk labels", []string{"risk/low", "risk/high"}, "the sub-issue #10 has more than one risk label"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := newPlanScene(t)
			sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Labels: tt.labels})
			sc.pollAndWait(t, sc.service())

			assertStoppedForTheOwner(t, sc, []string{"Row: R2", tt.reason}, []string{tt.reason})
		})
	}
}

// A blocked result posts the blocked_reason of the Planner and is not run
// again.
func TestR2_BlockedStopsForTheOwnerWithoutARetry(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "planner-blocked.jsonl"})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 6, Labels: []string{githubtest.RequirementLabel, "cumin/status/ready"}})
	sc.pollAndWait(t, sc.service())

	const question = "## Decision needed: which sign-in method does the login screen use?"
	assertStoppedForTheOwner(t, sc, []string{question}, []string{question})
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1", n)
	}
}

// An abnormal end runs the same request once more; after the second one the
// requirement issue stops for the Owner with the kind of each end.
func TestR2_ASecondAbnormalEndStopsForTheOwner(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "planner-invalid-result.jsonl"})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 6, Labels: []string{githubtest.RequirementLabel, "cumin/status/ready"}})
	sc.pollAndWait(t, sc.service())

	if n := sc.agentRuns(t); n != 2 {
		t.Fatalf("%d agent runs, want 2 (the request and one retry)", n)
	}
	assertStoppedForTheOwner(t, sc,
		[]string{"Row: R2", "The Planner run ended abnormally (invalid result)", "Retried: once", "Pull request: None"},
		[]string{"invalid result"})
	if !strings.Contains(sc.logs.String(), `"msg":"R2: the same request runs again in the same work directory"`) {
		t.Error("the log does not say that the request ran again")
	}
}

// A step of the stop that fails does not stop the next one: the Owner still
// gets the label and the notification when the comment was not written.
func TestR2_AFailedCommentStillChangesTheLabelAndNotifies(t *testing.T) {
	sc := newPlanScene(t)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Title: subIssueTitle})
	sc.fake.FailNext(http.MethodPost, "/repos/example-org/example-repo/issues/6/comments", http.StatusInternalServerError)
	sc.pollAndWait(t, sc.service())

	if n := len(sc.fake.Comments(sc.repo, 6)); n != 0 {
		t.Errorf("%d comments on #6, want none", n)
	}
	if got := sc.fake.Issue(sc.repo, 6).Labels; !slices.Contains(got, "cumin/status/awaiting-decision") {
		t.Errorf("labels of #6 = %v, want cumin/status/awaiting-decision", got)
	}
	if n := len(sc.webhook.messagesSent()); n != 1 {
		t.Errorf("%d notifications, want 1", n)
	}
}

// R2 with every sub-issue closed: the Owner resumed a requirement issue after
// a blocked acceptance check, and the Planner created nothing. The
// requirement issue goes back to implementing without a notification, and R4
// asks for the acceptance check at the next poll.
func TestR2_EverySubIssueClosedSendsTheIssueBackToTheAcceptanceCheck(t *testing.T) {
	sc := newPlanScene(t)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Closed: true, ClosedAt: sceneNow.Add(-time.Hour), Labels: []string{"risk/low"}})
	service := sc.service()

	sc.pollAndWait(t, service)
	want := []string{githubtest.RequirementLabel, "cumin/status/implementing"}
	if got := sc.fake.Issue(sc.repo, 6).Labels; !slices.Equal(got, want) {
		t.Fatalf("labels of #6 = %v, want %v", got, want)
	}
	if n := len(sc.webhook.messagesSent()); n != 0 {
		t.Errorf("%d notifications, want none", n)
	}

	// The next poll asks for the acceptance check (R4). The fake Planner
	// leaves no comment, so the acceptance check runs a second time.
	sc.pollAndWait(t, service)
	if n := sc.agentRuns(t); n != 3 {
		t.Errorf("%d agent runs, want 3 (the split and the acceptance check, twice)", n)
	}
	if text := promptOf(t, sc.record(t, "agent.args")); !strings.Contains(text, "Request: acceptance check") {
		t.Errorf("the second request is not an acceptance check:\n%s", text)
	}
}

// keptAfterPlanner runs the Planner of #6 to its result while the fake
// GitHub answers as fail sets it, and returns the service after the run.
// The run holds until the failure is set, so that the failure meets the
// step after the run and no call before it.
func keptAfterPlanner(t *testing.T, sc *scene, fail func()) *workflow.Service {
	t.Helper()
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 6, Labels: []string{githubtest.RequirementLabel, "cumin/status/ready"}})
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

// assertPlannerStepIsKept checks that #6 waits with a kept step: the label
// stays, no comment is written, the Owner is not notified, no second
// Planner started, and the issue counts as in work.
func assertPlannerStepIsKept(t *testing.T, sc *scene, service *workflow.Service) {
	t.Helper()
	want := []string{githubtest.RequirementLabel, workflow.LabelPlanning}
	if got := sc.fake.Issue(sc.repo, 6).Labels; !slices.Equal(got, want) {
		t.Errorf("labels of #6 = %v, want %v while the step is kept", got, want)
	}
	if comments := sc.fake.Comments(sc.repo, 6); len(comments) != 0 {
		t.Errorf("%d comments on #6, want none while the step is kept: %+v", len(comments), comments)
	}
	if messages := sc.webhook.messagesSent(); len(messages) != 0 {
		t.Errorf("notifications = %v, want none while the step is kept", messages)
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1: no Planner starts while the step is kept", n)
	}
	if got := workflow.InProgressIssues(service); !slices.Equal(got, []string{"example-org/example-repo#6"}) {
		t.Errorf("issues in work = %v, want #6 while the step is kept", got)
	}
}

// assertSplitWaitsForTheOwner checks the end of R2 on a pass: the label
// cumin/status/awaiting-plan-review, one notification, no comment, and no
// issue in work.
func assertSplitWaitsForTheOwner(t *testing.T, sc *scene, service *workflow.Service) {
	t.Helper()
	want := []string{githubtest.RequirementLabel, workflow.LabelAwaitingPlanReview}
	if got := sc.fake.Issue(sc.repo, 6).Labels; !slices.Equal(got, want) {
		t.Errorf("labels of #6 = %v, want %v after the kept step ran", got, want)
	}
	if n := len(sc.fake.Comments(sc.repo, 6)); n != 0 {
		t.Errorf("%d comments on #6, want none", n)
	}
	messages := sc.messagesExceptQ4()
	if len(messages) != 1 || !strings.Contains(messages[0], "needs a review") {
		t.Errorf("notifications = %v, want one that says that the split needs a review", messages)
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1", n)
	}
	if got := workflow.InProgressIssues(service); len(got) != 0 {
		t.Errorf("issues in work = %v, want none after the kept step ended", got)
	}
}

// R2 with Core-28 (cumin-core.md): the fake GitHub fails every try of the
// read after done. cumin keeps the check of the split: the label stays, and
// no comment is written. A poll before the delay does not run the step, and
// starts no Planner. The poll after the delay runs it, and the requirement
// issue moves as if the step had not failed.
func TestR2_TheCheckOfTheSplitRunsAgainAtALaterPollAfterATemporaryFailure(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "planner-done.jsonl", holds: true})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Labels: []string{"risk/low"}})
	service := keptAfterPlanner(t, sc, func() {
		sc.fake.FailTimes(http.MethodPost, "/graphql", 0, everyTry, http.StatusBadGateway)
	})

	assertPlannerStepIsKept(t, sc, service)
	logs := sc.logs.String()
	if n := strings.Count(logs, `"msg":"kept the check of the split after a temporary failure: a later poll runs it again"`); n != 1 {
		t.Errorf("%d log lines for the kept step, want 1:\n%s", n, logs)
	}

	// The fake answers again, and the delay is not over: the step waits.
	if err := pollAtMinute(sc, service, 4); err != nil {
		t.Fatalf("Poll at minute 4: %v", err)
	}
	assertPlannerStepIsKept(t, sc, service)

	if err := pollAtMinute(sc, service, 5); err != nil {
		t.Fatalf("Poll at minute 5: %v", err)
	}
	assertSplitWaitsForTheOwner(t, sc, service)
}

// A failed label change keeps the check of the split. The later run changes
// the label once and notifies once.
func TestR2_AFailedLabelChangeKeepsTheCheckOfTheSplit(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "planner-done.jsonl", holds: true})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Labels: []string{"risk/low"}})
	service := keptAfterPlanner(t, sc, func() {
		sc.fake.CloseTimes(http.MethodPut, putRequirementLabelsPath, 1)
	})
	assertPlannerStepIsKept(t, sc, service)
	writes := sc.fake.CountRequests(http.MethodPut, putRequirementLabelsPath)

	if err := pollAtMinute(sc, service, 5); err != nil {
		t.Fatalf("Poll at minute 5: %v", err)
	}
	if err := pollAtMinute(sc, service, 10); err != nil {
		t.Fatalf("Poll at minute 10: %v", err)
	}

	assertSplitWaitsForTheOwner(t, sc, service)
	if n := sc.fake.CountRequests(http.MethodPut, putRequirementLabelsPath); n != writes+1 {
		t.Errorf("%d label changes of #6 by the kept step, want 1", n-writes)
	}
}

// The label change of the check of the split reached GitHub, and its answer
// was lost. The kept step reads the requirement issue first and sees the
// new label: it does not change the label again, and it sends the
// notification that the first try did not send.
func TestR2_ALabelChangeThatAlreadyHappenedIsNotMadeTwice(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "planner-done.jsonl", holds: true})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Labels: []string{"risk/low"}})
	service := keptAfterPlanner(t, sc, func() {
		sc.fake.CloseTimes(http.MethodPut, putRequirementLabelsPath, 1)
	})
	assertPlannerStepIsKept(t, sc, service)
	if err := sc.fake.SetLabels(sc.repo, 6, []string{githubtest.RequirementLabel, workflow.LabelAwaitingPlanReview}); err != nil {
		t.Fatal(err)
	}
	writes := sc.fake.CountRequests(http.MethodPut, putRequirementLabelsPath)

	if err := pollAtMinute(sc, service, 5); err != nil {
		t.Fatalf("Poll at minute 5: %v", err)
	}

	assertSplitWaitsForTheOwner(t, sc, service)
	if n := sc.fake.CountRequests(http.MethodPut, putRequirementLabelsPath); n != writes {
		t.Errorf("%d label changes of #6 by the kept step, want none", n-writes)
	}
}

// A failure that is not temporary is not kept: a kept check of the split
// that runs again and finds a sub-issue without a risk label stops the
// requirement issue for the Owner (R2).
func TestR2_AKeptCheckOfTheSplitThatFailsStillStopsForTheOwner(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "planner-done.jsonl", holds: true})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle})
	service := keptAfterPlanner(t, sc, func() {
		sc.fake.FailTimes(http.MethodPost, "/graphql", 0, everyTry, http.StatusBadGateway)
	})
	assertPlannerStepIsKept(t, sc, service)

	if err := pollAtMinute(sc, service, 5); err != nil {
		t.Fatalf("Poll at minute 5: %v", err)
	}

	reason := "the sub-issue #10 has no risk label"
	assertStoppedForTheOwnerAfterAPoll(t, sc, []string{"Row: R2", reason}, []string{reason})
	if got := workflow.InProgressIssues(service); len(got) != 0 {
		t.Errorf("issues in work = %v, want none after the issue stopped", got)
	}
}

// R2 with Core-28 (cumin-core.md): after blocked, the fake GitHub fails
// every try of the read before the stop. cumin keeps the stop: no comment
// is written. The poll after the delay stops the requirement issue for the
// Owner with one comment, and no Planner runs again.
func TestR2_TheStopAfterBlockedRunsAgainAtALaterPollAfterATemporaryFailure(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "planner-blocked.jsonl", holds: true})
	// No status label on the sub-issue, so that I1 does not start it.
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Labels: []string{"risk/low"}})
	service := keptAfterPlanner(t, sc, func() {
		sc.fake.FailTimes(http.MethodPost, "/graphql", 0, everyTry, http.StatusBadGateway)
	})
	assertPlannerStepIsKept(t, sc, service)
	if !strings.Contains(sc.logs.String(), `"msg":"kept the stop after blocked after a temporary failure: a later poll runs it again"`) {
		t.Errorf("the log does not say that the stop after blocked is kept:\n%s", sc.logs.String())
	}

	if err := pollAtMinute(sc, service, 4); err != nil {
		t.Fatalf("Poll at minute 4: %v", err)
	}
	assertPlannerStepIsKept(t, sc, service)

	if err := pollAtMinute(sc, service, 5); err != nil {
		t.Fatalf("Poll at minute 5: %v", err)
	}
	if err := pollAtMinute(sc, service, 10); err != nil {
		t.Fatalf("Poll at minute 10: %v", err)
	}

	const question = "## Decision needed: which sign-in method does the login screen use?"
	assertStoppedForTheOwnerAfterAPoll(t, sc, []string{question}, []string{question})
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1", n)
	}
	if got := workflow.InProgressIssues(service); len(got) != 0 {
		t.Errorf("issues in work = %v, want none after the issue stopped", got)
	}
}

// The label change to cumin/status/implementing (every sub-issue is closed)
// reached GitHub, and its answer was lost. The kept step sees the new
// label: it changes no label and sends no notification, and the requirement
// issue leaves the set of issues in work.
func TestR2_ALostLabelChangeToImplementingIsNotMadeTwiceAndNotifiesNobody(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "planner-done.jsonl", holds: true})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Closed: true, ClosedAt: sceneNow.Add(-time.Hour), Labels: []string{"risk/low"}})
	service := keptAfterPlanner(t, sc, func() {
		sc.fake.CloseTimes(http.MethodPut, putRequirementLabelsPath, 1)
	})
	assertPlannerStepIsKept(t, sc, service)
	if err := sc.fake.SetLabels(sc.repo, 6, []string{githubtest.RequirementLabel, workflow.LabelImplementing}); err != nil {
		t.Fatal(err)
	}
	writes := sc.fake.CountRequests(http.MethodPut, putRequirementLabelsPath)

	// The poll runs the kept step first. Then it asks for the acceptance
	// check (R4), which moves the issue to cumin/status/accepting; that run
	// holds until the release.
	sc.clock.Set(sceneNow.Add(5 * time.Minute))
	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("Poll at minute 5: %v", err)
	}
	waitForAgentRun(t, sc)

	if !strings.Contains(sc.logs.String(), "while the check of the split was kept; nothing changes") {
		t.Errorf("the log does not say that the kept step changed nothing:\n%s", sc.logs.String())
	}
	if n := sc.fake.CountRequests(http.MethodPut, putRequirementLabelsPath); n != writes+1 {
		t.Errorf("%d label changes of #6, want only the move to accepting", n-writes)
	}
	if messages := sc.webhook.messagesSent(); len(messages) != 0 {
		t.Errorf("notifications = %v, want none", messages)
	}
	if n := len(sc.fake.Comments(sc.repo, 6)); n != 0 {
		t.Errorf("%d comments on #6, want none", n)
	}

	// The Planner writes its comment, and the acceptance check ends.
	acceptanceComment(sc, sceneNow, plannerLogin)
	sc.release(t)
	service.Wait()
	if got := workflow.InProgressIssues(service); len(got) != 0 {
		t.Errorf("issues in work = %v, want none after the kept step ended", got)
	}
}

// Another hand set cumin/status/awaiting-plan-review while the check of
// the split was kept after a failed read. cumin sent no label change, so it
// does not trust that label: it leaves the issue and notifies nobody.
func TestR2_AKeptCheckLeavesALabelThatAnotherHandSet(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "planner-done.jsonl", holds: true})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Labels: []string{"risk/low"}})
	service := keptAfterPlanner(t, sc, func() {
		sc.fake.FailTimes(http.MethodPost, "/graphql", 0, everyTry, http.StatusBadGateway)
	})
	assertPlannerStepIsKept(t, sc, service)
	labels := []string{githubtest.RequirementLabel, workflow.LabelAwaitingPlanReview}
	if err := sc.fake.SetLabels(sc.repo, 6, labels); err != nil {
		t.Fatal(err)
	}
	writes := sc.fake.CountRequests(http.MethodPut, putRequirementLabelsPath)

	if err := pollAtMinute(sc, service, 5); err != nil {
		t.Fatalf("Poll at minute 5: %v", err)
	}

	if got := sc.fake.Issue(sc.repo, 6).Labels; !slices.Equal(got, labels) {
		t.Errorf("labels of #6 = %v, want %v", got, labels)
	}
	if n := sc.fake.CountRequests(http.MethodPut, putRequirementLabelsPath); n != writes {
		t.Errorf("%d label changes of #6 by the kept step, want none", n-writes)
	}
	if messages := sc.messagesExceptQ4(); len(messages) != 0 {
		t.Errorf("notifications = %v, want none", messages)
	}
	if got := workflow.InProgressIssues(service); len(got) != 0 {
		t.Errorf("issues in work = %v, want none after the kept step ended", got)
	}
}

// The kept stop after blocked wrote nothing before it was kept. When
// another hand changed the label in between, the kept stop still posts the
// decision request of the Planner once and stops the issue for the Owner.
func TestR2_TheKeptStopAfterBlockedPostsTheDecisionRequestWhateverTheLabelIs(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "planner-blocked.jsonl", holds: true})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Labels: []string{"risk/low"}})
	service := keptAfterPlanner(t, sc, func() {
		sc.fake.FailTimes(http.MethodPost, "/graphql", 0, everyTry, http.StatusBadGateway)
	})
	assertPlannerStepIsKept(t, sc, service)
	if err := sc.fake.SetLabels(sc.repo, 6, []string{githubtest.RequirementLabel, workflow.LabelAwaitingPlanReview}); err != nil {
		t.Fatal(err)
	}

	if err := pollAtMinute(sc, service, 5); err != nil {
		t.Fatalf("Poll at minute 5: %v", err)
	}

	const question = "## Decision needed: which sign-in method does the login screen use?"
	assertStoppedForTheOwnerAfterAPoll(t, sc, []string{question}, []string{question})
	if got := workflow.InProgressIssues(service); len(got) != 0 {
		t.Errorf("issues in work = %v, want none after the issue stopped", got)
	}
}
