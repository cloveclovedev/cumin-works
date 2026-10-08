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

// awaitingChecks puts issue #10 in cumin/status/checking with one
// open pull request of the Implementer App, and gives the repository the
// required checks. checks are the results on the head commit.
func (sc *scene) awaitingChecks(t *testing.T, required []string, checks []githubtest.Check) {
	t.Helper()
	// Issue returns a copy, so the label is set by replacing the issue.
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{
		Number: 10, Parent: 6, Title: subIssueTitle,
		Labels: []string{"cumin/status/checking", "risk/low"},
	})
	sc.repo.DefaultBranch = "main"
	for _, name := range required {
		sc.repo.RequiredChecks = append(sc.repo.RequiredChecks, githubtest.RequiredCheck{Name: name})
	}
	sc.fake.AddPullRequest(sc.repo, &githubtest.PullRequest{
		Number: 21, HeadCommit: sc.remoteHead, HeadBranch: "cumin/10-add-the-login-screen",
		Author: implementerSlug, AuthorIsBot: true, Closes: []int{10}, Checks: checks,
	})
}

const branchRulesPath = "/repos/example-org/example-repo/rules/branches/main"

// TestEveryRequiredCheckPassedMovesTheIssueToTheReview is the success
// path of "request the review": the poll reads the required checks, they all passed on the
// head commit, and the issue waits for the Reviewer.
func TestEveryRequiredCheckPassedMovesTheIssueToTheReview(t *testing.T) {
	sc := newScene(t, cliOptions{reviews: []string{"APPROVE"}})
	sc.awaitingChecks(t, []string{"ci", "cumin-protected-paths"}, []githubtest.Check{
		{Name: "ci", Conclusion: "SUCCESS"},
		{Name: "cumin-protected-paths", Conclusion: "SKIPPED"},
		{Name: "optional", Conclusion: "FAILURE"},
	})
	service := sc.service()

	sc.pollAndWait(t, service)

	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelMerging}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/merging after the approval", got)
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want one Reviewer run", n)
	}
	for _, want := range []string{"request the review: the pull request is ready for review", "start the merge: the Reviewer approved the head commit"} {
		if !strings.Contains(sc.logs.String(), want) {
			t.Errorf("the log does not say %q: %s", want, sc.logs)
		}
	}
	// The approval reads the required checks once more, for "start the merge".
	if n := sc.fake.CountRequests(http.MethodGet, branchRulesPath); n != 2 {
		t.Errorf("%d reads of the required checks, want 2 (the request of the review and the start of the merge)", n)
	}
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != 2 {
		t.Errorf("%d label changes, want 2 (to the review, and to the merge)", n)
	}
}

// TestAnEmptyListOfRequiredChecksPassesAtOnce: a repository without a
// ruleset moves to the review in the next poll (issue-states.md).
func TestAnEmptyListOfRequiredChecksPassesAtOnce(t *testing.T) {
	sc := newScene(t, cliOptions{reviews: []string{"APPROVE"}})
	sc.awaitingChecks(t, nil, nil)
	service := sc.service()

	sc.pollAndWait(t, service)

	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelMerging) {
		t.Errorf("labels of #10 = %v, want cumin/status/merging: the review ran and approved", got)
	}
}

// TestAFailedOrRunningCheckKeepsTheIssueWaiting: "request a check fix" answers a failure,
// and a check that has not finished is not an answer at all.
func TestAFailedOrRunningCheckKeepsTheIssueWaiting(t *testing.T) {
	for _, tc := range []struct {
		name  string
		check githubtest.Check
	}{
		{name: "failed", check: githubtest.Check{Name: "ci", Conclusion: "FAILURE"}},
		{name: "running", check: githubtest.Check{Name: "ci", Status: "IN_PROGRESS"}},
		{name: "not reported", check: githubtest.Check{Name: "other", Conclusion: "SUCCESS"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := newScene(t)
			sc.awaitingChecks(t, []string{"ci"}, []githubtest.Check{tc.check})
			service := sc.service()

			sc.pollAndWait(t, service)

			if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelChecking) {
				t.Errorf("labels of #10 = %v, want cumin/status/checking to stay", got)
			}
		})
	}
}

