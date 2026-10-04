package workflow_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

const pull21Path = "/repos/example-org/example-repo/pulls/21"

// assertMergeStepIsKept checks that #10 waits with a kept step of the merge
// step: the labels stay, no comment is written, the issue counts as in
// work, and the log names the kept step that many times.
func assertMergeStepIsKept(t *testing.T, sc *scene, service *workflow.Service, labels []string, step string, times int) {
	t.Helper()
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, labels) {
		t.Errorf("labels of #10 = %v, want %v while the step is kept", got, labels)
	}
	if comments := sc.fake.Comments(sc.repo, 10); len(comments) != 0 {
		t.Errorf("%d comments on #10, want none while the step is kept: %+v", len(comments), comments)
	}
	if got := workflow.InProgressIssues(service); !slices.Equal(got, []string{"example-org/example-repo#10"}) {
		t.Errorf("issues in work = %v, want #10 while the step is kept", got)
	}
	if n := strings.Count(sc.logs.String(), `"msg":"kept `+step+` after a temporary failure: a later poll runs it again"`); n != times {
		t.Errorf("%d log lines for the kept step %q, want %d:\n%s", n, step, times, sc.logs.String())
	}
}

// assertMergedAndClosedOnce checks the end of the merge step for #10: the
// pull request is merged with that many merge requests, each at the
// approved head commit, the issue is closed as completed, no comment is
// written, and the issue is no longer in work.
func assertMergedAndClosedOnce(t *testing.T, sc *scene, service *workflow.Service, merges int) {
	t.Helper()
	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != merges {
		t.Errorf("%d merge requests, want %d", n, merges)
	}
	for _, r := range sc.fake.Requests() {
		if r.Method == http.MethodPut && r.Path == mergePath && !strings.Contains(string(r.Body), `"sha":"`+sc.remoteHead+`"`) {
			t.Errorf("a merge request does not name the approved head commit: %s", r.Body)
		}
	}
	if !sc.repo.PullRequests[21].Merged {
		t.Error("pull request #21 is not merged")
	}
	if issue := sc.fake.Issue(sc.repo, 10); !issue.Closed || issue.StateReason != "completed" {
		t.Errorf("issue #10: closed %v, reason %q; want closed as completed", issue.Closed, issue.StateReason)
	}
	if comments := sc.fake.Comments(sc.repo, 10); len(comments) != 0 {
		t.Errorf("%d comments on #10, want none: %+v", len(comments), comments)
	}
	if messages := sc.messagesExceptQ4(); len(messages) != 0 {
		t.Errorf("notifications = %v, want none", messages)
	}
	if got := workflow.InProgressIssues(service); len(got) != 0 {
		t.Errorf("issues in work = %v, want none after the merge step", got)
	}
}

// Core-28 (cumin-core.md), I6: the fake GitHub answers the merge with 502.
// cumin keeps the merge: the label stays, and no comment is written. A
// poll before the delay sends no merge. The poll after the delay tries
// again, fails again, and waits once more. The next try merges the pull
// request at the approved head commit, and the issue is closed.
func TestKeptStep_AMergeThatFailsWith502IsMergedAtALaterPoll(t *testing.T) {
	sc := approved(t, "risk/low")
	sc.fake.FailTimes(http.MethodPut, mergePath, 0, 2, http.StatusBadGateway)
	service := sc.service()
	reviewing := []string{"risk/low", workflow.LabelReviewing}

	sc.pollAndWait(t, service)
	assertMergeStepIsKept(t, sc, service, reviewing, "the merge", 1)
	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 1 {
		t.Errorf("%d merge requests, want 1: a write is not sent again at once", n)
	}

	if err := pollAtMinute(sc, service, 4); err != nil {
		t.Fatalf("Poll at minute 4: %v", err)
	}
	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 1 {
		t.Errorf("%d merge requests at minute 4, want 1: no try before the delay", n)
	}

	if err := pollAtMinute(sc, service, 5); err != nil {
		t.Fatalf("Poll at minute 5: %v", err)
	}
	assertMergeStepIsKept(t, sc, service, reviewing, "the merge", 2)
	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 2 {
		t.Errorf("%d merge requests at minute 5, want 2", n)
	}
	if sc.repo.PullRequests[21].Merged {
		t.Error("pull request #21 is merged after two failed tries")
	}

	if err := pollAtMinute(sc, service, 10); err != nil {
		t.Fatalf("Poll at minute 10: %v", err)
	}
	assertMergedAndClosedOnce(t, sc, service, 3)
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1: the review", n)
	}
}

