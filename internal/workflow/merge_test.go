package workflow_test

import (
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

const (
	mergePath   = "/repos/example-org/example-repo/pulls/21/merge"
	issue10Path = "/repos/example-org/example-repo/issues/10"
)

// approved puts issue #10 in cumin/status/checking with the risk
// labels, one passed required check, and the Reviewer that approves the
// head commit. One poll then runs "request the review", the review, and "start the merge" or "ask for the merge decision".
func approved(t *testing.T, risks ...string) *scene {
	t.Helper()
	sc := newScene(t, cliOptions{reviews: []string{"APPROVE"}})
	sc.awaitingChecks(t, []string{"ci"}, []githubtest.Check{{Name: "ci", Conclusion: "SUCCESS"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{
		Number: 10, Parent: 6, Title: subIssueTitle,
		Labels: append([]string{"cumin/status/checking"}, risks...),
	})
	return sc
}

// mergeMethod returns the merge method of the merge of #21, or "" when
// cumin did not merge it.
func (sc *scene) mergeMethod(t *testing.T) string {
	t.Helper()
	for _, r := range sc.fake.Requests() {
		if r.Method == http.MethodPut && r.Path == mergePath && strings.Contains(string(r.Body), `"merge_method"`) {
			_, after, _ := strings.Cut(string(r.Body), `"merge_method":"`)
			method, _, _ := strings.Cut(after, `"`)
			return method
		}
	}
	return ""
}

// A risk/low pull request that the Reviewer
// approved, with the required checks passed, is merged by cumin, and the
// implementation issue closes. GitHub leaves the issue open here, so cumin
// closes it once, as completed.
func TestARiskLowPullRequestIsMergedAndTheIssueCloses(t *testing.T) {
	sc := approved(t, "risk/low")
	service := sc.service()

	sc.pollTimes(t, service, 3)

	if got := sc.mergeMethod(t); got != "squash" {
		t.Errorf("merge method = %q, want squash (the default merge_method)", got)
	}
	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 1 {
		t.Errorf("%d merge requests, want 1", n)
	}
	for _, r := range sc.fake.Requests() {
		if r.Method == http.MethodPut && r.Path == mergePath && !strings.Contains(string(r.Body), `"sha":"`+sc.remoteHead+`"`) {
			t.Errorf("the merge does not name the approved head commit as sha: %s", r.Body)
		}
	}
	issue := sc.fake.Issue(sc.repo, 10)
	if !issue.Closed || issue.StateReason != "completed" {
		t.Errorf("issue #10: closed %v, reason %q; want closed as completed", issue.Closed, issue.StateReason)
	}
	if n := sc.fake.CountRequests(http.MethodPatch, issue10Path); n != 1 {
		t.Errorf("%d closes of #10, want 1", n)
	}
	for _, want := range []string{`"msg":"merged the pull request"`, `"msg":"close the merged issue: closed the issue that GitHub left open after the merge"`} {
		if !strings.Contains(sc.logs.String(), want) {
			t.Errorf("the log has no %s", want)
		}
	}
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 0 {
		t.Errorf("%d comments on #10, want none", n)
	}
}

// An issue that GitHub closed through the link is left as it is.
func TestAnIssueThatGitHubClosedIsLeftAsItIs(t *testing.T) {
	sc := approved(t, "risk/low")
	sc.fake.CloseIssuesOnMerge()
	service := sc.service()

	sc.pollTimes(t, service, 3)

	if !sc.fake.Issue(sc.repo, 10).Closed {
		t.Error("issue #10 is open")
	}
	if n := sc.fake.CountRequests(http.MethodPatch, issue10Path); n != 0 {
		t.Errorf("%d closes of #10, want none", n)
	}
	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 1 {
		t.Errorf("%d merge requests, want 1", n)
	}
}

// The merge uses merge_method of the repository settings.
func TestTheMergeMethodOfTheRepositorySettingsIsUsed(t *testing.T) {
	sc := approved(t, "risk/low")
	sc.fake.SetFile(sc.repo, ".cumin/config.toml", githubtest.File{Content: "merge_method = \"rebase\"\n"})
	service := sc.service()

	sc.pollTimes(t, service, 3)

	if got := sc.mergeMethod(t); got != "rebase" {
		t.Errorf("merge method = %q, want rebase", got)
	}
}

// A risk/medium pull request is not merged; the
// issue waits for the Maintainer, with one notification that links the pull
// request ("ask for the merge decision").
func TestARiskMediumPullRequestIsNotMerged(t *testing.T) {
	sc := approved(t, "risk/medium")
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
		t.Errorf("%d merge requests, want none", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/medium", workflow.LabelAwaitingMergeDecision}) {
		t.Errorf("labels of #10 = %v, want risk/medium and cumin/status/awaiting-merge-decision", got)
	}
	messages := sc.messagesExceptWaiting()
	if len(messages) != 1 {
		t.Fatalf("%d notifications, want 1: %v", len(messages), messages)
	}
	for _, want := range []string{"ask for the merge decision", "the merge needs a decision", "issue #10", "/pull/21"} {
		if !strings.Contains(messages[0], want) {
			t.Errorf("the notification has no %q: %s", want, messages[0])
		}
	}
}

// A risk/high pull request waits for the Maintainer too.
func TestARiskHighPullRequestWaitsForTheMaintainer(t *testing.T) {
	sc := approved(t, "risk/high")
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
		t.Errorf("%d merge requests, want none", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelAwaitingMergeDecision) {
		t.Errorf("labels of #10 = %v, want cumin/status/awaiting-merge-decision", got)
	}
}

// An issue without exactly one risk/* label is not merged, and stops once
// (principle 5: the risk is read from the issue only).
func TestAnApprovedIssueWithoutOneRiskLabelIsStopped(t *testing.T) {
	for _, tc := range []struct {
		name     string
		risks    []string
		decision workflow.MergeDecision
	}{
		{"no risk label", nil, workflow.MergeNoRiskLabel},
		{"two risk labels", []string{"risk/low", "risk/high"}, workflow.MergeTwoRiskLabels},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := approved(t, tc.risks...)
			service := sc.service()

			sc.pollAndWait(t, service)

			if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
				t.Errorf("%d merge requests, want none", n)
			}
			sc.assertStoppedAt(t, workflow.ActionStopTheReview, workflow.RiskLabelReason(tc.decision))
		})
	}
}

