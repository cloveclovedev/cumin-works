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

const (
	theMaintainer = "the-owner"
	olderCommit   = "0000000000000000000000000000000000000000"
)

// awaitingMaintainer puts issue #10 in cumin/status/awaiting-merge-decision after
// "ask for the merge decision", with risk/medium, the pull request #21 with one passed required
// check, and the Maintainer as an admin of the repository.
func awaitingMaintainer(t *testing.T, opts ...cliOptions) *scene {
	t.Helper()
	sc := newScene(t, opts...)
	sc.awaitingChecks(t, []string{"ci"}, []githubtest.Check{{Name: "ci", Conclusion: "SUCCESS"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{
		Number: 10, Parent: 6, Title: subIssueTitle,
		Labels: []string{"risk/medium", workflow.LabelAwaitingMergeDecision},
		// The reviews of the Maintainer in the tests are newer than this label.
		LabelEvents: []githubtest.LabelEvent{{Label: workflow.LabelAwaitingMergeDecision, At: sceneNow.Add(-10 * time.Minute)}},
	})
	sc.fake.SetPermission(theMaintainer, "admin", "User")
	return sc
}

func (sc *scene) review(author string, bot bool, state, commit string, minutesAgo int) {
	pr := sc.repo.PullRequests[21]
	pr.Reviews = append(pr.Reviews, githubtest.Review{Author: author, AuthorIsBot: bot, State: state, Commit: commit,
		SubmittedAt: sceneNow.Add(-time.Duration(minutesAgo) * time.Minute)})
}

func permissionReads(sc *scene) int {
	n := 0
	for _, r := range sc.fake.Requests() {
		if r.Method == http.MethodGet && strings.HasSuffix(r.Path, "/permission") {
			n++
		}
	}
	return n
}

// "start the merge" (issue-states.md): the Maintainer approves the head commit with a review,
// and cumin-core merges the pull request once, across polls, then closes
// the issue that GitHub left open.
func TestAnApprovalOfTheMaintainerOnTheHeadIsMergedOnce(t *testing.T) {
	sc := awaitingMaintainer(t)
	sc.review(implementerSlug, true, "APPROVED", sc.remoteHead, 30)
	sc.review(theMaintainer, false, "APPROVED", sc.remoteHead, 5)
	service := sc.service()

	for range 4 {
		sc.pollAndWait(t, service)
	}

	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 1 {
		t.Errorf("%d merge requests, want 1", n)
	}
	if got := sc.mergeMethod(t); got != "squash" {
		t.Errorf("merge method = %q, want squash", got)
	}
	if issue := sc.fake.Issue(sc.repo, 10); !issue.Closed || issue.StateReason != "completed" {
		t.Errorf("issue #10: closed %v, reason %q; want closed as completed", issue.Closed, issue.StateReason)
	}
	// Only the person is read: the bot of the Reviewer App is never an
	// Maintainer. "start the merge" reads the Maintainer once, and the merge reads the Maintainer again
	// for its conditions. The merge closes the last sub-issue, so "request
	// the acceptance check" reads once the person who added the status label
	// of the requirement issue.
	if n := permissionReads(sc); n != 3 {
		t.Errorf("%d permission reads, want 3 (the Maintainer, for 'start the merge' and for the merge; the account of the status label, for 'request the acceptance check')", n)
	}
	for _, want := range []string{`"msg":"start the merge: a Maintainer approved the head commit"`, `"msg":"merged the pull request"`} {
		if !strings.Contains(sc.logs.String(), want) {
			t.Errorf("the log has no %s", want)
		}
	}
}

// Approvals that do not count are not merged.
func TestApprovalsThatDoNotCountAreNotMerged(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(sc *scene)
	}{
		{"an approval on an older commit", func(sc *scene) {
			sc.review(theMaintainer, false, "APPROVED", olderCommit, 5)
		}},
		{"an approval of a bot", func(sc *scene) {
			sc.review(implementerSlug, true, "APPROVED", sc.remoteHead, 5)
		}},
		{"an approval of an account without write permission", func(sc *scene) {
			sc.review("someone", false, "APPROVED", sc.remoteHead, 5)
		}},
		{"an approval of a bot account that has write permission", func(sc *scene) {
			sc.fake.SetPermission("writer-bot", "write", "Bot")
			sc.review("writer-bot", false, "APPROVED", sc.remoteHead, 5)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := awaitingMaintainer(t)
			tc.setup(sc)
			service := sc.service()

			sc.pollAndWait(t, service)

			if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
				t.Errorf("%d merge requests, want none", n)
			}
			if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelAwaitingMergeDecision) {
				t.Errorf("labels of #10 = %v, want cumin/status/awaiting-merge-decision to stay", got)
			}
		})
	}
}