// The merge cannot be undone, and its answer can get lost: the fake merges
// the pull request and drops the answer. The try of the kept step reads
// the pull request first, sends no second merge, and closes the issue.
func TestKeptStep_AMergeWhoseAnswerIsLostIsNotSentTwice(t *testing.T) {
	sc := approved(t, "risk/low")
	sc.fake.DropAnswers(http.MethodPut, mergePath, 1)
	service := sc.service()

	sc.pollAndWait(t, service)
	assertMergeStepIsKept(t, sc, service, []string{"risk/low", workflow.LabelReviewing}, "the merge", 1)
	if !sc.repo.PullRequests[21].Merged {
		t.Fatal("pull request #21 is not merged: the fake did not handle the merge")
	}
	if sc.fake.Issue(sc.repo, 10).Closed {
		t.Error("issue #10 is closed while the merge is kept")
	}
	if n := sc.fake.CountRequests(http.MethodGet, pull21Path); n != 0 {
		t.Errorf("%d reads of the pull request at the first try, want none", n)
	}

	if err := pollAtMinute(sc, service, 5); err != nil {
		t.Fatalf("Poll at minute 5: %v", err)
	}
	assertMergedAndClosedOnce(t, sc, service, 1)
	if !strings.Contains(sc.logs.String(), `"msg":"I6: the pull request is merged already; no second merge is sent"`) {
		t.Errorf("the log does not say that the pull request is merged already:\n%s", sc.logs.String())
	}
}

// A temporary failure of the read of the pull request, at a try of the
// kept merge, keeps the step again: no merge is sent before cumin knows
// whether the pull request is merged.
func TestKeptStep_AFailedReadOfThePullRequestSendsNoMerge(t *testing.T) {
	sc := approved(t, "risk/low")
	sc.fake.DropAnswers(http.MethodPut, mergePath, 1)
	service := sc.service()
	sc.pollAndWait(t, service)

	sc.fake.FailTimes(http.MethodGet, pull21Path, 0, everyTry, http.StatusBadGateway)
	if err := pollAtMinute(sc, service, 5); err != nil {
		t.Fatalf("Poll at minute 5: %v", err)
	}
	assertMergeStepIsKept(t, sc, service, []string{"risk/low", workflow.LabelReviewing}, "the merge", 2)

	if err := pollAtMinute(sc, service, 10); err != nil {
		t.Fatalf("Poll at minute 10: %v", err)
	}
	assertMergedAndClosedOnce(t, sc, service, 1)
}

// A temporary failure of the read or of the close after the merge keeps
// the close: no comment is written, no second merge is sent, and the issue
// is closed at a later poll.
func TestKeptStep_AFailedReadOrCloseAfterTheMergeClosesTheIssueAtALaterPoll(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail func(sc *scene)
	}{
		{"every try of the read answers 502", func(sc *scene) {
			sc.fake.FailTimes(http.MethodGet, issue10Path, 0, everyTry, http.StatusBadGateway)
		}},
		{"the close answers 502", func(sc *scene) {
			sc.fake.FailTimes(http.MethodPatch, issue10Path, 0, 1, http.StatusBadGateway)
		}},
		{"the answer of the close is lost", func(sc *scene) {
			sc.fake.DropAnswers(http.MethodPatch, issue10Path, 1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := approved(t, "risk/low")
			tc.fail(sc)
			service := sc.service()

			sc.pollAndWait(t, service)
			assertMergeStepIsKept(t, sc, service, []string{"risk/low", workflow.LabelReviewing}, "the close of the issue after the merge", 1)
			if !sc.repo.PullRequests[21].Merged {
				t.Error("pull request #21 is not merged")
			}

			if err := pollAtMinute(sc, service, 4); err != nil {
				t.Fatalf("Poll at minute 4: %v", err)
			}
			assertMergeStepIsKept(t, sc, service, []string{"risk/low", workflow.LabelReviewing}, "the close of the issue after the merge", 1)

			if err := pollAtMinute(sc, service, 5); err != nil {
				t.Fatalf("Poll at minute 5: %v", err)
			}
			assertMergedAndClosedOnce(t, sc, service, 1)
		})
	}
}

// A head that moved (409) is no temporary failure: the issue stops for the
// Owner with one comment, and no step is kept.
func TestI6_AHeadThatMovedStopsTheIssueOnce(t *testing.T) {
	sc := approved(t, "risk/low")
	sc.fake.FailNext(http.MethodPut, mergePath, http.StatusConflict)
	service := sc.service()

	sc.pollAndWait(t, service)

	sc.assertStoppedAtI6(t, workflow.MergeHeadMovedReason(21))
	if got := workflow.InProgressIssues(service); len(got) != 0 {
		t.Errorf("issues in work = %v, want none: no step is kept", got)
	}
	if err := pollAtMinute(sc, service, 5); err != nil {
		t.Fatalf("Poll at minute 5: %v", err)
	}
	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 1 {
		t.Errorf("%d merge requests, want 1", n)
	}
	if comments := sc.fake.Comments(sc.repo, 10); len(comments) != 1 {
		t.Errorf("%d comments on #10, want 1", len(comments))
	}
}

