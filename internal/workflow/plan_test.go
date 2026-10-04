package workflow_test

import (
	"context"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/state"
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
	// after the run one read of the issue, of the account of its status
	// label, of its label times, and of its comments: R2 judges on the
	// facts of that moment.
	if n := sc.fake.CountRequests(http.MethodPost, "/graphql"); n != 6 {
		t.Errorf("%d GraphQL requests, want 6", n)
	}
	if !strings.Contains(sc.logs.String(), `"msg":"R2: the split waits for the Owner"`) {
		t.Error("the log does not say that the split waits for the Owner")
	}
}

// A split that fails the check is requested once more, in the same work
// directory. The second failure stops the requirement issue for the Owner
// with the sentence of the failed check.
func TestR2_NoSubIssueRequestsTheSplitAgainAndThenStopsForTheOwner(t *testing.T) {
	sc := newPlanScene(t)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Title: subIssueTitle})
	sc.pollAndWait(t, sc.service())

	if n := sc.agentRuns(t); n != 2 {
		t.Fatalf("%d agent runs, want 2 (the request and the second request)", n)
	}
	reason := "this requirement issue has no sub-issue"
	assertStoppedForTheOwner(t, sc, []string{"## Stopped for the Owner", "Row: R2", reason, "Retried: once"}, []string{reason})
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

// An abnormal end is decided from the facts on GitHub, as every other end:
// with no sub-issue, the same request runs once more; after the second run
// the requirement issue stops for the Owner with what the split lacks.
func TestR2_ASecondAbnormalEndStopsForTheOwner(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "planner-invalid-result.jsonl"})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 6, Labels: []string{githubtest.RequirementLabel, "cumin/status/ready"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Title: subIssueTitle})
	sc.pollAndWait(t, sc.service())

	if n := sc.agentRuns(t); n != 2 {
		t.Fatalf("%d agent runs, want 2 (the request and one retry)", n)
	}
	assertStoppedForTheOwner(t, sc,
		[]string{"Row: R2", "this requirement issue has no sub-issue", "Retried: once", "Pull request: None"},
		[]string{"this requirement issue has no sub-issue"})
	if !strings.Contains(sc.logs.String(), `"msg":"R2: the split does not pass the check; the same request runs again in the same work directory"`) {
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
// a blocked acceptance check, and the Planner created nothing. At the end of
// the split, the requirement issue goes to cumin/status/accepting without a
// notification, and the Planner is asked for the acceptance check.
func TestR2_EverySubIssueClosedGoesToTheAcceptanceCheckWithNoNotification(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "planner-done.jsonl", holds: true})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 6, Labels: []string{githubtest.RequirementLabel, "cumin/status/ready"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Closed: true, ClosedAt: sceneNow.Add(-time.Hour), Labels: []string{"risk/low"}})
	service := sc.service()
	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	waitForAgentRun(t, sc)
	sc.release(t)

	// The split ended, and the acceptance check runs.
	waitForAgentRun(t, sc)
	assertAcceptingWithNoNotification(t, sc)

	// The Planner writes its comment, and the acceptance check ends.
	acceptanceComment(sc, sceneNow, plannerLogin)
	sc.release(t)
	service.Wait()
	if n := sc.agentRuns(t); n != 2 {
		t.Errorf("%d agent runs, want 2 (the split and the acceptance check)", n)
	}
	want := []string{githubtest.RequirementLabel, "cumin/status/awaiting-acceptance"}
	if got := requirementLabels(t, sc); !slices.Equal(got, want) {
		t.Errorf("labels of #6 = %v, want %v", got, want)
	}
}

