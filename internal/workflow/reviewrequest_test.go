package workflow_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

const reviewRequestPath = "/repos/example-org/example-repo/pulls/21/requested_reviewers"

// reviewRequests returns how many requests of a review cumin sent for #21.
func (sc *scene) reviewRequests() int {
	return sc.fake.CountRequests(http.MethodPost, reviewRequestPath)
}

// readyByTheMaintainer makes the Maintainer the account that added the newest
// cumin/status/ready to #10, so that cumin has an Issue Owner login.
func (sc *scene) readyByTheMaintainer() {
	sc.repo.Issues[10].LabelEvents = []githubtest.LabelEvent{readyBy(theMaintainer, 30)}
	sc.fake.SetPermission(theMaintainer, "admin", "User")
}

// assertWaitsForMaintainerWithOneNotification checks what "ask for the merge decision" leaves without the
// review request: the label, one notification that links the pull request,
// and no merge.
func assertWaitsForMaintainerWithOneNotification(t *testing.T, sc *scene) {
	t.Helper()
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/medium", workflow.LabelAwaitingMergeDecision}) {
		t.Errorf("labels of #10 = %v, want risk/medium and cumin/status/awaiting-merge-decision", got)
	}
	messages := sc.messagesExceptWaiting()
	if len(messages) != 1 || !strings.Contains(messages[0], "the merge needs a decision") || !strings.Contains(messages[0], "/pull/21") {
		t.Errorf("notifications = %v, want one that asks for the merge decision and links #21", messages)
	}
	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
		t.Errorf("%d merge requests, want none", n)
	}
}

// "ask for the merge decision" (issue-states.md): an approved risk/medium pull request gets exactly
// one request of a review, for the account that added the newest
// cumin/status/ready. The request follows the label change, and the polls
// that follow send no second one.
func TestTheReviewOfTheIssueOwnerIsRequestedOnce(t *testing.T) {
	sc := approved(t, "risk/medium")
	sc.repo.Issues[10].LabelEvents = []githubtest.LabelEvent{readyBy("an-earlier-owner", 60), removedBefore(readyBy(theMaintainer, 5)), readyBy(theMaintainer, 5)}
	sc.fake.SetPermission("an-earlier-owner", "admin", "User")
	sc.fake.SetPermission(theMaintainer, "write", "User")
	service := sc.service()

	sc.pollTimes(t, service, 3)

	assertWaitsForMaintainerWithOneNotification(t, sc)
	if got := sc.fake.RequestedReviewers(sc.repo, 21); !slices.Equal(got, []string{theMaintainer}) {
		t.Errorf("requested reviewers of #21 = %v, want only %s", got, theMaintainer)
	}
	if n := sc.reviewRequests(); n != 1 {
		t.Fatalf("%d requests of a review, want 1", n)
	}
	label, request := -1, -1
	for i, r := range sc.fake.Requests() {
		switch {
		case r.Method == http.MethodPut && strings.HasSuffix(r.Path, "/issues/10/labels") && strings.Contains(string(r.Body), workflow.LabelAwaitingMergeDecision):
			label = i
		case r.Method == http.MethodPost && r.Path == reviewRequestPath:
			request = i
			if !strings.Contains(string(r.Body), `"reviewers":["`+theMaintainer+`"]`) {
				t.Errorf("the request does not name only %s: %s", theMaintainer, r.Body)
			}
		}
	}
	if label < 0 || request < label {
		t.Errorf("the request (%d) does not follow the label change (%d)", request, label)
	}
	if want := `"msg":"ask for the merge decision: requested the review of the Issue Owner"`; !strings.Contains(sc.logs.String(), want) {
		t.Errorf("the log has no %s", want)
	}
}

// An approved risk/low pull request gets no request: cumin merges it.
func TestARiskLowPullRequestGetsNoReviewRequest(t *testing.T) {
	sc := approved(t, "risk/low")
	service := sc.service()

	sc.pollTimes(t, service, 3)

	if n := sc.reviewRequests(); n != 0 {
		t.Errorf("%d requests of a review, want none", n)
	}
	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 1 {
		t.Errorf("%d merge requests, want 1", n)
	}
}

