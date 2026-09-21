package github_test

import (
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/platform/github"
)

// The names of the jobs in testdata/cumin-live-fixture.yml.
const (
	failOnMarkerCheck   = "live-fail-on-marker"
	skippedForBotsCheck = "live-skipped-for-bots"
	commitStatusCheck   = "live-commit-status"
	liveStatusContext   = "cumin-live-status"
	failMarkerPath      = "live/fail-marker"
)

// TestLiveGitHubFacts records facts about GitHub that the later requirements
// depend on, and that the official documentation does not answer. It needs the
// setup of TestLiveSetupChecks, the fixture workflow of testdata/ on the
// default branch, and the required check "live-skipped-for-bots"
// (scripts/setup-repo.sh --required-check).
func TestLiveGitHubFacts(t *testing.T) {
	l := newLive(t)
	defer func() { t.Log("\n" + l.table()) }()

	core := l.token(t, "cumin-core")
	chief := l.token(t, "chief-engineer")
	implementer := l.token(t, "implementer")
	reviewer := l.token(t, "reviewer")
	l.requireFactFixtures(t, core)

	botLogin := l.botLogin(t, "implementer")
	repo := l.newGitRepo(t, implementer, botLogin, botLogin+"@users.noreply.github.com")

	// A parent, a sub-issue, and an issue that blocks the sub-issue.
	parent := l.createIssue(t, chief, "test: live facts parent "+l.runID, nil, 0)
	blocker := l.createIssue(t, chief, "test: live facts blocker "+l.runID, nil, 0)
	child := l.createIssue(t, chief, "test: live facts child "+l.runID, nil, parent.ID)
	if resp := l.api(t, chief, http.MethodPost, fmt.Sprintf("/repos/{repo}/issues/%d/dependencies/blocked_by", child.Number), map[string]any{"issue_id": blocker.ID}); resp.status != http.StatusCreated {
		t.Fatalf("add blocked by: status %d: %s", resp.status, resp.message())
	}

	// Pull request A closes the sub-issue.
	branchA := "live-" + l.runID + "-facts"
	shaA := repo.commitFile(t, branchA, "live/"+l.runID+"-facts.md", "live facts\n")
	l.pushBranch(t, repo, implementer, branchA)
	pullA := l.openPullWithBody(t, implementer, branchA, "test: live facts "+l.runID, fmt.Sprintf("A live check of cumin-works.\n\nCloses #%d", child.Number))
	l.waitForCheck(t, core, shaA, protectedPathsCheck)
	l.waitForCheck(t, core, shaA, commitStatusCheck)
	skipped := l.checkRun(t, core, shaA, skippedForBotsCheck)

	// Fact 1: the cumin-core App reads check runs and commit statuses.
	checkRuns := l.api(t, core, http.MethodGet, "/repos/{repo}/commits/"+shaA+"/check-runs", nil)
	combined := l.api(t, core, http.MethodGet, "/repos/{repo}/commits/"+shaA+"/status", nil)
	var status struct {
		State    string `json:"state"`
		Statuses []struct {
			Context string `json:"context"`
			State   string `json:"state"`
		} `json:"statuses"`
	}
	if combined.status == http.StatusOK {
		combined.json(t, &status)
	}
	var found []string
	fixtureStatus := ""
	for _, s := range status.Statuses {
		found = append(found, s.Context+"="+s.State)
		if s.Context == liveStatusContext {
			fixtureStatus = s.State
		}
	}
	l.record("1", "The cumin-core App (no Checks and no Commit statuses permission) reads the check runs and the commit statuses of a commit in a public repository (row 35)", "Not known",
		fmt.Sprintf("`GET .../check-runs`: status %d. `GET .../status`: status %d, with the commit statuses `%s`", checkRuns.status, combined.status, strings.Join(found, "`, `")))
	if checkRuns.status != http.StatusOK || combined.status != http.StatusOK || fixtureStatus != "success" {
		t.Errorf("fact 1: check runs %d, status %d, the status %s of the fixture is %q (want success). If a call is refused, a permission is missing: stop and ask the Owner", checkRuns.status, combined.status, liveStatusContext, fixtureStatus)
	}

	// Fact 8: the rules of the default branch.
	rulesStatus, required := l.requiredChecks(t, core)
	l.record("8", "An installation token calls `GET /repos/{owner}/{repo}/rules/branches/{branch}` (row 34)", "Status 200 with the required checks",
		fmt.Sprintf("Status %d. Required checks: `%s`", rulesStatus, strings.Join(required, "`, `")))
	if rulesStatus != http.StatusOK || !contains(required, protectedPathsCheck) || !contains(required, skippedForBotsCheck) {
		t.Errorf("fact 8: status %d, required checks %v", rulesStatus, required)
	}

	// Fact 11: the cumin-core App labels a pull request of the Implementer App.
	const label = "cumin/status/reviewing"
	if resp := l.api(t, core, http.MethodPost, "/repos/{repo}/labels", map[string]any{"name": label, "color": "1D76DB"}); resp.status != http.StatusCreated && resp.status != http.StatusUnprocessableEntity {
		t.Errorf("fact 11: create the label: status %d: %s", resp.status, resp.message())
	}
	added := l.api(t, core, http.MethodPost, fmt.Sprintf("/repos/{repo}/issues/%d/labels", pullA.Number), map[string]any{"labels": []string{label}})
	removed := l.api(t, core, http.MethodDelete, fmt.Sprintf("/repos/{repo}/issues/%d/labels/%s", pullA.Number, strings.ReplaceAll(label, "/", "%2F")), nil)
	l.record("11", "The cumin-core App adds a label to a pull request of the Implementer App, and removes it (I11)", "Success with the permissions of today",
		fmt.Sprintf("Add: status %d. Remove: status %d", added.status, removed.status))
	if added.status != http.StatusOK || removed.status != http.StatusOK {
		t.Errorf("fact 11: add %d (%s), remove %d (%s)", added.status, added.message(), removed.status, removed.message())
	}

	// Fact 5: the fields of reviews.
	for _, review := range []map[string]any{
		{"event": "REQUEST_CHANGES", "commit_id": shaA, "body": "A live check of cumin-works: a request for changes."},
		{"event": "APPROVE", "commit_id": shaA},
	} {
		if resp := l.api(t, reviewer, http.MethodPost, fmt.Sprintf("/repos/{repo}/pulls/%d/reviews", pullA.Number), review); resp.status != http.StatusOK {
			t.Fatalf("fact 5: %s: status %d: %s", review["event"], resp.status, resp.message())
		}
	}
	var reviews []struct {
		State    string `json:"state"`
		CommitID string `json:"commit_id"`
		User     struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	l.api(t, core, http.MethodGet, fmt.Sprintf("/repos/{repo}/pulls/%d/reviews", pullA.Number), nil).mustJSON(t, http.StatusOK, &reviews)
	var states []string
	sameCommit := true
	for _, review := range reviews {
		states = append(states, review.State)
		sameCommit = sameCommit && review.CommitID == shaA
	}
	l.record("5", "The `state` and the `commit_id` of reviews in `GET /pulls/{n}/reviews`, after the events `REQUEST_CHANGES` and `APPROVE`", "Not known",
		fmt.Sprintf("States in order: `%s`. `commit_id` is the full SHA of the reviewed commit: %v", strings.Join(states, "`, `"), sameCommit))
	if strings.Join(states, ",") != "CHANGES_REQUESTED,APPROVED" || !sameCommit {
		t.Errorf("fact 5: states %v, same commit %v", states, sameCommit)
	}

	// Fact 10: a mention by an App. The Owner looks at the notifications.
	if login := os.Getenv("CUMIN_LIVE_MENTION"); login != "" {
		resp := l.api(t, core, http.MethodPost, fmt.Sprintf("/repos/{repo}/issues/%d/comments", pullA.Number), map[string]any{"body": "@" + login + " a live check of cumin-works: does this mention send a notification?"})
		if resp.status != http.StatusCreated {
			// With no comment there is no notification to confirm.
			t.Errorf("fact 10: the comment was not created: status %d: %s", resp.status, resp.message())
		}
		l.record("10", "A comment of the cumin-core App that mentions a person (row 19)", "The person gets a notification", fmt.Sprintf("Comment posted: status %d. The Owner confirms the notification by hand", resp.status))
	} else {
		l.record("10", "A comment of the cumin-core App that mentions a person (row 19)", "The person gets a notification", "Not run: `CUMIN_LIVE_MENTION` is not set")
	}

	// Fact 3: a required check that an `if` condition skipped does not stop the merge.
	merge := l.api(t, core, http.MethodPut, fmt.Sprintf("/repos/{repo}/pulls/%d/merge", pullA.Number), map[string]any{"merge_method": "squash", "sha": shaA})
	l.record("3", "A required check whose job an `if` condition skipped, and the merge by the cumin-core App (row 20)", "The merge works",
		fmt.Sprintf("Check run `%s`: status `%s`, conclusion `%s`. Merge: status %d", skippedForBotsCheck, skipped.Status, skipped.Conclusion, merge.status))
	if skipped.Conclusion != "skipped" || merge.status != http.StatusOK {
		t.Errorf("fact 3: conclusion %q, merge status %d: %s", skipped.Conclusion, merge.status, merge.message())
	}

	// Fact 6: the merge closes the sub-issue.
	closed := false
	for range 12 {
		var current struct {
			State string `json:"state"`
		}
		l.api(t, core, http.MethodGet, fmt.Sprintf("/repos/{repo}/issues/%d", child.Number), nil).mustJSON(t, http.StatusOK, &current)
		if closed = current.State == "closed"; closed {
			break
		}
		time.Sleep(5 * time.Second)
	}
	l.record("6", "A pull request from the Implementer App with `Closes #N` in its body, where #N is a sub-issue, is merged by the cumin-core App", "GitHub closes the sub-issue", fmt.Sprintf("Sub-issue closed: %v", closed))
	if !closed {
		t.Error("fact 6: the sub-issue is still open")
	}

	// Fact 7: the GraphQL fields with an installation token.
	l.recordGraphQLFact(t, core, parent.Number, child.Number, pullA.Number)

	// Pull request B has the marker file, so one check fails.
	branchB := "live-" + l.runID + "-fail"
	shaB := repo.commitFile(t, branchB, failMarkerPath, l.runID+"\n")
	l.pushBranch(t, repo, implementer, branchB)
	l.openPull(t, implementer, branchB, "test: live facts failing check "+l.runID)

	// Fact 4: what a token can read of a failed check.
	l.waitForCheck(t, core, shaB, failOnMarkerCheck)
	l.recordFailedCheckFact(t, core, shaB)

	// Fact 2: a token with fewer permissions than the App.
	l.recordNarrowTokenFact(t)
}

type checkRun struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	DetailsURL string `json:"details_url"`
	Output     struct {
		Title            *string `json:"title"`
		Summary          *string `json:"summary"`
		AnnotationsCount int     `json:"annotations_count"`
	} `json:"output"`
}

