package workflow_test

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/state"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

// The fake knows one App, so the Reviewer is the bot of that App.
const reviewerLogin = implementerSlug + "[bot]"

// fixtureSession is the session ID that done.jsonl and blocked.jsonl end
// with.
const fixtureSession = "11111111-2222-4333-8444-555555555555"

// reviewing puts issue #10 in cumin/status/awaiting-checks with the pull
// request #21 at the head of the remote, no required check, and a state
// file with the given entry. The next poll applies I3.
func (sc *scene) reviewing(t *testing.T, service *workflow.Service, stored state.Issue) string {
	t.Helper()
	sc.awaitingChecks(t, nil, nil)
	path := filepath.Join(t.TempDir(), "state.json")
	service.State = state.Open(path, nil)
	if err := service.State.Set("example-org/example-repo", 10, stored); err != nil {
		t.Fatal(err)
	}
	return path
}

// addReview adds a review of the past to the pull request #21. The fake
// keeps the pointer, so the review is set on the pull request itself.
func (sc *scene) addReview(t *testing.T, review githubtest.Review) {
	t.Helper()
	pr := sc.repo.PullRequests[21]
	pr.Reviews = append(pr.Reviews, review)
}

// I3 (issue-states.md), round 1: the Reviewer starts in a new session, in a
// detached checkout of the head commit next to nothing else, with the round
// and the limit in the request. A Reviewer session stored from before the
// last cumin/status/ready is not resumed.
func TestI3_Round1StartsANewSessionAtTheHeadCommit(t *testing.T) {
	sc := newScene(t, cliOptions{reviews: []string{"APPROVE"}})
	service := sc.service()
	path := sc.reviewing(t, service, state.Issue{SessionID: "implementer-session", ReviewerSessionID: "old-reviewer-session"})

	sc.pollAndWait(t, service)

	args := sc.record(t, "agent.args")
	if strings.Contains(args, "--resume") {
		t.Errorf("round 1 resumed a session:\n%q", args)
	}
	text := promptOf(t, args)
	for _, want := range []string{"Request: review", "Implementation issue: #10", "Pull request: #21",
		"Head commit: " + sc.remoteHead, "Round: 1 of 3", "find as much as you can"} {
		if !strings.Contains(text, want) {
			t.Errorf("the request text has no %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Last reviewed commit") {
		t.Errorf("round 1 names a last reviewed commit:\n%s", text)
	}
	wantDir := filepath.Join(sc.workRoot, "example-org", "example-repo", "10-reviewer")
	if got := strings.TrimSpace(sc.record(t, "agent.cwd")); got != realPath(t, wantDir) {
		t.Errorf("the CLI ran in %q, want %q", got, realPath(t, wantDir))
	}
	// The review of the fake CLI is on the commit that the work directory
	// held: the head commit of the pull request.
	reviews := sc.fake.Reviews(sc.repo, 21)
	if len(reviews) != 1 || reviews[0].Commit != sc.remoteHead || reviews[0].State != "APPROVED" {
		t.Errorf("reviews = %+v, want one APPROVED on the head commit", reviews)
	}
	// The Reviewer keeps its own session; the session of the Implementer
	// stays for I5.
	got := state.Open(path, nil).Issue("example-org/example-repo", 10)
	if got.ReviewerSessionID != fixtureSession || got.SessionID != "implementer-session" {
		t.Errorf("state = %+v, want the new Reviewer session and the Implementer session kept", got)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelReviewing) {
		t.Errorf("labels of #10 = %v, want cumin/status/reviewing to stay after APPROVE", got)
	}
	for _, want := range []string{`"msg":"I3: requested the review"`, `"round":1`, `"resumed":false`,
		`"msg":"I3: the Reviewer approved the head commit"`} {
		if !strings.Contains(sc.logs.String(), want) {
			t.Errorf("the log has no %s", want)
		}
	}
}

// I3, round 2: one request for changes of the Reviewer since the last
// cumin/status/ready makes the next review round 2. It resumes the
// Reviewer session and names the commit of the last review. The round
// comes from GitHub, so a new Service (a restart) counts the same.
func TestI3_Round2ResumesTheReviewerSessionAndNamesTheLastReviewedCommit(t *testing.T) {
	sc := newScene(t, cliOptions{reviews: []string{"APPROVE"}})
	service := sc.service()
	sc.reviewing(t, service, state.Issue{SessionID: "implementer-session", ReviewerSessionID: "reviewer-session"})
	readyAt := time.Now().Add(-time.Hour)
	issue := sc.repo.Issues[10]
	issue.LabelEvents = []githubtest.LabelEvent{{Label: workflow.LabelReady, At: readyAt}}
	const older = "1111111111111111111111111111111111111111"
	// Before the last cumin/status/ready: not a round of this count.
	sc.addReview(t, githubtest.Review{Author: implementerSlug, AuthorIsBot: true, State: "CHANGES_REQUESTED", Commit: "2222222222222222222222222222222222222222", SubmittedAt: readyAt.Add(-time.Minute)})
	sc.addReview(t, githubtest.Review{Author: implementerSlug, AuthorIsBot: true, State: "CHANGES_REQUESTED", Commit: older, SubmittedAt: readyAt.Add(time.Minute)})
	// A person does not count.
	sc.addReview(t, githubtest.Review{Author: "octocat", State: "CHANGES_REQUESTED", Commit: older, SubmittedAt: readyAt.Add(2 * time.Minute)})

	sc.pollAndWait(t, service)

	args := sc.record(t, "agent.args")
	if got := argumentOf(t, args, "--resume"); got != "reviewer-session" {
		t.Errorf("--resume = %q, want the Reviewer session, never the Implementer one", got)
	}
	text := promptOf(t, args)
	for _, want := range []string{"Round: 2 of 3", "Last reviewed commit: " + older, "This is round 2"} {
		if !strings.Contains(text, want) {
			t.Errorf("the request text has no %q:\n%s", want, text)
		}
	}
}

// The check after done (agents/reviewer.md, completion): a run that left no
// review on the head commit with APPROVE or REQUEST_CHANGES is asked once
// more, in the same session. A review with COMMENT only does not count.
func TestI3_AReviewThatIsNotOnTheHeadCommitIsRequestedOnceMore(t *testing.T) {
	sc := newScene(t, cliOptions{reviews: []string{"COMMENT", "APPROVE"}})
	service := sc.service()
	sc.reviewing(t, service, state.Issue{})
	// An APPROVE of the Reviewer on an older commit is not a review of the
	// head commit.
	sc.addReview(t, githubtest.Review{Author: implementerSlug, AuthorIsBot: true, State: "APPROVED", Commit: "1111111111111111111111111111111111111111", SubmittedAt: time.Now().Add(-time.Hour)})

	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 2 {
		t.Errorf("%d agent runs, want 2: the review and one more request", n)
	}
	args := sc.record(t, "agent.args")
	if got := argumentOf(t, args, "--resume"); got != fixtureSession {
		t.Errorf("--resume = %q, want the session of the first run", got)
	}
	if text := promptOf(t, args); !strings.Contains(text, "cumin found no review of yours on the head commit") {
		t.Errorf("the second request does not say what is missing:\n%s", text)
	}
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 0 {
		t.Errorf("%d comments on #10, want none: the second run left its review", n)
	}
	if !strings.Contains(sc.logs.String(), `"msg":"I3: the Reviewer approved the head commit"`) {
		t.Errorf("the log does not say that the head commit was approved: %s", sc.logs)
	}
}

// A second run without a review stops the issue for the Owner with the row
// I5 (its failure column): one comment, one label change, one notification.
func TestI3_ASecondRunWithoutAReviewStopsForTheOwner(t *testing.T) {
	sc := newScene(t, cliOptions{reviews: []string{"NONE", "NONE"}})
	service := sc.service()
	sc.reviewing(t, service, state.Issue{})

	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 2 {
		t.Errorf("%d agent runs, want 2", n)
	}
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 || !strings.Contains(comments[0].Body, "Row: I5") || !strings.Contains(comments[0].Body, workflow.MissingReviewReason) {
		t.Fatalf("comments on #10 = %+v, want one stop note with the row I5", comments)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelAwaitingOwnerDecision}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/awaiting-owner-decision", got)
	}
	if n := len(sc.webhook.messagesSent()); n != 1 {
		t.Errorf("%d notifications, want 1", n)
	}
}

// I10 (issue-states.md): a blocked result of the Reviewer stops the issue at
// once, with the blocked_reason as the comment, and nothing is retried.
func TestI10_ABlockedReviewerStopsWithoutARetry(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "blocked.jsonl"})
	service := sc.service()
	path := sc.reviewing(t, service, state.Issue{})

	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1: a blocked result is not retried", n)
	}
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 || !strings.HasPrefix(comments[0].Body, "## Decision needed") {
		t.Fatalf("comments on #10 = %+v, want the blocked_reason of the Reviewer", comments)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelAwaitingOwnerDecision) {
		t.Errorf("labels of #10 = %v, want cumin/status/awaiting-owner-decision", got)
	}
	if n := len(sc.webhook.messagesSent()); n != 1 {
		t.Errorf("%d notifications, want 1", n)
	}
	for _, want := range []string{`"msg":"I10: the agent returned blocked"`, `"row":"I10"`} {
		if !strings.Contains(sc.logs.String(), want) {
			t.Errorf("the log has no %s", want)
		}
	}
	if got := state.Open(path, nil).Issue("example-org/example-repo", 10).ReviewerSessionID; got != fixtureSession {
		t.Errorf("Reviewer session = %q, want the session of the blocked run", got)
	}
}