// I12 uses the same merge step: a merge after the approval of the Owner
// that answers 502 is kept, and a later poll merges the pull request once.
func TestKeptStep_AMergeAfterTheApprovalOfTheOwnerIsKeptToo(t *testing.T) {
	sc := awaitingOwner(t)
	sc.review(theOwner, false, "APPROVED", sc.remoteHead, 5)
	sc.fake.FailTimes(http.MethodPut, mergePath, 0, 1, http.StatusBadGateway)
	service := sc.service()
	waiting := []string{"risk/medium", workflow.LabelAwaitingOwnerReview}

	sc.pollAndWait(t, service)
	assertMergeStepIsKept(t, sc, service, waiting, "the merge", 1)

	if err := pollAtMinute(sc, service, 4); err != nil {
		t.Fatalf("Poll at minute 4: %v", err)
	}
	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 1 {
		t.Errorf("%d merge requests at minute 4, want 1: the issue in work is no candidate of I12", n)
	}

	if err := pollAtMinute(sc, service, 5); err != nil {
		t.Fatalf("Poll at minute 5: %v", err)
	}
	assertMergedAndClosedOnce(t, sc, service, 2)
}

// The decision to merge is older than a try of the kept merge. The Owner
// changes risk/low to risk/medium while the merge is kept: the try decides
// again, sends no merge, and asks the Owner (I7) with one notification.
func TestKeptStep_ARiskLabelThatChangesWhileTheMergeIsKeptSendsNoMerge(t *testing.T) {
	sc := approved(t, "risk/low")
	sc.fake.FailTimes(http.MethodPut, mergePath, 0, 1, http.StatusBadGateway)
	service := sc.service()
	sc.pollAndWait(t, service)
	assertMergeStepIsKept(t, sc, service, []string{"risk/low", workflow.LabelReviewing}, "the merge", 1)

	if err := sc.fake.SetLabels(sc.repo, 10, []string{"risk/medium", workflow.LabelReviewing}); err != nil {
		t.Fatal(err)
	}
	if err := pollAtMinute(sc, service, 5); err != nil {
		t.Fatalf("Poll at minute 5: %v", err)
	}

	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 1 {
		t.Errorf("%d merge requests, want 1: the kept try sends no merge", n)
	}
	if sc.repo.PullRequests[21].Merged {
		t.Error("pull request #21 is merged after the risk changed to risk/medium")
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/medium", workflow.LabelAwaitingOwnerReview}) {
		t.Errorf("labels of #10 = %v, want risk/medium and cumin/status/awaiting-owner-review", got)
	}
	if messages := sc.messagesExceptQ4(); len(messages) != 1 || !strings.Contains(messages[0], "the merge needs a decision") {
		t.Errorf("notifications = %v, want one of I7", messages)
	}
	if got := workflow.InProgressIssues(service); len(got) != 0 {
		t.Errorf("issues in work = %v, want none after the kept merge ended", got)
	}
}

// The Owner requests changes on the same head commit while the merge of
// I12 is kept: the request cancels the approval, and the kept try sends no
// merge.
func TestKeptStep_ARequestForChangesOfTheOwnerWhileTheMergeIsKeptSendsNoMerge(t *testing.T) {
	sc := awaitingOwner(t)
	sc.review(theOwner, false, "APPROVED", sc.remoteHead, 5)
	sc.fake.FailTimes(http.MethodPut, mergePath, 0, 1, http.StatusBadGateway)
	service := sc.serviceWithSession(t)
	sc.pollAndWait(t, service)
	assertMergeStepIsKept(t, sc, service, []string{"risk/medium", workflow.LabelAwaitingOwnerReview}, "the merge", 1)

	sc.review(theOwner, false, "CHANGES_REQUESTED", sc.remoteHead, -1)
	if err := pollAtMinute(sc, service, 5); err != nil {
		t.Fatalf("Poll at minute 5: %v", err)
	}

	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 1 {
		t.Errorf("%d merge requests, want 1: the kept try sends no merge", n)
	}
	if sc.repo.PullRequests[21].Merged {
		t.Error("pull request #21 is merged after the Owner requested changes")
	}
	if !strings.Contains(sc.logs.String(), `"msg":"I12: no approval of an Owner on the head commit any more"`) {
		t.Errorf("the log does not say that the approval is gone:\n%s", sc.logs.String())
	}
}