// checkRun waits for the named check run to be completed and returns it.
func (l *live) checkRun(t *testing.T, token, sha, name string) checkRun {
	t.Helper()
	l.waitForCheck(t, token, sha, name)
	var runs struct {
		CheckRuns []checkRun `json:"check_runs"`
	}
	l.api(t, token, http.MethodGet, "/repos/{repo}/commits/"+sha+"/check-runs", nil).mustJSON(t, http.StatusOK, &runs)
	for _, run := range runs.CheckRuns {
		if run.Name == name {
			return run
		}
	}
	t.Fatalf("no check run %s", name)
	return checkRun{}
}

func (l *live) recordFailedCheckFact(t *testing.T, token, sha string) {
	t.Helper()
	run := l.checkRun(t, token, sha, failOnMarkerCheck)
	annotations := l.api(t, token, http.MethodGet, fmt.Sprintf("/repos/{repo}/check-runs/%d/annotations", run.ID), nil)
	var notes []struct {
		Path    string `json:"path"`
		Level   string `json:"annotation_level"`
		Message string `json:"message"`
	}
	if annotations.status == http.StatusOK {
		annotations.json(t, &notes)
	}
	var messages []string
	for _, note := range notes {
		if note.Level == "failure" {
			messages = append(messages, fmt.Sprintf("%s: %s", note.Path, note.Message))
		}
	}
	firstMessage := strings.Join(messages, " / ")
	// The job ID is the last part of the details address of an Actions check run.
	jobID := run.DetailsURL[strings.LastIndex(run.DetailsURL, "/")+1:]
	logs := l.api(t, token, http.MethodGet, "/repos/{repo}/actions/jobs/"+jobID+"/logs", nil)
	anonymousLogs := l.api(t, "", http.MethodGet, "/repos/{repo}/actions/jobs/"+jobID+"/logs", nil)
	title := "null"
	if run.Output.Title != nil {
		title = *run.Output.Title
	}
	l.record("4", "What an installation token of the cumin-core App reads of a failed GitHub Actions check", "Not known",
		fmt.Sprintf("Check run: conclusion `%s`, output title `%s`, %d annotations. `GET /check-runs/{id}/annotations`: status %d, failure annotations: \"%s\". `GET /actions/jobs/{id}/logs`: status %d with the token, status %d without authentication",
			run.Conclusion, title, run.Output.AnnotationsCount, annotations.status, firstMessage, logs.status, anonymousLogs.status))
	if run.Conclusion != "failure" || annotations.status != http.StatusOK || !strings.Contains(firstMessage, failMarkerPath) {
		t.Errorf("fact 4: conclusion %q, annotations status %d, message %q", run.Conclusion, annotations.status, firstMessage)
	}
	// The recorded fact: the token reads the job log, and a call without
	// authentication does not. A change of either one is news for cumin.
	if logs.status != http.StatusOK || anonymousLogs.status != http.StatusForbidden {
		t.Errorf("fact 4: job log: status %d with the token (want 200), status %d without authentication (want 403)", logs.status, anonymousLogs.status)
	}
}