// An abnormal end of the Reviewer runs the same request once more, in a new
// session; the second one stops the issue with the row I3.
func TestI3_TwoAbnormalEndsOfTheReviewerStopForTheOwner(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "invalid-result.jsonl"})
	service := sc.service()
	sc.reviewing(t, service, state.Issue{})

	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 2 {
		t.Errorf("%d agent runs, want 2", n)
	}
	if strings.Contains(sc.record(t, "agent.args"), "--resume") {
		t.Error("the retry resumed a session")
	}
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 || !strings.Contains(comments[0].Body, "Row: I3") || !strings.Contains(comments[0].Body, "Reviewer") {
		t.Fatalf("comments on #10 = %+v, want one stop note with the row I3", comments)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelAwaitingOwnerDecision) {
		t.Errorf("labels of #10 = %v, want cumin/status/awaiting-owner-decision", got)
	}
}

// Several polls while the Reviewer works start one review: the label
// changes before the request (principle 3).
func TestI3_TheReviewIsRequestedOnceAcrossPolls(t *testing.T) {
	sc := newScene(t, cliOptions{reviews: []string{"APPROVE"}})
	service := sc.service()
	sc.reviewing(t, service, state.Issue{})
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
}

// The head can move while the Reviewer works. Only the old head passed the
// required checks, so the issue goes back to cumin/status/awaiting-checks
// and nothing is reviewed on the new head yet (review of #244): the checks
// run on it, and I3 or I4 decides again.
func TestI3_AHeadThatMovedDuringTheReviewWaitsForTheChecksAgain(t *testing.T) {
	sc := newScene(t, cliOptions{reviews: []string{"APPROVE"}, movesHeadOnRun: 1})
	service := sc.service()
	sc.reviewing(t, service, state.Issue{})

	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want only the review of the old head", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelAwaitingChecks}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/awaiting-checks", got)
	}
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 0 {
		t.Errorf("%d comments on #10, want none", n)
	}
	if !strings.Contains(sc.logs.String(), `"msg":"I3: the head commit moved during the review; the issue waits for the checks again"`) {
		t.Errorf("the log does not say that the head moved: %s", sc.logs)
	}
}

