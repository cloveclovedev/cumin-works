package workflow_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

// newRequirementScene is a scene for R3 and R6. The sub-issue #19 is in
// cumin/status/reviewing, so it fills the limit of issues in progress (1 in
// the scene) and no agent starts: these rows move labels only.
func newRequirementScene(t *testing.T, requirementLabels ...string) *scene {
	t.Helper()
	sc := newScene(t)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 6, Labels: append([]string{githubtest.RequirementLabel}, requirementLabels...)})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Labels: []string{"risk/low"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 19, Parent: 6, Title: "Busy", Labels: []string{"cumin/status/reviewing", "risk/low"}})
	return sc
}

func requirementLabels(t *testing.T, sc *scene) []string {
	t.Helper()
	return sc.fake.Issue(sc.repo, 6).Labels
}

// Core-13 (cumin-core.md): a sub-issue that kept cumin/status/ready from
// before the review label does not move the requirement issue; a
// cumin/status/ready that the Owner adds afterwards does.
func TestCore13_OnlyAReadyAddedAfterTheReviewMovesTheRequirementIssue(t *testing.T) {
	review := sceneNow.Add(-time.Hour)
	sc := newRequirementScene(t)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 6, Labels: []string{githubtest.RequirementLabel, "cumin/status/awaiting-plan-review"},
		LabelEvents: []githubtest.LabelEvent{{Label: "cumin/status/awaiting-plan-review", At: review}}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Labels: []string{"cumin/status/ready", "risk/low"},
		LabelEvents: []githubtest.LabelEvent{{Label: "cumin/status/ready", At: review.Add(-time.Hour)}}})
	service := sc.service()

	sc.pollAndWait(t, service)
	if got := requirementLabels(t, sc); !slices.Contains(got, "cumin/status/awaiting-plan-review") {
		t.Fatalf("labels of #6 = %v, want awaiting-plan-review kept", got)
	}
	// The two queries of the poll, and the query of the label times.
	if n := sc.fake.CountRequests(http.MethodPost, "/graphql"); n != 3 {
		t.Errorf("%d GraphQL requests, want 3", n)
	}

	// The Owner reviews the new sub-issue and lets it start.
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 11, Parent: 6, Title: "New", Labels: []string{"cumin/status/ready", "risk/low"},
		LabelEvents: []githubtest.LabelEvent{{Label: "cumin/status/ready", At: review.Add(time.Minute)}}})
	sc.pollAndWait(t, service)

	want := []string{githubtest.RequirementLabel, "cumin/status/implementing"}
	if got := requirementLabels(t, sc); !slices.Equal(got, want) {
		t.Errorf("labels of #6 = %v, want %v", got, want)
	}
	if !strings.Contains(sc.logs.String(), `"msg":"R3: the sub-issues of the requirement issue are in progress"`) {
		t.Error("the log has no R3 line")
	}
	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none", n)
	}
}

// R3 after the acceptance check: the requirement issue waits in
// cumin/status/awaiting-acceptance, and the Owner sends work back with a new
// sub-issue. cumin compares its cumin/status/ready with the time of
// cumin/status/awaiting-acceptance, and marks the requirement as in work.
func TestR3_AReadyAddedAfterTheAcceptanceMovesTheRequirementIssue(t *testing.T) {
	accepted := sceneNow.Add(-time.Hour)
	sc := newRequirementScene(t)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 6, Labels: []string{githubtest.RequirementLabel, "cumin/status/awaiting-acceptance"},
		LabelEvents: []githubtest.LabelEvent{
			{Label: "cumin/status/awaiting-plan-review", At: accepted.Add(2 * time.Hour)},
			{Label: "cumin/status/awaiting-acceptance", At: accepted},
		}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Closed: true, Labels: []string{"risk/low"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 11, Parent: 6, Title: "New", Labels: []string{"cumin/status/ready", "risk/low"},
		LabelEvents: []githubtest.LabelEvent{{Label: "cumin/status/ready", At: accepted.Add(time.Minute)}}})

	sc.pollAndWait(t, sc.service())

	want := []string{githubtest.RequirementLabel, "cumin/status/implementing"}
	if got := requirementLabels(t, sc); !slices.Equal(got, want) {
		t.Errorf("labels of #6 = %v, want %v", got, want)
	}
}

// R3 without a status label on the requirement issue: the Owner wrote the
// sub-issues by hand, and one ready sub-issue is enough. No label times are
// read.
func TestR3_ARequirementIssueWithoutAStatusLabelFollowsAReadySubIssue(t *testing.T) {
	sc := newRequirementScene(t)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Labels: []string{"cumin/status/ready", "risk/low"}})
	sc.pollAndWait(t, sc.service())

	want := []string{githubtest.RequirementLabel, "cumin/status/implementing"}
	if got := requirementLabels(t, sc); !slices.Equal(got, want) {
		t.Errorf("labels of #6 = %v, want %v", got, want)
	}
	// The two queries of the poll: #10 is open and has a status label.
	if n := sc.fake.CountRequests(http.MethodPost, "/graphql"); n != 2 {
		t.Errorf("%d GraphQL requests, want 2", n)
	}
}

