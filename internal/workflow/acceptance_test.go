package workflow_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

// plannerLogin is the author of a comment of the Planner App. The fake knows
// one App, so it is the bot of that App.
const plannerLogin = implementerSlug

// newAcceptanceScene is the scene of R4 and R7: the requirement issue #6 is
// in cumin/status/implementing and its only sub-issue #10 closed an hour
// ago. The fake CLI answers as a Planner that returned done.
func newAcceptanceScene(t *testing.T, fixture string) (*scene, time.Time) {
	t.Helper()
	sc := newScene(t, cliOptions{fixture: fixture})
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

// Core-7 (cumin-core.md): when every sub-issue closes, the Planner is asked
// for the acceptance check once, across polls and a restart; the label stays
// cumin/status/implementing until the comment exists. Then R7 moves it to
// cumin/status/awaiting-owner-review with one notification, although the
// table holds a Fail.
func TestCore07_TheAcceptanceCheckIsRequestedOnceAndHandedToTheOwner(t *testing.T) {
	sc, closedAt := newAcceptanceScene(t, "planner-done.jsonl")

	sc.pollAndWait(t, sc.service())
	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs, want 1", n)
	}
	if got := requirementLabels(t, sc); !slices.Contains(got, "cumin/status/implementing") {
		t.Errorf("labels of #6 = %v, want implementing while the check runs", got)
	}
	text := promptOf(t, sc.record(t, "agent.args"))
	if !strings.Contains(text, "Request: acceptance check") || !strings.Contains(text, "#6") {
		t.Errorf("the request text is not an acceptance check of #6:\n%s", text)
	}
	requireIssueOfTheRun(t, text, 6, "requirement issue")

	// The Planner wrote the comment during its run. A restart polls again.
	acceptanceComment(sc, closedAt.Add(30*time.Minute), plannerLogin)
	service := sc.service()
	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want still 1", n)
	}
	want := []string{githubtest.RequirementLabel, "cumin/status/awaiting-owner-review"}
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

// A comment from before the last close belongs to an earlier round, and a
// comment of another author never counts: R4 asks, and R7 does not apply.
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
			sc, closedAt := newAcceptanceScene(t, "planner-done.jsonl")
			acceptanceComment(sc, closedAt.Add(tt.offset), tt.author)
			sc.pollAndWait(t, sc.service())

			if n := sc.agentRuns(t); n != 1 {
				t.Errorf("%d agent runs, want 1", n)
			}
			if got := requirementLabels(t, sc); !slices.Contains(got, "cumin/status/implementing") {
				t.Errorf("labels of #6 = %v, want implementing", got)
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
	sc, _ := newAcceptanceScene(t, "planner-blocked.jsonl")
	sc.pollAndWait(t, sc.service())

	const question = "## Decision needed: which sign-in method does the login screen use?"
	comments := sc.fake.Comments(sc.repo, 6)
	if len(comments) != 1 || comments[0].Body != question {
		t.Fatalf("the comments of #6 = %+v, want one with the blocked reason", comments)
	}
	want := []string{githubtest.RequirementLabel, "cumin/status/awaiting-owner-decision"}
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
	sc, _ := newAcceptanceScene(t, "planner-done.jsonl")

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
