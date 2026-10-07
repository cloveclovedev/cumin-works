package workflow_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/state"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

// This file proves "stop agent starts" and "resume agent starts" of
// issue-states.md for the requests of an issue in work: at a quota limit no
// agent starts, no label changes for that start, and nothing is counted.
// After the limit, one poll sends each waiting request exactly once.

// atAQuotaLimit makes the minimal run read a 5h usage above the limit of
// the scene, and returns the reset time of that window.
func (sc *scene) atAQuotaLimit(t *testing.T) time.Time {
	t.Helper()
	reset := sceneNow.Add(2 * time.Hour)
	sc.setQuota(t, 0.90, reset, 0.10, sceneNow.Add(time.Hour))
	return reset
}

// limitReachedByARun makes the usage that an agent run reports (weekly
// 0.51, done.jsonl) reach the pace limit, while the minimal run before that
// run reads a usage below every limit. Call it before sc.service().
func (sc *scene) limitReachedByARun(t *testing.T) time.Time {
	t.Helper()
	reset := sceneNow.Add(2 * time.Hour)
	sc.setQuota(t, 0.10, reset, 0.10, sceneNow.Add(time.Hour))
	sc.quota.Weekly.Target = 50
	return reset
}

// afterTheReset moves the clock to the reset of both windows, where the
// minimal run reads a usage below every limit.
func (sc *scene) afterTheReset(t *testing.T, reset time.Time) {
	t.Helper()
	sc.clock.Set(reset)
	sc.setQuota(t, 0.05, reset.Add(5*time.Hour), 0.10, reset.Add(time.Hour))
}

// assertNoStartAtTheLimit checks that the polls at the limit started no
// agent and left the labels of #10 as they were.
func (sc *scene) assertNoStartAtTheLimit(t *testing.T, want []string) {
	t.Helper()
	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs at the limit, want 0", n)
	}
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != 0 {
		t.Errorf("%d label changes of #10 at the limit, want none", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, want) {
		t.Errorf("labels of #10 at the limit = %v, want %v", got, want)
	}
}

// Core-31 (cumin-core.md): at a limit, an issue with passed checks keeps
// cumin/status/checking and no Reviewer starts. After cumin quota allow,
// one poll requests the review exactly once.
func TestCore31_PassedChecksAtALimitStartNoReviewerAndKeepChecking(t *testing.T) {
	sc := approved(t, "risk/low")
	reset := sc.atAQuotaLimit(t)
	service := sc.service()
	withState(t, service)
	sc.pollTimes(t, service, 3)

	sc.assertNoStartAtTheLimit(t, []string{"cumin/status/checking", "risk/low"})
	if q1 := sc.q1Messages(); len(q1) != 1 {
		t.Errorf("notifications of the stop = %q, want one over three polls", q1)
	}

	allow(t, service, reset)
	sc.pollAndWait(t, service)
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs after the limit, want 1: the review is requested once", n)
	}
	if text := promptOf(t, sc.record(t, "agent.args")); !strings.Contains(text, "Request: review") {
		t.Errorf("the request is not a review:\n%s", text)
	}
}

// Core-32: the limit is reached by the Reviewer run that ends with
// REQUEST_CHANGES. No Implementer starts, and the issue keeps
// cumin/status/reviewing. After the reset, one poll requests the review fix
// exactly once.
func TestCore32_AChangeRequestAtALimitStartsNoImplementerAndKeepsReviewing(t *testing.T) {
	sc := newScene(t, cliOptions{reviews: []string{"REQUEST_CHANGES", "NONE"}})
	reset := sc.limitReachedByARun(t)
	service := sc.service()
	sc.reviewing(t, service, state.Issue{SessionID: "implementer-session"})
	sc.pollTimes(t, service, 3)

	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs at the limit, want 1: the Reviewer only", n)
	}
	want := []string{"risk/low", workflow.LabelReviewing}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, want) {
		t.Errorf("labels of #10 at the limit = %v, want %v", got, want)
	}
	if q1 := sc.q1Messages(); len(q1) != 1 || !strings.Contains(q1[0], "weekly") {
		t.Errorf("notifications of the stop = %q, want one about the weekly window", q1)
	}

	sc.afterTheReset(t, reset)
	sc.pollAndWait(t, service)
	if n := sc.agentRuns(t); n != 2 {
		t.Errorf("%d agent runs after the limit, want 2: the review fix is requested once", n)
	}
	if text := promptOf(t, sc.record(t, "agent.args")); !strings.Contains(text, "Request: review fix") {
		t.Errorf("the request is not a review fix:\n%s", text)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; slices.Contains(got, workflow.LabelReviewing) {
		t.Errorf("labels of #10 after the limit = %v, want the issue to leave cumin/status/reviewing", got)
	}
}

