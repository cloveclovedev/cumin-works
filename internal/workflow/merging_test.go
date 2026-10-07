package workflow_test

import (
	"context"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/state"
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
	sc.fake.SetPermission(theMaintainer, "admin", "User")
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
	sc.review(theMaintainer, false, "APPROVED", sc.remoteHead, 5)
	sc.review(theMaintainer, false, "CHANGES_REQUESTED", sc.remoteHead, 1)
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
	if n := strings.Count(sc.logs.String(), "is not of cumin-core or of a Maintainer"); n != 1 {
		t.Errorf("%d log lines for one label event across three polls, want 1:\n%s", n, sc.logs.String())
	}
}

// An issue in cumin/status/merging counts toward the limit of issues in
// work: with a limit of one, a ready issue does not start.
func TestMerging_AnIssueInMergingCountsTowardTheLimit(t *testing.T) {
	merging := workflow.SubIssue{Number: 10, Labels: []string{"risk/low", workflow.LabelMerging}}
	ready := workflow.SubIssue{Number: 11, Labels: []string{workflow.LabelReady}, ReadyRead: true, ReadyOwner: theMaintainer}
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

// While cumin stops after the current runs, a merge that GitHub refuses
// for a conflict changes nothing: the merge is sent, the issue keeps
// cumin/status/merging, and no Implementer starts. After the next start
// of cumin, one poll requests the conflict resolution.
func TestMerging_AConflictWhileCuminStopsAfterTheRunsWaitsForTheNextStart(t *testing.T) {
	sc := mergingScene(t, "risk/low")
	sc.repo.Issues[10].LabelEvents = append(sc.repo.Issues[10].LabelEvents, readyBy(theMaintainer, 30))
	// The poll still reads MERGEABLE, so the conflict shows only at the
	// merge.
	sc.fake.SetPullRequestConflict(sc.repo, 21)
	sc.fake.SetPullRequestMergeable(sc.repo, 21, "MERGEABLE")
	stopped := sc.service()
	stopped.PollInterval = 10 * time.Millisecond
	stopped.StopRequestPath = filepath.Join(t.TempDir(), state.StopRequestFileName)
	// A request of the start, so that the first poll takes it
	// (TestStopAfterRuns_ARequestOfTheStartIsKept).
	if err := state.WriteStopRequest(stopped.StopRequestPath, state.StopRequest{RequestedAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}

	if err := stopped.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 1 {
		t.Errorf("%d merge requests, want 1: the merge is still sent while cumin stops", n)
	}
	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none: the conflict resolution waits for the next start", n)
	}
	merging := []string{"risk/low", workflow.LabelMerging}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, merging) {
		t.Errorf("labels of #10 = %v, want %v", got, merging)
	}
	if n := sc.fake.CountRequests(http.MethodPut, "/repos/example-org/example-repo/issues/10/labels"); n != 0 {
		t.Errorf("%d label changes of #10, want none", n)
	}
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 0 {
		t.Errorf("%d comments on #10, want none", n)
	}
	if !strings.Contains(sc.logs.String(), `"request":"conflict resolution"`) {
		t.Errorf("the log does not say that the conflict resolution waits:\n%s", sc.logs.String())
	}

	restarted := sc.restartedWith(stopped)
	sc.pollAndWait(t, restarted)

	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs after the restart, want one conflict resolution", n)
	}
	if text := promptOf(t, sc.record(t, "agent.args")); !strings.Contains(text, "Request: conflict resolution") {
		t.Errorf("the request text is not a conflict resolution:\n%s", text)
	}
	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 2 {
		t.Errorf("%d merge requests, want 2: the poll after the restart sends the merge again", n)
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

// The Owner reopened an issue whose first pull request #20 is merged. The
// new pull request #21 reached cumin/status/merging, and was closed without
// a merge. Only the newest linked pull request is the one of this merge, so
// cumin does not close the issue: it goes back to the checks.
func TestMerging_AnOldMergedPullRequestDoesNotCloseTheIssue(t *testing.T) {
	sc := mergingScene(t, "risk/low")
	sc.fake.AddPullRequest(sc.repo, &githubtest.PullRequest{
		Number: 20, HeadCommit: olderCommit, HeadBranch: "cumin/10-add-the-login-screen",
		Author: implementerSlug, AuthorIsBot: true, Closes: []int{10}, Closed: true, Merged: true,
	})
	if err := sc.fake.ClosePullRequest(sc.repo, 21); err != nil {
		t.Fatal(err)
	}
	service := sc.service()

	sc.pollAndWait(t, service)

	issue := sc.fake.Issue(sc.repo, 10)
	if issue.Closed {
		t.Error("issue #10 is closed for the merged pull request of an earlier stay")
	}
	if n := sc.fake.CountRequests(http.MethodPatch, issue10Path); n != 0 {
		t.Errorf("%d closes of #10, want none", n)
	}
	if !slices.Equal(issue.Labels, []string{"risk/low", workflow.LabelChecking}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/checking", issue.Labels)
	}
	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
		t.Errorf("%d merge requests, want none", n)
	}
}