// A merge that GitHub refuses stops the issue once, with the answer of
// GitHub. A ruleset refusal is 405 too, but the pull request is mergeable.
func TestAFailedMergeStopsTheIssueOnce(t *testing.T) {
	sc := approved(t, "risk/low")
	sc.fake.FailNext(http.MethodPut, mergePath, http.StatusMethodNotAllowed)
	service := sc.service()

	sc.pollTimes(t, service, 3)

	sc.assertTheMergeStopped(t, workflow.MergeFailedReason(21, "status 405: Failure requested by the test"))
	if sc.fake.Issue(sc.repo, 10).Closed {
		t.Error("issue #10 is closed after a failed merge")
	}
}

// A conflict (405, and mergeable false) goes back to the Implementer: the
// label becomes cumin/status/implementing, and exactly one resolution
// request resumes the Implementer session on the branch of the pull
// request. The run pushes a new head, so after done "wait for the checks" runs again
// (the failed merge of "start the merge").
func TestAConflictSendsOneResolutionRequestInTheSameSession(t *testing.T) {
	sc := conflicting(t, cliOptions{reviews: []string{"APPROVE"}, movesHeadOnRun: 2})
	sc.repo.Issues[10].LabelEvents = []githubtest.LabelEvent{readyBy(theMaintainer, 30)}
	sc.fake.SetPermission(theMaintainer, "admin", "User")
	service := sc.serviceWithSession(t)

	sc.pollTimes(t, service, 2)

	if n := sc.agentRuns(t); n != 2 {
		t.Fatalf("%d agent runs, want the review and one resolution", n)
	}
	// The login of the Issue Owner is read for the review, and again for the
	// resolution request, which a later poll sends.
	if n := sc.fake.CountRequests(http.MethodGet, "/repos/example-org/example-repo/collaborators/"+theMaintainer+"/permission"); n != 2 {
		t.Errorf("%d reads of the permission of the Maintainer, want 2", n)
	}
	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 1 {
		t.Errorf("%d merge requests, want 1", n)
	}
	args := sc.record(t, "agent.args")
	if got := argumentOf(t, args, "--resume"); got != "implementer-session" {
		t.Errorf("--resume = %q, want the Implementer session", got)
	}
	text := promptOf(t, args)
	if !strings.Contains(text, issueOwnerLoginLine) {
		t.Errorf("the conflict resolution request does not name the Issue Owner %s:\n%s", theMaintainer, text)
	}
	for _, want := range []string{"Request: conflict resolution", "Pull request: #21", "Branch: cumin/10-add-the-login-screen",
		"Default branch: main", "git merge origin/main", "do not force-push"} {
		if !strings.Contains(text, want) {
			t.Errorf("the request text has no %q:\n%s", want, text)
		}
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelChecking}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/checking", got)
	}
	if sc.fake.Issue(sc.repo, 10).Closed || len(sc.fake.Comments(sc.repo, 10)) != 0 {
		t.Error("the conflict closed or commented on #10")
	}
	for _, want := range []string{`"msg":"the merge conflicts; the issue goes back to the Implementer"`,
		`"msg":"request a conflict resolution: requested the work"`, `"kind":"conflict resolution"`, `"msg":"wait for the checks: verified the pull request"`} {
		if !strings.Contains(sc.logs.String(), want) {
			t.Errorf("the log has no %s", want)
		}
	}
}