// Core-12 (cumin-core.md): the Owner let only a part of the sub-issues
// start, and those closed. The requirement issue goes back to the Owner
// with one notification; a ready on a remaining sub-issue moves it to
// implementing again.
func TestCore12_TheRemainingSubIssuesGoBackToTheOwnerOnce(t *testing.T) {
	sc := newRequirementScene(t, "cumin/status/implementing")
	// #19 closed: the part that the Owner let start is done.
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 19, Parent: 6, Title: "Done", Closed: true, Labels: []string{"risk/low"}})
	// A sub-issue of another requirement issue fills the limit, so the
	// ready below starts no agent.
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 7, Labels: []string{githubtest.RequirementLabel, "cumin/status/implementing"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 29, Parent: 7, Title: "Busy", Labels: []string{"cumin/status/reviewing", "risk/low"}})
	service := sc.service()

	sc.pollAndWait(t, service)
	sc.pollAndWait(t, service)

	want := []string{githubtest.RequirementLabel, "cumin/status/awaiting-plan-review"}
	if got := requirementLabels(t, sc); !slices.Equal(got, want) {
		t.Fatalf("labels of #6 = %v, want %v", got, want)
	}
	messages := sc.messagesExceptQ4()
	if len(messages) != 1 {
		t.Fatalf("%d notifications after two polls, want 1: %v", len(messages), messages)
	}
	for _, want := range []string{"R6", "need a review", "issue #6", "/issues/6"} {
		if !strings.Contains(messages[0], want) {
			t.Errorf("the notification has no %q:\n%s", want, messages[0])
		}
	}

	// The Owner lets the remaining sub-issue start.
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Labels: []string{"cumin/status/ready", "risk/low"},
		LabelEvents: []githubtest.LabelEvent{{Label: "cumin/status/ready", At: sceneNow.Add(time.Minute)}}})
	sc.pollAndWait(t, service)

	if got := requirementLabels(t, sc); !slices.Contains(got, "cumin/status/implementing") {
		t.Errorf("labels of #6 = %v, want implementing again", got)
	}
	if n := len(sc.messagesExceptQ4()); n != 1 {
		t.Errorf("%d notifications, want still 1", n)
	}
}

// R6 waits while a sub-issue is open with a status label.
func TestR6_AnOpenSubIssueWithAStatusLabelKeepsImplementing(t *testing.T) {
	sc := newRequirementScene(t, "cumin/status/implementing")
	sc.pollAndWait(t, sc.service())

	if got := requirementLabels(t, sc); !slices.Contains(got, "cumin/status/implementing") {
		t.Errorf("labels of #6 = %v, want implementing kept", got)
	}
	if n := len(sc.messagesExceptQ4()); n != 0 {
		t.Errorf("%d notifications, want none", n)
	}
}

// When R3 cannot move the requirement issue, its ready sub-issue is not
// claimed in the same poll: the claim would take away the cumin/status/ready
// that R3 needs to apply again.
func TestR3_AFailedMoveKeepsTheSubIssueReady(t *testing.T) {
	sc := newScene(t)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 6, Labels: []string{githubtest.RequirementLabel}})
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	sc.fake.FailNext(http.MethodPut, "/repos/example-org/example-repo/issues/6/labels", http.StatusInternalServerError)
	service := sc.service()

	if err := service.Poll(context.Background()); err == nil {
		t.Fatal("the poll returned no error for the failed move")
	}
	service.Wait()
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/ready") {
		t.Fatalf("labels of #10 = %v, want cumin/status/ready kept", got)
	}
	if n := sc.agentRuns(t); n != 0 {
		t.Fatalf("%d agent runs, want none", n)
	}

	sc.pollAndWait(t, service)
	if got := requirementLabels(t, sc); !slices.Contains(got, "cumin/status/implementing") {
		t.Errorf("labels of #6 = %v, want implementing", got)
	}
	if n := sc.agentRuns(t); n != 1 {
		t.Errorf("%d agent runs, want 1", n)
	}
}

// A poll of a repository with no open sub-issue with a status label sends
// the poll query only: no second query reads pull requests.
func TestPoll_NoSubIssueWithAStatusLabelSendsNoSecondQuery(t *testing.T) {
	sc := newScene(t)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 6, Labels: []string{githubtest.RequirementLabel}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Labels: []string{"risk/low"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 11, Parent: 6, Title: "Done", Closed: true, Labels: []string{"cumin/status/checking", "risk/low"}})
	sc.fake.AddPullRequest(sc.repo, &githubtest.PullRequest{Number: 20, Closes: []int{10}})
	sc.pollAndWait(t, sc.service())

	if n := sc.fake.CountRequests(http.MethodPost, "/graphql"); n != 1 {
		t.Errorf("%d GraphQL requests, want 1 (the poll query)", n)
	}
	if n := sc.agentRuns(t); n != 0 {
		t.Errorf("%d agent runs, want none", n)
	}
}
