package github_test

import (
	"context"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

// The comments come oldest first, with the author as the REST API shows
// it: a GitHub App is "<slug>[bot]".
func TestReadIssueComments_AuthorsAndTimes(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	t0 := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{"cumin/type/requirement"}})
	fake.AddComment(repo, 6, githubtest.Comment{Body: "A question", Author: "octocat", At: t0})
	fake.AddComment(repo, 6, githubtest.Comment{Body: "## Acceptance check\n", Author: "example-planner", AuthorIsBot: true, At: t0.Add(time.Hour)})
	client := github.NewAppClient(server.URL, server.Client())

	comments, _, err := client.ReadIssueComments(context.Background(), githubtest.Token, "example-org", "example-repo", 6)
	if err != nil {
		t.Fatalf("ReadIssueComments: %v", err)
	}
	if len(comments) != 2 {
		t.Fatalf("%d comments, want 2", len(comments))
	}
	if c := comments[0]; c.Author != "octocat" || !c.CreatedAt.Equal(t0) {
		t.Errorf("first comment = %+v, want octocat at %v", c, t0)
	}
	if c := comments[1]; c.Author != "example-planner[bot]" || c.Body != "## Acceptance check\n" {
		t.Errorf("second comment = %+v, want the Planner App with the acceptance check", c)
	}
}

func TestReadSnapshot_ReadsTheCloseTimeOfASubIssue(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	closed := time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)
	fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{"cumin/type/requirement"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 10, Parent: 6, Closed: true, ClosedAt: closed})
	fake.AddIssue(repo, &githubtest.Issue{Number: 11, Parent: 6})
	client := github.NewAppClient(server.URL, server.Client())

	snapshot, err := client.ReadSnapshot(context.Background(), githubtest.Token, "example-org", "example-repo")
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	subs := snapshot.RequirementIssues[0].SubIssues
	if !subs[0].ClosedAt.Equal(closed) || !subs[1].ClosedAt.IsZero() {
		t.Errorf("close times = %v and %v, want %v and zero", subs[0].ClosedAt, subs[1].ClosedAt, closed)
	}
}