// At a limit, a failed required check starts no check fix, changes no
// label, and counts nothing. A ready issue waits for the same limit, and
// the Owner hears once for both. After cumin quota allow, one poll sends
// the check fix exactly once.
func TestStopAgentStarts_ACheckFixWaitsAtALimitAndTwoWaitingIssuesNotifyOnce(t *testing.T) {
	sc := newScene(t, cliOptions{})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 11, Parent: 6, Title: "second", Labels: []string{"cumin/status/ready", "risk/low"}})
	reset := sc.atAQuotaLimit(t)
	service := sc.service()
	service.Settings.MaxIssuesInProgress = 2
	sc.failingCheck(t, service, 0)
	sc.pollTimes(t, service, 3)

	sc.assertNoStartAtTheLimit(t, []string{"cumin/status/checking", "risk/low"})
	if n := service.State.Issue("example-org/example-repo", 10).CheckFixRequests; n != 0 {
		t.Errorf("the state file counts %d check fix requests at the limit, want none", n)
	}
	if got := sc.fake.Issue(sc.repo, 11).Labels; !slices.Equal(got, []string{"cumin/status/ready", "risk/low"}) {
		t.Errorf("labels of #11 at the limit = %v, want the ready issue as it was", got)
	}
	if q1 := sc.q1Messages(); len(q1) != 1 {
		t.Errorf("notifications of the stop = %q, want one for two waiting issues", q1)
	}

	service.Settings.MaxIssuesInProgress = 1
	allow(t, service, reset)
	sc.pollAndWait(t, service)
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs after the limit, want 1: the check fix is requested once", n)
	}
	if text := promptOf(t, sc.record(t, "agent.args")); !strings.Contains(text, "Request: check fix") {
		t.Errorf("the request is not a check fix:\n%s", text)
	}
	if n := service.State.Issue("example-org/example-repo", 10).CheckFixRequests; n != 1 {
		t.Errorf("the state file counts %d check fix requests after the limit, want 1", n)
	}
}

// Core-33 and the conflict resolution: at a limit, the merge of an approved
// pull request under cumin/status/merging is still sent. GitHub refuses it
// for a conflict, and no Implementer starts: the issue keeps
// cumin/status/merging. After cumin quota allow, one poll requests the
// conflict resolution exactly once.
func TestStopAgentStarts_AConflictResolutionWaitsAtALimit(t *testing.T) {
	sc := mergingScene(t, "risk/low")
	sc.repo.Issues[10].LabelEvents = append(sc.repo.Issues[10].LabelEvents, readyBy(theOwner, 30))
	// The poll still reads MERGEABLE, so the conflict shows only at the
	// merge.
	sc.fake.SetPullRequestConflict(sc.repo, 21)
	sc.fake.SetPullRequestMergeable(sc.repo, 21, "MERGEABLE")
	reset := sc.atAQuotaLimit(t)
	service := sc.serviceWithSession(t)
	sc.pollAndWait(t, service)

	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 1 {
		t.Errorf("%d merge requests at the limit, want 1: the merge starts no agent", n)
	}
	sc.assertNoStartAtTheLimit(t, []string{"risk/low", workflow.LabelMerging})

	allow(t, service, reset)
	sc.pollAndWait(t, service)
	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs after the limit, want one conflict resolution", n)
	}
	if text := promptOf(t, sc.record(t, "agent.args")); !strings.Contains(text, "Request: conflict resolution") {
		t.Errorf("the request is not a conflict resolution:\n%s", text)
	}
}

// At a limit, a pull request under cumin/status/merging that GitHub reports
// as CONFLICTING gets no merge over two polls: no Implementer starts, and
// the issue keeps cumin/status/merging. After cumin quota allow, one poll
// requests the conflict resolution exactly once, still with no merge.
func TestStopAgentStarts_AKnownConflictAtALimitSendsNoMerge(t *testing.T) {
	sc := knownConflictScene(t)
	reset := sc.atAQuotaLimit(t)
	service := sc.serviceWithSession(t)
	sc.pollTimes(t, service, 2)

	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
		t.Errorf("%d merge requests at the limit, want none", n)
	}
	sc.assertNoStartAtTheLimit(t, []string{"risk/low", workflow.LabelMerging})

	allow(t, service, reset)
	sc.pollAndWait(t, service)
	assertOneConflictResolutionWithoutAMerge(t, sc)
}

// Core-33: at a limit, an approved pull request under cumin/status/merging
// merges. The steps that start no agent go on, and no usage is read.
func TestCore33_AnApprovedPullRequestMergesAtALimit(t *testing.T) {
	sc := mergingScene(t, "risk/low")
	sc.repo.Issues[10].LabelEvents = append(sc.repo.Issues[10].LabelEvents, readyBy(theOwner, 30))
	sc.atAQuotaLimit(t)
	service := sc.service()
	withState(t, service)
	storeUsage(service, 0.90, sceneNow)
	sc.pollTimes(t, service, 2)

	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 1 {
		t.Errorf("%d merge requests at the limit, want 1", n)
	}
	if got := sc.fake.Issue(sc.repo, 10); !got.Closed {
		t.Errorf("issue #10 is open with the labels %v, want it closed by the merge", got.Labels)
	}
	if n := sc.agentRuns(t) + sc.quotaRuns(t); n != 0 {
		t.Errorf("%d runs of the agent CLI, want none", n)
	}
}