// A resolution that ends with done and leaves the head at the commit that
// conflicted stops the issue, so that the same conflict does not go round
// the review again.
func TestAResolutionThatLeavesTheHeadStopsTheIssue(t *testing.T) {
	sc := conflicting(t, cliOptions{reviews: []string{"APPROVE"}})
	sc.fake.SetPullRequestHeadCommitTime(sc.repo, 21, headBeforeTheLabel)
	service := sc.serviceWithSession(t)

	sc.pollTimes(t, service, 2)

	if n := sc.agentRuns(t); n != 2 {
		t.Fatalf("%d agent runs, want the review and one resolution", n)
	}
	sc.assertStoppedForAConflictThatStays(t)
}

// headBeforeTheLabel is a commit time of the head of a pull request that
// is older than every label of a scene.
var headBeforeTheLabel = time.Unix(1000, 0)

// assertStoppedForAConflictThatStays checks that #10 stopped for the Maintainer
// because a conflict resolution left the head commit: one stop note of
// "stop the implementation", and
// cumin/status/awaiting-decision.
func (sc *scene) assertStoppedForAConflictThatStays(t *testing.T) {
	t.Helper()
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 {
		t.Fatalf("%d comments on #10, want 1: %+v", len(comments), comments)
	}
	for _, want := range []string{"## Stopped for a Maintainer", "Step: stop the implementation", "Reason: " + workflow.ConflictNotResolvedReason(21), "Pull request: #21", "Retried: no"} {
		if !strings.Contains(comments[0].Body, want) {
			t.Errorf("the comment has no %q:\n%s", want, comments[0].Body)
		}
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelAwaitingDecision}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/awaiting-decision", got)
	}
}

// conflicting is approved with risk/low, and the pull request #21
// conflicts with the default branch. The poll still reads MERGEABLE, as
// GitHub answers right after a merge into the default branch, so the
// conflict shows only at the merge ("start the merge"), not while the issue waits for
// the checks ("request a conflict resolution").
func conflicting(t *testing.T, opts cliOptions) *scene {
	t.Helper()
	sc := newScene(t, opts)
	sc.awaitingChecks(t, []string{"ci"}, []githubtest.Check{{Name: "ci", Conclusion: "SUCCESS"}})
	sc.fake.SetPullRequestConflict(sc.repo, 21)
	sc.fake.SetPullRequestMergeable(sc.repo, 21, "MERGEABLE")
	return sc
}

// conflictingBeforeChecks puts issue #10 in cumin/status/checking
// with a pull request whose required check has not reported, and whose
// mergeable value on GitHub is the given one.
func conflictingBeforeChecks(t *testing.T, opts cliOptions, mergeable string) *scene {
	t.Helper()
	sc := newScene(t, opts)
	sc.awaitingChecks(t, []string{"ci"}, nil)
	sc.fake.SetPullRequestMergeable(sc.repo, 21, mergeable)
	return sc
}

