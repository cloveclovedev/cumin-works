package github_test

import (
	"context"
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
