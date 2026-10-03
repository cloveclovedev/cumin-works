package workflow_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

// pollQueries counts the poll queries that the fake GitHub received. Only
// the poll query carries the variable repositoryFiles.
func (sc *scene) pollQueries() int {
	n := 0
	for _, r := range sc.fake.Requests() {
		if r.Method == http.MethodPost && r.Path == "/graphql" && strings.Contains(string(r.Body), `"repositoryFiles"`) {
			n++
		}
	}
	return n
}

// issueReads counts the queries that read one issue with the fields of the
// poll query: they carry the number of the issue and the page size of the
// pull requests.
func (sc *scene) issueReads(number int) int {
	n := 0
	for _, r := range sc.fake.Requests() {
		body := string(r.Body)
		if r.Method == http.MethodPost && r.Path == "/graphql" &&
			strings.Contains(body, fmt.Sprintf(`"number":%d,`, number)) && strings.Contains(body, `"pullRequests"`) {
			n++
		}
	}
	return n
}

// After an Implementer run, cumin reads only the issue of the run: once for
// the verification (I2), and once more after it added the closing link. The
// one poll query is the poll that started the run.
func TestI2_TheEndOfAnImplementerRunReadsOnlyTheIssueOfTheRun(t *testing.T) {
	sc := newScene(t)
	sc.addUnlinkedPullRequest(21, sc.remoteHead)

	sc.pollAndWait(t, sc.service())

	if !strings.Contains(sc.logs.String(), `"msg":"I2: verified the pull request"`) {
		t.Fatal("the log does not say that I2 verified the pull request")
	}
	if n := sc.pollQueries(); n != 1 {
		t.Errorf("%d poll queries, want 1 (the poll; none after the run)", n)
	}
	if n := sc.issueReads(10); n != 2 {
		t.Errorf("%d reads of issue #10, want 2 (the verification, and after the closing link)", n)
	}
}

// After a Reviewer run and after the approval of the Reviewer, cumin reads
// only the issue of the run: once to check the review, and once to decide
// the merge (I6).
func TestI6_TheApprovalOfTheReviewerReadsOnlyTheIssueOfTheRun(t *testing.T) {
	sc := approved(t, "risk/low")

	sc.pollAndWait(t, sc.service())

	if !strings.Contains(sc.logs.String(), `"msg":"I6: merged the pull request"`) {
		t.Fatal("the log does not say that I6 merged the pull request")
	}
	if n := sc.pollQueries(); n != 1 {
		t.Errorf("%d poll queries, want 1 (the poll; none after the run)", n)
	}
	if n := sc.issueReads(10); n != 2 {
		t.Errorf("%d reads of issue #10, want 2 (the review, and the decision of the merge)", n)
	}
}

// After a Planner run, cumin reads only the requirement issue of the run,
// with its sub-issues (R2).
func TestR2_TheEndOfAPlannerRunReadsOnlyTheIssueOfTheRun(t *testing.T) {
	sc := newPlanScene(t)

	sc.pollAndWait(t, sc.service())

	if !strings.Contains(sc.logs.String(), `"msg":"R2: the split waits for the Owner"`) {
		t.Fatal("the log does not say that the split waits for the Owner")
	}
	if n := sc.pollQueries(); n != 1 {
		t.Errorf("%d poll queries, want 1 (the poll; none after the run)", n)
	}
	if n := sc.issueReads(6); n != 1 {
		t.Errorf("%d reads of issue #6, want 1", n)
	}
}

// closeRequirementIssue closes the requirement issue #6, as the Owner does
// on GitHub.
func (sc *scene) closeRequirementIssue(t *testing.T) {
	t.Helper()
	requirement := sc.fake.Issue(sc.repo, 6)
	requirement.Closed = true
	sc.fake.AddIssue(sc.repo, requirement)
}

// Principle 6 (issue-states.md): closed requirement issues and their
// sub-issues are not read. A requirement issue that closes during an
// Implementer run gets no action at the end of the run: a poll would not
// read its sub-issue, so the read of one issue does not either, and the
// label stays.
func TestI2_ARequirementIssueClosedDuringTheRunGetsNoAction(t *testing.T) {
	sc := newScene(t, cliOptions{holds: true})
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := sc.service()
	if err := service.Poll(t.Context()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	waitForAgentRun(t, sc)

	sc.closeRequirementIssue(t)
	sc.release(t)
	service.Wait()

	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", "cumin/status/implementing"}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/implementing (no action)", got)
	}
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 0 {
		t.Errorf("%d comments on #10, want none", n)
	}
	if !strings.Contains(sc.logs.String(), "the parent of issue #10 is not a requirement issue of the poll: issue #6 is not open") {
		t.Error("the log does not say that the requirement issue #6 is not open")
	}
	if n := sc.pollQueries(); n != 1 {
		t.Errorf("%d poll queries, want 1", n)
	}
}

// The same for the approval of the Reviewer: a risk/low pull request is not
// merged when the requirement issue closed during the Reviewer run.
func TestI6_ARequirementIssueClosedDuringTheReviewerRunIsNotMerged(t *testing.T) {
	sc := newScene(t, cliOptions{reviews: []string{"APPROVE"}, holds: true})
	sc.awaitingChecks(t, []string{"ci"}, []githubtest.Check{{Name: "ci", Conclusion: "SUCCESS"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{
		Number: 10, Parent: 6, Title: subIssueTitle,
		Labels: []string{"cumin/status/awaiting-checks", "risk/low"},
	})
	service := sc.service()
	if err := service.Poll(t.Context()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	waitForAgentRun(t, sc)

	sc.closeRequirementIssue(t)
	sc.release(t)
	service.Wait()

	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
		t.Errorf("%d merge requests, want none", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", "cumin/status/reviewing"}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/reviewing (no action)", got)
	}
	if !strings.Contains(sc.logs.String(), "the parent of issue #10 is not a requirement issue of the poll: issue #6 is not open") {
		t.Error("the log does not say that the requirement issue #6 is not open")
	}
}

// The same for the Planner: a requirement issue that closes during the
// split keeps its label, and the Owner gets no notification.
func TestR2_ARequirementIssueClosedDuringTheSplitGetsNoAction(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "planner-done.jsonl", holds: true})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 6, Labels: []string{githubtest.RequirementLabel, "cumin/status/ready"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{Number: 10, Parent: 6, Title: subIssueTitle, Labels: []string{"risk/low"}})
	service := sc.service()
	if err := service.Poll(t.Context()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	waitForAgentRun(t, sc)

	sc.closeRequirementIssue(t)
	sc.release(t)
	service.Wait()

	want := []string{githubtest.RequirementLabel, "cumin/status/planning"}
	if got := sc.fake.Issue(sc.repo, 6).Labels; !slices.Equal(got, want) {
		t.Errorf("labels of #6 = %v, want %v (no action)", got, want)
	}
	if messages := sc.webhook.messagesSent(); len(messages) != 0 {
		t.Errorf("%d notifications, want none: %v", len(messages), messages)
	}
	if !strings.Contains(sc.logs.String(), "issue #6 is not open") {
		t.Error("the log does not say that the requirement issue #6 is not open")
	}
}