// "request a conflict resolution" (issue-states.md): a pull request that conflicts while its issue
// waits for the checks goes back to the Implementer. The label becomes
// cumin/status/implementing before the request, exactly one conflict
// resolution request resumes the Implementer session, and it does not
// count as a check fix request. The run pushes a new head, so after done
// "wait for the checks" runs and the issue returns to cumin/status/checking.
func TestAConflictingPullRequestSendsOneResolutionRequestAndReturnsToTheChecks(t *testing.T) {
	sc := conflictingBeforeChecks(t, cliOptions{movesHeadOnRun: 1}, "CONFLICTING")
	sc.repo.Issues[10].LabelEvents = []githubtest.LabelEvent{readyBy(theMaintainer, 30)}
	sc.fake.SetPermission(theMaintainer, "admin", "User")
	service := sc.serviceWithSession(t)

	sc.pollAndWait(t, service)
	// The new head has no conflict; its required check has not reported.
	sc.fake.SetPullRequestMergeable(sc.repo, 21, "MERGEABLE")
	for range 2 {
		sc.pollAndWait(t, service)
	}

	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs, want one resolution", n)
	}
	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
		t.Errorf("%d merge requests, want none", n)
	}
	args := sc.record(t, "agent.args")
	if got := argumentOf(t, args, "--resume"); got != "implementer-session" {
		t.Errorf("--resume = %q, want the Implementer session", got)
	}
	text := promptOf(t, args)
	for _, want := range []string{"Request: conflict resolution", "Pull request: #21", "Branch: cumin/10-add-the-login-screen",
		"Default branch: main", "The pull request #21 has merge conflicts with the default branch main", "git merge origin/main",
		"do not force-push", issueOwnerLoginLine} {
		if !strings.Contains(text, want) {
			t.Errorf("the request text has no %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "could not merge") {
		t.Errorf("the request text says that a merge failed:\n%s", text)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelChecking}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/checking", got)
	}
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 0 {
		t.Errorf("%d comments on #10, want none", n)
	}
	if got := service.State.Issue("example-org/example-repo", 10).CheckFixRequests; got != 0 {
		t.Errorf("check fix requests = %d, want 0: a conflict does not count", got)
	}
	// The label changes before the request, then "wait for the checks" verifies the new head.
	logs := sc.logs.String()
	at := -1
	for _, want := range []string{
		`"msg":"request a conflict resolution: the pull request conflicts with the default branch; the issue goes back to the Implementer"`,
		`"msg":"request a conflict resolution: requested the work"`, `"msg":"wait for the checks: verified the pull request"`} {
		i := strings.Index(logs, want)
		if i < 0 {
			t.Fatalf("the log has no %s", want)
		}
		if i < at {
			t.Errorf("the log line %s comes too early", want)
		}
		at = i
	}
	if !strings.Contains(logs, `"kind":"conflict resolution"`) {
		t.Error("the log does not name the kind of the request")
	}
}

// "request a conflict resolution": UNKNOWN says that GitHub is still calculating, so that poll sends
// nothing and changes no label. A later poll that reads CONFLICTING sends
// the request.
func TestAnUnknownMergeStateWaitsForALaterPoll(t *testing.T) {
	sc := conflictingBeforeChecks(t, cliOptions{}, "UNKNOWN")
	service := sc.serviceWithSession(t)

	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 0 {
		t.Fatalf("%d agent runs while GitHub calculates, want none", n)
	}
	if n := sc.fake.CountRequests(http.MethodPut, issue10Path+"/labels"); n != 0 {
		t.Errorf("%d label changes while GitHub calculates, want none", n)
	}

	sc.fake.SetPullRequestMergeable(sc.repo, 21, "CONFLICTING")
	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs after CONFLICTING, want one resolution", n)
	}
	if text := promptOf(t, sc.record(t, "agent.args")); !strings.Contains(text, "Request: conflict resolution") {
		t.Errorf("the request is not a conflict resolution:\n%s", text)
	}
}

// "request a conflict resolution": a resolution that ends with done and leaves the head at the commit
// that conflicted stops the issue once for a Maintainer: the head commit is
// older than cumin/status/implementing. The polls that follow send nothing
// more.
func TestAResolutionWhileCheckingThatLeavesTheHeadStopsTheIssueOnce(t *testing.T) {
	sc := conflictingBeforeChecks(t, cliOptions{}, "CONFLICTING")
	sc.fake.SetPullRequestHeadCommitTime(sc.repo, 21, headBeforeTheLabel)
	service := sc.serviceWithSession(t)

	for range 3 {
		sc.pollAndWait(t, service)
	}

	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs, want one resolution", n)
	}
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 {
		t.Fatalf("%d comments on #10, want 1: %+v", len(comments), comments)
	}
	for _, want := range []string{"## Stopped for a Maintainer", "Step: stop the implementation", "Reason: " + workflow.ConflictNotResolvedReason(21), "Pull request: #21"} {
		if !strings.Contains(comments[0].Body, want) {
			t.Errorf("the comment has no %q:\n%s", want, comments[0].Body)
		}
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelAwaitingDecision}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/awaiting-decision", got)
	}
	if messages := sc.messagesExceptWaiting(); len(messages) != 1 {
		t.Errorf("%d notifications, want 1: %v", len(messages), messages)
	}
}

