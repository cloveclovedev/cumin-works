package github_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

func TestCreateIssueComment_WritesTheBodyOnTheIssue(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 12, Title: "Add the login screen"})
	client := github.NewAppClient(server.URL, server.Client())

	const body = "## Decision needed: which sign-in method does the login screen use?"
	comment, err := client.CreateIssueComment(context.Background(), githubtest.Token, "example-org", "example-repo", 12, body)
	if err != nil {
		t.Fatalf("CreateIssueComment: %v", err)
	}
	if comment.ID == 0 {
		t.Error("the comment has no id")
	}
	if !strings.Contains(comment.URL, "example-org/example-repo/issues/12") {
		t.Errorf("URL = %q, want the address of the comment on the issue", comment.URL)
	}

	comments := fake.Comments(repo, 12)
	if len(comments) != 1 || comments[0].Body != body {
		t.Fatalf("the comments of the issue = %+v, want one with the body", comments)
	}
	if n := fake.CountRequests(http.MethodPost, "/repos/example-org/example-repo/issues/12/comments"); n != 1 {
		t.Errorf("%d POST requests, want 1", n)
	}
}

func TestCreateIssueComment_KeepsTheOrderOfTheComments(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 12})
	fake.AddIssue(repo, &githubtest.Issue{Number: 13})
	client := github.NewAppClient(server.URL, server.Client())
	ctx := context.Background()

	for _, body := range []string{"first", "second"} {
		if _, err := client.CreateIssueComment(ctx, githubtest.Token, "example-org", "example-repo", 12, body); err != nil {
			t.Fatalf("CreateIssueComment(%q): %v", body, err)
		}
	}
	if _, err := client.CreateIssueComment(ctx, githubtest.Token, "example-org", "example-repo", 13, "other issue"); err != nil {
		t.Fatalf("CreateIssueComment on #13: %v", err)
	}

	comments := fake.Comments(repo, 12)
	if len(comments) != 2 || comments[0].Body != "first" || comments[1].Body != "second" {
		t.Errorf("the comments of #12 = %+v, want first then second", comments)
	}
	if comments[0].ID == comments[1].ID {
		t.Errorf("both comments have the id %d, want two ids", comments[0].ID)
	}
	if others := fake.Comments(repo, 13); len(others) != 1 {
		t.Errorf("the comments of #13 = %+v, want one", others)
	}
}

func TestCreateIssueComment_AFailureNamesTheIssue(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 12})
	client := github.NewAppClient(server.URL, server.Client())
	ctx := context.Background()

	// An issue that the repository does not have.
	_, err := client.CreateIssueComment(ctx, githubtest.Token, "example-org", "example-repo", 99, "stopped")
	if err == nil {
		t.Fatal("CreateIssueComment on a missing issue = nil, want an error")
	}
	if !strings.Contains(err.Error(), "example-org/example-repo#99") {
		t.Errorf("the error %q does not name the repository and the issue", err.Error())
	}

	// A refusal of the API, such as a token without the write permission.
	fake.FailNext(http.MethodPost, "/repos/example-org/example-repo/issues/12/comments", http.StatusForbidden)
	_, err = client.CreateIssueComment(ctx, githubtest.Token, "example-org", "example-repo", 12, "stopped")
	if err == nil {
		t.Fatal("CreateIssueComment with a refusal = nil, want an error")
	}
	if !strings.Contains(err.Error(), "example-org/example-repo#12") || !strings.Contains(err.Error(), "403") {
		t.Errorf("the error %q does not name the issue and the status", err.Error())
	}
	if len(fake.Comments(repo, 12)) != 0 {
		t.Error("a failed request left a comment on the issue")
	}
}
