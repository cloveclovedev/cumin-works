package workflow_test

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/agent"
	"github.com/cloveclovedev/cumin-works/internal/core/state"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

// The fake knows one App, so the Reviewer is the bot of that App.
const reviewerLogin = implementerSlug + "[bot]"

// fixtureSession is the session ID that done.jsonl and blocked.jsonl end
// with.
const fixtureSession = "11111111-2222-4333-8444-555555555555"

// reviewing puts issue #10 in cumin/status/checking with the pull
// request #21 at the head of the remote, no required check, and a state
// file with the given entry. The next poll applies "request the review".
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

// "request the review" (issue-states.md), round 1: the Reviewer starts in a new session, in a
// detached checkout of the head commit next to nothing else, with the round
// and the limit in the request. A Reviewer session stored from before the
// last cumin/status/ready is not resumed.
func TestTheReviewOfRound1StartsANewSessionAtTheHeadCommit(t *testing.T) {
	sc := newScene(t, cliOptions{reviews: []string{"APPROVE"}})
	service := sc.service()
	path := sc.reviewing(t, service, state.Issue{SessionID: "implementer-session", ReviewerSessionID: "old-reviewer-session"})

	sc.pollAndWait(t, service)

	args := sc.record(t, "agent.args")
	if strings.Contains(args, "--resume") {
		t.Errorf("round 1 resumed a session:\n%q", args)
	}
	text := promptOf(t, args)
	requireIssueOfTheRun(t, text, 10, "implementation issue")
	for _, want := range []string{"Request: review", "Implementation issue: #10", "Pull request: #21",
		"Head commit: " + sc.remoteHead, "Round: 1 of 3", "find as much as you can"} {
		if !strings.Contains(text, want) {
			t.Errorf("the request text has no %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Last reviewed commit") {
		t.Errorf("round 1 names a last reviewed commit:\n%s", text)
	}
	if strings.Contains(text, "Approved commit") {
		t.Errorf("the request names an approved commit, and the Reviewer approved none:\n%s", text)
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
	// stays for "request a review fix".
	got := state.Open(path, nil).Issue("example-org/example-repo", 10)
	if got.ReviewerSessionID != fixtureSession || got.SessionID != "implementer-session" {
		t.Errorf("state = %+v, want the new Reviewer session and the Implementer session kept", got)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelMerging) {
		t.Errorf("labels of #10 = %v, want cumin/status/merging after APPROVE with risk/low", got)
	}
	for _, want := range []string{`"msg":"request the review: requested the review"`, `"round":1`, `"resumed":false`,
		`"msg":"start the merge: the Reviewer approved the head commit"`} {
		if !strings.Contains(sc.logs.String(), want) {
			t.Errorf("the log has no %s", want)
		}
	}
}

// "request the review", round 2: one request for changes of the Reviewer since the last
// cumin/status/ready makes the next review round 2. It resumes the
// Reviewer session and names the commit of the last review. The round
// comes from GitHub, so a new Service (a restart) counts the same.
func TestTheReviewOfRound2ResumesTheReviewerSessionAndNamesTheLastReviewedCommit(t *testing.T) {
	sc := newScene(t, cliOptions{reviews: []string{"APPROVE"}})
	service := sc.service()
	sc.reviewing(t, service, state.Issue{SessionID: "implementer-session", ReviewerSessionID: "reviewer-session"})
	readyAt := sceneNow.Add(-time.Hour)
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

// "request the review", round 1 after an approval: the rounds start again at 1 in a new
// session, and the request names the commit that the Reviewer approved
// last, so that the Reviewer looks only at the diff from it. A dismissed
// review and an approval of a person are not named.
func TestTheReviewOfRound1AfterAnApprovalNamesTheApprovedCommit(t *testing.T) {
	sc := newScene(t, cliOptions{reviews: []string{"APPROVE"}})
	service := sc.service()
	sc.reviewing(t, service, state.Issue{SessionID: "implementer-session", ReviewerSessionID: "old-reviewer-session"})
	readyAt := sceneNow.Add(-time.Hour)
	issue := sc.repo.Issues[10]
	issue.LabelEvents = []githubtest.LabelEvent{{Label: workflow.LabelReady, At: readyAt}}
	const first = "1111111111111111111111111111111111111111"
	const second = "2222222222222222222222222222222222222222"
	const other = "3333333333333333333333333333333333333333"
	sc.addReview(t, githubtest.Review{Author: implementerSlug, AuthorIsBot: true, State: "CHANGES_REQUESTED", Commit: first, SubmittedAt: readyAt.Add(time.Minute)})
	sc.addReview(t, githubtest.Review{Author: implementerSlug, AuthorIsBot: true, State: "APPROVED", Commit: first, SubmittedAt: readyAt.Add(2 * time.Minute)})
	sc.addReview(t, githubtest.Review{Author: implementerSlug, AuthorIsBot: true, State: "APPROVED", Commit: second, SubmittedAt: readyAt.Add(3 * time.Minute)})
	sc.addReview(t, githubtest.Review{Author: implementerSlug, AuthorIsBot: true, State: "DISMISSED", Commit: other, SubmittedAt: readyAt.Add(4 * time.Minute)})
	sc.addReview(t, githubtest.Review{Author: "octocat", State: "APPROVED", Commit: other, SubmittedAt: readyAt.Add(5 * time.Minute)})

	sc.pollAndWait(t, service)

	args := sc.record(t, "agent.args")
	if strings.Contains(args, "--resume") {
		t.Errorf("round 1 after an approval resumed a session:\n%q", args)
	}
	text := promptOf(t, args)
	for _, want := range []string{"Round: 1 of 3\nApproved commit: " + second + "\n",
		"review only the diff from that commit to the head commit, with the depth of round 1"} {
		if !strings.Contains(text, want) {
			t.Errorf("the request text has no %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Last reviewed commit") {
		t.Errorf("round 1 names a last reviewed commit:\n%s", text)
	}
}

// An approval of the head commit leaves no diff to review, so the request
// names no approved commit.
func TestAnApprovalOfTheHeadCommitIsNotNamed(t *testing.T) {
	sc := newScene(t, cliOptions{reviews: []string{"APPROVE"}})
	service := sc.service()
	sc.reviewing(t, service, state.Issue{})
	sc.addReview(t, githubtest.Review{Author: implementerSlug, AuthorIsBot: true, State: "APPROVED", Commit: sc.remoteHead, SubmittedAt: sceneNow.Add(-time.Hour)})

	sc.pollAndWait(t, service)

	text := promptOf(t, sc.record(t, "agent.args"))
	if !strings.Contains(text, "Round: 1 of 3") || !strings.Contains(text, "find as much as you can") {
		t.Errorf("the request is not the one of round 1:\n%s", text)
	}
	if strings.Contains(text, "Approved commit") {
		t.Errorf("the request names the head commit as the approved commit:\n%s", text)
	}
}

// The check after done (agents/reviewer.md, completion): a run that left no
// review on the head commit with APPROVE or REQUEST_CHANGES is asked once
// more, in the same session. A review with COMMENT only does not count.
func TestAReviewThatIsNotOnTheHeadCommitIsRequestedOnceMore(t *testing.T) {
	sc := newScene(t, cliOptions{reviews: []string{"COMMENT", "APPROVE"}})
	service := sc.service()
	sc.reviewing(t, service, state.Issue{})
	// An APPROVE of the Reviewer on an older commit is not a review of the
	// head commit.
	sc.addReview(t, githubtest.Review{Author: implementerSlug, AuthorIsBot: true, State: "APPROVED", Commit: "1111111111111111111111111111111111111111", SubmittedAt: sceneNow.Add(-time.Hour)})

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
	if !strings.Contains(sc.logs.String(), `"msg":"start the merge: the Reviewer approved the head commit"`) {
		t.Errorf("the log does not say that the head commit was approved: %s", sc.logs)
	}
}

// A second run without a review stops the issue for the Maintainer with the action
// "stop the review": one comment, one label change, one notification.
func TestASecondRunWithoutAReviewStopsForTheMaintainer(t *testing.T) {
	sc := newScene(t, cliOptions{reviews: []string{"NONE", "NONE"}})
	service := sc.service()
	sc.reviewing(t, service, state.Issue{})

	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 2 {
		t.Errorf("%d agent runs, want 2", n)
	}
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 || !strings.Contains(comments[0].Body, "Step: stop the review") || !strings.Contains(comments[0].Body, workflow.MissingReviewReason) {
		t.Fatalf("comments on #10 = %+v, want one stop note of 'stop the review'", comments)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelAwaitingDecision}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/awaiting-decision", got)
	}
	if n := len(sc.webhook.messagesSent()); n != 1 {
		t.Errorf("%d notifications, want 1", n)
	}
}

// "stop the review" (issue-states.md): a blocked result of the Reviewer stops the issue at
// once, with the blocked_reason as the comment, and nothing is retried.
func TestABlockedReviewerStopsWithoutARetry(t *testing.T) {
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
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelAwaitingDecision) {
		t.Errorf("labels of #10 = %v, want cumin/status/awaiting-decision", got)
	}
	if n := len(sc.webhook.messagesSent()); n != 1 {
		t.Errorf("%d notifications, want 1", n)
	}
	for _, want := range []string{`"msg":"stop the review: the agent returned blocked"`, `"action":"stop the review"`} {
		if !strings.Contains(sc.logs.String(), want) {
			t.Errorf("the log has no %s", want)
		}
	}
	if got := state.Open(path, nil).Issue("example-org/example-repo", 10).ReviewerSessionID; got != fixtureSession {
		t.Errorf("Reviewer session = %q, want the session of the blocked run", got)
	}
}

// An abnormal end of the Reviewer is decided as every other end: no review
// is on the head commit, so the review is requested again once, in a new
// session. A second end without a review stops the review for the Maintainer,
// and the note names the kind of the abnormal end. The polls that follow
// start nothing: one stay starts the Reviewer two times at most.
func TestTwoAbnormalEndsOfTheReviewerStopForTheMaintainer(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "invalid-result.jsonl"})
	service := sc.service()
	sc.reviewing(t, service, state.Issue{})

	for range 3 {
		sc.pollAndWait(t, service)
	}

	if n := sc.agentRuns(t); n != 2 {
		t.Errorf("%d agent runs, want 2: the review and one second request", n)
	}
	if strings.Contains(sc.record(t, "agent.args"), "--resume") {
		t.Error("the second request resumed a session")
	}
	reason := workflow.AfterAbnormalEndReason(workflow.MissingReviewReason, "Reviewer", agent.EndInvalidResult)
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 {
		t.Fatalf("%d comments on #10, want one stop note: %+v", len(comments), comments)
	}
	for _, want := range []string{"Step: stop the review", "Reason: " + reason, "ended abnormally (" + agent.EndInvalidResult.String() + ")", "Retried: once"} {
		if !strings.Contains(comments[0].Body, want) {
			t.Errorf("the comment has no %q:\n%s", want, comments[0].Body)
		}
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelAwaitingDecision}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/awaiting-decision", got)
	}
	if messages := sc.messagesExceptWaiting(); len(messages) != 1 {
		t.Errorf("notifications = %v, want one", messages)
	}
}

// Several polls while the Reviewer works start one review: the label
// changes before the request (principle 3).
func TestTheReviewIsRequestedOnceAcrossPolls(t *testing.T) {
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
// required checks, so the issue goes back to cumin/status/checking
// and nothing is reviewed on the new head yet (review of #244): the checks
// run on it, and "request the review" or "request a check fix" decides again.
func TestAHeadThatMovedDuringTheReviewWaitsForTheChecksAgain(t *testing.T) {
	sc := newScene(t, cliOptions{reviews: []string{"APPROVE"}, movesHeadOnRun: 1})
	service := sc.service()
	sc.reviewing(t, service, state.Issue{})

	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want only the review of the old head", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelChecking}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/checking", got)
	}
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 0 {
		t.Errorf("%d comments on #10, want none", n)
	}
	if !strings.Contains(sc.logs.String(), `"msg":"go back to the checks: the head commit moved during the review; the issue waits for the checks again"`) {
		t.Errorf("the log does not say that the head moved: %s", sc.logs)
	}
}

