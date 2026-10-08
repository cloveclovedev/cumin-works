package workflow_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

// "wait for the checks": after done, an open pull request closes the issue,
// its author is the Implementer App, and the head commit of the worktree is
// pushed. Then the label becomes cumin/status/checking.
func TestDoneWithTheVerifiedPullRequestMovesTheIssueToAwaitingChecks(t *testing.T) {
	sc := newScene(t)
	// The agent makes no commit, so the head of the worktree is the head of
	// main of the remote. The pull request is at the same commit.
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := sc.service()

	sc.pollAndWait(t, service)

	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", "cumin/status/checking"}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/checking", got)
	}
	if n := sc.fake.CountRequests(http.MethodPut, putLabelsPath); n != 2 {
		t.Errorf("%d label changes, want 2 (the claim and the wait for the checks)", n)
	}
	// The end of the run reads the issue again, so that a pull request
	// that the agent opened just before it ended is seen.
	// The read of the login of the Issue Owner before the start is one more
	// GraphQL request.
	if n := sc.fake.CountRequests(http.MethodPost, "/graphql"); n != 6 {
		t.Errorf("%d GraphQL requests, want 6 (the two queries of the poll, the login of the Issue Owner, and the three reads after the run)", n)
	}
	logs := sc.logs.String()
	for _, want := range []string{`"msg":"wait for the checks: verified the pull request"`, `"pull_request":21`, `"issue":10`} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log has no %s:\n%s", want, logs)
		}
	}
}

// Of two open pull requests that close the issue, the one with the highest
// number is checked (poll.md, the topic on the end of a run).
func TestDoneChecksThePullRequestWithTheHighestNumber(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, "0000000000000000000000000000000000000000", implementerSlug, true)
	sc.addPullRequest(22, sc.remoteHead, implementerSlug, true)
	service := sc.service()

	sc.pollAndWait(t, service)

	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/checking") {
		t.Errorf("labels of #10 = %v, want cumin/status/checking", got)
	}
	if !strings.Contains(sc.logs.String(), `"pull_request":22`) {
		t.Errorf("the log does not name pull request 22:\n%s", sc.logs.String())
	}
}

func TestDoneWithoutAPullRequestStopsTheIssue(t *testing.T) {
	sc := newScene(t)
	service := sc.service()

	sc.pollAndWait(t, service)

	assertVerificationFailed(t, sc, "no open pull request is on the branch of the issue", workflow.FailureNoOpenPullRequest, 0)
}

func TestDoneWithAPullRequestOfAnotherAuthorStopsTheIssue(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, sc.remoteHead, "another-person", false)
	service := sc.service()

	sc.pollAndWait(t, service)

	assertVerificationFailed(t, sc, "the author of the pull request is not the Implementer App", workflow.FailureAuthorMismatch, 21)
	if !strings.Contains(sc.logs.String(), `"pull_request":21`) {
		t.Errorf("the log does not name the pull request that was checked:\n%s", sc.logs.String())
	}
}

// The agent commits in the worktree and does not push. The head of the pull
// request is then behind the head of the worktree.
func TestDoneWithACommitThatIsNotPushedStopsTheIssue(t *testing.T) {
	sc := newScene(t, cliOptions{commit: true})
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := sc.service()

	sc.pollAndWait(t, service)

	assertVerificationFailed(t, sc, "the head commit of the worktree is not pushed", workflow.FailureHeadNotPushed, 21)
}

// "wait for the checks": GitHub made no closing link from "Closes #10". The
// pull request is found on the branch that cumin chose, cumin-core adds
// exactly one link, reads it back, and the issue moves on.
func TestAPullRequestWithoutALinkGetsExactlyOneLink(t *testing.T) {
	sc := newScene(t)
	sc.addUnlinkedPullRequest(21, sc.remoteHead)
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := closingLinkRequests(sc); n != 1 {
		t.Errorf("%d closing link requests, want 1", n)
	}
	if got := sc.fake.PullRequestCloses(sc.repo, 21); !slices.Equal(got, []int{10}) {
		t.Errorf("pull request #21 closes %v, want [10]", got)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", "cumin/status/checking"}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/checking", got)
	}
	logs := sc.logs.String()
	for _, want := range []string{`"msg":"wait for the checks: added the closing link"`, `"msg":"wait for the checks: verified the pull request"`, `"pull_request":21`} {
		if !strings.Contains(logs, want) {
			t.Errorf("the log has no %s:\n%s", want, logs)
		}
	}
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 0 {
		t.Errorf("%d comments on #10, want none", n)
	}
}

