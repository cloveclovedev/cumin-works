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

func TestListOpenPullRequestsOfBranch_ReadsTheOpenPullRequestsOfTheBranchOnly(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	const branch = "cumin/10-add-the-login-screen"
	fake.AddPullRequest(repo, &githubtest.PullRequest{Number: 21, HeadBranch: branch, HeadCommit: "1111111111111111111111111111111111111111", Author: "example-implementer", AuthorIsBot: true})
	fake.AddPullRequest(repo, &githubtest.PullRequest{Number: 22, HeadBranch: branch, Closed: true})
	fake.AddPullRequest(repo, &githubtest.PullRequest{Number: 23, HeadBranch: "cumin/11-other"})
	client := github.NewAppClient(server.URL, server.Client())

	got, err := client.ListOpenPullRequestsOfBranch(context.Background(), githubtest.Token, "example-org", "example-repo", branch)
	if err != nil {
		t.Fatalf("ListOpenPullRequestsOfBranch: %v", err)
	}
	want := []github.BranchPullRequest{{
		Number: 21, NodeID: githubtest.PullRequestNodeID(repo, 21), Author: "example-implementer[bot]",
		HeadCommit: "1111111111111111111111111111111111111111", HeadBranch: branch,
	}}
	if !slices.Equal(got, want) {
		t.Errorf("ListOpenPullRequestsOfBranch = %+v, want %+v", got, want)
	}
}

func TestAddClosingLink_LinksThePullRequestToTheIssue(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 10})
	fake.AddPullRequest(repo, &githubtest.PullRequest{Number: 21})
	client := github.NewAppClient(server.URL, server.Client())

	if err := client.AddClosingLink(context.Background(), githubtest.Token, githubtest.IssueNodeID(repo, 10), githubtest.PullRequestNodeID(repo, 21)); err != nil {
		t.Fatalf("AddClosingLink: %v", err)
	}
	if got := fake.PullRequestCloses(repo, 21); !slices.Equal(got, []int{10}) {
		t.Errorf("pull request #21 closes %v, want [10]", got)
	}
}

func TestAddClosingLink_ReturnsTheAnswerOfGitHub(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 10})
	fake.AddPullRequest(repo, &githubtest.PullRequest{Number: 21})
	fake.SetLinkErrors("Resource not accessible by integration")
	client := github.NewAppClient(server.URL, server.Client())

	err := client.AddClosingLink(context.Background(), githubtest.Token, githubtest.IssueNodeID(repo, 10), githubtest.PullRequestNodeID(repo, 21))
	if err == nil || !strings.Contains(err.Error(), "Resource not accessible by integration") {
		t.Fatalf("AddClosingLink error = %v, want the answer of GitHub", err)
	}
	if got := fake.PullRequestCloses(repo, 21); len(got) != 0 {
		t.Errorf("pull request #21 closes %v, want none", got)
	}
}

// Both failure paths of AddClosingLink return a ClosingLinkError: a caller
// reads the answer of GitHub from it, and the message keeps its text.
func TestAddClosingLink_ReturnsATypedErrorWithTheAnswerOfGitHub(t *testing.T) {
	tests := []struct {
		name       string
		fail       func(fake *githubtest.Fake)
		wantAnswer string
	}{
		{
			name:       "a GraphQL answer with errors",
			fail:       func(fake *githubtest.Fake) { fake.SetLinkErrors("Resource not accessible by integration", "Not found") },
			wantAnswer: "Resource not accessible by integration; Not found",
		},
		{
			name:       "a request that GitHub refuses",
			fail:       func(fake *githubtest.Fake) { fake.FailTimes(http.MethodPost, "/graphql", 0, 1, http.StatusForbidden) },
			wantAnswer: "POST /graphql: status 403: Failure requested by the test",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake, server := githubtest.New(t)
			repo := fake.AddRepository("example-org", "example-repo")
			fake.AddIssue(repo, &githubtest.Issue{Number: 10})
			fake.AddPullRequest(repo, &githubtest.PullRequest{Number: 21})
			tt.fail(fake)
			client := github.NewAppClient(server.URL, server.Client())

			err := client.AddClosingLink(context.Background(), githubtest.Token, githubtest.IssueNodeID(repo, 10), githubtest.PullRequestNodeID(repo, 21))
			var link *github.ClosingLinkError
			if !errors.As(err, &link) {
				t.Fatalf("AddClosingLink error = %v, want a ClosingLinkError", err)
			}
			if link.Answer != tt.wantAnswer {
				t.Errorf("Answer = %q, want %q", link.Answer, tt.wantAnswer)
			}
			if want := "github: add the closing link: " + tt.wantAnswer; err.Error() != want {
				t.Errorf("Error() = %q, want %q", err.Error(), want)
			}
		})
	}
}