// TestTheRequiredChecksAreReadOnlyWhenAnIssueWaits keeps the extra REST
// call out of a poll that has nothing to decide.
func TestTheRequiredChecksAreReadOnlyWhenAnIssueWaits(t *testing.T) {
	sc := newScene(t)
	sc.repo.DefaultBranch = "main"
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := sc.fake.CountRequests(http.MethodGet, branchRulesPath); n != 0 {
		t.Errorf("%d reads of the required checks, want none while no issue waits", n)
	}
}

// The test of a top-level requirement in cumin-core.md: cumin changes a label
// of the issue, the Maintainer
// changes the risk of the issue, and the Maintainer changes a label of the pull
// request. After each, the next poll makes the cumin/status/* and risk/*
// labels of the pull request equal to those of the issue ("copy the labels to
// the pull request"), and the
// labels of the pull request change no decision (principle 5).
func TestTheLabelsOfThePullRequestFollowTheIssue(t *testing.T) {
	sc := newScene(t)
	// A pull request of another author: the run ends, "stop the implementation"
	// stops the issue for
	// the Maintainer, and no later rule moves it again.
	sc.addPullRequest(21, sc.remoteHead, "someone", false)
	service := sc.service()
	prLabelsPath := "/repos/example-org/example-repo/issues/21/labels"
	assertEqual := func(step string) {
		t.Helper()
		issue := sc.fake.Issue(sc.repo, 10).Labels
		pr := sc.fake.PullRequestLabels(sc.repo, 21)
		if !sameLabelsAnyOrder(workflow.PullRequestLabels(issue, pr), pr) {
			t.Errorf("%s: labels of the pull request = %v, want the status and risk of the issue %v", step, pr, issue)
		}
	}

	// cumin changes the label of the issue: "request the implementation", then
	// "stop the implementation" stops it.
	sc.pollAndWait(t, service)
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelAwaitingDecision) {
		t.Fatalf("labels of #10 = %v, want cumin/status/awaiting-decision", got)
	}
	sc.pollAndWait(t, service)
	assertEqual("after cumin changed the issue")

	// The Maintainer changes the risk of the issue.
	if err := sc.fake.SetLabels(sc.repo, 10, []string{"risk/high", workflow.LabelAwaitingDecision}); err != nil {
		t.Fatal(err)
	}
	sc.pollAndWait(t, service)
	assertEqual("after the Maintainer changed the risk of the issue")

	// The Maintainer changes the labels of the pull request, and adds
	// cumin/status/ready there. The issue is not claimed.
	if err := sc.fake.SetLabels(sc.repo, 21, []string{"docs", workflow.LabelReady, "risk/low"}); err != nil {
		t.Fatal(err)
	}
	sc.pollAndWait(t, service)
	assertEqual("after the Maintainer changed the pull request")
	if got := sc.fake.PullRequestLabels(sc.repo, 21); !slices.Contains(got, "docs") {
		t.Errorf("labels of the pull request = %v, want the label docs kept", got)
	}
	if n := sc.agentRuns(t); n != 2 {
		t.Errorf("%d agent runs, want 2 (the request and the second request): the labels of a pull request decide nothing", n)
	}

	// Equal labels cause no write.
	writes := sc.fake.CountRequests(http.MethodPut, prLabelsPath)
	sc.pollAndWait(t, service)
	if n := sc.fake.CountRequests(http.MethodPut, prLabelsPath); n != writes {
		t.Errorf("%d writes to the pull request after a poll with equal labels, want %d", n, writes)
	}
	if !strings.Contains(sc.logs.String(), `"msg":"copy the labels to the pull request: copied the labels of the issue to the pull request"`) {
		t.Error("the log has no line of the label copy")
	}
}