// An approval of the Maintainer after a comment of another person, and a comment
// of the Maintainer after the approval, still count: comments decide nothing.
func TestACommentOfTheMaintainerKeepsTheApproval(t *testing.T) {
	sc := awaitingMaintainer(t)
	sc.review(implementerSlug, true, "APPROVED", sc.remoteHead, 30)
	sc.review(theMaintainer, false, "APPROVED", sc.remoteHead, 10)
	sc.review(theMaintainer, false, "COMMENTED", sc.remoteHead, 5)
	service := sc.service()

	sc.pollTimes(t, service, 3)

	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 1 {
		t.Errorf("%d merge requests, want 1", n)
	}
}

// Without a review of a person that decides on the head commit, cumin reads
// no permission and no required checks.
func TestThePermissionIsReadOnlyForAMergeCandidate(t *testing.T) {
	sc := awaitingMaintainer(t)
	sc.review(theMaintainer, false, "CHANGES_REQUESTED", olderCommit, 5)
	service := sc.service()

	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	service.Wait()

	if n := permissionReads(sc); n != 0 {
		t.Errorf("%d permission reads, want none", n)
	}
	if n := sc.fake.CountRequests(http.MethodGet, branchRulesPath); n != 0 {
		t.Errorf("%d reads of the required checks, want none", n)
	}
}

// The Maintainer approved, but a required check does not pass on the head
// commit: cumin waits.
func TestTheMergeWaitsForTheRequiredChecks(t *testing.T) {
	sc := awaitingMaintainer(t)
	sc.repo.PullRequests[21].Checks = []githubtest.Check{{Name: "ci", Status: "IN_PROGRESS"}}
	sc.review(theMaintainer, false, "APPROVED", sc.remoteHead, 5)
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
		t.Errorf("%d merge requests, want none", n)
	}
	if !strings.Contains(sc.logs.String(), `"msg":"start the merge: a Maintainer approved; the merge waits for the required checks"`) {
		t.Error("the log does not say that the merge waits")
	}
}

// The risk label is read from the issue after the approval of the Maintainer too: not exactly
// one stops the issue with the step "start the merge".
func TestAnIssueThatTheMaintainerApprovedWithoutOneRiskLabelIsStopped(t *testing.T) {
	sc := awaitingMaintainer(t)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{
		Number: 10, Parent: 6, Title: subIssueTitle,
		Labels: []string{"risk/medium", "risk/high", workflow.LabelAwaitingMergeDecision},
	})
	sc.review(theMaintainer, false, "APPROVED", sc.remoteHead, 5)
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
		t.Errorf("%d merge requests, want none", n)
	}
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 || !strings.Contains(comments[0].Body, "Step: start the merge") ||
		!strings.Contains(comments[0].Body, workflow.RiskLabelReason(workflow.MergeTwoRiskLabels)) {
		t.Errorf("comments of #10 = %+v, want one stop note of 'start the merge'", comments)
	}
}

