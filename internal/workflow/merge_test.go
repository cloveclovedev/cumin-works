package workflow_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

const (
	mergePath   = "/repos/example-org/example-repo/pulls/21/merge"
	issue10Path = "/repos/example-org/example-repo/issues/10"
)

// approved puts issue #10 in cumin/status/awaiting-checks with the risk
// labels, one passed required check, and the Reviewer that approves the
// head commit. One poll then runs I3, the review, and I6 or I7.
func approved(t *testing.T, risks ...string) *scene {
	t.Helper()
	sc := newScene(t, cliOptions{reviews: []string{"APPROVE"}})
	sc.awaitingChecks(t, []string{"ci"}, []githubtest.Check{{Name: "ci", Conclusion: "SUCCESS"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{
		Number: 10, Parent: 6, Title: subIssueTitle,
		Labels: append([]string{"cumin/status/awaiting-checks"}, risks...),
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

// Core-3 (cumin-core.md): a risk/low pull request that the Reviewer
// approved, with the required checks passed, is merged by cumin, and the
// implementation issue closes. GitHub leaves the issue open here, so cumin
// closes it once, as completed.
func TestCore03_ARiskLowPullRequestIsMergedAndTheIssueCloses(t *testing.T) {
	sc := approved(t, "risk/low")
	service := sc.service()

	sc.pollAndWait(t, service)

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
	for _, want := range []string{`"msg":"I6: merged the pull request"`, `"msg":"I6: closed the issue that GitHub left open after the merge"`} {
		if !strings.Contains(sc.logs.String(), want) {
			t.Errorf("the log has no %s", want)
		}
	}
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 0 {
		t.Errorf("%d comments on #10, want none", n)
	}
}

// An issue that GitHub closed through the link is left as it is.
func TestI6_AnIssueThatGitHubClosedIsLeftAsItIs(t *testing.T) {
	sc := approved(t, "risk/low")
	sc.fake.CloseIssuesOnMerge()
	service := sc.service()

	sc.pollAndWait(t, service)

	if !sc.fake.Issue(sc.repo, 10).Closed {
		t.Error("issue #10 is open")
	}
	if n := sc.fake.CountRequests(http.MethodPatch, issue10Path); n != 0 {
		t.Errorf("%d closes of #10, want none", n)
	}
	if !strings.Contains(sc.logs.String(), `"msg":"I6: GitHub closed the issue"`) {
		t.Error("the log does not say that GitHub closed the issue")
	}
}

// The merge uses merge_method of the repository settings.
func TestI6_TheMergeMethodOfTheRepositorySettingsIsUsed(t *testing.T) {
	sc := approved(t, "risk/low")
	sc.fake.SetFile(sc.repo, ".cumin/config.toml", githubtest.File{Content: "merge_method = \"rebase\"\n"})
	service := sc.service()

	sc.pollAndWait(t, service)

	if got := sc.mergeMethod(t); got != "rebase" {
		t.Errorf("merge method = %q, want rebase", got)
	}
}

// Core-4 (cumin-core.md): a risk/medium pull request is not merged; the
// issue waits for the Owner, with one notification that links the pull
// request (I7).
func TestCore04_ARiskMediumPullRequestIsNotMerged(t *testing.T) {
	sc := approved(t, "risk/medium")
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
		t.Errorf("%d merge requests, want none", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/medium", workflow.LabelAwaitingOwnerReview}) {
		t.Errorf("labels of #10 = %v, want risk/medium and cumin/status/awaiting-owner-review", got)
	}
	messages := sc.messagesExceptQ4()
	if len(messages) != 1 {
		t.Fatalf("%d notifications, want 1: %v", len(messages), messages)
	}
	for _, want := range []string{"I7", "the merge needs a decision", "issue #10", "/pull/21"} {
		if !strings.Contains(messages[0], want) {
			t.Errorf("the notification has no %q: %s", want, messages[0])
		}
	}
}

// A risk/high pull request waits for the Owner too.
func TestI7_ARiskHighPullRequestWaitsForTheOwner(t *testing.T) {
	sc := approved(t, "risk/high")
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
		t.Errorf("%d merge requests, want none", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelAwaitingOwnerReview) {
		t.Errorf("labels of #10 = %v, want cumin/status/awaiting-owner-review", got)
	}
}

// An issue without exactly one risk/* label is not merged, and stops once
// (principle 5: the risk is read from the issue only).
func TestI6_AnIssueWithoutOneRiskLabelIsStopped(t *testing.T) {
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
			sc.assertStoppedAtI6(t, workflow.RiskLabelReason(tc.decision))
		})
	}
}

// A merge that GitHub refuses stops the issue once, with the answer of
// GitHub. A ruleset refusal is 405 too, but the pull request is mergeable.
func TestI6_AFailedMergeStopsTheIssueOnce(t *testing.T) {
	sc := approved(t, "risk/low")
	sc.fake.FailNext(http.MethodPut, mergePath, http.StatusMethodNotAllowed)
	service := sc.service()

	sc.pollAndWait(t, service)

	sc.assertStoppedAtI6(t, workflow.MergeFailedReason(21, "status 405: Failure requested by the test"))
	if sc.fake.Issue(sc.repo, 10).Closed {
		t.Error("issue #10 is closed after a failed merge")
	}
}

// A conflict is told apart from the other 405 answers by mergeable.
func TestI6_AConflictStopsTheIssueWithItsOwnSentence(t *testing.T) {
	sc := approved(t, "risk/low")
	sc.fake.SetPullRequestConflict(sc.repo, 21)
	service := sc.service()

	sc.pollAndWait(t, service)

	sc.assertStoppedAtI6(t, workflow.MergeConflictReason(21))
}

// A close after the merge that fails stops the issue once; the Owner
// closes it.
func TestI6_AFailedCloseAfterTheMergeStopsTheIssueOnce(t *testing.T) {
	sc := approved(t, "risk/low")
	sc.fake.FailNext(http.MethodPatch, issue10Path, http.StatusForbidden)
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 1 {
		t.Errorf("%d merge requests, want 1", n)
	}
	sc.assertStoppedAtI6(t, workflow.CloseFailedReason(21, "status 403: Failure requested by the test"))
}

// assertStoppedAtI6 checks the stop step of I6 for #10: one comment with
// the reason, cumin/status/awaiting-owner-decision, and one notification.
func (sc *scene) assertStoppedAtI6(t *testing.T, reason string) {
	t.Helper()
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 {
		t.Fatalf("%d comments on #10, want 1: %+v", len(comments), comments)
	}
	for _, want := range []string{"## Stopped for the Owner", "Row: I6", "Reason: " + reason, "Pull request: #21"} {
		if !strings.Contains(comments[0].Body, want) {
			t.Errorf("the comment has no %q:\n%s", want, comments[0].Body)
		}
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelAwaitingOwnerDecision) {
		t.Errorf("labels of #10 = %v, want cumin/status/awaiting-owner-decision", got)
	}
	messages := sc.messagesExceptQ4()
	if len(messages) != 1 || !strings.Contains(messages[0], "I6") {
		t.Errorf("notifications = %v, want one of I6", messages)
	}
}

// DecideMerge reads the risk from the labels of the issue, and the checks
// of the head commit.
func TestDecideMerge_I6_I7(t *testing.T) {
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
		{"risk/medium asks the Owner", []string{"risk/medium"}, passed, workflow.MergeAskOwner},
		{"risk/high asks the Owner", []string{"risk/high"}, passed, workflow.MergeAskOwner},
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