// I5 (issue-states.md): REQUEST_CHANGES on the head commit below the limit
// moves the issue back to cumin/status/implementing and asks the
// Implementer to fix the comments in its own session, naming the review.
// After done, I2 runs again and the issue waits for the checks.
func TestI5_ChangesRequestedGoToTheImplementerInItsSession(t *testing.T) {
	sc := newScene(t, cliOptions{reviews: []string{"REQUEST_CHANGES"}})
	service := sc.service()
	path := sc.reviewing(t, service, state.Issue{SessionID: "implementer-session"})
	ctx := context.Background()

	for i := range 2 {
		if err := service.Poll(ctx); err != nil {
			t.Fatalf("poll %d: %v", i+1, err)
		}
	}
	service.Wait()

	if n := sc.agentRuns(t); n != 2 {
		t.Fatalf("%d agent runs, want the review and one fix", n)
	}
	args := sc.record(t, "agent.args")
	if got := argumentOf(t, args, "--resume"); got != "implementer-session" {
		t.Errorf("--resume = %q, want the Implementer session, never the Reviewer one", got)
	}
	reviews := sc.fake.Reviews(sc.repo, 21)
	if len(reviews) != 1 {
		t.Fatalf("reviews = %+v, want the one of the Reviewer", reviews)
	}
	text := promptOf(t, args)
	for _, want := range []string{"Request: review fix", "Pull request: #21", "Review: " + reviews[0].URL,
		"Branch: cumin/10-add-the-login-screen", "cumin-review-reply", "Do not open a new pull request"} {
		if !strings.Contains(text, want) {
			t.Errorf("the request text has no %q:\n%s", want, text)
		}
	}
	wantDir := filepath.Join(sc.workRoot, "example-org", "example-repo", "10-implementer")
	if got := strings.TrimSpace(sc.record(t, "agent.cwd")); got != realPath(t, wantDir) {
		t.Errorf("the fix ran in %q, want %q", got, realPath(t, wantDir))
	}
	// The fix is pushed (the fake CLI adds no commit), so I2 passes.
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelAwaitingChecks}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/awaiting-checks", got)
	}
	got := state.Open(path, nil).Issue("example-org/example-repo", 10)
	if got.SessionID != fixtureSession || got.ReviewerSessionID != fixtureSession {
		t.Errorf("state = %+v, want both sessions of the two runs", got)
	}
	for _, want := range []string{`"msg":"I5: the Reviewer requested changes; the issue goes back to the Implementer"`,
		`"round":1`, `"msg":"I5: requested the work"`, `"kind":"review fix"`, `"msg":"I2: verified the pull request"`} {
		if !strings.Contains(sc.logs.String(), want) {
			t.Errorf("the log has no %s", want)
		}
	}
}