func TestMaintainerApproved_StartTheMerge(t *testing.T) {
	const head = "2222222222222222222222222222222222222222"
	at := func(minutes int) time.Time { return time.Date(2026, 10, 1, 10, minutes, 0, 0, time.UTC) }
	maintainers := map[string]bool{"owner": true, "owner-two": true, "app[bot]": true}
	for _, tc := range []struct {
		name    string
		reviews []workflow.Review
		want    bool
	}{
		{"approved on the head", []workflow.Review{{Author: "owner", State: workflow.ReviewApproved, Commit: head, SubmittedAt: at(1)}}, true},
		{"approved on an older commit", []workflow.Review{{Author: "owner", State: workflow.ReviewApproved, Commit: "old", SubmittedAt: at(1)}}, false},
		{"a later change request of another Maintainer", []workflow.Review{
			{Author: "owner", State: workflow.ReviewApproved, Commit: head, SubmittedAt: at(1)},
			{Author: "owner-two", State: workflow.ReviewChangesRequested, Commit: head, SubmittedAt: at(2)}}, false},
		{"a later approval after a change request", []workflow.Review{
			{Author: "owner", State: workflow.ReviewChangesRequested, Commit: head, SubmittedAt: at(1)},
			{Author: "owner", State: workflow.ReviewApproved, Commit: head, SubmittedAt: at(2)}}, true},
		{"a later comment does not count", []workflow.Review{
			{Author: "owner", State: workflow.ReviewApproved, Commit: head, SubmittedAt: at(1)},
			{Author: "owner", State: workflow.ReviewCommented, Commit: head, SubmittedAt: at(2)}}, true},
		{"a person who is not a Maintainer", []workflow.Review{{Author: "someone", State: workflow.ReviewApproved, Commit: head, SubmittedAt: at(1)}}, false},
		{"a bot never counts", []workflow.Review{{Author: "app[bot]", State: workflow.ReviewApproved, Commit: head, SubmittedAt: at(1)}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := workflow.MaintainerApproved(tc.reviews, head, maintainers); got != tc.want {
				t.Errorf("MaintainerApproved = %v, want %v", got, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		permission, userType string
		want                 bool
	}{{"admin", "User", true}, {"write", "User", true}, {"read", "User", false}, {"none", "User", false}, {"write", "Bot", false}, {"admin", "Bot", false}} {
		if got := workflow.IsMaintainer(tc.permission, tc.userType); got != tc.want {
			t.Errorf("IsMaintainer(%q, %q) = %v, want %v", tc.permission, tc.userType, got, tc.want)
		}
	}
}

// A candidate that is not an approval of a Maintainer does nothing, so "tell that cumin waits" still
// tells the Maintainer once that cumin waits.
func TestAMergeCandidateThatDoesNothingLeavesCuminToTellThatItWaits(t *testing.T) {
	sc := awaitingMaintainer(t)
	sc.review("someone", false, "APPROVED", sc.remoteHead, 5)
	service := sc.service()

	for range 2 {
		sc.pollAndWait(t, service)
	}

	if n := len(sc.waitingMessages()); n != 1 {
		t.Errorf("%d notifications that cumin waits, want 1", n)
	}
}

// A conflict of the merge after the approval of the Maintainer goes to the
// Implementer as it does for a risk/low pull request. A
// resolution that leaves the head stops the implementation for the Maintainer.
func TestAConflictThatStaysStopsTheImplementationForTheMaintainer(t *testing.T) {
	sc := awaitingMaintainer(t)
	sc.fake.SetPullRequestHeadCommitTime(sc.repo, 21, headBeforeTheLabel)
	sc.fake.SetPullRequestConflict(sc.repo, 21)
	sc.review(implementerSlug, true, "APPROVED", sc.remoteHead, 30)
	sc.review(theMaintainer, false, "APPROVED", sc.remoteHead, 5)
	sc.repo.Issues[10].LabelEvents = []githubtest.LabelEvent{readyBy(theMaintainer, 30)}
	service := sc.serviceWithSession(t)

	sc.pollTimes(t, service, 3)

	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs, want one resolution", n)
	}
	text := promptOf(t, sc.record(t, "agent.args"))
	if !strings.Contains(text, "Request: conflict resolution") {
		t.Error("the run was not a conflict resolution")
	}
	if !strings.Contains(text, issueOwnerLoginLine) {
		t.Errorf("the conflict resolution request does not name the Issue Owner %s:\n%s", theMaintainer, text)
	}
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 || !strings.Contains(comments[0].Body, "Step: stop the implementation") ||
		!strings.Contains(comments[0].Body, workflow.ConflictNotResolvedReason(21)) {
		t.Errorf("comments of #10 = %+v, want one stop note of the conflict that stays", comments)
	}
}

// A failed read of the login of the Issue Owner at a conflict of the merge sends
// no request and keeps cumin/status/merging, so the next poll sends the
// merge again and requests the resolution.
func TestAFailedReadOfTheIssueOwnerLoginAtAConflictIsTriedAgainAtTheNextPoll(t *testing.T) {
	sc := awaitingMaintainer(t)
	sc.fake.SetPullRequestConflict(sc.repo, 21)
	sc.review(implementerSlug, true, "APPROVED", sc.remoteHead, 30)
	sc.review(theMaintainer, false, "APPROVED", sc.remoteHead, 5)
	sc.repo.Issues[10].LabelEvents = []githubtest.LabelEvent{readyBy(theMaintainer, 30)}
	service := sc.serviceWithSession(t)
	// The first read of the permission is the one of the reviewer for the candidate of the merge,
	// the second one is the same read for the conditions of the merge, and
	// the third one is the read of the login of the Issue Owner.
	sc.fake.FailTimes(http.MethodGet, "/repos/example-org/example-repo/collaborators/"+theMaintainer+"/permission", 2, everyTry, http.StatusBadGateway)

	sc.pollAndWait(t, service)
	_ = service.Poll(t.Context())
	service.Wait()

	if n := sc.agentRuns(t); n != 0 {
		t.Fatalf("%d agent runs, want none after a failed read", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelMerging) {
		t.Errorf("labels of #10 = %v, want cumin/status/merging: a failed read changes nothing", got)
	}

	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs, want one resolution after the next poll", n)
	}
	if text := promptOf(t, sc.record(t, "agent.args")); !strings.Contains(text, issueOwnerLoginLine) {
		t.Errorf("the conflict resolution request does not name the Issue Owner %s:\n%s", theMaintainer, text)
	}
}

// "send back for changes" (issue-states.md): the Maintainer requests changes on the head commit of a
// pull request that waits for the merge decision. cumin changes the label
// to cumin/status/implementing and sends one request of the kind "owner
// review fix" in the session of the Implementer, across polls. The request
// for changes also takes an earlier approval back, so nothing is merged.
func TestARequestForChangesOfTheMaintainerOnTheHeadSendsOneRequest(t *testing.T) {
	sc := awaitingMaintainer(t, cliOptions{holds: true})
	sc.review(implementerSlug, true, "APPROVED", sc.remoteHead, 30)
	sc.review(theMaintainer, false, "APPROVED", sc.remoteHead, 10)
	sc.review(theMaintainer, false, "CHANGES_REQUESTED", sc.remoteHead, 5)
	sc.repo.PullRequests[21].Reviews[2].URL = "https://github.com/example-org/example-repo/pull/21#pullrequestreview-7"
	sc.repo.Issues[10].LabelEvents = append([]githubtest.LabelEvent{readyBy(theMaintainer, 60)}, sc.repo.Issues[10].LabelEvents...)
	service := sc.serviceWithSession(t)
	ctx := context.Background()

	if err := service.Poll(ctx); err != nil {
		t.Fatalf("poll 1: %v", err)
	}
	waitForAgentRun(t, sc)
	for i := range 2 {
		if err := service.Poll(ctx); err != nil {
			t.Fatalf("poll %d: %v", i+2, err)
		}
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/medium", workflow.LabelImplementing}) {
		t.Errorf("labels of #10 = %v, want risk/medium and cumin/status/implementing", got)
	}
	sc.release(t)
	service.Wait()

	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs, want one fix", n)
	}
	args := sc.record(t, "agent.args")
	if got := argumentOf(t, args, "--resume"); got != "implementer-session" {
		t.Errorf("--resume = %q, want the session of the Implementer", got)
	}
	text := promptOf(t, args)
	requireIssueOfTheRun(t, text, 10, "implementation issue")
	for _, want := range []string{"Request: owner review fix", "Pull request: #21",
		"Review: https://github.com/example-org/example-repo/pull/21#pullrequestreview-7",
		"Branch: cumin/10-add-the-login-screen", issueOwnerLoginLine} {
		if !strings.Contains(text, want) {
			t.Errorf("the request text has no %q:\n%s", want, text)
		}
	}
	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
		t.Errorf("%d merge requests, want none", n)
	}
	for _, want := range []string{`"msg":"send back for changes: a Maintainer requested changes; the issue goes back to the Implementer"`,
		`"msg":"send back for changes: requested the work"`, `"kind":"owner review fix"`} {
		if !strings.Contains(sc.logs.String(), want) {
			t.Errorf("the log has no %s", want)
		}
	}
}

// Requests for changes that do not count send no request and change no
// label.
func TestRequestsForChangesThatDoNotCountSendNoRequest(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(sc *scene)
	}{
		{"a request for changes on an older commit", func(sc *scene) {
			sc.review(theMaintainer, false, "CHANGES_REQUESTED", olderCommit, 5)
		}},
		{"a request for changes of a bot", func(sc *scene) {
			sc.review(implementerSlug, true, "CHANGES_REQUESTED", sc.remoteHead, 5)
		}},
		{"a request for changes of an account without write permission", func(sc *scene) {
			sc.review("someone", false, "CHANGES_REQUESTED", sc.remoteHead, 5)
		}},
		{"a request for changes of a bot account that has write permission", func(sc *scene) {
			sc.fake.SetPermission("writer-bot", "write", "Bot")
			sc.review("writer-bot", false, "CHANGES_REQUESTED", sc.remoteHead, 5)
		}},
		{"a comment-only review of the Maintainer", func(sc *scene) {
			sc.review(theMaintainer, false, "COMMENTED", sc.remoteHead, 5)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := awaitingMaintainer(t)
			tc.setup(sc)
			service := sc.service()

			for range 2 {
				sc.pollAndWait(t, service)
			}

			if n := sc.agentRuns(t); n != 0 {
				t.Errorf("%d agent runs, want none", n)
			}
			if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/medium", workflow.LabelAwaitingMergeDecision}) {
				t.Errorf("labels of #10 = %v, want risk/medium and cumin/status/awaiting-merge-decision", got)
			}
		})
	}
}

