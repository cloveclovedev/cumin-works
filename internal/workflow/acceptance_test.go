package workflow_test

import (
	"net/http"
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

// newAcceptanceScene is the scene of "request the acceptance check" and "ask
// for the acceptance": the requirement issue #6 is
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

// The test of a top-level requirement in cumin-core.md: when every sub-issue
// closes, the requirement
// issue gets cumin/status/accepting without a new ready of the Maintainer, and
// the Planner is asked for the acceptance check once. While the Planner
// runs, a poll changes nothing on the issue. The end of the run finds the
// comment and moves the issue to cumin/status/awaiting-acceptance with one
// notification, although the table holds a Fail. A restart asks nothing
// again.
func TestTheAcceptanceCheckIsRequestedOnceAndHandedToTheMaintainer(t *testing.T) {
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
	messages := sc.messagesExceptWaiting()
	if len(messages) != 1 {
		t.Fatalf("%d notifications, want 1: %v", len(messages), messages)
	}
	for _, want := range []string{"ask for the acceptance", "can be accepted", "issue #6"} {
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
func TestAccepting_ARestartRequestsTheCheckOnceMoreAndThenStopsForTheMaintainer(t *testing.T) {
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
		messages := sc.messagesExceptWaiting()
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

// The start of the second request fails: the work directory cannot be
// prepared. No Planner ran, so the state file does not count the request,
// and the issue is not stopped. A later poll, whose start works, sends the
// one second request; only after it does the issue stop for the Maintainer.
func TestAccepting_AFailedStartDoesNotUseUpTheSecondRequest(t *testing.T) {
	sc, _ := acceptingScene(t, cliOptions{fixture: "planner-done.jsonl"})
	path := filepath.Join(t.TempDir(), "state.json")
	broken := sc.serviceWithState(path)
	broken.Targets[0].RemoteURL = filepath.Join(t.TempDir(), "no-such-repository.git")

	sc.pollAndWait(t, broken)
	sc.pollAndWait(t, broken)

	if n := sc.agentRuns(t); n != 0 {
		t.Fatalf("%d agent runs, want none: the start failed", n)
	}
	if got := broken.State.Issue("example-org/example-repo", 6).AcceptanceRequests; got != 0 {
		t.Errorf("the count of the second request = %d, want 0 after a failed start", got)
	}
	if got := requirementLabels(t, sc); !slices.Contains(got, "cumin/status/accepting") {
		t.Errorf("labels of #6 = %v, want accepting after a failed start", got)
	}
	if n := len(sc.fake.Comments(sc.repo, 6)); n != 0 {
		t.Errorf("%d comments on #6 after a failed start, want none", n)
	}

	sc.pollAndWait(t, sc.serviceWithState(path))

	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1 (the second request)", n)
	}
	want := []string{githubtest.RequirementLabel, "cumin/status/awaiting-decision"}
	if got := requirementLabels(t, sc); !slices.Equal(got, want) {
		t.Errorf("labels of #6 = %v, want %v", got, want)
	}
	if comments := sc.fake.Comments(sc.repo, 6); len(comments) != 1 || !strings.Contains(comments[0].Body, workflow.NoAcceptanceCheckReason) {
		t.Errorf("the comments of #6 = %+v, want one with the reason", comments)
	}
}

// At a quota limit, "request the acceptance check again" starts no Planner
// and counts nothing: the issue keeps cumin/status/accepting. After the
// limit, one poll sends the one second request.
func TestAccepting_AQuotaLimitDoesNotUseUpTheSecondRequest(t *testing.T) {
	sc, _ := acceptingScene(t, cliOptions{fixture: "planner-done.jsonl"})
	reset := sceneNow.Add(2 * time.Hour)
	sc.setQuota(t, 0.90, reset, 0.10, sceneNow.Add(time.Hour))
	service := sc.serviceWithState(filepath.Join(t.TempDir(), "state.json"))

	sc.pollTimes(t, service, 3)

	if n := sc.agentRuns(t); n != 0 {
		t.Fatalf("%d agent runs at the limit, want none: the quota stops the second request", n)
	}
	if n := service.State.Issue("example-org/example-repo", 6).AcceptanceRequests; n != 0 {
		t.Errorf("the state file counts %d second requests, want none", n)
	}
	want := []string{githubtest.RequirementLabel, "cumin/status/accepting"}
	if got := requirementLabels(t, sc); !slices.Equal(got, want) {
		t.Errorf("labels of #6 = %v, want %v", got, want)
	}
	if n := len(sc.fake.Comments(sc.repo, 6)); n != 0 {
		t.Errorf("%d comments on #6 at the limit, want none", n)
	}

	sc.clock.Set(reset)
	sc.setQuota(t, 0.05, reset.Add(5*time.Hour), 0.10, reset.Add(time.Hour))
	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs after the limit, want 1: one poll sends the second request", n)
	}
	if text := promptOf(t, sc.record(t, "agent.args")); !strings.Contains(text, "Request: acceptance check") {
		t.Errorf("the request text is not an acceptance check:\n%s", text)
	}
	// The Planner left no comment again, so the issue stops for the Maintainer:
	// the request after the limit was the one second request.
	stopped := []string{githubtest.RequirementLabel, "cumin/status/awaiting-decision"}
	if got := requirementLabels(t, sc); !slices.Equal(got, stopped) {
		t.Errorf("labels of #6 = %v, want %v", got, stopped)
	}
}

// An acceptance check run that hit the quota limit does not use up the
// second request: the end of the run checks the quota before the request is
// counted, so the issue keeps cumin/status/accepting with a count of zero.
func TestAccepting_ARunThatHitTheQuotaLimitDoesNotUseUpTheSecondRequest(t *testing.T) {
	sc, _ := newAcceptanceScene(t, cliOptions{fixture: "planner-done.jsonl"})
	// The first request has no quota check. The Planner run reports a
	// weekly usage of 0.51 (planner-done.jsonl) against a target of 50, and
	// leaves no comment.
	sc.setQuota(t, 0.10, sceneNow.Add(time.Hour), 0.10, sceneNow.Add(time.Hour))
	sc.quota.Weekly.Target = 50
	service := sc.serviceWithState(filepath.Join(t.TempDir(), "state.json"))

	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs, want 1: the quota stops the second request", n)
	}
	if n := service.State.Issue("example-org/example-repo", 6).AcceptanceRequests; n != 0 {
		t.Errorf("the state file counts %d second requests, want none", n)
	}
	want := []string{githubtest.RequirementLabel, "cumin/status/accepting"}
	if got := requirementLabels(t, sc); !slices.Equal(got, want) {
		t.Errorf("labels of #6 = %v, want %v", got, want)
	}
	if n := len(sc.fake.Comments(sc.repo, 6)); n != 0 {
		t.Errorf("%d comments on #6, want none", n)
	}
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

// The label change of the stop fails once. The state file keeps the count
// of the second request, and nothing is written: the next poll stops the
// issue with one comment, one notification, and no new request.
func TestAccepting_AFailedLabelChangeOfTheStopRequestsNothingAtTheNextPoll(t *testing.T) {
	sc, _ := acceptingScene(t, cliOptions{fixture: "planner-done.jsonl"})
	service := sc.serviceWithState(filepath.Join(t.TempDir(), "state.json"))
	if err := service.State.Set("example-org/example-repo", 6, state.Issue{AcceptanceRequests: 1}); err != nil {
		t.Fatal(err)
	}
	sc.fake.FailNext(http.MethodPut, putRequirementLabelsPath, http.StatusForbidden)

	if err := service.Poll(t.Context()); err == nil {
		t.Error("the poll with the failed label change returned no error")
	}
	service.Wait()
	if got := requirementLabels(t, sc); !slices.Contains(got, "cumin/status/accepting") {
		t.Errorf("labels of #6 = %v, want accepting after the failed label change", got)
	}
	if n := len(sc.fake.Comments(sc.repo, 6)); n != 0 {
		t.Errorf("%d comments on #6 after the failed label change, want none", n)
	}
	if got := service.State.Issue("example-org/example-repo", 6).AcceptanceRequests; got != 1 {
		t.Errorf("the count of the second request = %d, want still 1", got)
	}

	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none", n)
	}
	want := []string{githubtest.RequirementLabel, "cumin/status/awaiting-decision"}
	if got := requirementLabels(t, sc); !slices.Equal(got, want) {
		t.Errorf("labels of #6 = %v, want %v", got, want)
	}
	if comments := sc.fake.Comments(sc.repo, 6); len(comments) != 1 || !strings.Contains(comments[0].Body, workflow.NoAcceptanceCheckReason) {
		t.Errorf("the comments of #6 = %+v, want one with the reason", comments)
	}
	if messages := sc.messagesExceptWaiting(); len(messages) != 1 {
		t.Errorf("notifications = %v, want one", messages)
	}
}

// A restart after the Planner wrote the comment: the issue moves to
// cumin/status/awaiting-acceptance with one notification and no request.
func TestAccepting_ARestartAfterTheCommentAsksTheMaintainerToAccept(t *testing.T) {
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
	if messages := sc.messagesExceptWaiting(); len(messages) != 1 || !strings.Contains(messages[0], "ask for the acceptance") {
		t.Errorf("notifications = %v, want one that asks for the acceptance", messages)
	}
}

// A restart after the Planner wrote a question: the issue goes to
// cumin/status/awaiting-decision with one notification. cumin requests
// nothing and writes no comment of its own.
func TestAccepting_AQuestionOfThePlannerStopsForTheMaintainerWithNoRequest(t *testing.T) {
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
	if messages := sc.messagesExceptWaiting(); len(messages) != 1 || !strings.Contains(messages[0], "asked a question") {
		t.Errorf("notifications = %v, want one about the question", messages)
	}
}

// A comment from before the last close belongs to an earlier round, and a
// comment of another author never counts: "request the acceptance check"
// asks. Neither run leaves a
// comment that counts, so cumin asks once more and then stops for the
// Maintainer, in one run of cumin.
func TestAnOldAcceptanceCommentOrAnotherAuthorAsksAgain(t *testing.T) {
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
func TestNoSubIssueNoAcceptanceCheck(t *testing.T) {
	sc := newScene(t)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Title: subIssueTitle})
	sc.pollAndWait(t, sc.service())

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none", n)
	}
}