func (l *live) recordGraphQLFact(t *testing.T, token string, parentNumber, issueNumber, pullNumber int) {
	t.Helper()
	query := fmt.Sprintf(`query {
  repository(owner: %q, name: %q) {
    issue(number: %d) {
      closedByPullRequestsReferences(first: 5, includeClosedPrs: true) { nodes { number } }
      blockedBy(first: 5) { nodes { number } }
      parent { number }
    }
    parentIssue: issue(number: %d) {
      subIssuesSummary { total completed }
    }
    pullRequest(number: %d) {
      closingIssuesReferences(first: 5) { nodes { number } }
      statusCheckRollup { state }
    }
  }
}`, l.owner, l.repo, issueNumber, parentNumber, pullNumber)
	resp := l.api(t, token, http.MethodPost, "/graphql", map[string]any{"query": query})
	var result struct {
		Data struct {
			Repository struct {
				Issue struct {
					ClosedBy  struct{ Nodes []struct{ Number int } } `json:"closedByPullRequestsReferences"`
					BlockedBy struct{ Nodes []struct{ Number int } } `json:"blockedBy"`
					Parent    *struct{ Number int }                  `json:"parent"`
				} `json:"issue"`
				ParentIssue struct {
					Summary *struct{ Total, Completed int } `json:"subIssuesSummary"`
				} `json:"parentIssue"`
				PullRequest struct {
					Closing struct{ Nodes []struct{ Number int } } `json:"closingIssuesReferences"`
					Rollup  *struct{ State string }                `json:"statusCheckRollup"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	resp.json(t, &result)
	var errs []string
	for _, e := range result.Errors {
		errs = append(errs, e.Message)
	}
	issue, pull := result.Data.Repository.Issue, result.Data.Repository.PullRequest
	rollup := "null"
	if pull.Rollup != nil {
		rollup = pull.Rollup.State
	}
	summary := "null"
	if s := result.Data.Repository.ParentIssue.Summary; s != nil {
		summary = fmt.Sprintf("%d of %d completed", s.Completed, s.Total)
	}
	l.record("7", "An installation token of the cumin-core App reads the GraphQL fields of the design note (row 36)", "Every field is readable",
		fmt.Sprintf("Status %d, errors: [%s]. `closedByPullRequestsReferences`: %d, `blockedBy`: %d, `parent`: %v, `subIssuesSummary` of the parent: %s, `closingIssuesReferences`: %d, `statusCheckRollup.state`: `%s`",
			resp.status, strings.Join(errs, "; "), len(issue.ClosedBy.Nodes), len(issue.BlockedBy.Nodes), issue.Parent != nil, summary, len(pull.Closing.Nodes), rollup))
	if resp.status != http.StatusOK || len(errs) > 0 || len(issue.ClosedBy.Nodes) != 1 || len(issue.BlockedBy.Nodes) != 1 || issue.Parent == nil || summary == "null" || len(pull.Closing.Nodes) != 1 || pull.Rollup == nil || pull.Rollup.State == "" {
		t.Errorf("fact 7: status %d, errors %v, issue %+v, pull %+v", resp.status, errs, issue, pull)
	}
}

// recordNarrowTokenFact asks GitHub for a token of the Implementer App with
// the permission "issues: read" only, and for a permission that the App does
// not have.
func (l *live) recordNarrowTokenFact(t *testing.T) {
	t.Helper()
	jwt, err := github.SignJWTForTest(l.credentials(t, "implementer"))
	if err != nil {
		t.Fatal(err)
	}
	var installation struct {
		ID int64 `json:"id"`
	}
	l.api(t, jwt, http.MethodGet, "/repos/{repo}/installation", nil).mustJSON(t, http.StatusOK, &installation)
	path := fmt.Sprintf("/app/installations/%d/access_tokens", installation.ID)

	narrow := l.api(t, jwt, http.MethodPost, path, map[string]any{"repositories": []string{l.repo}, "permissions": map[string]string{"issues": "read"}})
	var token struct {
		Token               string            `json:"token"`
		Permissions         map[string]string `json:"permissions"`
		RepositorySelection string            `json:"repository_selection"`
		Repositories        []struct {
			Name string `json:"name"`
		} `json:"repositories"`
	}
	if narrow.status != http.StatusCreated {
		t.Fatalf("fact 2: status %d: %s", narrow.status, narrow.message())
	}
	narrow.json(t, &token)
	read := l.api(t, token.Token, http.MethodGet, "/repos/{repo}/issues?per_page=1", nil)
	// In a public repository every GitHub account can open an issue, so this
	// call does not need the Issues write permission. A label needs it.
	target := l.createIssue(t, l.token(t, "chief-engineer"), "test: live facts narrow token "+l.runID, nil, 0)
	labeled := l.api(t, token.Token, http.MethodPost, fmt.Sprintf("/repos/{repo}/issues/%d/labels", target.Number), map[string]any{"labels": []string{"risk/low"}})
	opened := l.api(t, token.Token, http.MethodPost, "/repos/{repo}/issues", map[string]any{"title": "test: live facts issue from a read-only token " + l.runID})
	if opened.status == http.StatusCreated {
		var created issue
		opened.json(t, &created)
		l.cleanUp(t, fmt.Sprintf("close the issue %d", created.Number), l.api(t, l.token(t, "chief-engineer"), http.MethodPatch, fmt.Sprintf("/repos/{repo}/issues/%d", created.Number), map[string]any{"state": "closed"}), http.StatusOK)
	}
	tooMuch := l.api(t, jwt, http.MethodPost, path, map[string]any{"repositories": []string{l.repo}, "permissions": map[string]string{"administration": "read"}})

	l.record("2", "A token of the Implementer App with `permissions: {issues: read}` and one repository, and a request for a permission that the App does not have", "The token is limited. The second request is refused",
		fmt.Sprintf("Token: permissions `%v`, `repository_selection` `%s`, %d repository. Read issues: status %d. Add a label to an issue: status %d (%s). Open an issue: status %d (in a public repository, an account needs no write permission to open an issue). Request for `administration: read`: status %d (%s)",
			token.Permissions, token.RepositorySelection, len(token.Repositories), read.status, labeled.status, labeled.message(), opened.status, tooMuch.status, tooMuch.message()))
	if read.status != http.StatusOK || labeled.status != http.StatusForbidden || tooMuch.status != http.StatusUnprocessableEntity {
		t.Errorf("fact 2: read %d, add a label %d, too much %d", read.status, labeled.status, tooMuch.status)
	}
	// The token itself must say that it is limited: only the asked permission
	// (GitHub adds metadata), and only the one repository.
	wantPermissions := map[string]string{"issues": "read", "metadata": "read"}
	if !maps.Equal(token.Permissions, wantPermissions) || token.RepositorySelection != "selected" || len(token.Repositories) != 1 || !strings.EqualFold(token.Repositories[0].Name, l.repo) {
		t.Errorf("fact 2: the token has the permissions %v, the selection %q, and the repositories %v, want %v, selected, and only %s", token.Permissions, token.RepositorySelection, token.Repositories, wantPermissions, l.repo)
	}
}

// requireFactFixtures stops the test, before it creates anything, when the
// sandbox does not have the fixture workflow or the required checks. Without
// the required check, fact 3 would record a merge that says nothing about a
// required check.
func (l *live) requireFactFixtures(t *testing.T, token string) {
	t.Helper()
	const path = ".github/workflows/cumin-live-fixture.yml"
	if resp := l.api(t, token, http.MethodGet, "/repos/{repo}/contents/"+path+"?ref="+url.QueryEscape(l.branch), nil); resp.status != http.StatusOK {
		t.Fatalf("the sandbox has no %s on its default branch (status %d). docs/ja/development/live-tests.md says how to add it", path, resp.status)
	}
	status, required := l.requiredChecks(t, token)
	for _, check := range []string{protectedPathsCheck, skippedForBotsCheck} {
		if status != http.StatusOK || !contains(required, check) {
			t.Fatalf("the default branch of the sandbox does not require the check %s (status %d, required checks %v). Run scripts/setup-repo.sh with --required-check %s", check, status, required, skippedForBotsCheck)
		}
	}
}

// requiredChecks reads the required checks of the default branch.
func (l *live) requiredChecks(t *testing.T, token string) (int, []string) {
	t.Helper()
	resp := l.api(t, token, http.MethodGet, "/repos/{repo}/rules/branches/"+url.PathEscape(l.branch), nil)
	if resp.status != http.StatusOK {
		return resp.status, nil
	}
	var rules []struct {
		Parameters struct {
			RequiredStatusChecks []struct {
				Context string `json:"context"`
			} `json:"required_status_checks"`
		} `json:"parameters"`
	}
	resp.json(t, &rules)
	var required []string
	for _, rule := range rules {
		for _, check := range rule.Parameters.RequiredStatusChecks {
			required = append(required, check.Context)
		}
	}
	return resp.status, required
}

func (l *live) openPullWithBody(t *testing.T, token, branch, title, body string) pullRequest {
	t.Helper()
	request := map[string]any{"title": title, "head": branch, "base": l.branch}
	if body != "" {
		request["body"] = body
	}
	resp := l.api(t, token, http.MethodPost, "/repos/{repo}/pulls", request)
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

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}