// A label that does not change sends no request; the next poll sends it.
func TestWithoutTheLabelChangeNoRequestIsSent(t *testing.T) {
	sc := awaitingMaintainer(t)
	sc.review(theMaintainer, false, "CHANGES_REQUESTED", sc.remoteHead, 5)
	service := sc.service()
	sc.fake.FailNext(http.MethodPut, issue10Path+"/labels", http.StatusBadGateway)

	if err := service.Poll(context.Background()); err == nil {
		t.Error("the poll reported no error after a label change that failed")
	}
	service.Wait()

	if n := sc.agentRuns(t); n != 0 {
		t.Fatalf("%d agent runs, want none without the label change", n)
	}

	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want one fix after the next poll", n)
	}
}

// After the fix run ends with done, the issue passes the verification ("wait for the checks"),
// the required checks, and the review, and waits for the Maintainer again ("ask for the merge decision").
// The request for changes is then on an older commit and sends nothing
// more. An approval of the Maintainer on the new head is merged ("start the merge"). The
// review after the fix is round 1: the rounds count again from the last
// APPROVE of the Reviewer.
func TestAfterTheFixTheMaintainerDecidesAgainAndAnApprovalIsMerged(t *testing.T) {
	// Run 1 is the fix, which pushes a new head; run 2 is the review.
	sc := awaitingMaintainer(t, cliOptions{movesHeadOnRun: 1, reviews: []string{"NONE", "APPROVE"}})
	oldHead := sc.remoteHead
	// The Reviewer asked for changes once before it approved.
	sc.review(implementerSlug, true, "CHANGES_REQUESTED", olderCommit, 30)
	sc.review(implementerSlug, true, "APPROVED", oldHead, 20)
	sc.review(theMaintainer, false, "CHANGES_REQUESTED", oldHead, 5)
	service := sc.serviceWithSession(t)

	for range 3 {
		sc.pollAndWait(t, service)
	}

	if n := sc.agentRuns(t); n != 2 {
		t.Fatalf("%d agent runs, want the fix and one review", n)
	}
	newHead := sc.repo.PullRequests[21].HeadCommit
	if newHead == oldHead {
		t.Fatal("the fix did not move the head of the pull request")
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/medium", workflow.LabelAwaitingMergeDecision}) {
		t.Fatalf("labels of #10 = %v, want risk/medium and cumin/status/awaiting-merge-decision", got)
	}
	for _, want := range []string{`"msg":"wait for the checks: verified the pull request"`, `"msg":"ask for the merge decision: the Reviewer approved the head commit"`,
		`"msg":"ask for the merge decision: the merge waits for a Maintainer"`} {
		if !strings.Contains(sc.logs.String(), want) {
			t.Errorf("the log has no %s", want)
		}
	}
	requested := ""
	for line := range strings.Lines(sc.logs.String()) {
		if strings.Contains(line, `"msg":"request the review: requested the review"`) {
			requested = line
		}
	}
	if !strings.Contains(requested, `"round":1,`) {
		t.Errorf("the review after the fix is not round 1: %s", requested)
	}
	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
		t.Fatalf("%d merge requests before the approval of the Maintainer, want none", n)
	}

	sc.repo.PullRequests[21].Reviews = append(sc.repo.PullRequests[21].Reviews, githubtest.Review{
		Author: theMaintainer, State: "APPROVED", Commit: newHead, SubmittedAt: sceneNow.Add(time.Hour)})
	sc.pollTimes(t, service, 2)

	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 1 {
		t.Errorf("%d merge requests, want 1", n)
	}
	if n := sc.agentRuns(t); n != 2 {
		t.Errorf("%d agent runs, want no run after the approval", n)
	}
}

