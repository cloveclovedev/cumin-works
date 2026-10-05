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

const pull21Path = "/repos/example-org/example-repo/pulls/21"

// mergingScene puts issue #10 in cumin/status/merging, as cumin-core left
// it: the risk label, the pull request #21 with one passed required check,
// and the approval of the Reviewer on the head commit. The Owner is an
// admin of the repository.
func mergingScene(t *testing.T, risk string) *scene {
	t.Helper()
	sc := newScene(t)
	sc.awaitingChecks(t, []string{"ci"}, []githubtest.Check{{Name: "ci", Conclusion: "SUCCESS"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{
		Number: 10, Parent: 6, Title: subIssueTitle,
		Labels:      []string{risk, workflow.LabelMerging},
		LabelEvents: []githubtest.LabelEvent{{Label: workflow.LabelMerging, At: sceneNow.Add(-10 * time.Minute), Actor: cuminSlug, ActorType: "Bot"}},
	})
	sc.review(implementerSlug, true, "APPROVED", sc.remoteHead, 30)
	sc.fake.SetPermission(theOwner, "admin", "User")
	return sc
}

// pollTimes runs that many polls, each to its end.
func (sc *scene) pollTimes(t *testing.T, service *workflow.Service, times int) {
	t.Helper()
	for range times {
		sc.pollAndWait(t, service)
	}
}

// assertMergedAndClosedOnce checks the end of the merge for #10: the pull
// request is merged with that many merge requests, each at the approved
// head commit, the issue is closed as completed, no comment is written,
// and the issue is no longer in work.
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
	if n := sc.fake.CountRequests(http.MethodPatch, issue10Path); n != 1 {
		t.Errorf("%d closes of #10, want 1", n)
	}
	if comments := sc.fake.Comments(sc.repo, 10); len(comments) != 0 {
		t.Errorf("%d comments on #10, want none: %+v", len(comments), comments)
	}
	// The close of the last sub-issue starts the acceptance check of #6. The
	// fake agent leaves no comment, so that check may stop for the Owner in
	// the same poll. That notification is not one of the merge.
	messages := slices.DeleteFunc(sc.messagesExceptQ4(), func(message string) bool {
		return strings.Contains(message, workflow.NoAcceptanceCheckReason)
	})
	if len(messages) != 0 {
		t.Errorf("notifications = %v, want none", messages)
	}
	if got := workflow.InProgressIssues(service); len(got) != 0 {
		t.Errorf("issues in work = %v, want none after the merge", got)
	}
}

// The merge cannot be undone, and its answer can get lost: the fake merges
// the pull request and drops the answer. The issue keeps
// cumin/status/merging. The next poll reads the merged pull request, sends
// no second merge, and closes the issue.
func TestMerging_AMergeWhoseAnswerIsLostIsSentOnce(t *testing.T) {
	sc := mergingScene(t, "risk/low")
	sc.fake.DropAnswers(http.MethodPut, mergePath, 1)
	service := sc.service()

	_ = service.Poll(t.Context())
	service.Wait()
	if !sc.repo.PullRequests[21].Merged {
		t.Fatal("pull request #21 is not merged: the fake did not handle the merge")
	}
	if got := sc.fake.Issue(sc.repo, 10); got.Closed || !slices.Equal(got.Labels, []string{"risk/low", workflow.LabelMerging}) {
		t.Errorf("issue #10: closed %v, labels %v; want open in cumin/status/merging after the lost answer", got.Closed, got.Labels)
	}

	sc.pollTimes(t, service, 2)

	assertMergedAndClosedOnce(t, sc, service, 1)
	if !strings.Contains(sc.logs.String(), `"msg":"close the merged issue: closed the issue that GitHub left open after the merge"`) {
		t.Errorf("the log does not name the close of the merged issue:\n%s", sc.logs.String())
	}
}

// A restart of cumin in cumin/status/merging: a new cumin holds nothing in
// memory. It reads the pull request, merges the approved head commit, and
// closes the issue at the next poll.
func TestMerging_ARestartMergesTheApprovedHeadCommit(t *testing.T) {
	sc := mergingScene(t, "risk/low")
	service := sc.service()

	sc.pollAndWait(t, service)
	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 1 {
		t.Fatalf("%d merge requests after the first poll, want 1", n)
	}
	if got := sc.mergeMethod(t); got != "squash" {
		t.Errorf("merge method = %q, want squash (the default merge_method)", got)
	}
	sc.pollTimes(t, service, 2)

	assertMergedAndClosedOnce(t, sc, service, 1)
}

// A restart of cumin after the merge: the pull request is merged, and
// GitHub left the issue open. cumin sends no merge, and closes the issue.
func TestMerging_ARestartAfterTheMergeClosesTheIssue(t *testing.T) {
	sc := mergingScene(t, "risk/low")
	pr := sc.repo.PullRequests[21]
	pr.Closed, pr.Merged = true, true
	service := sc.service()

	sc.pollTimes(t, service, 2)

	assertMergedAndClosedOnce(t, sc, service, 0)
}

// An issue that GitHub closed through the link is left as it is.
func TestMerging_AnIssueThatGitHubClosedIsLeftAsItIs(t *testing.T) {
	sc := mergingScene(t, "risk/low")
	sc.fake.CloseIssuesOnMerge()
	service := sc.service()

	sc.pollTimes(t, service, 3)

	if !sc.fake.Issue(sc.repo, 10).Closed {
		t.Error("issue #10 is open")
	}
	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 1 {
		t.Errorf("%d merge requests, want 1", n)
	}
	if n := sc.fake.CountRequests(http.MethodPatch, issue10Path); n != 0 {
		t.Errorf("%d closes of #10, want none", n)
	}
}

// GitHub refuses a merge right after another merge with 405 "Base branch
// was modified". The issue keeps cumin/status/merging and gets no comment,
// and the next poll sends the merge again.
func TestMerging_ABaseBranchThatWasModifiedIsMergedAtTheNextPoll(t *testing.T) {
	sc := mergingScene(t, "risk/low")
	sc.fake.RefuseMergesForBaseBranch(1)
	service := sc.service()

	sc.pollAndWait(t, service)

	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelMerging}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/merging after the refusal", got)
	}
	if comments := sc.fake.Comments(sc.repo, 10); len(comments) != 0 {
		t.Errorf("%d comments on #10, want none: %+v", len(comments), comments)
	}
	if messages := sc.messagesExceptQ4(); len(messages) != 0 {
		t.Errorf("notifications = %v, want none", messages)
	}
	if sc.repo.PullRequests[21].Merged {
		t.Fatal("pull request #21 is merged by a refused merge")
	}

	sc.pollTimes(t, service, 2)

	assertMergedAndClosedOnce(t, sc, service, 2)
}