// At a limit, a request for changes of the Owner starts no Implementer, and
// the issue keeps cumin/status/awaiting-merge-decision. After cumin quota
// allow, one poll requests the fix exactly once.
func TestStopAgentStarts_TheResponseToTheReviewOfTheOwnerWaitsAtALimit(t *testing.T) {
	sc := awaitingOwner(t, cliOptions{})
	sc.review(implementerSlug, true, "APPROVED", sc.remoteHead, 30)
	sc.review(theOwner, false, "CHANGES_REQUESTED", sc.remoteHead, 5)
	sc.repo.Issues[10].LabelEvents = append([]githubtest.LabelEvent{readyBy(theOwner, 60)}, sc.repo.Issues[10].LabelEvents...)
	reset := sc.atAQuotaLimit(t)
	service := sc.serviceWithSession(t)
	sc.pollTimes(t, service, 3)

	sc.assertNoStartAtTheLimit(t, []string{"risk/medium", workflow.LabelAwaitingMergeDecision})

	allow(t, service, reset)
	sc.pollAndWait(t, service)
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs after the limit, want 1: the fix is requested once", n)
	}
	if text := promptOf(t, sc.record(t, "agent.args")); !strings.Contains(text, "Request: owner review fix") {
		t.Errorf("the request is not an owner review fix:\n%s", text)
	}
}

// At a limit, a requirement issue whose sub-issues are all closed starts no
// acceptance check and keeps cumin/status/implementing. After cumin quota
// allow, one poll requests the acceptance check exactly once.
func TestStopAgentStarts_TheFirstAcceptanceCheckWaitsAtALimit(t *testing.T) {
	sc, _ := newAcceptanceScene(t, cliOptions{fixture: "planner-done.jsonl", holds: true})
	reset := sc.atAQuotaLimit(t)
	service := sc.service()
	withState(t, service)
	sc.pollTimes(t, service, 3)

	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs at the limit, want 0", n)
	}
	if n := sc.fake.CountRequests(http.MethodPut, putRequirementLabelsPath); n != 0 {
		t.Errorf("%d label changes of #6 at the limit, want none", n)
	}
	if q1 := sc.q1Messages(); len(q1) != 1 || !strings.Contains(q1[0], "issue #6") {
		t.Errorf("notifications of the stop = %q, want one that links #6", q1)
	}

	allow(t, service, reset)
	if err := service.Poll(t.Context()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	waitForAgentRun(t, sc)
	if err := service.Poll(t.Context()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs after the limit, want 1: the acceptance check is requested once", n)
	}
	if got := requirementLabels(t, sc); !slices.Equal(got, []string{githubtest.RequirementLabel, "cumin/status/accepting"}) {
		t.Errorf("labels of #6 after the limit = %v, want cumin/status/accepting", got)
	}
	// The Planner writes the comment during its run, and the run ends.
	acceptanceComment(sc, sceneNow, plannerLogin)
	sc.release(t)
	service.Wait()
}

// A request for changes of the Owner on a conflicting head that waits for
// its permit keeps the issue for the poll: the first read of the usage
// fails, a second read would succeed, and no conflict resolution starts.
// The conflict resolution would move the head commit away from the review
// of the Owner. The next poll requests the fix of that review.
func TestStopAgentStarts_AWaitingReviewOfTheOwnerKeepsTheConflictResolutionBack(t *testing.T) {
	sc := awaitingOwner(t)
	sc.review(implementerSlug, true, "APPROVED", sc.remoteHead, 30)
	sc.review(theOwner, false, "CHANGES_REQUESTED", sc.remoteHead, 5)
	sc.fake.SetPullRequestMergeable(sc.repo, 21, "CONFLICTING")
	sc.repo.Issues[10].LabelEvents = append([]githubtest.LabelEvent{readyBy(theOwner, 60)}, sc.repo.Issues[10].LabelEvents...)
	sc.failQuotaOnce(t)
	service := sc.serviceWithSession(t)
	sc.pollAndWait(t, service)

	sc.assertNoStartAtTheLimit(t, []string{"risk/medium", workflow.LabelAwaitingMergeDecision})
	if n := sc.quotaRuns(t); n != 1 {
		t.Errorf("%d minimal runs, want 1: the conflict resolution asks for no permit in this poll", n)
	}

	sc.pollAndWait(t, service)
	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs after the read, want 1", n)
	}
	if text := promptOf(t, sc.record(t, "agent.args")); !strings.Contains(text, "Request: owner review fix") {
		t.Errorf("the request is not an owner review fix:\n%s", text)
	}
}