// "request a review fix" (issue-states.md): REQUEST_CHANGES on the head commit below the limit
// moves the issue back to cumin/status/implementing and asks the
// Implementer to fix the comments in its own session, naming the review.
// After done, "wait for the checks" runs again and the issue waits for the checks.
func TestChangesRequestedGoToTheImplementerInItsSession(t *testing.T) {
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
	requireIssueOfTheRun(t, text, 10, "implementation issue")
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
	// The fix is pushed (the fake CLI adds no commit), so "wait for the checks" passes.
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelChecking}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/checking", got)
	}
	got := state.Open(path, nil).Issue("example-org/example-repo", 10)
	if got.SessionID != fixtureSession || got.ReviewerSessionID != fixtureSession {
		t.Errorf("state = %+v, want both sessions of the two runs", got)
	}
	for _, want := range []string{`"msg":"request a review fix: the Reviewer requested changes; the issue goes back to the Implementer"`,
		`"round":1`, `"msg":"request a review fix: requested the work"`, `"kind":"review fix"`, `"msg":"wait for the checks: verified the pull request"`} {
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
			Commit: commit, SubmittedAt: sceneNow.Add(time.Duration(i-10) * time.Minute)})
	}
}

// "stop at the round limit" (issue-states.md): REQUEST_CHANGES at the limit of rounds does not
// go to the Implementer. The Reviewer explains the cause in its session;
// its decision request on the pull request is the reason, so cumin writes
// no comment, moves the issue to cumin/status/awaiting-decision, and
// sends one notification that links the explanation.
func TestTheRoundLimitEndsWithTheExplanationAndOneNotification(t *testing.T) {
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
		t.Errorf("the request text is not the request of 'request the cause':\n%s", text)
	}
	requireIssueOfTheRun(t, promptOf(t, args), 10, "implementation issue")
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelAwaitingDecision}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/awaiting-decision", got)
	}
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 0 {
		t.Errorf("%d comments on #10, want none: the Reviewer wrote the reason", n)
	}
	explanations := sc.fake.Comments(sc.repo, 21)
	if len(explanations) != 1 {
		t.Fatalf("comments on #21 = %+v, want the explanation", explanations)
	}
	messages := sc.webhook.messagesSent()
	if len(messages) != 1 || !strings.Contains(messages[0], "stop at the round limit") || !strings.Contains(messages[0], "issuecomment-") {
		t.Errorf("notifications = %q, want one of 'stop at the round limit' that links the comment", messages)
	}
	for _, want := range []string{`"msg":"request the cause: blocking comments remain at the limit of rounds"`,
		`"msg":"request the cause: requested the explanation of the cause"`, `"msg":"stop at the round limit: the issue waits for a Maintainer"`} {
		if !strings.Contains(sc.logs.String(), want) {
			t.Errorf("the log has no %s", want)
		}
	}
}

// Without the explanation, the stop step hands the issue to the Maintainer with
// the action "stop at the round limit": one comment, one label change, one notification.
func TestAtTheRoundLimitWithoutTheExplanationTheStopStepHandsOverTheIssue(t *testing.T) {
	sc := newScene(t, cliOptions{reviews: []string{"REQUEST_CHANGES", "NONE"}})
	service := sc.service()
	sc.reviewing(t, service, state.Issue{SessionID: "implementer-session"})
	sc.atTheLimit(t)

	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 2 {
		t.Errorf("%d agent runs, want 2", n)
	}
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 || !strings.Contains(comments[0].Body, "Step: stop at the round limit") || !strings.Contains(comments[0].Body, workflow.MissingExplanationReason) {
		t.Fatalf("comments on #10 = %+v, want one stop note of 'stop at the round limit'", comments)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelAwaitingDecision) {
		t.Errorf("labels of #10 = %v, want cumin/status/awaiting-decision", got)
	}
	if n := len(sc.webhook.messagesSent()); n != 1 {
		t.Errorf("%d notifications, want 1", n)
	}
}