// A temporary failure of the merge (502) changes nothing: the issue keeps
// cumin/status/merging, and the next poll sends the merge again.
func TestMerging_AMergeThatFailsWith502IsSentAgainAtTheNextPoll(t *testing.T) {
	sc := mergingScene(t, "risk/low")
	sc.fake.FailTimes(http.MethodPut, mergePath, 0, 1, http.StatusBadGateway)
	service := sc.service()

	_ = service.Poll(t.Context())
	service.Wait()
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelMerging}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/merging after the 502", got)
	}

	sc.pollTimes(t, service, 2)

	assertMergedAndClosedOnce(t, sc, service, 2)
}

// The Owner approved a risk/medium pull request, and then requests changes
// while the issue is in cumin/status/merging. The approval no longer holds:
// "go back to the checks", and cumin sends no merge.
func TestMerging_AChangeRequestOfTheOwnerGoesBackToTheChecksWithoutAMerge(t *testing.T) {
	sc := mergingScene(t, "risk/medium")
	sc.review(theOwner, false, "APPROVED", sc.remoteHead, 5)
	sc.review(theOwner, false, "CHANGES_REQUESTED", sc.remoteHead, 1)
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
		t.Errorf("%d merge requests, want none", n)
	}
	if sc.repo.PullRequests[21].Merged {
		t.Error("pull request #21 is merged after the Owner requested changes")
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/medium", workflow.LabelChecking}) {
		t.Errorf("labels of #10 = %v, want risk/medium and cumin/status/checking", got)
	}
	if !strings.Contains(sc.logs.String(), `"msg":"go back to the checks: the conditions of the merge do not hold; no merge is sent"`) {
		t.Errorf("the log does not name the way back to the checks:\n%s", sc.logs.String())
	}
}

