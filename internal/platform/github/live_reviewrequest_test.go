package github_test

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
)

// TestLiveReviewRequestFacts measures what the review request of #305
// relies on: whether the token of cumin-core may request the review of the
// Maintainer on a pull request of the Implementer App, what a second same
// request does, and how GitHub answers for an account that is not a
// collaborator.
//
// Official, REST: "Request reviewers for a pull request" (201; 422 if the
// user is not a collaborator; the endpoint triggers notifications); "Get
// all requested reviewers for a pull request"; "Permissions required for
// GitHub Apps" (the request needs "Pull requests" write and accepts an
// installation access token; the list needs "Pull requests" read).
func TestLiveReviewRequestFacts(t *testing.T) {
	l := newLive(t)
	defer func() { t.Log("\n" + l.table()) }()

	maintainer := os.Getenv("CUMIN_LIVE_OWNER")
	if maintainer == "" {
		t.Fatal("set CUMIN_LIVE_OWNER to the login of a human account with admin or write permission on the sandbox")
	}
	core := l.token(t, "cumin-core")
	implementer := l.token(t, "implementer")
	botLogin := l.botLogin(t, "implementer")
	repo := l.newGitRepo(t, implementer, botLogin, botLogin+"@users.noreply.github.com")

	branch := "live-" + l.runID + "-review-request"
	repo.commitFile(t, branch, "live/"+l.runID+"-review-request.md", "live check "+l.runID+"\n")
	l.pushBranch(t, repo, implementer, branch)
	pull := l.openPullWithBody(t, implementer, branch, "test: live review request facts "+l.runID, "A live check of cumin-works. The test closes it.")
	requestPath := fmt.Sprintf("/repos/{repo}/pulls/%d/requested_reviewers", pull.Number)

	// Fact V1: the request for the Maintainer, with the token of cumin-core.
	first := l.api(t, core, http.MethodPost, requestPath, map[string]any{"reviewers": []string{maintainer}})
	count := l.requestedReviewers(t, core, pull.Number, maintainer)
	l.record("V1", "The cumin-core App requests the review of the Issue Owner on a pull request of the Implementer App", "201, the pull request lists the login under `requested_reviewers`",
		fmt.Sprintf("Status %d: %s. The login is listed %d time(s)", first.status, first.message(), count))
	if first.status != http.StatusCreated || count != 1 {
		t.Errorf("fact V1: status %d: %s, listed %d time(s)", first.status, first.message(), count)
	}

	// Fact V2: the same request again.
	second := l.api(t, core, http.MethodPost, requestPath, map[string]any{"reviewers": []string{maintainer}})
	count = l.requestedReviewers(t, core, pull.Number, maintainer)
	l.record("V2", "The cumin-core App sends the same request a second time", "No failure, the login is listed once",
		fmt.Sprintf("Status %d: %s. The login is listed %d time(s)", second.status, second.message(), count))
	if second.status != http.StatusCreated || count != 1 {
		t.Errorf("fact V2: status %d: %s, listed %d time(s)", second.status, second.message(), count)
	}

	// Fact V3: the request for an account that is not a collaborator.
	stranger := l.api(t, core, http.MethodPost, requestPath, map[string]any{"reviewers": []string{"octocat"}})
	l.record("V3", "The cumin-core App requests the review of an account that is not a collaborator", "422",
		fmt.Sprintf("Status %d: %s. The account is listed %d time(s)", stranger.status, stranger.message(), l.requestedReviewers(t, core, pull.Number, "octocat")))
	if stranger.status != http.StatusUnprocessableEntity {
		t.Errorf("fact V3: status %d: %s", stranger.status, stranger.message())
	}
}

// requestedReviewers returns how many times the pull request lists the login
// among its requested users. GitHub does not keep the case of a login in a
// request, so the comparison ignores the case.
func (l *live) requestedReviewers(t *testing.T, token string, number int, login string) int {
	t.Helper()
	var requested struct {
		Users []struct {
			Login string `json:"login"`
		} `json:"users"`
	}
	l.api(t, token, http.MethodGet, fmt.Sprintf("/repos/{repo}/pulls/%d/requested_reviewers", number), nil).mustJSON(t, http.StatusOK, &requested)
	count := 0
	for _, user := range requested.Users {
		if strings.EqualFold(user.Login, login) {
			count++
		}
	}
	return count
}
