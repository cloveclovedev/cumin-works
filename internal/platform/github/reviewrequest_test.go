package github_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

const reviewRequestPath = "/repos/example-org/example-repo/pulls/21/requested_reviewers"

// The request names one login, and the same request a second time leaves
// the login listed once.
func TestRequestReview_RequestsTheReviewOfOneLogin(t *testing.T) {
	fake, repo, client := mergeScene(t)
	fake.SetPermission("the-owner", "admin", "User")
	ctx := context.Background()
	for range 2 {
		if err := client.RequestReview(ctx, githubtest.Token, "example-org", "example-repo", 21, "the-owner"); err != nil {
			t.Fatalf("RequestReview: %v", err)
		}
	}
	if got := fake.RequestedReviewers(repo, 21); !slices.Equal(got, []string{"the-owner"}) {
		t.Errorf("requested reviewers = %v, want the-owner once", got)
	}
	requests := 0
	for _, r := range fake.Requests() {
		if r.Method != http.MethodPost || r.Path != reviewRequestPath {
			continue
		}
		requests++
		if strings.TrimSpace(string(r.Body)) != `{"reviewers":["the-owner"]}` {
			t.Errorf("body of the request = %s", r.Body)
		}
	}
	if requests != 2 {
		t.Errorf("%d requests, want 2", requests)
	}
}

// A request that GitHub refuses is an error with the answer of GitHub, and
// it is sent once: a write is not sent again.
func TestRequestReview_TellsTheFailuresApart(t *testing.T) {
	ctx := context.Background()
	t.Run("an account that is not a collaborator", func(t *testing.T) {
		fake, repo, client := mergeScene(t)
		err := client.RequestReview(ctx, githubtest.Token, "example-org", "example-repo", 21, "a-stranger")
		var status *github.StatusError
		if !errors.As(err, &status) || status.Status != http.StatusUnprocessableEntity || github.IsTemporary(err) {
			t.Fatalf("error = %v, want a 422 that is no temporary failure", err)
		}
		if !strings.Contains(err.Error(), "Reviews may only be requested from collaborators") {
			t.Errorf("the error does not hold the message of GitHub: %v", err)
		}
		if got := fake.RequestedReviewers(repo, 21); len(got) != 0 {
			t.Errorf("requested reviewers = %v, want none", got)
		}
	})
	t.Run("a 500", func(t *testing.T) {
		fake, _, client := mergeScene(t)
		fake.SetPermission("the-owner", "admin", "User")
		fake.FailNext(http.MethodPost, reviewRequestPath, http.StatusInternalServerError)
		err := client.RequestReview(ctx, githubtest.Token, "example-org", "example-repo", 21, "the-owner")
		if !github.IsTemporary(err) {
			t.Fatalf("error = %v, want a temporary failure", err)
		}
		if n := fake.CountRequests(http.MethodPost, reviewRequestPath); n != 1 {
			t.Errorf("%d requests, want 1", n)
		}
	})
}