// serviceWithSession is the service with a state file that holds the
// Implementer session of #10.
func (sc *scene) serviceWithSession(t *testing.T) *workflow.Service {
	t.Helper()
	service := sc.service()
	service.State = state.Open(filepath.Join(t.TempDir(), "state.json"), nil)
	if err := service.State.Set("example-org/example-repo", 10, state.Issue{SessionID: "implementer-session"}); err != nil {
		t.Fatal(err)
	}
	return service
}

// A close after the merge that fails stops the issue once; the Maintainer
// closes it.
func TestAFailedCloseAfterTheMergeStopsTheIssueOnce(t *testing.T) {
	sc := approved(t, "risk/low")
	sc.fake.FailNext(http.MethodPatch, issue10Path, http.StatusForbidden)
	service := sc.service()

	sc.pollTimes(t, service, 3)

	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 1 {
		t.Errorf("%d merge requests, want 1", n)
	}
	sc.assertTheMergeStopped(t, workflow.CloseFailedReason(21, "status 403: Failure requested by the test"))
}

// assertTheMergeStopped checks "stop the merge" for #10: one comment with
// the reason, cumin/status/awaiting-decision, and one notification.
func (sc *scene) assertTheMergeStopped(t *testing.T, reason string) {
	t.Helper()
	sc.assertStoppedAt(t, workflow.ActionStopTheMerge, reason)
}

// assertStoppedAt checks a stop of #10 for the Maintainer with the name of the action: one
// comment with the reason, cumin/status/awaiting-decision, and one
// notification.
func (sc *scene) assertStoppedAt(t *testing.T, action workflow.ActionName, reason string) {
	t.Helper()
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 {
		t.Fatalf("%d comments on #10, want 1: %+v", len(comments), comments)
	}
	for _, want := range []string{"## Stopped for a Maintainer", "Step: " + string(action), "Reason: " + reason, "Pull request: #21"} {
		if !strings.Contains(comments[0].Body, want) {
			t.Errorf("the comment has no %q:\n%s", want, comments[0].Body)
		}
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelAwaitingDecision) {
		t.Errorf("labels of #10 = %v, want cumin/status/awaiting-decision", got)
	}
	messages := sc.messagesExceptWaiting()
	if len(messages) != 1 || !strings.Contains(messages[0], string(action)+": ") {
		t.Errorf("notifications = %v, want one of %s", messages, action)
	}
}

// DecideMerge reads the risk from the labels of the issue, and the checks
// of the head commit.
func TestDecideMerge_StartTheMergeOrAskForTheMergeDecision(t *testing.T) {
	required := []workflow.RequiredCheck{{Name: "ci"}}
	passed := []workflow.CheckResult{{Name: "ci", Conclusion: workflow.CheckPassed}}
	failed := []workflow.CheckResult{{Name: "ci", Conclusion: workflow.CheckFailed}}
	for _, tc := range []struct {
		name   string
		labels []string
		checks []workflow.CheckResult
		want   workflow.MergeDecision
	}{
		{"risk/low merges", []string{"cumin/status/reviewing", "risk/low"}, passed, workflow.MergeNow},
		{"risk/medium asks the Maintainer", []string{"risk/medium"}, passed, workflow.MergeAskMaintainer},
		{"risk/high asks the Maintainer", []string{"risk/high"}, passed, workflow.MergeAskMaintainer},
		{"no risk label", []string{"cumin/status/reviewing"}, passed, workflow.MergeNoRiskLabel},
		{"two risk labels", []string{"risk/low", "risk/medium"}, passed, workflow.MergeTwoRiskLabels},
		{"a wrong risk label stops before the checks", nil, failed, workflow.MergeNoRiskLabel},
		{"a failed check waits for the checks", []string{"risk/low"}, failed, workflow.MergeChecksNotPassed},
		{"a missing check waits for the checks", []string{"risk/low"}, nil, workflow.MergeChecksNotPassed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := workflow.DecideMerge(tc.labels, required, tc.checks); got != tc.want {
				t.Errorf("DecideMerge = %v, want %v", got, tc.want)
			}
		})
	}
}