// When GitHub already linked the pull request, cumin adds nothing.
func TestALinkedPullRequestGetsNoLink(t *testing.T) {
	sc := newScene(t)
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := closingLinkRequests(sc); n != 0 {
		t.Errorf("%d closing link requests, want none", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, "cumin/status/checking") {
		t.Errorf("labels of #10 = %v, want cumin/status/checking", got)
	}
}

// A pull request of the Implementer App on another branch is not the pull
// request of the issue, even when it is linked: the issue stops as today.
func TestAPullRequestOnAnotherBranchIsNotTaken(t *testing.T) {
	sc := newScene(t)
	sc.fake.AddPullRequest(sc.repo, &githubtest.PullRequest{
		Number: 21, HeadCommit: sc.remoteHead, Author: implementerSlug, AuthorIsBot: true, HeadBranch: "cumin/10-another-branch",
	})
	service := sc.service()

	sc.pollAndWait(t, service)

	assertVerificationFailed(t, sc, "no open pull request is on the branch of the issue", workflow.FailureNoOpenPullRequest, 0)
	if n := closingLinkRequests(sc); n != 0 {
		t.Errorf("%d closing link requests, want none", n)
	}
}

// A closing link that GitHub refuses stops the issue once, and the comment
// and the notification name the answer of GitHub.
func TestAFailedLinkStopsTheIssueOnce(t *testing.T) {
	sc := newScene(t)
	sc.addUnlinkedPullRequest(21, sc.remoteHead)
	sc.fake.SetLinkErrors("Resource not accessible by integration")
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := closingLinkRequests(sc); n != 1 {
		t.Errorf("%d closing link requests, want 1", n)
	}
	assertImplementationStopped(t, sc, workflow.LinkFailedReason(21, "Resource not accessible by integration"), 21)
	if !strings.Contains(sc.fake.Comments(sc.repo, 10)[0].Body, "GitHub answered: Resource not accessible by integration.") {
		t.Errorf("the comment does not name the answer of GitHub:\n%s", sc.fake.Comments(sc.repo, 10)[0].Body)
	}
}

// A link that GitHub accepted and the issue does not show stops the issue
// once.
func TestALinkThatIsMissingAfterwardsStopsTheIssueOnce(t *testing.T) {
	sc := newScene(t)
	sc.addUnlinkedPullRequest(21, sc.remoteHead)
	sc.fake.IgnoreLinks()
	service := sc.service()

	sc.pollAndWait(t, service)

	assertImplementationStopped(t, sc, workflow.LinkMissingReason(21), 21)
}

// An issue that GitHub already links to as many open pull requests as the
// poll reads gets no more links: one more would make every later poll of
// the repository fail. The issue stops once instead.
func TestALinkOverTheLimitOfThePollIsNotAdded(t *testing.T) {
	sc := newScene(t)
	// Two pull requests without an author already close #10; #19 is on the
	// branch of the issue, so the claim continues on that branch. The
	// Implementer App opened #21 on the same branch, and GitHub linked it
	// to nothing.
	sc.fake.AddPullRequest(sc.repo, &githubtest.PullRequest{Number: 18, HeadCommit: sc.remoteHead, HeadBranch: "cumin/10-old", Closes: []int{10}})
	sc.fake.AddPullRequest(sc.repo, &githubtest.PullRequest{Number: 19, HeadCommit: sc.remoteHead, HeadBranch: wantBranch, Closes: []int{10}})
	sc.addUnlinkedPullRequest(21, sc.remoteHead)
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := closingLinkRequests(sc); n != 0 {
		t.Errorf("%d closing link requests, want none", n)
	}
	assertVerificationFailed(t, sc, "the issue has too many open closing pull requests for one more link", workflow.FailureTooManyLinks, 21)
}

// closingLinkRequests counts the requests of the closing link
// (addCloseIssueReferences).
func closingLinkRequests(sc *scene) int {
	n := 0
	for _, r := range sc.fake.Requests() {
		if r.Path == "/graphql" && strings.Contains(string(r.Body), "addCloseIssueReferences") {
			n++
		}
	}
	return n
}

// An abnormal end is decided from the facts on GitHub, as every other end:
// the pull request of the run is verified, so the issue moves to
// cumin/status/checking with no second request, and nothing is said to the
// Maintainer.
func TestAnAbnormalEndWithAVerifiedPullRequestWaitsForTheChecks(t *testing.T) {
	sc := newScene(t, cliOptions{fixture: "is-error.jsonl", secondFixture: "done.jsonl"})
	sc.addPullRequest(21, sc.remoteHead, implementerSlug, true)
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := sc.agentRuns(t); n != 1 {
		t.Fatalf("%d agent runs, want 1", n)
	}
	if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Equal(got, []string{"risk/low", "cumin/status/checking"}) {
		t.Errorf("labels of #10 = %v, want risk/low and cumin/status/checking", got)
	}
	if n := len(sc.fake.Comments(sc.repo, 10)); n != 0 {
		t.Errorf("%d comments on #10, want none", n)
	}
	if messages := sc.webhook.messagesSent(); len(messages) != 0 {
		t.Errorf("%d notifications, want none: %v", len(messages), messages)
	}
}
