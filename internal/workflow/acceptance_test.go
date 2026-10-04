package workflow_test

import (
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

// plannerLogin is the author of a comment of the Planner App. The fake knows
// one App, so it is the bot of that App.
const plannerLogin = implementerSlug

// newAcceptanceScene is the scene of R4 and R7: the requirement issue #6 is
// in cumin/status/implementing and its only sub-issue #10 closed an hour
// ago. The options say how the fake CLI answers as the Planner.
func newAcceptanceScene(t *testing.T, options cliOptions) (*scene, time.Time) {
	t.Helper()
	sc := newScene(t, options)
	closedAt := sceneNow.Add(-time.Hour)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Closed: true, ClosedAt: closedAt, Labels: []string{"risk/low"}})
	return sc, closedAt
}

// acceptanceComment adds the acceptance check comment of the Planner App,
// written at the time.
func acceptanceComment(sc *scene, at time.Time, author string) {
	sc.fake.AddComment(sc.repo, 6, githubtest.Comment{
		Body: "## Acceptance check\n\n| Rule | Result | Evidence |\n|---|---|---|\n| One | Fail | test |\n", Author: author, AuthorIsBot: true, At: at,
	})
}

// acceptingScene is the scene of a restart during the acceptance check: the
// requirement issue #6 is in cumin/status/accepting, its only sub-issue #10
// is closed, and no Planner runs.
func acceptingScene(t *testing.T, options ...cliOptions) (*scene, time.Time) {
	t.Helper()
	sc := newScene(t, options...)
	closedAt := sceneNow.Add(-time.Hour)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 6, Labels: []string{githubtest.RequirementLabel, "cumin/status/accepting"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Closed: true, ClosedAt: closedAt, Labels: []string{"risk/low"}})
	return sc, closedAt
}

// serviceWithState is a new Service that reads the state file at the path,
// as cumin does after a restart.
func (sc *scene) serviceWithState(path string) *workflow.Service {
	service := sc.service()
	service.State = state.Open(path, nil)
	return service
}

// Core-7 (cumin-core.md): when every sub-issue closes, the requirement
// issue gets cumin/status/accepting without a new ready of the Owner, and
// the Planner is asked for the acceptance check once. While the Planner
// runs, a poll changes nothing on the issue. The end of the run finds the
// comment and moves the issue to cumin/status/awaiting-acceptance with one
// notification, although the table holds a Fail. A restart asks nothing
// again.
func TestCore07_TheAcceptanceCheckIsRequestedOnceAndHandedToTheOwner(t *testing.T) {
	sc, closedAt := newAcceptanceScene(t, cliOptions{fixture: "planner-done.jsonl", holds: true})
	service := sc.service()

	if err := service.Poll(t.Context()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	waitForAgentRun(t, sc)
	accepting := []string{githubtest.RequirementLabel, "cumin/status/accepting"}
	if got := requirementLabels(t, sc); !slices.Equal(got, accepting) {
		t.Errorf("labels of #6 = %v, want %v while the check runs", got, accepting)
	}
	text := promptOf(t, sc.record(t, "agent.args"))
	if !strings.Contains(text, "Request: acceptance check") || !strings.Contains(text, "#6") {
		t.Errorf("the request text is not an acceptance check of #6:\n%s", text)
	}
	requireIssueOfTheRun(t, text, 6, "requirement issue")

	// A poll while the Planner runs changes nothing on the issue.
	changes := sc.labelChanges()
	if err := service.Poll(t.Context()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if got := requirementLabels(t, sc); !slices.Equal(got, accepting) {
		t.Errorf("labels of #6 = %v, want %v after a poll during the run", got, accepting)
	}
	if n := sc.labelChanges(); n != changes {
		t.Errorf("%d label changes after a poll during the run, want %d", n, changes)
	}
	if n := len(sc.fake.Comments(sc.repo, 6)); n != 0 {
		t.Errorf("%d comments on #6 after a poll during the run, want none", n)
	}

	// The Planner writes the comment during its run, and the run ends.
	acceptanceComment(sc, closedAt.Add(30*time.Minute), plannerLogin)
	sc.release(t)
	service.Wait()

	// A restart polls again.
	restarted := sc.service()
	sc.pollAndWait(t, restarted)
	sc.pollAndWait(t, restarted)

	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want still 1", n)
	}
	want := []string{githubtest.RequirementLabel, "cumin/status/awaiting-acceptance"}
	if got := requirementLabels(t, sc); !slices.Equal(got, want) {
		t.Errorf("labels of #6 = %v, want %v", got, want)
	}
	messages := sc.messagesExceptQ4()
	if len(messages) != 1 {
		t.Fatalf("%d notifications, want 1: %v", len(messages), messages)
	}
	for _, want := range []string{"R7", "can be accepted", "issue #6"} {
		if !strings.Contains(messages[0], want) {
			t.Errorf("the notification has no %q:\n%s", want, messages[0])
		}
	}
}

// A restart during the acceptance check: the issue is in
// cumin/status/accepting, no Planner runs, and no comment exists. The new
// cumin requests the acceptance check once more, in the session that the
// state file holds. With still no comment, the issue goes to
// cumin/status/awaiting-decision with the reason, and a later restart
// requests nothing.
func TestAccepting_ARestartRequestsTheCheckOnceMoreAndThenStopsForTheOwner(t *testing.T) {
	sc, _ := acceptingScene(t, cliOptions{fixture: "planner-done.jsonl"})
	path := filepath.Join(t.TempDir(), "state.json")
	first := sc.serviceWithState(path)
	if err := first.State.Set("example-org/example-repo", 6, state.Issue{SessionID: "planner-session"}); err != nil {
		t.Fatal(err)
	}

	sc.pollAndWait(t, sc.serviceWithState(path))

	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs, want 1 (the second request)", n)
	}
	args := sc.record(t, "agent.args")
	if got := argumentOf(t, args, "--resume"); got != "planner-session" {
		t.Errorf("--resume = %q, want the session of the first request", got)
	}
	if text := promptOf(t, args); !strings.Contains(text, "Request: acceptance check") {
		t.Errorf("the request text is not an acceptance check:\n%s", text)
	}
	assertAcceptanceStopped := func() {
		t.Helper()
		want := []string{githubtest.RequirementLabel, "cumin/status/awaiting-decision"}
		if got := requirementLabels(t, sc); !slices.Equal(got, want) {
			t.Errorf("labels of #6 = %v, want %v", got, want)
		}
		comments := sc.fake.Comments(sc.repo, 6)
		if len(comments) != 1 || !strings.Contains(comments[0].Body, workflow.NoAcceptanceCheckReason) {
			t.Errorf("the comments of #6 = %+v, want one with the reason", comments)
		}
		messages := sc.messagesExceptQ4()
		if len(messages) != 1 || !strings.Contains(messages[0], workflow.NoAcceptanceCheckReason) {
			t.Errorf("notifications = %v, want one with the reason", messages)
		}
	}
	assertAcceptanceStopped()

	sc.pollAndWait(t, sc.serviceWithState(path))
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs after one more restart, want still 1", n)
	}
	assertAcceptanceStopped()
}