// atTheLimit gives the pull request two rounds of the Reviewer before the
// next review; the limit of the scene is 3, so the next one is round 3.
func (sc *scene) atTheLimit(t *testing.T) {
	t.Helper()
	for i, commit := range []string{"1111111111111111111111111111111111111111", "2222222222222222222222222222222222222222"} {
		sc.addReview(t, githubtest.Review{Author: implementerSlug, AuthorIsBot: true, State: "CHANGES_REQUESTED",
			Commit: commit, SubmittedAt: time.Now().Add(time.Duration(i-10) * time.Minute)})
	}
}

// I8 (issue-states.md): REQUEST_CHANGES at the limit of rounds does not
// go to the Implementer. The Reviewer explains the cause in its session;
// its decision request on the pull request is the reason, so cumin writes
// no comment, moves the issue to cumin/status/awaiting-owner-decision, and
// sends one notification that links the explanation.
func TestI8_TheLimitEndsWithTheExplanationAndOneNotification(t *testing.T) {
	sc := newScene(t, cliOptions{reviews: []string{"REQUEST_CHANGES", "NONE"}, comments: []string{"NONE", "DECISION"}})
	service := sc.service()
	sc.reviewing(t, service, state.Issue{SessionID: "implementer-session"})
	sc.atTheLimit(t)

	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 2 {
		t.Fatalf("%d agent runs, want the review of round 3 and the explanation", n)
	}
	args := sc.record(t, "agent.args")
	if got := argumentOf(t, args, "--resume"); got != fixtureSession {
		t.Errorf("--resume = %q, want the Reviewer session of round 3", got)
	}
	if text := promptOf(t, args); !strings.Contains(text, "Request: explain the cause") || !strings.Contains(text, "after 3 review rounds") {
		t.Errorf("the request text is not the explanation of I8:\n%s", text)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelAwaitingOwnerDecision}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/awaiting-owner-decision", got)
	}
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 0 {
		t.Errorf("%d comments on #10, want none: the Reviewer wrote the reason", n)
	}
	explanations := sc.fake.Comments(sc.repo, 21)
	if len(explanations) != 1 {
		t.Fatalf("comments on #21 = %+v, want the explanation", explanations)
	}
	messages := sc.webhook.messagesSent()
	if len(messages) != 1 || !strings.Contains(messages[0], "I8") || !strings.Contains(messages[0], "issuecomment-") {
		t.Errorf("notifications = %q, want one of I8 that links the comment", messages)
	}
	for _, want := range []string{`"msg":"I8: blocking comments remain at the limit of rounds"`,
		`"msg":"I8: requested the explanation of the cause"`, `"msg":"I8: the issue waits for the Owner"`} {
		if !strings.Contains(sc.logs.String(), want) {
			t.Errorf("the log has no %s", want)
		}
	}
}

// Without the explanation, the stop step hands the issue to the Owner with
// the row I8: one comment, one label change, one notification.
func TestI8_WithoutTheExplanationTheStopStepHandsOverTheIssue(t *testing.T) {
	sc := newScene(t, cliOptions{reviews: []string{"REQUEST_CHANGES", "NONE"}})
	service := sc.service()
	sc.reviewing(t, service, state.Issue{SessionID: "implementer-session"})
	sc.atTheLimit(t)

	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 2 {
		t.Errorf("%d agent runs, want 2", n)
	}
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 || !strings.Contains(comments[0].Body, "Row: I8") || !strings.Contains(comments[0].Body, workflow.MissingExplanationReason) {
		t.Fatalf("comments on #10 = %+v, want one stop note with the row I8", comments)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelAwaitingOwnerDecision) {
		t.Errorf("labels of #10 = %v, want cumin/status/awaiting-owner-decision", got)
	}
	if n := len(sc.webhook.messagesSent()); n != 1 {
		t.Errorf("%d notifications, want 1", n)
	}
}
