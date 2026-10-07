package github_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestLiveCloseReferences measures what "wait for the checks" and the merge of #222 rely on,
// without the automatic link of "Closes #N": the cumin-core App adds a
// closing link with the GraphQL mutation addCloseIssueReferences, reads it
// back, and closes an issue once and twice. Whether a merge closes an issue
// that the mutation linked is only recorded, never a pass condition.
//
// Official, GraphQL reference, Issues: addCloseIssueReferences "adds one or
// more pull requests as manually linked closing references on an issue"
// (input issueId, pullRequestIds). The reference names no permission for
// the mutation, so this test measures it.
func TestLiveCloseReferences(t *testing.T) {
	l := newLive(t)
	defer func() { t.Log("\n" + l.table()) }()

	core := l.token(t, "cumin-core")
	planner := l.token(t, "planner")
	implementer := l.token(t, "implementer")

	botLogin := l.botLogin(t, "implementer")
	repo := l.newGitRepo(t, implementer, botLogin, botLogin+"@users.noreply.github.com")

	// The pull request has no "Closes #N" in its body, so any link comes
	// from the mutation.
	parent := l.createIssue(t, planner, "test: live close references parent "+l.runID, []string{requirementLabel}, 0)
	linked := l.createIssue(t, planner, "test: live close references linked "+l.runID, nil, parent.ID)
	closing := l.createIssue(t, planner, "test: live close references closed by cumin-core "+l.runID, nil, parent.ID)

	branch := "live-" + l.runID + "-close"
	sha := repo.commitFile(t, branch, "live/"+l.runID+"-close.md", "live close references\n")
	l.pushBranch(t, repo, implementer, branch)
	pull := l.openPullWithBody(t, implementer, branch, "test: live close references "+l.runID, "A live check of cumin-works. The test closes it.")

	// Fact 1: the cumin-core App adds the link.
	issueID, pullID := l.nodeID(t, core, "issues", linked.Number), l.nodeID(t, core, "pulls", pull.Number)
	mutation := `mutation($issue: ID!, $pull: ID!) {
  addCloseIssueReferences(input: {issueId: $issue, pullRequestIds: [$pull]}) { issue { number } }
}`
	resp := l.api(t, core, http.MethodPost, "/graphql", map[string]any{"query": mutation, "variables": map[string]any{"issue": issueID, "pull": pullID}})
	errs := graphQLErrors(t, resp)
	l.record("C1", "The cumin-core App calls `addCloseIssueReferences` for a pull request of the Implementer App", "Success",
		fmt.Sprintf("Status %d, errors: [%s]", resp.status, strings.Join(errs, "; ")))
	if resp.status != http.StatusOK || len(errs) > 0 {
		t.Fatalf("fact C1: status %d, errors %v", resp.status, errs)
	}

	// Fact 2: the link is in closedByPullRequestsReferences right away.
	numbers := l.closingPullRequests(t, core, linked.Number)
	l.record("C2", "`closedByPullRequestsReferences` of the issue, read right after the mutation", "It holds the pull request",
		fmt.Sprintf("Pull requests: %v (the pull request is #%d)", numbers, pull.Number))
	if !containsNumber(numbers, pull.Number) {
		t.Errorf("fact C2: %v does not hold #%d", numbers, pull.Number)
	}

	// Fact 3: the cumin-core App closes an issue, as completed.
	closed := map[string]any{"state": "closed", "state_reason": "completed"}
	path := fmt.Sprintf("/repos/{repo}/issues/%d", closing.Number)
	first := l.api(t, core, http.MethodPatch, path, closed)
	var state struct {
		State       string `json:"state"`
		StateReason string `json:"state_reason"`
	}
	if first.status == http.StatusOK {
		first.json(t, &state)
	}
	before := l.closedEvents(t, core, closing.Number)
	l.record("C3", "The cumin-core App closes an issue (`PATCH` with `state: closed`, `state_reason: completed`)", "Status 200, the issue is closed as completed",
		fmt.Sprintf("Status %d, state `%s`, reason `%s`, `closed` events: %d", first.status, state.State, state.StateReason, before))
	if first.status != http.StatusOK || state.State != "closed" || state.StateReason != "completed" {
		t.Errorf("fact C3: status %d, state %q, reason %q: %s", first.status, state.State, state.StateReason, first.message())
	}

	// Fact 4: closing a closed issue changes nothing.
	second := l.api(t, core, http.MethodPatch, path, closed)
	after := l.closedEvents(t, core, closing.Number)
	l.record("C4", "The cumin-core App closes the same issue again", "No error, no new `closed` event",
		fmt.Sprintf("Status %d, `closed` events: %d before, %d after", second.status, before, after))
	if second.status != http.StatusOK || after != before {
		t.Errorf("fact C4: status %d, closed events %d then %d: %s", second.status, before, after, second.message())
	}

	// Observation 5: the cumin-core App merges the pull request. Whether
	// GitHub closes the linked issue is recorded only.
	// The merge needs every required check of the default branch. The
	// sandbox may require the checks of the fixture workflow too.
	status, required := l.requiredChecks(t, core)
	if status != http.StatusOK {
		t.Fatalf("observation C5: read the required checks: status %d", status)
	}
	for _, name := range required {
		l.waitForCheck(t, core, sha, name)
	}
	merge := l.api(t, core, http.MethodPut, fmt.Sprintf("/repos/{repo}/pulls/%d/merge", pull.Number), map[string]any{"merge_method": "squash", "sha": sha})
	if merge.status != http.StatusOK {
		t.Fatalf("observation C5: the merge failed: status %d: %s", merge.status, merge.message())
	}
	closedAfter := "not closed within 30 seconds"
	start := time.Now()
	// The last read is at or after the end of the window, so a close in
	// the last sleep is seen.
	for {
		var current struct {
			State string `json:"state"`
		}
		l.api(t, core, http.MethodGet, fmt.Sprintf("/repos/{repo}/issues/%d", linked.Number), nil).mustJSON(t, http.StatusOK, &current)
		if current.State == "closed" {
			closedAfter = fmt.Sprintf("closed after about %d seconds", int(time.Since(start).Seconds()))
			break
		}
		if time.Since(start) >= 30*time.Second {
			break
		}
		time.Sleep(5 * time.Second)
	}
	l.record("C5", "The cumin-core App merges the pull request that the mutation linked (observation only)", "Recorded, not assumed",
		fmt.Sprintf("Merge: status %d. The linked issue: %s", merge.status, closedAfter))
}