// A restart after the second request: the state file says that the
// acceptance check was requested again, and no comment exists. The issue
// goes to cumin/status/awaiting-decision with no request.
func TestAccepting_ARestartAfterTheSecondRequestStopsWithNoRequest(t *testing.T) {
	sc, _ := acceptingScene(t, cliOptions{fixture: "planner-done.jsonl"})
	service := sc.serviceWithState(filepath.Join(t.TempDir(), "state.json"))
	if err := service.State.Set("example-org/example-repo", 6, state.Issue{AcceptanceRequests: 1}); err != nil {
		t.Fatal(err)
	}

	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none", n)
	}
	want := []string{githubtest.RequirementLabel, "cumin/status/awaiting-decision"}
	if got := requirementLabels(t, sc); !slices.Equal(got, want) {
		t.Errorf("labels of #6 = %v, want %v", got, want)
	}
	if got := service.State.Issue("example-org/example-repo", 6); got != (state.Issue{}) {
		t.Errorf("the state of #6 = %+v, want none after the stop", got)
	}
}

// A restart after the Planner wrote the comment: the issue moves to
// cumin/status/awaiting-acceptance with one notification and no request.
func TestAccepting_ARestartAfterTheCommentAsksTheOwnerToAccept(t *testing.T) {
	sc, closedAt := acceptingScene(t, cliOptions{fixture: "planner-done.jsonl"})
	acceptanceComment(sc, closedAt.Add(30*time.Minute), plannerLogin)

	service := sc.service()
	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none", n)
	}
	want := []string{githubtest.RequirementLabel, "cumin/status/awaiting-acceptance"}
	if got := requirementLabels(t, sc); !slices.Equal(got, want) {
		t.Errorf("labels of #6 = %v, want %v", got, want)
	}
	if messages := sc.messagesExceptQ4(); len(messages) != 1 || !strings.Contains(messages[0], "R7") {
		t.Errorf("notifications = %v, want one with R7", messages)
	}
}