// assertAcceptingWithNoNotification checks that #6 is in
// cumin/status/accepting, that the newest request is an acceptance check,
// and that the Owner got no notification and no comment.
func assertAcceptingWithNoNotification(t *testing.T, sc *scene) {
	t.Helper()
	want := []string{githubtest.RequirementLabel, "cumin/status/accepting"}
	if got := requirementLabels(t, sc); !slices.Equal(got, want) {
		t.Errorf("labels of #6 = %v, want %v", got, want)
	}
	if text := promptOf(t, sc.record(t, "agent.args")); !strings.Contains(text, "Request: acceptance check") {
		t.Errorf("the newest request is not an acceptance check:\n%s", text)
	}
	if messages := sc.webhook.messagesSent(); len(messages) != 0 {
		t.Errorf("notifications = %v, want none", messages)
	}
	if n := len(sc.fake.Comments(sc.repo, 6)); n != 0 {
		t.Errorf("%d comments on #6, want none", n)
	}
}

// planningScene is the scene of a restart during the split: the requirement
// issue #6 is in cumin/status/planning since labeledAt, and no Planner
// runs. The sub-issues are the ones that the test adds.
func planningScene(t *testing.T, options ...cliOptions) (*scene, time.Time) {
	t.Helper()
	sc := newScene(t, options...)
	labeledAt := sceneNow.Add(-time.Hour)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 6, Labels: []string{githubtest.RequirementLabel, "cumin/status/planning"},
		LabelEvents: []githubtest.LabelEvent{{Label: "cumin/status/planning", At: labeledAt, Actor: cuminSlug, ActorType: "Bot"}}})
	// The sub-issue of newScene belongs to no requirement issue here.
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Title: subIssueTitle})
	return sc, labeledAt
}

// A restart in cumin/status/planning with a split that passes the check:
// the issue moves to cumin/status/awaiting-plan-review with one
// notification and no request.
func TestPlanning_ARestartWithAVerifiedSplitAsksTheOwnerToReviewThePlan(t *testing.T) {
	sc, _ := planningScene(t, cliOptions{fixture: "planner-done.jsonl"})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Labels: []string{"risk/low"}})

	service := sc.service()
	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none", n)
	}
	want := []string{githubtest.RequirementLabel, "cumin/status/awaiting-plan-review"}
	if got := requirementLabels(t, sc); !slices.Equal(got, want) {
		t.Errorf("labels of #6 = %v, want %v", got, want)
	}
	if n := len(sc.fake.Comments(sc.repo, 6)); n != 0 {
		t.Errorf("%d comments on #6, want none", n)
	}
	if messages := sc.messagesExceptQ4(); len(messages) != 1 || !strings.Contains(messages[0], "needs a review") {
		t.Errorf("notifications = %v, want one that says that the split needs a review", messages)
	}
}

// A restart in cumin/status/planning with no sub-issue: the new cumin
// requests the split once more. With still no sub-issue, the issue goes to
// cumin/status/awaiting-decision with the reason, and a later restart
// requests nothing.
func TestPlanning_ARestartWithNoSubIssueRequestsTheSplitOnceMoreAndThenStopsForTheOwner(t *testing.T) {
	sc, _ := planningScene(t, cliOptions{fixture: "planner-done.jsonl"})
	path := filepath.Join(t.TempDir(), "state.json")

	sc.pollAndWait(t, sc.serviceWithState(path))

	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs, want 1 (the second request)", n)
	}
	if text := promptOf(t, sc.record(t, "agent.args")); !strings.Contains(text, "Request: plan") {
		t.Errorf("the request text is not a plan:\n%s", text)
	}
	reason := "this requirement issue has no sub-issue"
	assertStoppedForTheOwner(t, sc, []string{"Row: R2", reason, "Retried: once"}, []string{reason})

	restarted := sc.serviceWithState(path)
	sc.pollAndWait(t, restarted)
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs after one more restart, want still 1", n)
	}
	assertStoppedForTheOwnerAfterAPoll(t, sc, []string{"Row: R2", reason}, []string{reason})
	if got := restarted.State.Issue("example-org/example-repo", 6); got != (state.Issue{}) {
		t.Errorf("the state of #6 = %+v, want none after the stop", got)
	}
}

