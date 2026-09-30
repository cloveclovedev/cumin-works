package github_test

import (
	"fmt"
	"net/http"
	"testing"
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
	var created issue
	l.api(t, token, http.MethodPost, "/repos/{repo}/issues", body).mustJSON(t, http.StatusCreated, &created)
	return created
}