// The label cumin/status/merging never stands in for the conditions of the
// merge: a risk/medium issue without an approval of an Owner, a failed
// required check, and a head commit that moved after the approval all go
// back to the checks without a merge.
func TestMerging_TheLabelDoesNotStandInForTheConditionsOfTheMerge(t *testing.T) {
	cases := map[string]func(sc *scene){
		"risk/medium without an approval of an Owner": func(sc *scene) {
			_ = sc.fake.SetLabels(sc.repo, 10, []string{"risk/medium", workflow.LabelMerging})
		},
		"a required check that failed": func(sc *scene) {
			sc.repo.PullRequests[21].Checks = []githubtest.Check{{Name: "ci", Conclusion: "FAILURE"}}
		},
		"a head commit after the approval": func(sc *scene) {
			sc.repo.PullRequests[21].HeadCommit = olderCommit
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			sc := mergingScene(t, "risk/low")
			change(sc)
			service := sc.service()

			sc.pollAndWait(t, service)

			if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
				t.Errorf("%d merge requests, want none", n)
			}
			if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelChecking) {
				t.Errorf("labels of #10 = %v, want cumin/status/checking", got)
			}
		})
	}
}

// A cumin/status/merging that an account with only triage permission added
// is not a state (issue-states.md, the account that added a status label):
// no agent starts, nothing is merged, no label changes, and the Owner is
// told once.
func TestMerging_ALabelOfAnAccountWithTriagePermissionDoesNothing(t *testing.T) {
	sc := mergingScene(t, "risk/low")
	sc.repo.Issues[10].LabelEvents = []githubtest.LabelEvent{statusBy(workflow.LabelMerging, "a-triager")}
	sc.fake.SetPermission("a-triager", "triage", "User")
	service := sc.service()

	sc.pollTimes(t, service, 3)

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none", n)
	}
	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
		t.Errorf("%d merge requests, want none", n)
	}
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != 0 {
		t.Errorf("%d label changes of #10, want none", n)
	}
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 0 {
		t.Errorf("%d comments on #10, want none", n)
	}
	if n := strings.Count(sc.logs.String(), "is not of cumin-core or of an Owner"); n != 1 {
		t.Errorf("%d log lines for one label event across three polls, want 1:\n%s", n, sc.logs.String())
	}
}

// An issue in cumin/status/merging counts toward the limit of issues in
// work: with a limit of one, a ready issue does not start.
func TestMerging_AnIssueInMergingCountsTowardTheLimit(t *testing.T) {
	merging := workflow.SubIssue{Number: 10, Labels: []string{"risk/low", workflow.LabelMerging}}
	ready := workflow.SubIssue{Number: 11, Labels: []string{workflow.LabelReady}, ReadyRead: true, ReadyOwner: theOwner}
	snapshot := workflow.Snapshot{RequirementIssues: []workflow.RequirementIssue{{
		Number: 6, Labels: []string{workflow.LabelImplementing}, SubIssues: []workflow.SubIssue{merging, ready},
	}}}

	for _, action := range workflow.Decide(snapshot, 1, nil, nil, sceneNow, 0) {
		if claim, ok := action.(workflow.Claim); ok {
			t.Errorf("issue #%d starts while #10 is in cumin/status/merging and the limit is 1", claim.Number)
		}
	}
	if !snapshot.HasIssueInWork() {
		t.Error("a repository with an issue in cumin/status/merging is not in work")
	}
	if got := workflow.Decide(snapshot, 2, nil, nil, sceneNow, 0); !slices.ContainsFunc(got, func(a workflow.Action) bool {
		claim, ok := a.(workflow.Claim)
		return ok && claim.Number == 11
	}) {
		t.Errorf("actions with a limit of 2 = %+v, want the claim of #11: the test would pass for a wrong reason", got)
	}
}

// A head that moved (409) is a lasting refusal: "stop the merge for the
// Owner" with one comment, and no later poll sends the merge again.
func TestMerging_AHeadThatMovedStopsTheMergeForTheOwnerOnce(t *testing.T) {
	sc := mergingScene(t, "risk/low")
	sc.fake.FailNext(http.MethodPut, mergePath, http.StatusConflict)
	service := sc.service()

	sc.pollTimes(t, service, 3)

	sc.assertStoppedAtI6(t, workflow.MergeHeadMovedReason(21))
	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 1 {
		t.Errorf("%d merge requests, want 1", n)
	}
}