// A restart after the Planner wrote a question: the issue goes to
// cumin/status/awaiting-decision with one notification. cumin requests
// nothing and writes no comment of its own.
func TestAccepting_AQuestionOfThePlannerStopsForTheOwnerWithNoRequest(t *testing.T) {
	sc, closedAt := acceptingScene(t, cliOptions{fixture: "planner-done.jsonl"})
	sc.fake.AddComment(sc.repo, 6, githubtest.Comment{
		Body: workflow.DecisionRequestHeading + ": which sign-in method does the login screen use?", Author: plannerLogin, AuthorIsBot: true, At: closedAt.Add(30 * time.Minute),
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
	if messages := sc.messagesExceptQ4(); len(messages) != 1 || !strings.Contains(messages[0], "asked a question") {
		t.Errorf("notifications = %v, want one about the question", messages)
	}
}

// A comment from before the last close belongs to an earlier round, and a
// comment of another author never counts: R4 asks. Neither run leaves a
// comment that counts, so cumin asks once more and then stops for the
// Owner, in one run of cumin.
func TestR4_AnOldCommentOrAnotherAuthorAsksAgain(t *testing.T) {
	tests := []struct {
		name   string
		offset time.Duration
		author string
	}{
		{"written before the last close", -time.Minute, plannerLogin},
		{"written by another author", time.Minute, "octocat"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc, closedAt := newAcceptanceScene(t, cliOptions{fixture: "planner-done.jsonl"})
			acceptanceComment(sc, closedAt.Add(tt.offset), tt.author)
			sc.pollAndWait(t, sc.service())

			if n := sc.agentRuns(t); n != 2 {
				t.Errorf("%d agent runs, want 2 (the request and one more)", n)
			}
			if got := requirementLabels(t, sc); !slices.Contains(got, "cumin/status/awaiting-decision") {
				t.Errorf("labels of #6 = %v, want awaiting-decision", got)
			}
		})
	}
}

// A requirement issue without a sub-issue is not checked.
func TestR4_NoSubIssueNoCheck(t *testing.T) {
	sc := newScene(t)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Title: subIssueTitle})
	sc.pollAndWait(t, sc.service())

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none", n)
	}
}

// A blocked acceptance check stops the requirement issue for the Owner with
// the row R4, and is not run again.
func TestR4_BlockedStopsForTheOwner(t *testing.T) {
	sc, _ := newAcceptanceScene(t, cliOptions{fixture: "planner-blocked.jsonl"})
	sc.pollAndWait(t, sc.service())

	const question = "## Decision needed: which sign-in method does the login screen use?"
	comments := sc.fake.Comments(sc.repo, 6)
	if len(comments) != 1 || comments[0].Body != question {
		t.Fatalf("the comments of #6 = %+v, want one with the blocked reason", comments)
	}
	want := []string{githubtest.RequirementLabel, "cumin/status/awaiting-decision"}
	if got := requirementLabels(t, sc); !slices.Equal(got, want) {
		t.Errorf("labels of #6 = %v, want %v", got, want)
	}
	if messages := sc.webhook.messagesSent(); len(messages) != 1 || !strings.Contains(messages[0], "R4") {
		t.Errorf("notifications = %v, want one with R4", messages)
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1", n)
	}
}

// The acceptance check reads the merged work: the work directory of the
// split, made before the merge, is made again at the head of main.
func TestR4_TheCheckReadsTheMergedWork(t *testing.T) {
	sc, _ := newAcceptanceScene(t, cliOptions{fixture: "planner-done.jsonl"})

	// The split ran at the first commit of main.
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 6, Labels: []string{githubtest.RequirementLabel, "cumin/status/ready"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Labels: []string{"risk/low"}})
	sc.pollAndWait(t, sc.service())
	if got := strings.TrimSpace(sc.record(t, "agent.head")); got != sc.remoteHead {
		t.Fatalf("the split ran at %s, want %s", got, sc.remoteHead)
	}

	// The sub-issue merged a commit and closed: after what the split wrote
	// on GitHub, and before what the acceptance check writes.
	merged := pushCommit(t, sc.remote)
	closedAt := sceneNow.Add(time.Minute)
	sc.clock.Set(closedAt.Add(time.Minute))
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 6, Labels: []string{githubtest.RequirementLabel, "cumin/status/implementing"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Closed: true, ClosedAt: closedAt, Labels: []string{"risk/low"}})
	sc.pollAndWait(t, sc.service())

	if got := strings.TrimSpace(sc.record(t, "agent.head")); got != merged {
		t.Errorf("the acceptance check ran at %s, want the merged head %s", got, merged)
	}
}

// pushCommit adds one commit to main of the bare remote, as a merge does,
// and returns it.
func pushCommit(t *testing.T, bare string) string {
	t.Helper()
	work := filepath.Join(t.TempDir(), "work")
	git(t, filepath.Dir(work), "clone", "--quiet", bare, work)
	if err := os.WriteFile(filepath.Join(work, "merged.txt"), []byte("merged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", "merged.txt")
	git(t, work, "commit", "--quiet", "-m", "merge the sub-issue")
	git(t, work, "push", "--quiet", "origin", "main")
	return git(t, work, "rev-parse", "HEAD")
}