// A restart after the second request: the state file says that the split
// was requested again, and no sub-issue exists. The issue goes to
// cumin/status/awaiting-decision with no request.
func TestPlanning_ARestartAfterTheSecondRequestStopsWithNoRequest(t *testing.T) {
	sc, _ := planningScene(t, cliOptions{fixture: "planner-done.jsonl"})
	service := sc.serviceWithState(filepath.Join(t.TempDir(), "state.json"))
	if err := service.State.Set("example-org/example-repo", 6, state.Issue{SplitRequests: 1}); err != nil {
		t.Fatal(err)
	}

	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none", n)
	}
	reason := "this requirement issue has no sub-issue"
	assertStoppedForTheOwnerAfterAPoll(t, sc, []string{"Row: R2", reason, "Retried: once"}, []string{reason})
}

// A question of the Planner, written after the issue got
// cumin/status/planning: the issue goes to cumin/status/awaiting-decision
// with one notification. cumin requests nothing and writes no comment of
// its own.
func TestPlanning_AQuestionOfThePlannerStopsForTheOwnerWithNoRequest(t *testing.T) {
	sc, labeledAt := planningScene(t, cliOptions{fixture: "planner-done.jsonl"})
	sc.fake.AddComment(sc.repo, 6, githubtest.Comment{
		Body: workflow.DecisionRequestHeading + ": which sign-in method does the login screen use?", Author: plannerLogin, AuthorIsBot: true, At: labeledAt.Add(30 * time.Minute),
	})

	service := sc.service()
	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none", n)
	}
	want := []string{githubtest.RequirementLabel, "cumin/status/awaiting-decision"}
	if got := requirementLabels(t, sc); !slices.Equal(got, want) {
		t.Errorf("labels of #6 = %v, want %v", got, want)
	}
	if n := len(sc.fake.Comments(sc.repo, 6)); n != 1 {
		t.Errorf("%d comments on #6, want only the question of the Planner", n)
	}
	if messages := sc.messagesExceptQ4(); len(messages) != 1 || !strings.Contains(messages[0], "asked a question during the split") {
		t.Errorf("notifications = %v, want one about the question", messages)
	}
}

// A restart in cumin/status/planning with every sub-issue closed: the issue
// goes to cumin/status/accepting, the Planner is asked for the acceptance
// check once, and the Owner gets no notification.
func TestPlanning_EverySubIssueClosedGoesToAcceptingWithNoNotification(t *testing.T) {
	sc, _ := planningScene(t, cliOptions{fixture: "planner-done.jsonl", holds: true})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Closed: true, ClosedAt: sceneNow.Add(-2 * time.Hour), Labels: []string{"risk/low"}})
	service := sc.service()
	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	waitForAgentRun(t, sc)

	assertAcceptingWithNoNotification(t, sc)

	acceptanceComment(sc, sceneNow, plannerLogin)
	sc.release(t)
	service.Wait()
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1 (the acceptance check)", n)
	}
}

// While the Planner runs, a poll changes nothing on the issue: no label, no
// comment, no notification, and no second request.
func TestPlanning_APollChangesNothingWhileThePlannerRuns(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "planner-done.jsonl", holds: true})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 6, Labels: []string{githubtest.RequirementLabel, "cumin/status/ready"}})
	// No sub-issue yet: without the running Planner, the poll would request
	// the split again.
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Title: subIssueTitle})
	service := sc.service()
	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	waitForAgentRun(t, sc)
	writes := sc.fake.CountRequests(http.MethodPut, putRequirementLabelsPath)

	for range 2 {
		if err := service.Poll(context.Background()); err != nil {
			t.Fatalf("Poll while the Planner runs: %v", err)
		}
	}

	want := []string{githubtest.RequirementLabel, workflow.LabelPlanning}
	if got := requirementLabels(t, sc); !slices.Equal(got, want) {
		t.Errorf("labels of #6 = %v, want %v while the Planner runs", got, want)
	}
	if n := sc.fake.CountRequests(http.MethodPut, putRequirementLabelsPath); n != writes {
		t.Errorf("%d label changes of #6 while the Planner runs, want none", n-writes)
	}
	if n := len(sc.fake.Comments(sc.repo, 6)); n != 0 {
		t.Errorf("%d comments on #6 while the Planner runs, want none", n)
	}
	if messages := sc.webhook.messagesSent(); len(messages) != 0 {
		t.Errorf("notifications = %v, want none while the Planner runs", messages)
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1 while the Planner runs", n)
	}

	// The Planner creates the sub-issue and ends.
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Labels: []string{"risk/low"}})
	sc.release(t)
	service.Wait()
	assertSplitWaitsForTheOwner(t, sc, service)
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
		t.Errorf("labels of #6 = %v, want %v", got, want)
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
		t.Errorf("issues in work = %v, want none after the split ended", got)
	}
}

