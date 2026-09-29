package workflow_test

import (
	"context"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
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
	wantDir := filepath.Join(sc.workRoot, "example-org", "example-repo", "6-planner")
	if got := strings.TrimSpace(sc.record(t, "agent.cwd")); got != realPath(t, wantDir) {
		t.Errorf("the CLI ran in %q, want %q", got, realPath(t, wantDir))
	}
	if got := branchOf(t, wantDir); got != "HEAD" {
		t.Errorf("the work directory is on the branch %q, want a detached HEAD", got)
	}
	if got := git(t, wantDir, "rev-parse", "HEAD"); got != sc.remoteHead {
		t.Errorf("the work directory is at %s, want the head of main %s", got, sc.remoteHead)
	}
	if branches := git(t, wantDir, "branch", "--list", "cumin/*"); branches != "" {
		t.Errorf("a branch was made for the Planner: %q", branches)
	}

	args := sc.record(t, "agent.args")
	text := promptOf(t, args)
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
		`"msg":"R1: requested the split"`, `"role":"planner"`,
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
// cumin/status/awaiting-owner-decision, and one notification that holds
// each of message.
func assertStoppedForTheOwner(t *testing.T, sc *scene, body, message []string) {
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
	want := []string{githubtest.RequirementLabel, "cumin/status/awaiting-owner-decision"}
	if got := sc.fake.Issue(sc.repo, 6).Labels; !slices.Equal(got, want) {
		t.Errorf("labels of #6 = %v, want %v", got, want)
	}
	messages := sc.webhook.messagesSent()
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
// cumin/status/awaiting-owner-review, and the Owner is notified once.
func TestR2_ASplitWithOneRiskLabelEachGoesToTheOwner(t *testing.T) {
	sc := newPlanScene(t)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 11, Parent: 6, Title: "Second", Labels: []string{"risk/high"}})
	sc.pollAndWait(t, sc.service())

	want := []string{githubtest.RequirementLabel, "cumin/status/awaiting-owner-review"}
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
	// One poll, and one read again after the run: R2 judges on the facts
	// of that moment.
	if n := sc.fake.CountRequests(http.MethodPost, "/graphql"); n != 2 {
		t.Errorf("%d snapshot reads, want 2", n)
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
	if got := sc.fake.Issue(sc.repo, 6).Labels; !slices.Contains(got, "cumin/status/awaiting-owner-decision") {
		t.Errorf("labels of #6 = %v, want cumin/status/awaiting-owner-decision", got)
	}
	if n := len(sc.webhook.messagesSent()); n != 1 {
		t.Errorf("%d notifications, want 1", n)
	}
}