// One request for changes sends the pull request back once. The Implementer
// answers the review without a commit, so the head stays and the review
// still stands on it when the issue waits for the Maintainer again ("ask for the merge decision"). The next
// polls send no request for that review; a new request for changes of the
// Maintainer sends one.
func TestAnAnswerWithoutACommitSendsNoSecondRequestForTheSameReview(t *testing.T) {
	const sentBack = `"msg":"send back for changes: a Maintainer requested changes; the issue goes back to the Implementer"`
	// Run 1 is the answer, which pushes nothing; run 2 is the review.
	sc := awaitingMaintainer(t, cliOptions{reviews: []string{"NONE", "APPROVE"}})
	head := sc.remoteHead
	sc.review(theMaintainer, false, "CHANGES_REQUESTED", head, 5)
	service := sc.serviceWithSession(t)

	for range 5 {
		sc.pollAndWait(t, service)
	}

	if got := sc.repo.PullRequests[21].HeadCommit; got != head {
		t.Fatalf("the head of the pull request moved to %s", got)
	}
	if !strings.Contains(sc.logs.String(), `"msg":"ask for the merge decision: the merge waits for a Maintainer"`) {
		t.Fatal("the issue did not come back to the Maintainer after the answer")
	}
	if n := strings.Count(sc.logs.String(), sentBack); n != 1 {
		t.Errorf("%d send-backs for one request for changes, want 1", n)
	}
	if n := sc.agentRuns(t); n != 2 {
		t.Errorf("%d agent runs, want the answer and one review", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/medium", workflow.LabelAwaitingMergeDecision}) {
		t.Fatalf("labels of #10 = %v, want risk/medium and cumin/status/awaiting-merge-decision", got)
	}

	sc.repo.PullRequests[21].Reviews = append(sc.repo.PullRequests[21].Reviews, githubtest.Review{
		Author: theMaintainer, State: "CHANGES_REQUESTED", Commit: head, SubmittedAt: sceneNow.Add(time.Hour)})
	sc.pollAndWait(t, service)

	if n := strings.Count(sc.logs.String(), sentBack); n != 2 {
		t.Errorf("%d send-backs after a new request for changes, want 2", n)
	}
	if n := sc.agentRuns(t); n < 3 {
		t.Errorf("%d agent runs, want a run for the new request for changes", n)
	}
}

// A comment and cumin/status/ready on the issue still start the Implementer
// in a new session ("request the implementation"), also when the Maintainer requested changes on the head.
func TestAReadyOfTheMaintainerStillStartsTheImplementer(t *testing.T) {
	sc := awaitingMaintainer(t)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{
		Number: 10, Parent: 6, Title: subIssueTitle,
		Labels:      []string{"risk/medium", workflow.LabelAwaitingMergeDecision, workflow.LabelReady},
		LabelEvents: []githubtest.LabelEvent{readyBy(theMaintainer, 1)},
	})
	sc.review(theMaintainer, false, "CHANGES_REQUESTED", sc.remoteHead, 5)
	service := sc.serviceWithSession(t)

	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs, want one", n)
	}
	args := sc.record(t, "agent.args")
	if strings.Contains(args, "--resume") {
		t.Errorf("the run after cumin/status/ready resumed a session:\n%q", args)
	}
	if text := promptOf(t, args); !strings.Contains(text, "Request: continue") {
		t.Errorf("the run was not a continuation of 'request the implementation':\n%s", text)
	}
}