// A blocked acceptance check stops the requirement issue for the Maintainer with
// the step "stop the acceptance check", and is not run again.
func TestABlockedAcceptanceCheckStopsForTheMaintainer(t *testing.T) {
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
	if messages := sc.webhook.messagesSent(); len(messages) != 1 || !strings.Contains(messages[0], "stop the acceptance check") {
		t.Errorf("notifications = %v, want one that stops the acceptance check", messages)
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1", n)
	}
}

// The acceptance check reads the merged work: the work directory of the
// split, made before the merge, is made again at the head of main.
func TestTheAcceptanceCheckReadsTheMergedWork(t *testing.T) {
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

// planReviewScene is the scene of "request the acceptance check" from
// cumin/status/awaiting-plan-review: the requirement issue #6 waits for the
// plan review, and its sub-issue #10 closed an hour ago. The options say how
// the fake CLI answers as the Planner.
func planReviewScene(t *testing.T, options cliOptions) (*scene, time.Time) {
	t.Helper()
	sc, closedAt := newAcceptanceScene(t, options)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 6, Labels: []string{githubtest.RequirementLabel, "cumin/status/awaiting-plan-review"}})
	return sc, closedAt
}

// The test of a top-level requirement in cumin-core.md (the Owner task
// closes, the check starts): a requirement issue in
// cumin/status/awaiting-plan-review with an open cumin/type/owner-task
// sub-issue keeps its label, and nothing is requested. When the Maintainer
// closes the Owner task, the issue gets cumin/status/accepting before the
// Planner starts, and the acceptance check is requested exactly once across
// polls and a restart.
func TestAwaitingPlanReview_TheAcceptanceCheckIsRequestedOnceWhenTheOwnerTaskCloses(t *testing.T) {
	sc, closedAt := planReviewScene(t, cliOptions{fixture: "planner-done.jsonl", holds: true})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 9, Parent: 6, Title: "Change a workflow", Labels: []string{"cumin/type/owner-task", "risk/high"}})
	service := sc.service()

	// The Owner task is open.
	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)
	if n := sc.agentRuns(t); n != 0 {
		t.Fatalf("%d agent runs with an open Owner task, want none", n)
	}
	waiting := []string{githubtest.RequirementLabel, "cumin/status/awaiting-plan-review"}
	if got := requirementLabels(t, sc); !slices.Equal(got, waiting) {
		t.Fatalf("labels of #6 = %v, want %v with an open Owner task", got, waiting)
	}
	if n := sc.labelChanges(); n != 0 {
		t.Errorf("%d label changes with an open Owner task, want none", n)
	}

	// The Maintainer closes the Owner task.
	ownerTask := sc.fake.Issue(sc.repo, 9)
	ownerTask.Closed, ownerTask.ClosedAt = true, closedAt.Add(10*time.Minute)
	sc.fake.AddIssue(sc.repo, ownerTask)
	before := len(sc.fake.Requests())
	if err := service.Poll(t.Context()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	waitForAgentRun(t, sc)
	accepting := []string{githubtest.RequirementLabel, "cumin/status/accepting"}
	if got := requirementLabels(t, sc); !slices.Equal(got, accepting) {
		t.Errorf("labels of #6 = %v, want %v while the check runs", got, accepting)
	}
	requests := sc.fake.Requests()
	label := indexOf(requests, before, "PUT", "/issues/6/labels")
	token := indexOf(requests, before, "POST", "/access_tokens")
	if label < 0 || token < 0 || token < label {
		t.Errorf("the label change (request %d) does not come before the token of the Planner (request %d)", label, token)
	}
	text := promptOf(t, sc.record(t, "agent.args"))
	if !strings.Contains(text, "Request: acceptance check") || !strings.Contains(text, "#6") {
		t.Errorf("the request text is not an acceptance check of #6:\n%s", text)
	}

	// A poll while the Planner runs requests nothing more.
	if err := service.Poll(t.Context()); err != nil {
		t.Fatalf("Poll: %v", err)
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
		t.Errorf("%d agent runs, want 1", n)
	}
	want := []string{githubtest.RequirementLabel, "cumin/status/awaiting-acceptance"}
	if got := requirementLabels(t, sc); !slices.Equal(got, want) {
		t.Errorf("labels of #6 = %v, want %v", got, want)
	}
}

