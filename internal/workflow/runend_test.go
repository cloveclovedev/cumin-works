package workflow_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
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