// knownConflictScene is mergingScene with a pull request that GitHub
// reports as CONFLICTING at the poll. The fake would refuse a merge too.
func knownConflictScene(t *testing.T, opts ...cliOptions) *scene {
	t.Helper()
	sc := newScene(t, opts...)
	sc.awaitingChecks(t, []string{"ci"}, []githubtest.Check{{Name: "ci", Conclusion: "SUCCESS"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{
		Number: 10, Parent: 6, Title: subIssueTitle,
		Labels: []string{"risk/low", workflow.LabelMerging},
		LabelEvents: []githubtest.LabelEvent{
			{Label: workflow.LabelMerging, At: sceneNow.Add(-10 * time.Minute), Actor: cuminSlug, ActorType: "Bot"},
			readyBy(theMaintainer, 30),
		},
	})
	sc.review(implementerSlug, true, "APPROVED", sc.remoteHead, 30)
	sc.fake.SetPermission(theMaintainer, "admin", "User")
	sc.fake.SetPullRequestConflict(sc.repo, 21)
	sc.fake.SetPullRequestMergeable(sc.repo, 21, "CONFLICTING")
	return sc
}

// assertOneConflictResolutionWithoutAMerge checks that no merge was sent,
// that the first label change of #10 is cumin/status/implementing, and that
// exactly one agent ran, with a conflict resolution request.
func assertOneConflictResolutionWithoutAMerge(t *testing.T, sc *scene) {
	t.Helper()
	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
		t.Errorf("%d merge requests, want none: GitHub reports the conflict already", n)
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs, want one conflict resolution", n)
	}
	if text := promptOf(t, sc.record(t, "agent.args")); !strings.Contains(text, "Request: conflict resolution") {
		t.Errorf("the request text is not a conflict resolution:\n%s", text)
	}
	for _, r := range sc.fake.Requests() {
		if r.Method != http.MethodPut || r.Path != putLabelsPath {
			continue
		}
		if !strings.Contains(string(r.Body), workflow.LabelImplementing) {
			t.Errorf("the first label change of #10 = %s, want %s", r.Body, workflow.LabelImplementing)
		}
		return
	}
	t.Errorf("no label change of #10, want %s", workflow.LabelImplementing)
}

// "Request a conflict resolution" (issue-states.md, from merging to
// implementing): a pull request that GitHub reports as CONFLICTING gets no
// merge. The label becomes cumin/status/implementing, and exactly one
// conflict resolution request resumes the Implementer session. The run
// pushes a new head, so the issue goes on to the checks.
func TestMerging_AKnownConflictSendsOneResolutionRequestAndNoMerge(t *testing.T) {
	sc := knownConflictScene(t, cliOptions{movesHeadOnRun: 1})
	service := sc.serviceWithSession(t)

	sc.pollAndWait(t, service)

	assertOneConflictResolutionWithoutAMerge(t, sc)
	if got := argumentOf(t, sc.record(t, "agent.args"), "--resume"); got != "implementer-session" {
		t.Errorf("--resume = %q, want the Implementer session", got)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelChecking}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/checking after the run", got)
	}
	if sc.fake.Issue(sc.repo, 10).Closed || len(sc.fake.Comments(sc.repo, 10)) != 0 {
		t.Error("the conflict closed or commented on #10")
	}
}

// While cumin stops after the current runs, a pull request that GitHub
// reports as CONFLICTING changes nothing over two runs of cumin: no merge
// is sent, the issue keeps cumin/status/merging, and no Implementer starts.
// After the next start of cumin, one poll requests the conflict resolution
// exactly once.
func TestMerging_AKnownConflictWhileCuminStopsAfterTheRunsSendsNoMerge(t *testing.T) {
	sc := knownConflictScene(t)
	stopped := sc.serviceWithSession(t)
	runWithAStopRequestOfTheStart(t, stopped)
	runWithAStopRequestOfTheStart(t, sc.restartedWith(stopped))

	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
		t.Errorf("%d merge requests, want none while cumin stops", n)
	}
	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none: the conflict resolution waits for the next start", n)
	}
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != 0 {
		t.Errorf("%d label changes of #10, want none", n)
	}
	merging := []string{"risk/low", workflow.LabelMerging}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, merging) {
		t.Errorf("labels of #10 = %v, want %v", got, merging)
	}
	if logs := sc.logs.String(); strings.Count(logs, heldBackLog) < 2 || !strings.Contains(logs, `"request":"conflict resolution"`) {
		t.Errorf("the log does not say twice that the conflict resolution waits:\n%s", logs)
	}

	sc.pollAndWait(t, sc.restartedWith(stopped))

	assertOneConflictResolutionWithoutAMerge(t, sc)
}