func TestMaintainerRequestedChanges_SendBackForChanges(t *testing.T) {
	const head = "2222222222222222222222222222222222222222"
	at := func(minutes int) time.Time { return time.Date(2026, 10, 1, 10, minutes, 0, 0, time.UTC) }
	maintainers := map[string]bool{"owner": true, "owner-two": true, "app[bot]": true}
	// The issue last got cumin/status/awaiting-merge-decision at minute 0,
	// before the reviews of the table.
	for _, tc := range []struct {
		name    string
		reviews []workflow.Review
		want    string
	}{
		{"a request for changes on the head", []workflow.Review{{Author: "owner", State: workflow.ReviewChangesRequested, Commit: head, SubmittedAt: at(1), URL: "review-1"}}, "review-1"},
		{"a request for changes from before the issue last waited for the Maintainer", []workflow.Review{{Author: "owner", State: workflow.ReviewChangesRequested, Commit: head, SubmittedAt: at(-1), URL: "review-1"}}, ""},
		{"a request for changes at the time of the label", []workflow.Review{{Author: "owner", State: workflow.ReviewChangesRequested, Commit: head, SubmittedAt: at(0), URL: "review-1"}}, ""},
		{"a new request for changes after an answered one", []workflow.Review{
			{Author: "owner", State: workflow.ReviewChangesRequested, Commit: head, SubmittedAt: at(-1), URL: "review-1"},
			{Author: "owner", State: workflow.ReviewChangesRequested, Commit: head, SubmittedAt: at(1), URL: "review-2"}}, "review-2"},
		{"an answered request for changes of a Maintainer and a new one of a person who is not a Maintainer", []workflow.Review{
			{Author: "owner", State: workflow.ReviewChangesRequested, Commit: head, SubmittedAt: at(-1), URL: "review-1"},
			{Author: "someone", State: workflow.ReviewChangesRequested, Commit: head, SubmittedAt: at(1), URL: "review-2"}}, ""},
		{"a request for changes on an older commit", []workflow.Review{{Author: "owner", State: workflow.ReviewChangesRequested, Commit: "old", SubmittedAt: at(1), URL: "review-1"}}, ""},
		{"a later approval of another Maintainer", []workflow.Review{
			{Author: "owner", State: workflow.ReviewChangesRequested, Commit: head, SubmittedAt: at(1), URL: "review-1"},
			{Author: "owner-two", State: workflow.ReviewApproved, Commit: head, SubmittedAt: at(2), URL: "review-2"}}, ""},
		{"a later request for changes after an approval", []workflow.Review{
			{Author: "owner", State: workflow.ReviewApproved, Commit: head, SubmittedAt: at(1), URL: "review-1"},
			{Author: "owner", State: workflow.ReviewChangesRequested, Commit: head, SubmittedAt: at(2), URL: "review-2"}}, "review-2"},
		{"a later comment does not count", []workflow.Review{
			{Author: "owner", State: workflow.ReviewChangesRequested, Commit: head, SubmittedAt: at(1), URL: "review-1"},
			{Author: "owner", State: workflow.ReviewCommented, Commit: head, SubmittedAt: at(2), URL: "review-2"}}, "review-1"},
		{"a comment-only review", []workflow.Review{{Author: "owner", State: workflow.ReviewCommented, Commit: head, SubmittedAt: at(1), URL: "review-1"}}, ""},
		{"a person who is not a Maintainer", []workflow.Review{{Author: "someone", State: workflow.ReviewChangesRequested, Commit: head, SubmittedAt: at(1), URL: "review-1"}}, ""},
		{"a bot never counts", []workflow.Review{{Author: "app[bot]", State: workflow.ReviewChangesRequested, Commit: head, SubmittedAt: at(1), URL: "review-1"}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			review, ok := workflow.MaintainerRequestedChanges(tc.reviews, head, maintainers, at(0))
			if ok != (tc.want != "") || review.URL != tc.want {
				t.Errorf("MaintainerRequestedChanges = %q, %v; want %q", review.URL, ok, tc.want)
			}
		})
	}
	t.Run("the time of the label is not known", func(t *testing.T) {
		reviews := []workflow.Review{{Author: "owner", State: workflow.ReviewChangesRequested, Commit: head, SubmittedAt: at(1), URL: "review-1"}}
		if review, ok := workflow.MaintainerRequestedChanges(reviews, head, maintainers, time.Time{}); ok {
			t.Errorf("MaintainerRequestedChanges = %q, true; want no review without the time of the label", review.URL)
		}
	})
}