// The fake GitHub fails every try of the read after done. cumin keeps
// nothing: the label stays, no comment is written, and the issue is not in
// work. The next poll decides from the same facts, and the requirement
// issue moves as if the read had not failed. No second Planner starts.
func TestR2_AFailedReadAfterTheRunIsDecidedAtTheNextPoll(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "planner-done.jsonl", holds: true})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Labels: []string{"risk/low"}})
	service := keptAfterPlanner(t, sc, func() {
		sc.fake.FailTimes(http.MethodPost, "/graphql", 0, everyTry, http.StatusBadGateway)
	})
	assertSplitWaitsForThePoll(t, sc, service)

	sc.pollAndWait(t, service)

	assertSplitWaitsForTheOwner(t, sc, service)
}

// A failed label change after done leaves the issue in
// cumin/status/planning with no notification. The next poll changes the
// label once and notifies once.
func TestR2_AFailedLabelChangeAfterTheRunIsDecidedAtTheNextPoll(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "planner-done.jsonl", holds: true})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Labels: []string{"risk/low"}})
	service := keptAfterPlanner(t, sc, func() {
		sc.fake.FailNext(http.MethodPut, putRequirementLabelsPath, http.StatusForbidden)
	})
	assertSplitWaitsForThePoll(t, sc, service)
	writes := sc.fake.CountRequests(http.MethodPut, putRequirementLabelsPath)

	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)

	assertSplitWaitsForTheOwner(t, sc, service)
	if n := sc.fake.CountRequests(http.MethodPut, putRequirementLabelsPath); n != writes+1 {
		t.Errorf("%d label changes of #6 by the polls, want 1", n-writes)
	}
}

// assertSplitWaitsForThePoll checks that #6 waits for the next poll after
// a Planner run whose end was not decided: the label stays, no comment is
// written, the Owner is not notified, and the issue is not in work.
func assertSplitWaitsForThePoll(t *testing.T, sc *scene, service *workflow.Service) {
	t.Helper()
	want := []string{githubtest.RequirementLabel, workflow.LabelPlanning}
	if got := sc.fake.Issue(sc.repo, 6).Labels; !slices.Equal(got, want) {
		t.Errorf("labels of #6 = %v, want %v until the next poll", got, want)
	}
	if n := len(sc.fake.Comments(sc.repo, 6)); n != 0 {
		t.Errorf("%d comments on #6, want none until the next poll", n)
	}
	if messages := sc.webhook.messagesSent(); len(messages) != 0 {
		t.Errorf("notifications = %v, want none until the next poll", messages)
	}
	if got := workflow.InProgressIssues(service); len(got) != 0 {
		t.Errorf("issues in work = %v, want none: cumin keeps no step", got)
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

// The Planner run ends between the two reads of a poll: after the poll read
// the snapshot, which still shows cumin/status/planning. The run decides its
// own end, changes the label, and notifies the Owner. The poll read the set
// of running agents before the snapshot, so the run still counts as
// running: the poll decides nothing from the old label. The label changes
// once, the Owner gets one notification, and no second request starts.
func TestPlanning_ARunThatEndsDuringThePollIsNotDecidedTwice(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "planner-done.jsonl", holds: true})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 6, Labels: []string{githubtest.RequirementLabel, "cumin/status/ready"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Labels: []string{"risk/low"}})
	service := sc.service()
	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	waitForAgentRun(t, sc)
	writes := sc.fake.CountRequests(http.MethodPut, putRequirementLabelsPath)

	// The answer of the snapshot query is built, and then the run ends.
	sc.fake.BeforeNextAnswer(http.MethodPost, "/graphql", func() {
		sc.release(t)
		service.Wait()
	})
	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("Poll during the end of the run: %v", err)
	}
	service.Wait()

	assertSplitWaitsForTheOwner(t, sc, service)
	if n := sc.fake.CountRequests(http.MethodPut, putRequirementLabelsPath); n != writes+1 {
		t.Errorf("%d label changes of #6 after the run ended, want 1", n-writes)
	}

	// The next poll sees the new label and has nothing to decide.
	sc.pollAndWait(t, service)
	assertSplitWaitsForTheOwner(t, sc, service)
}

