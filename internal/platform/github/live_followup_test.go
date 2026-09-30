package github_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// followUpLiveBody is the description of the pull request of Follow-1: the
// headings of templates/pull-request.md, with text under "Follow-up".
const followUpLiveBody = `## What
Add one file for the live scenario Follow-1.

## Why
Closes #%d

## Follow-up
- Follow-1 left this line on purpose. The note must copy it as it is.
`

// TestLiveFollowUpFixture prepares the live scenario Follow-1
// (docs/ja/development/live-tests.md): a requirement issue with only
// cumin/type/requirement, its sub-issue, and a pull request of the
// Implementer App that closes the sub-issue. The Reviewer App leaves two
// non-blocking comments; the Implementer App answers one with "Fixed". The
// Owner merges the pull request by hand, then runs cumin. Nothing is closed
// at the end of the test: the procedure says how to clean up.
//
// No Claude Code runs, so it uses no quota. The requirement issue carries
// no status label, so cumin starts no agent for it (R1, R3, R4).
func TestLiveFollowUpFixture(t *testing.T) {
	l := newLive(t)
	planner := l.token(t, "planner")
	implementer := l.token(t, "implementer")
	reviewer := l.token(t, "reviewer")

	requirement := l.createFixtureIssue(t, planner, "test: live Follow-1 requirement "+l.runID, []string{requirementLabel}, 0)
	sub := l.createFixtureIssue(t, planner, "test: live Follow-1 sub-issue "+l.runID, nil, requirement.ID)

	botLogin := l.botLogin(t, "implementer")
	repo := l.newGitRepo(t, implementer, botLogin, botLogin+"@users.noreply.github.com")
	branch := "live-" + l.runID + "-follow-1"
	path := "live/" + l.runID + "-follow-1.md"
	head := repo.commitFile(t, branch, path, "line one\nline two\n")
	repo.mustRun(t, "push", "--quiet", "origin", branch)

	resp := l.api(t, implementer, http.MethodPost, "/repos/{repo}/pulls", map[string]any{
		"title": "test: live Follow-1 " + l.runID, "head": branch, "base": l.branch,
		"body": fmt.Sprintf(followUpLiveBody, sub.Number),
	})
	var pull pullRequest
	resp.mustJSON(t, http.StatusCreated, &pull)
	// Since 2026-09-30, GitHub has created no closing link for a new pull
	// request, in every repository that we checked. Without the link, the
	// merge leaves the sub-issue open. The Owner then links the pull request
	// to the sub-issue by hand before the merge (the procedure, step 4).
	linked := l.waitForClosingLink(t, implementer, pull.Number, sub.Number)
	if !linked {
		t.Logf("GitHub made no closing link: link pull request #%d to issue #%d by hand before the merge", pull.Number, sub.Number)
	}

	// Official: REST "Create a review for a pull request". COMMENT needs a
	// body; each comment names a line of the diff.
	review := map[string]any{
		"commit_id": head, "event": "COMMENT", "body": "Result: Approved (round 1 of 3)",
		"comments": []map[string]any{
			{"path": path, "line": 1, "side": "RIGHT", "body": "suggestion (non-blocking): Say in the file which scenario wrote it.\n\nWhy: a reader of main does not know.\nFix: add one line."},
			{"path": path, "line": 2, "side": "RIGHT", "body": "nitpick (non-blocking): End the file with a period.\n\nWhy: style.\nFix: add a period."},
		},
	}
	if resp := l.api(t, reviewer, http.MethodPost, fmt.Sprintf("/repos/{repo}/pulls/%d/reviews", pull.Number), review); resp.status != http.StatusOK {
		t.Fatalf("create the review: status %d: %s", resp.status, resp.message())
	}
	var comments []struct {
		ID   int64 `json:"id"`
		Line int   `json:"line"`
	}
	l.api(t, implementer, http.MethodGet, fmt.Sprintf("/repos/{repo}/pulls/%d/comments", pull.Number), nil).mustJSON(t, http.StatusOK, &comments)
	answered := int64(0)
	for _, c := range comments {
		if c.Line == 2 {
			answered = c.ID
		}
	}
	if answered == 0 {
		t.Fatalf("the review comment on line 2 was not found: %+v", comments)
	}
	// Official: REST "Create a reply for a review comment".
	reply := l.api(t, implementer, http.MethodPost, fmt.Sprintf("/repos/{repo}/pulls/%d/comments/%d/replies", pull.Number, answered),
		map[string]any{"body": "Fixed: the Owner may ignore this; Follow-1 answers it only to test the rule."})
	if reply.status != http.StatusCreated {
		t.Fatalf("reply to the review comment: status %d: %s", reply.status, reply.message())
	}

	t.Logf("Follow-1 is ready: requirement issue #%d, sub-issue #%d, pull request #%d (branch %s)", requirement.Number, sub.Number, pull.Number, branch)
}

// waitForClosingLink waits up to 30 seconds until the pull request lists
// the issue in closingIssuesReferences, the link that closes the issue at
// the merge.
func (l *live) waitForClosingLink(t *testing.T, token string, pull, issue int) bool {
	t.Helper()
	query := `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) { closingIssuesReferences(first: 10) { nodes { number } } }
  }
}`
	for range 6 {
		var resp struct {
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
			Data struct {
				Repository struct {
					PullRequest struct {
						ClosingIssuesReferences struct {
							Nodes []struct {
								Number int `json:"number"`
							} `json:"nodes"`
						} `json:"closingIssuesReferences"`
					} `json:"pullRequest"`
				} `json:"repository"`
			} `json:"data"`
		}
		l.api(t, token, http.MethodPost, "/graphql", map[string]any{"query": query, "variables": map[string]any{"owner": l.owner, "name": l.repo, "number": pull}}).mustJSON(t, http.StatusOK, &resp)
		// A GraphQL error comes with the status 200. It must stop the test,
		// not look like a missing link.
		if len(resp.Errors) > 0 {
			t.Fatalf("read the closing link of pull request #%d: %s", pull, resp.Errors[0].Message)
		}
		for _, n := range resp.Data.Repository.PullRequest.ClosingIssuesReferences.Nodes {
			if n.Number == issue {
				return true
			}
		}
		time.Sleep(5 * time.Second)
	}
	return false
}

// createFixtureIssue creates an issue that stays after the test.
func (l *live) createFixtureIssue(t *testing.T, token, title string, labels []string, parentID int64) issue {
	t.Helper()
	body := map[string]any{"title": title, "body": "The live scenario Follow-1 of cumin-works. The Owner closes it after the scenario."}
	if len(labels) > 0 {
		body["labels"] = labels
	}
	if parentID != 0 {
		body["parent_issue_id"] = parentID
	}
	resp := l.api(t, token, http.MethodPost, "/repos/{repo}/issues", body)
	var created issue
	resp.mustJSON(t, http.StatusCreated, &created)
	// GitHub drops a label that the repository does not have, without an
	// error. cumin run creates its labels at start.
	var withLabels struct {
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	resp.json(t, &withLabels)
	for _, want := range labels {
		found := false
		for _, got := range withLabels.Labels {
			found = found || got.Name == want
		}
		if !found {
			t.Fatalf("issue #%d has no label %s: start cumin run once so that it creates its labels, then close the issue and run the test again", created.Number, want)
		}
	}
	return created
}