// "request a conflict resolution" (issue-states.md): a pull request that conflicts while its issue
// waits for the Maintainer's review goes back to the Implementer. The label
// becomes cumin/status/implementing before the request, and exactly one
// conflict resolution request resumes the Implementer session across
// polls. cumin calls no merge.
func TestAConflictWhileTheMaintainerDecidesSendsOneResolutionRequest(t *testing.T) {
	sc := awaitingMaintainer(t, cliOptions{holds: true})
	sc.review(implementerSlug, true, "APPROVED", sc.remoteHead, 30)
	sc.fake.SetPullRequestMergeable(sc.repo, 21, "CONFLICTING")
	sc.repo.Issues[10].LabelEvents = append([]githubtest.LabelEvent{readyBy(theMaintainer, 60)}, sc.repo.Issues[10].LabelEvents...)
	service := sc.serviceWithSession(t)
	ctx := context.Background()

	if err := service.Poll(ctx); err != nil {
		t.Fatalf("poll 1: %v", err)
	}
	waitForAgentRun(t, sc)
	for i := range 2 {
		if err := service.Poll(ctx); err != nil {
			t.Fatalf("poll %d: %v", i+2, err)
		}
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/medium", workflow.LabelImplementing}) {
		t.Errorf("labels of #10 = %v, want risk/medium and cumin/status/implementing", got)
	}
	sc.release(t)
	service.Wait()

	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs, want one resolution", n)
	}
	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
		t.Errorf("%d merge requests, want none", n)
	}
	args := sc.record(t, "agent.args")
	if got := argumentOf(t, args, "--resume"); got != "implementer-session" {
		t.Errorf("--resume = %q, want the session of the Implementer", got)
	}
	text := promptOf(t, args)
	for _, want := range []string{"Request: conflict resolution", "Pull request: #21",
		"The pull request #21 has merge conflicts with the default branch main", issueOwnerLoginLine} {
		if !strings.Contains(text, want) {
			t.Errorf("the request text has no %q:\n%s", want, text)
		}
	}
	logs := sc.logs.String()
	label := strings.Index(logs, `"msg":"request a conflict resolution: the pull request conflicts with the default branch; the issue goes back to the Implementer"`)
	request := strings.Index(logs, `"msg":"request a conflict resolution: requested the work"`)
	if label < 0 || request < 0 || request < label {
		t.Errorf("the log does not show the label change of 'request a conflict resolution' before the request (label at %d, request at %d)", label, request)
	}
}