// "request the acceptance check" from cumin/status/awaiting-plan-review
// shares the free slots: with no free slot, the requirement issue keeps its
// label and nothing is requested. With a free slot, the next poll requests
// the acceptance check.
func TestAwaitingPlanReview_NoFreeSlotKeepsTheAcceptanceCheckWaiting(t *testing.T) {
	sc, _ := planReviewScene(t, cliOptions{fixture: "planner-done.jsonl"})
	service := sc.service()
	service.Settings.MaxIssuesInProgress = 0

	sc.pollAndWait(t, service)
	if n := sc.agentRuns(t); n != 0 {
		t.Fatalf("%d agent runs with no free slot, want none", n)
	}
	waiting := []string{githubtest.RequirementLabel, "cumin/status/awaiting-plan-review"}
	if got := requirementLabels(t, sc); !slices.Equal(got, waiting) {
		t.Fatalf("labels of #6 = %v, want %v with no free slot", got, waiting)
	}

	service.Settings.MaxIssuesInProgress = 1
	sc.pollAndWait(t, service)
	// The fake Planner leaves no comment, so the acceptance check runs a
	// second time.
	if n := sc.agentRuns(t); n != 2 {
		t.Errorf("%d agent runs with a free slot, want 2", n)
	}
	if got := requirementLabels(t, sc); slices.Equal(got, waiting) {
		t.Errorf("labels of #6 = %v with a free slot, want the label changed", got)
	}
}

// An acceptance check comment that is newer than the last close stops
// "request the acceptance check" in cumin/status/awaiting-plan-review, as
// in cumin/status/implementing: the check of this round exists.
func TestAwaitingPlanReview_AnAcceptanceCommentAfterTheLastCloseRequestsNothing(t *testing.T) {
	sc, closedAt := planReviewScene(t, cliOptions{fixture: "planner-done.jsonl"})
	acceptanceComment(sc, closedAt.Add(time.Minute), plannerLogin)
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
}