// sameLabelsAnyOrder reports whether a and b hold the same labels.
func sameLabelsAnyOrder(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// notReporting puts issue #10 in cumin/status/checking since the time
// of the scene, with the required checks ci, lint, and unit. Only unit
// reported: ci has no result, and lint has not finished.
func (sc *scene) notReporting(t *testing.T) {
	t.Helper()
	sc.awaitingChecks(t, []string{"ci", "lint", "unit"}, []githubtest.Check{
		{Name: "lint", Status: "IN_PROGRESS"},
		{Name: "unit", Conclusion: "SUCCESS"},
	})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{
		Number: 10, Parent: 6, Title: subIssueTitle,
		Labels:      []string{workflow.LabelChecking, "risk/low"},
		LabelEvents: []githubtest.LabelEvent{{Label: workflow.LabelChecking, At: sceneNow}},
	})
	sc.fake.SetPullRequestHeadCommitTime(sc.repo, 21, sceneNow.Add(-time.Minute))
}

// "stop for missing checks": required checks that do not report on the head
// commit within the wait time stop the issue for the Maintainer exactly once:
// one label change, one comment, and one notification, with the head
// commit, the checks that have not reported, and the time waited. Before
// the wait time is over, the poll changes nothing. No agent starts.
func TestRequiredChecksThatDoNotReportInTimeStopTheIssueOnce(t *testing.T) {
	sc := newScene(t)
	sc.notReporting(t)
	service := sc.service()

	sc.clock.Set(sceneNow.Add(59 * time.Minute))
	sc.pollAndWait(t, service)
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{workflow.LabelChecking, "risk/low"}) {
		t.Errorf("labels of #10 before the wait time is over = %v, want them unchanged", got)
	}
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != 0 {
		t.Errorf("%d label changes before the wait time is over, want none", n)
	}
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 0 {
		t.Errorf("%d comments before the wait time is over, want none", n)
	}
	if n := len(sc.messagesExceptWaiting()); n != 0 {
		t.Errorf("%d notifications before the wait time is over, want none", n)
	}

	sc.clock.Set(sceneNow.Add(61 * time.Minute))
	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)

	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelAwaitingDecision}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/awaiting-decision", got)
	}
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != 1 {
		t.Errorf("%d label changes, want 1", n)
	}
	facts := []string{"stop for missing checks", "(ci, lint)", "head commit " + sc.remoteHead, "waited 1h1m0s"}
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 {
		t.Fatalf("%d comments, want 1", len(comments))
	}
	for _, want := range append([]string{"Step: stop for missing checks", "Pull request: #21"}, facts...) {
		if !strings.Contains(comments[0].Body, want) {
			t.Errorf("the comment has no %q:\n%s", want, comments[0].Body)
		}
	}
	messages := sc.messagesExceptWaiting()
	if len(messages) != 1 {
		t.Fatalf("%d notifications, want 1: %v", len(messages), messages)
	}
	for _, want := range facts {
		if !strings.Contains(messages[0], want) {
			t.Errorf("the notification has no %q:\n%s", want, messages[0])
		}
	}
	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none", n)
	}
}

// "stop for missing checks": an issue in cumin/status/checking whose pull request someone
// closed stops for the Maintainer exactly once, after the wait time since the
// label. The comment and the notification say that no open pull request
// closes the issue, and the time waited. Before the wait time is over, the
// poll changes nothing.
func TestNoOpenPullRequestStopsTheIssueOnceAfterTheWaitTime(t *testing.T) {
	sc := newScene(t)
	sc.notReporting(t)
	if err := sc.fake.ClosePullRequest(sc.repo, 21); err != nil {
		t.Fatal(err)
	}
	service := sc.service()

	sc.clock.Set(sceneNow.Add(59 * time.Minute))
	sc.pollAndWait(t, service)
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != 0 {
		t.Errorf("%d label changes before the wait time is over, want none", n)
	}
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 0 {
		t.Errorf("%d comments before the wait time is over, want none", n)
	}
	if n := len(sc.messagesExceptWaiting()); n != 0 {
		t.Errorf("%d notifications before the wait time is over, want none", n)
	}

	sc.clock.Set(sceneNow.Add(61 * time.Minute))
	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)

	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", workflow.LabelAwaitingDecision}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/awaiting-decision", got)
	}
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != 1 {
		t.Errorf("%d label changes, want 1", n)
	}
	facts := []string{"stop for missing checks", "No open pull request closes this issue", "waited 1h1m0s"}
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 {
		t.Fatalf("%d comments, want 1", len(comments))
	}
	for _, want := range append([]string{"Step: stop for missing checks", "Pull request: None"}, facts...) {
		if !strings.Contains(comments[0].Body, want) {
			t.Errorf("the comment has no %q:\n%s", want, comments[0].Body)
		}
	}
	messages := sc.messagesExceptWaiting()
	if len(messages) != 1 {
		t.Fatalf("%d notifications, want 1: %v", len(messages), messages)
	}
	for _, want := range facts {
		if !strings.Contains(messages[0], want) {
			t.Errorf("the notification has no %q:\n%s", want, messages[0])
		}
	}
	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none", n)
	}
}

