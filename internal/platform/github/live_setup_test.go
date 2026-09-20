package github_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const protectedPathsCheck = "cumin-protected-paths"

// TestLiveSetupChecks runs the checks of docs/ja/development/github-app-setup.md
// ("確認すること") on the sandbox repository. It needs the four registered Apps,
// their installation on the sandbox, and the repository setup of
// scripts/setup-repo.sh with --core-app.
func TestLiveSetupChecks(t *testing.T) {
	l := newLive(t)
	defer func() { t.Log("\n" + l.table()) }()

	core := l.token(t, "cumin-core")
	chief := l.token(t, "chief-engineer")
	implementer := l.token(t, "implementer")
	reviewer := l.token(t, "reviewer")

	// The Implementer App commits as its bot user, as an agent will.
	var bot struct {
		Login string `json:"login"`
		ID    int64  `json:"id"`
	}
	userResp := l.api(t, "", http.MethodGet, "/users/"+url.PathEscape(l.botLogin(t, "implementer")), nil)
	if userResp.status != http.StatusOK {
		t.Fatalf("read the bot user of the Implementer App: status %d: %s", userResp.status, userResp.message())
	}
	userResp.json(t, &bot)
	email := fmt.Sprintf("%d+%s@users.noreply.github.com", bot.ID, bot.Login)
	repo := l.newGitRepo(t, implementer, bot.Login, email)

	// Check 1: the Implementer App pushes to main.
	repo.commitFile(t, "live-"+l.runID+"-main", "live/"+l.runID+"-direct.md", "direct push\n")
	out, err := repo.run("push", "origin", "HEAD:"+l.branch)
	switch {
	case err == nil:
		l.record("1", "The Implementer App pushes to the default branch with git", "The ruleset rejects the push", "NOT rejected: the push succeeded")
		t.Error("check 1: the Implementer App pushed to the default branch")
	case !strings.Contains(out, "GH013") && !strings.Contains(out, "rule violations"):
		// Another failure, such as a network error, says nothing about the ruleset.
		l.record("1", "The Implementer App pushes to the default branch with git", "The ruleset rejects the push", "The push failed for another reason: "+firstLineWith(out, "error", "fatal", "rejected"))
		t.Errorf("check 1: the push failed, but not because of the ruleset: %s", out)
	default:
		l.record("1", "The Implementer App pushes to the default branch with git", "The ruleset rejects the push", "Rejected: "+firstLineWith(out, "GH013", "rule violations"))
	}

	// A pull request from the Implementer App that changes an unprotected file.
	branch := "live-" + l.runID + "-unprotected"
	sha := repo.commitFile(t, branch, "live/"+l.runID+".md", "live check\n")
	l.pushBranch(t, repo, implementer, branch)
	pull := l.openPull(t, implementer, branch, "test: live setup checks "+l.runID)

	// Checks 6 and 8: the names and the author type.
	var commit struct {
		Author struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"author"`
	}
	l.api(t, implementer, http.MethodGet, "/repos/{repo}/commits/"+sha, nil).json(t, &commit)
	l.record("6", "The display name of the App, and the author of a commit that the Implementer App pushes", "`<slug>[bot]`",
		fmt.Sprintf("Pull request author `%s`. Commit author `%s`. The commit used the email `<bot user id>+<slug>[bot]@users.noreply.github.com`", pull.User.Login, commit.Author.Login))
	if pull.User.Login != bot.Login || commit.Author.Login != bot.Login {
		t.Errorf("check 6: pull request author %q, commit author %q, want %q", pull.User.Login, commit.Author.Login, bot.Login)
	}

	conclusion := l.waitForCheck(t, implementer, sha, protectedPathsCheck)
	l.record("8", "The author type of a pull request that an App created, in the API and in the workflow", "`Bot`. The protected-path job runs (it is not skipped)",
		fmt.Sprintf("API `user.type` is `%s`. The job ran with the conclusion `%s`", pull.User.Type, conclusion))
	if pull.User.Type != "Bot" || conclusion == "skipped" {
		t.Errorf("check 8: user.type = %q, conclusion = %q. The job condition does not work for an App. Stop and write a decision request", pull.User.Type, conclusion)
	}
	l.record("7a", "A pull request from the Implementer App that changes an unprotected file", "The `cumin-protected-paths` check passes", "Conclusion `"+conclusion+"`")
	if conclusion != "success" {
		t.Errorf("check 7a: conclusion = %q, want success", conclusion)
	}

	// Check 2: the Implementer App merges its pull request.
	merge := l.api(t, implementer, http.MethodPut, fmt.Sprintf("/repos/{repo}/pulls/%d/merge", pull.Number), map[string]any{"merge_method": "squash"})
	l.record("2", "The Implementer App merges a pull request", "The ruleset rejects the merge", fmt.Sprintf("Status %d: %s", merge.status, merge.message()))
	// Only the answer of the ruleset counts. Another error, such as a rate
	// limit, does not show that the ruleset works.
	if merge.status != http.StatusMethodNotAllowed || !strings.Contains(merge.message(), "rule violations") {
		t.Errorf("check 2: status %d: %s, want 405 with a rule violation", merge.status, merge.message())
	}

	// Check 5: the Reviewer App approves the pull request of the Implementer App.
	review := l.api(t, reviewer, http.MethodPost, fmt.Sprintf("/repos/{repo}/pulls/%d/reviews", pull.Number), map[string]any{"event": "APPROVE", "commit_id": sha})
	var reviewed struct {
		State string `json:"state"`
		User  struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if review.status == http.StatusOK {
		review.json(t, &reviewed)
	}
	l.record("5", "The Reviewer App approves a pull request that the Implementer App opened", "Success", fmt.Sprintf("Status %d, state `%s`, reviewer `%s`", review.status, reviewed.State, reviewed.User.Login))
	if review.status != http.StatusOK || reviewed.State != "APPROVED" {
		t.Errorf("check 5: status %d, state %q: %s", review.status, reviewed.State, review.message())
	}

	// Check 3: the cumin-core App merges the pull request.
	merge = l.api(t, core, http.MethodPut, fmt.Sprintf("/repos/{repo}/pulls/%d/merge", pull.Number), map[string]any{"merge_method": "squash", "sha": sha})
	l.record("3", "The cumin-core App merges the pull request", "Success", fmt.Sprintf("Status %d: %s", merge.status, merge.message()))
	if merge.status != http.StatusOK {
		t.Errorf("check 3: status %d: %s", merge.status, merge.message())
	}

	// Check 7b: a pull request from the Implementer App that changes a protected path.
	protectedBranch := "live-" + l.runID + "-protected"
	protectedSHA := repo.commitFile(t, protectedBranch, "live/CLAUDE.md", "live check of a protected path\n")
	l.pushBranch(t, repo, implementer, protectedBranch)
	l.openPull(t, implementer, protectedBranch, "test: live protected path "+l.runID)
	conclusion = l.waitForCheck(t, implementer, protectedSHA, protectedPathsCheck)
	l.record("7b", "A pull request from the Implementer App that adds `live/CLAUDE.md`", "The `cumin-protected-paths` check fails", "Conclusion `"+conclusion+"`")
	if conclusion != "failure" {
		t.Errorf("check 7b: conclusion = %q, want failure", conclusion)
	}

	// Check 4: the Chief Engineer App creates issues with a label, a sub-issue,
	// and a "blocked by" relationship. cumin creates missing labels, so the
	// cumin-core App makes sure that the label exists.
	label := l.api(t, core, http.MethodPost, "/repos/{repo}/labels", map[string]any{"name": "risk/low", "color": "C2E0C6"})
	if label.status != http.StatusCreated && label.status != http.StatusUnprocessableEntity {
		t.Errorf("check 4: create the label: status %d: %s", label.status, label.message())
	}
	parent := l.createIssue(t, chief, "test: live parent "+l.runID, nil, 0)
	blocker := l.createIssue(t, chief, "test: live blocker "+l.runID, nil, 0)
	child := l.createIssue(t, chief, "test: live child "+l.runID, []string{"risk/low"}, parent.ID)
	dependency := l.api(t, chief, http.MethodPost, fmt.Sprintf("/repos/{repo}/issues/%d/dependencies/blocked_by", child.Number), map[string]any{"issue_id": blocker.ID})
	var subIssues []struct {
		Number int `json:"number"`
	}
	l.api(t, chief, http.MethodGet, fmt.Sprintf("/repos/{repo}/issues/%d/sub_issues", parent.Number), nil).json(t, &subIssues)
	labels := strings.Join(child.labelNames(), ", ")
	l.record("4", "The Chief Engineer App creates an issue with a label, as a sub-issue (`parent_issue_id`), and adds a \"blocked by\" relationship", "Success, and the label is on the issue",
		fmt.Sprintf("Issue created by `%s` with the labels [%s]. Sub-issues of the parent: %d. \"blocked by\": status %d", child.User.Login, labels, len(subIssues), dependency.status))
	if labels != "risk/low" || len(subIssues) != 1 || dependency.status != http.StatusCreated {
		t.Errorf("check 4: labels [%s], %d sub-issues, blocked by status %d: %s", labels, len(subIssues), dependency.status, dependency.message())
	}
}

type pullRequest struct {
	Number int `json:"number"`
	User   struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"user"`
}

type issue struct {
	ID     int64 `json:"id"`
	Number int   `json:"number"`
	User   struct {
		Login string `json:"login"`
	} `json:"user"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

func (i issue) labelNames() []string {
	var names []string
	for _, label := range i.Labels {
		names = append(names, label.Name)
	}
	return names
}

// Each helper registers its clean-up directly after GitHub created the thing,
// so a later failure of the test leaves nothing open on the sandbox.

// cleanUp fails the test when a clean-up call did not work, so that a run
// cannot pass and leave something open on the sandbox.
func (l *live) cleanUp(t *testing.T, what string, resp response, allowed ...int) {
	t.Helper()
	for _, status := range allowed {
		if resp.status == status {
			return
		}
	}
	t.Errorf("clean-up: %s: status %d: %s", what, resp.status, resp.message())
}

// pushBranch pushes the branch and deletes it at the end of the test.
func (l *live) pushBranch(t *testing.T, repo *gitRepo, token, branch string) {
	t.Helper()
	repo.mustRun(t, "push", "--quiet", "origin", branch)
	t.Cleanup(func() {
		// 422: the branch is gone already, for example after a merge that deletes it.
		l.cleanUp(t, "delete the branch "+branch, l.api(t, token, http.MethodDelete, "/repos/{repo}/git/refs/heads/"+branch, nil), http.StatusNoContent, http.StatusUnprocessableEntity)
	})
}

// openPull opens a pull request and closes it at the end of the test. To
// close a merged pull request changes nothing.
func (l *live) openPull(t *testing.T, token, branch, title string) pullRequest {
	t.Helper()
	resp := l.api(t, token, http.MethodPost, "/repos/{repo}/pulls", map[string]any{"title": title, "head": branch, "base": l.branch, "body": "A live check of cumin-works. The test closes it."})
	if resp.status != http.StatusCreated {
		t.Fatalf("open the pull request: status %d: %s", resp.status, resp.message())
	}
	var pull pullRequest
	resp.json(t, &pull)
	t.Cleanup(func() {
		l.cleanUp(t, fmt.Sprintf("close the pull request %d", pull.Number), l.api(t, token, http.MethodPatch, fmt.Sprintf("/repos/{repo}/pulls/%d", pull.Number), map[string]any{"state": "closed"}), http.StatusOK)
	})
	return pull
}

func (l *live) createIssue(t *testing.T, token, title string, labels []string, parentID int64) issue {
	t.Helper()
	body := map[string]any{"title": title, "body": "A live check of cumin-works. The test closes it."}
	if len(labels) > 0 {
		body["labels"] = labels
	}
	if parentID != 0 {
		body["parent_issue_id"] = parentID
	}
	resp := l.api(t, token, http.MethodPost, "/repos/{repo}/issues", body)
	if resp.status != http.StatusCreated {
		t.Fatalf("create the issue %q: status %d: %s", title, resp.status, resp.message())
	}
	var created issue
	resp.json(t, &created)
	t.Cleanup(func() {
		l.cleanUp(t, fmt.Sprintf("close the issue %d", created.Number), l.api(t, token, http.MethodPatch, fmt.Sprintf("/repos/{repo}/issues/%d", created.Number), map[string]any{"state": "closed"}), http.StatusOK)
	})
	return created
}

// firstLineWith returns the first line of the text that holds one of the words.
func firstLineWith(text string, words ...string) string {
	for _, line := range strings.Split(text, "\n") {
		for _, word := range words {
			if strings.Contains(line, word) {
				return "`" + strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "remote:")) + "`"
			}
		}
	}
	return "(no matching line in the output of git)"
}