// Without an Issue Owner login cumin sends no request, and a request that fails
// is only logged. The label and the one notification are as before.
func TestWithoutTheReviewRequestTheLabelAndTheNotificationAreAsBefore(t *testing.T) {
	tests := []struct {
		name     string
		prepare  func(sc *scene)
		requests int
		log      string
	}{
		{name: "no Issue Owner login", requests: 0,
			prepare: func(sc *scene) {
				sc.repo.Issues[10].LabelEvents = []githubtest.LabelEvent{readyBy("a-triager", 5)}
				// GitHub reports triage as read.
				sc.fake.SetPermission("a-triager", "read", "User")
			},
			log: `"msg":"ask for the merge decision: there is no Issue Owner login; the review of the Issue Owner is not requested"`},
		{name: "the request answers 422", requests: 1,
			prepare: func(sc *scene) {
				sc.readyByTheMaintainer()
				sc.fake.FailNext(http.MethodPost, reviewRequestPath, http.StatusUnprocessableEntity)
			},
			log: `"level":"ERROR","msg":"ask for the merge decision: the review of the Issue Owner was not requested; the notification still goes out"`},
		{name: "the request answers 500", requests: 1,
			prepare: func(sc *scene) {
				sc.readyByTheMaintainer()
				sc.fake.FailNext(http.MethodPost, reviewRequestPath, http.StatusInternalServerError)
			},
			log: `"level":"ERROR","msg":"ask for the merge decision: the review of the Issue Owner was not requested; the notification still goes out"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := approved(t, "risk/medium")
			tt.prepare(sc)
			service := sc.service()

			sc.pollTimes(t, service, 3)

			assertWaitsForMaintainerWithOneNotification(t, sc)
			if n := sc.reviewRequests(); n != tt.requests {
				t.Errorf("%d requests of a review, want %d: nothing is tried again", n, tt.requests)
			}
			if got := sc.fake.RequestedReviewers(sc.repo, 21); len(got) != 0 {
				t.Errorf("requested reviewers of #21 = %v, want none", got)
			}
			if !strings.Contains(sc.logs.String(), tt.log) {
				t.Errorf("the log has no %s", tt.log)
			}
		})
	}
}

// The request is no review: after the request alone, "start the merge" sends no merge.
// After the Maintainer approves the head commit, "start the merge" merges.
func TestTheReviewRequestAloneIsNotMergedAndTheApprovalOfTheMaintainerIs(t *testing.T) {
	sc := approved(t, "risk/medium")
	sc.readyByTheMaintainer()
	service := sc.service()

	sc.pollTimes(t, service, 3)

	if got := sc.fake.RequestedReviewers(sc.repo, 21); !slices.Equal(got, []string{theMaintainer}) {
		t.Fatalf("requested reviewers of #21 = %v, want %s", got, theMaintainer)
	}
	assertWaitsForMaintainerWithOneNotification(t, sc)

	sc.repo.PullRequests[21].Reviews = append(sc.repo.PullRequests[21].Reviews, githubtest.Review{
		Author: theMaintainer, State: "APPROVED", Commit: sc.remoteHead, SubmittedAt: sceneNow.Add(time.Hour)})
	sc.pollTimes(t, service, 2)

	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 1 {
		t.Errorf("%d merge requests after the approval of the Maintainer, want 1", n)
	}
	if n := sc.reviewRequests(); n != 1 {
		t.Errorf("%d requests of a review, want 1", n)
	}
}

// After a request for changes of the Maintainer ("send back for changes"), the fix, and a new
// approval of the Reviewer, "ask for the merge decision" holds again and cumin sends a second
// request.
func TestAfterTheFixAndANewApprovalTheReviewIsRequestedAgain(t *testing.T) {
	// Run 1 is the review, run 2 is the fix, which pushes a new head, and
	// run 3 is the review of the new head.
	sc := newScene(t, cliOptions{movesHeadOnRun: 2, reviews: []string{"APPROVE", "NONE", "APPROVE"}})
	sc.awaitingChecks(t, []string{"ci"}, []githubtest.Check{{Name: "ci", Conclusion: "SUCCESS"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{
		Number: 10, Parent: 6, Title: subIssueTitle,
		Labels: []string{"cumin/status/checking", "risk/medium"},
	})
	sc.readyByTheMaintainer()
	service := sc.serviceWithSession(t)

	sc.pollAndWait(t, service)

	if n := sc.reviewRequests(); n != 1 {
		t.Fatalf("%d requests of a review after the first approval, want 1", n)
	}
	oldHead := sc.repo.PullRequests[21].HeadCommit
	sc.repo.PullRequests[21].Reviews = append(sc.repo.PullRequests[21].Reviews, githubtest.Review{
		Author: theMaintainer, State: "CHANGES_REQUESTED", Commit: oldHead, SubmittedAt: sceneNow.Add(time.Hour)})
	sc.pollTimes(t, service, 4)

	if n := sc.agentRuns(t); n != 3 {
		t.Fatalf("%d agent runs, want the review, the fix, and the second review", n)
	}
	if sc.repo.PullRequests[21].HeadCommit == oldHead {
		t.Fatal("the fix did not move the head of the pull request")
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/medium", workflow.LabelAwaitingMergeDecision}) {
		t.Fatalf("labels of #10 = %v, want risk/medium and cumin/status/awaiting-merge-decision", got)
	}
	if n := sc.reviewRequests(); n != 2 {
		t.Errorf("%d requests of a review, want 2: one for each time that 'ask for the merge decision' holds", n)
	}
	if got := sc.fake.RequestedReviewers(sc.repo, 21); !slices.Equal(got, []string{theMaintainer}) {
		t.Errorf("requested reviewers of #21 = %v, want %s once", got, theMaintainer)
	}
}
