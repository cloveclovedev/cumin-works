package github_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

const head = "1111111111111111111111111111111111111111"

func mergeScene(t *testing.T) (*githubtest.Fake, *githubtest.Repository, *github.AppClient) {
	t.Helper()
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 10})
	fake.AddPullRequest(repo, &githubtest.PullRequest{Number: 21, HeadCommit: head, Closes: []int{10}})
	return fake, repo, github.NewAppClient(server.URL, server.Client())
}

func TestMergePullRequest_TellsTheAnswersApart(t *testing.T) {
	ctx := context.Background()
	t.Run("a clean merge", func(t *testing.T) {
		_, _, client := mergeScene(t)
		if err := client.MergePullRequest(ctx, githubtest.Token, "example-org", "example-repo", 21, head, "squash"); err != nil {
			t.Fatalf("MergePullRequest: %v", err)
		}
	})
	t.Run("a head that moved", func(t *testing.T) {
		_, _, client := mergeScene(t)
		err := client.MergePullRequest(ctx, githubtest.Token, "example-org", "example-repo", 21, "2222222222222222222222222222222222222222", "squash")
		if !errors.Is(err, github.ErrHeadMoved) {
			t.Fatalf("error = %v, want ErrHeadMoved", err)
		}
	})
	t.Run("a conflict", func(t *testing.T) {
		fake, repo, client := mergeScene(t)
		fake.SetPullRequestConflict(repo, 21)
		err := client.MergePullRequest(ctx, githubtest.Token, "example-org", "example-repo", 21, head, "squash")
		if !errors.Is(err, github.ErrConflict) {
			t.Fatalf("error = %v, want ErrConflict", err)
		}
	})
	t.Run("a 405 of a mergeable pull request is not a conflict", func(t *testing.T) {
		fake, _, client := mergeScene(t)
		fake.FailNext(http.MethodPut, "/repos/example-org/example-repo/pulls/21/merge", http.StatusMethodNotAllowed)
		err := client.MergePullRequest(ctx, githubtest.Token, "example-org", "example-repo", 21, head, "squash")
		var status *github.StatusError
		if err == nil || errors.Is(err, github.ErrConflict) || !errors.As(err, &status) || status.Status != http.StatusMethodNotAllowed {
			t.Fatalf("error = %v, want a 405 that is not a conflict", err)
		}
	})
}

func TestCloseIssueAsCompleted_ClosesAnOpenIssue(t *testing.T) {
	fake, repo, client := mergeScene(t)
	ctx := context.Background()
	open, err := client.IssueIsOpen(ctx, githubtest.Token, "example-org", "example-repo", 10)
	if err != nil || !open {
		t.Fatalf("IssueIsOpen = %v, %v; want true", open, err)
	}
	if err := client.CloseIssueAsCompleted(ctx, githubtest.Token, "example-org", "example-repo", 10); err != nil {
		t.Fatalf("CloseIssueAsCompleted: %v", err)
	}
	if issue := fake.Issue(repo, 10); !issue.Closed || issue.StateReason != "completed" {
		t.Errorf("issue #10 = %+v, want closed as completed", issue)
	}
	if got := github.PullRequestURL("example-org", "example-repo", 21); !strings.HasSuffix(got, "/example-org/example-repo/pull/21") {
		t.Errorf("PullRequestURL = %q", got)
	}
}

func TestRepositoryPermission_ReadsThePermissionAndTheType(t *testing.T) {
	fake, _, client := mergeScene(t)
	fake.SetPermission("the-owner", "admin", "User")
	ctx := context.Background()
	for _, tc := range []struct{ login, permission, userType string }{
		{"the-owner", "admin", "User"},
		{"someone", "read", "User"},
		{"example-implementer[bot]", "none", "Bot"},
	} {
		permission, userType, err := client.RepositoryPermission(ctx, githubtest.Token, "example-org", "example-repo", tc.login)
		if err != nil || permission != tc.permission || userType != tc.userType {
			t.Errorf("RepositoryPermission(%s) = %q, %q, %v; want %q, %q", tc.login, permission, userType, err, tc.permission, tc.userType)
		}
	}
}

// A merge whose answer is lost is a temporary failure, and the pull request
// then reads as merged: the caller sends no second merge.
func TestPullRequestIsMerged_ShowsAMergeWhoseAnswerWasLost(t *testing.T) {
	ctx := context.Background()
	fake, _, client := mergeScene(t)
	merged, err := client.PullRequestIsMerged(ctx, githubtest.Token, "example-org", "example-repo", 21)
	if err != nil || merged {
		t.Fatalf("PullRequestIsMerged before the merge = %v, %v; want false", merged, err)
	}
	fake.DropAnswers(http.MethodPut, "/repos/example-org/example-repo/pulls/21/merge", 1)
	err = client.MergePullRequest(ctx, githubtest.Token, "example-org", "example-repo", 21, head, "squash")
	if !github.IsTemporary(err) {
		t.Fatalf("error of the merge without an answer = %v, want a temporary failure", err)
	}
	merged, err = client.PullRequestIsMerged(ctx, githubtest.Token, "example-org", "example-repo", 21)
	if err != nil || !merged {
		t.Fatalf("PullRequestIsMerged after the merge = %v, %v; want true", merged, err)
	}
}