// "stop for missing checks": a new head commit while the issue waits starts
// the wait time again,
// from the commit time of that commit.
func TestANewHeadCommitStartsTheWaitTimeAgain(t *testing.T) {
	sc := newScene(t)
	sc.notReporting(t)
	sc.fake.SetPullRequestHeadCommitTime(sc.repo, 21, sceneNow.Add(30*time.Minute))
	service := sc.service()

	sc.clock.Set(sceneNow.Add(61 * time.Minute))
	sc.pollAndWait(t, service)
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelChecking) {
		t.Errorf("labels of #10 = %v, want cumin/status/checking to stay after the new head commit", got)
	}

	sc.clock.Set(sceneNow.Add(91 * time.Minute))
	sc.pollAndWait(t, service)
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelAwaitingDecision) {
		t.Errorf("labels of #10 = %v, want cumin/status/awaiting-decision", got)
	}
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 || !strings.Contains(comments[0].Body, "waited 1h1m0s") {
		t.Errorf("comments = %v, want one that says waited 1h1m0s", comments)
	}
}

// "stop for missing checks": at the stop, the label changes first. When it
// cannot change, cumin
// posts no comment and sends no notification, so that the polls that
// follow do not repeat them.
func TestAStopForMissingChecksWhoseLabelFailsWritesNothingElse(t *testing.T) {
	sc := newScene(t)
	sc.notReporting(t)
	service := sc.service()
	sc.clock.Set(sceneNow.Add(61 * time.Minute))
	sc.fake.FailNext(http.MethodPut, putLabelsPath, http.StatusInternalServerError)

	if err := service.Poll(context.Background()); err == nil {
		t.Error("the poll hid the failed label change")
	}
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 0 {
		t.Errorf("%d comments, want none", n)
	}
	if n := len(sc.messagesExceptWaiting()); n != 0 {
		t.Errorf("%d notifications, want none", n)
	}

	sc.pollAndWait(t, service)
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 1 {
		t.Errorf("%d comments after the label changed, want 1", n)
	}
	if n := len(sc.messagesExceptWaiting()); n != 1 {
		t.Errorf("%d notifications after the label changed, want 1", n)
	}
}

// "stop for missing checks": checks that report after the wait time still go on to the review
// ("request the review"): a result decides before the wait time does.
func TestChecksThatReportedAfterTheWaitTimeGoOnToTheReview(t *testing.T) {
	sc := newScene(t, cliOptions{reviews: []string{"APPROVE"}})
	sc.awaitingChecks(t, []string{"ci"}, []githubtest.Check{{Name: "ci", Conclusion: "SUCCESS"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{
		Number: 10, Parent: 6, Title: subIssueTitle,
		Labels:      []string{workflow.LabelChecking, "risk/low"},
		LabelEvents: []githubtest.LabelEvent{{Label: workflow.LabelChecking, At: sceneNow}},
	})
	service := sc.service()
	sc.clock.Set(sceneNow.Add(2 * time.Hour))

	sc.pollAndWait(t, service)

	if got := sc.fake.Issue(sc.repo, 10).Labels; slices.Contains(got, workflow.LabelAwaitingDecision) {
		t.Errorf("labels of #10 = %v, want no stop for the Maintainer", got)
	}
	if !strings.Contains(sc.logs.String(), "request the review: the pull request is ready for review") {
		t.Errorf("the log does not say that cumin requested the review: %s", sc.logs)
	}
}