// The start of the second request fails: the work directory cannot be
// prepared. No Planner ran, so the state file does not count the request,
// and the issue is not stopped. A later poll, whose start works, sends the
// one second request; only after it does the issue stop for the Owner.
func TestPlanning_AFailedStartDoesNotUseUpTheSecondRequest(t *testing.T) {
	sc, _ := planningScene(t, cliOptions{fixture: "planner-done.jsonl"})
	path := filepath.Join(t.TempDir(), "state.json")
	broken := sc.serviceWithState(path)
	broken.Targets[0].RemoteURL = filepath.Join(t.TempDir(), "no-such-repository.git")

	sc.pollAndWait(t, broken)
	sc.pollAndWait(t, broken)

	if n := sc.agentRuns(t); n != 0 {
		t.Fatalf("%d agent runs, want none: the start failed", n)
	}
	if got := broken.State.Issue("example-org/example-repo", 6).SplitRequests; got != 0 {
		t.Errorf("the count of the second request = %d, want 0 after a failed start", got)
	}
	want := []string{githubtest.RequirementLabel, workflow.LabelPlanning}
	if got := requirementLabels(t, sc); !slices.Equal(got, want) {
		t.Errorf("labels of #6 = %v, want %v after a failed start", got, want)
	}
	if n := len(sc.fake.Comments(sc.repo, 6)); n != 0 {
		t.Errorf("%d comments on #6 after a failed start, want none", n)
	}

	sc.pollAndWait(t, sc.serviceWithState(path))

	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1 (the second request)", n)
	}
	reason := "this requirement issue has no sub-issue"
	assertStoppedForTheOwnerAfterAPoll(t, sc, []string{"Row: R2", reason, "Retried: once"}, []string{reason})
}

// A blocked result whose stop wrote the comment and then failed to change
// the label: cumin-core wrote the decision request, and the issue stays in
// cumin/status/planning. The next poll reads that comment as the question
// of the Planner: the issue goes to cumin/status/awaiting-decision with no
// second request and no second comment.
func TestPlanning_ABlockedResultWithAFailedLabelChangeStopsAtTheNextPollWithNoRequest(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "planner-blocked.jsonl", holds: true})
	// cumin-core posts the blocked_reason, so the comment is of its App.
	sc.fake.SetCommentAuthor(cuminSlug)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Labels: []string{"risk/low"}})
	service := keptAfterPlanner(t, sc, func() {
		sc.fake.FailNext(http.MethodPut, putRequirementLabelsPath, http.StatusForbidden)
	})
	want := []string{githubtest.RequirementLabel, workflow.LabelPlanning}
	if got := requirementLabels(t, sc); !slices.Equal(got, want) {
		t.Fatalf("labels of #6 = %v, want %v after the failed label change", got, want)
	}
	if n := len(sc.fake.Comments(sc.repo, 6)); n != 1 {
		t.Fatalf("%d comments on #6 after the stop, want the decision request", n)
	}

	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)

	want = []string{githubtest.RequirementLabel, "cumin/status/awaiting-decision"}
	if got := requirementLabels(t, sc); !slices.Equal(got, want) {
		t.Errorf("labels of #6 = %v, want %v", got, want)
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1: no second request", n)
	}
	if n := len(sc.fake.Comments(sc.repo, 6)); n != 1 {
		t.Errorf("%d comments on #6, want still 1", n)
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