// nodeID reads the GraphQL node ID of an issue or a pull request (kind
// "issues" or "pulls") through REST.
func (l *live) nodeID(t *testing.T, token, kind string, number int) string {
	t.Helper()
	var node struct {
		NodeID string `json:"node_id"`
	}
	l.api(t, token, http.MethodGet, fmt.Sprintf("/repos/{repo}/%s/%d", kind, number), nil).mustJSON(t, http.StatusOK, &node)
	if node.NodeID == "" {
		t.Fatalf("%s %d has no node_id", kind, number)
	}
	return node.NodeID
}

// closingPullRequests reads the numbers of the open pull requests that close
// the issue, as the poll of cumin reads them.
func (l *live) closingPullRequests(t *testing.T, token string, number int) []int {
	t.Helper()
	query := `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    issue(number: $number) { closedByPullRequestsReferences(first: 5) { nodes { number } } }
  }
}`
	resp := l.api(t, token, http.MethodPost, "/graphql", map[string]any{"query": query, "variables": map[string]any{"owner": l.owner, "name": l.repo, "number": number}})
	if errs := graphQLErrors(t, resp); resp.status != http.StatusOK || len(errs) > 0 {
		t.Fatalf("read the closing pull requests: status %d, errors %v", resp.status, errs)
	}
	var result struct {
		Data struct {
			Repository struct {
				Issue struct {
					ClosedBy struct{ Nodes []struct{ Number int } } `json:"closedByPullRequestsReferences"`
				} `json:"issue"`
			} `json:"repository"`
		} `json:"data"`
	}
	resp.json(t, &result)
	var numbers []int
	for _, node := range result.Data.Repository.Issue.ClosedBy.Nodes {
		numbers = append(numbers, node.Number)
	}
	return numbers
}

// closedEvents counts the "closed" events of an issue.
func (l *live) closedEvents(t *testing.T, token string, number int) int {
	t.Helper()
	var events []struct {
		Event string `json:"event"`
	}
	l.api(t, token, http.MethodGet, fmt.Sprintf("/repos/{repo}/issues/%d/events?per_page=100", number), nil).mustJSON(t, http.StatusOK, &events)
	count := 0
	for _, e := range events {
		if e.Event == "closed" {
			count++
		}
	}
	return count
}

// graphQLErrors returns the messages of the "errors" field of a GraphQL
// answer, or none.
func graphQLErrors(t *testing.T, resp response) []string {
	t.Helper()
	var body struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if resp.status == http.StatusOK {
		resp.json(t, &body)
	}
	var messages []string
	for _, e := range body.Errors {
		messages = append(messages, e.Message)
	}
	return messages
}

func containsNumber(list []int, value int) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