// "request a conflict resolution": UNKNOWN says that GitHub is still calculating, and MERGEABLE says
// that nothing conflicts. Both send nothing and change no label: the issue
// keeps waiting for the Maintainer.
func TestUnknownAndMergeableLeaveTheIssueWaitingForTheMaintainer(t *testing.T) {
	for _, mergeable := range []string{"UNKNOWN", "MERGEABLE"} {
		t.Run(mergeable, func(t *testing.T) {
			sc := awaitingMaintainer(t)
			sc.review(implementerSlug, true, "APPROVED", sc.remoteHead, 30)
			sc.fake.SetPullRequestMergeable(sc.repo, 21, mergeable)
			service := sc.serviceWithSession(t)

			for range 2 {
				sc.pollAndWait(t, service)
			}

			if n := sc.agentRuns(t); n != 0 {
				t.Errorf("%d agent runs, want none", n)
			}
			if n := sc.fake.CountRequests(http.MethodPut, issue10Path+"/labels"); n != 0 {
				t.Errorf("%d label changes, want none", n)
			}
			if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelAwaitingMergeDecision) {
				t.Errorf("labels of #10 = %v, want cumin/status/awaiting-merge-decision", got)
			}
		})
	}
}

// A request for changes of the Maintainer on a conflicting head goes through
// "send back for changes": the poll sends the fix of the Maintainer's review, and no conflict
// resolution of "request a conflict resolution" beside it.
func TestARequestForChangesOfTheMaintainerOnAConflictingHeadIsSentBackForChanges(t *testing.T) {
	sc := awaitingMaintainer(t, cliOptions{holds: true})
	sc.review(theMaintainer, false, "CHANGES_REQUESTED", sc.remoteHead, 5)
	sc.fake.SetPullRequestMergeable(sc.repo, 21, "CONFLICTING")
	sc.repo.Issues[10].LabelEvents = append([]githubtest.LabelEvent{readyBy(theMaintainer, 60)}, sc.repo.Issues[10].LabelEvents...)
	service := sc.serviceWithSession(t)

	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	waitForAgentRun(t, sc)
	sc.release(t)
	service.Wait()

	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs, want one fix of the Maintainer's review", n)
	}
	if text := promptOf(t, sc.record(t, "agent.args")); strings.Contains(text, "Request: conflict resolution") {
		t.Errorf("the request is a conflict resolution, want the fix of the Maintainer's review:\n%s", text)
	}
	if logs := sc.logs.String(); strings.Contains(logs, `"msg":"request a conflict resolution: requested the work"`) {
		t.Error("'request a conflict resolution' sent a request beside 'send back for changes'")
	}
}

// An approval of the Maintainer on a conflicting head whose required checks do
// not pass leaves "start the merge" waiting, so "request a conflict resolution" sends the conflict resolution at the
// same poll: the checks of a conflicting pull request do not run again.
func TestAnApprovalThatWaitsForTheChecksOnAConflictingHeadSendsTheResolution(t *testing.T) {
	sc := awaitingMaintainer(t, cliOptions{holds: true})
	sc.repo.PullRequests[21].Checks = []githubtest.Check{{Name: "ci", Status: "IN_PROGRESS"}}
	sc.review(theMaintainer, false, "APPROVED", sc.remoteHead, 5)
	sc.fake.SetPullRequestMergeable(sc.repo, 21, "CONFLICTING")
	sc.repo.Issues[10].LabelEvents = append([]githubtest.LabelEvent{readyBy(theMaintainer, 60)}, sc.repo.Issues[10].LabelEvents...)
	service := sc.serviceWithSession(t)

	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	waitForAgentRun(t, sc)
	sc.release(t)
	service.Wait()

	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs, want one resolution", n)
	}
	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
		t.Errorf("%d merge requests, want none", n)
	}
	if text := promptOf(t, sc.record(t, "agent.args")); !strings.Contains(text, "Request: conflict resolution") {
		t.Errorf("the request is not a conflict resolution:\n%s", text)
	}
}
