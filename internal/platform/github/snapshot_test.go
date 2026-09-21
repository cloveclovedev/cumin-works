package github_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

func TestReadSnapshot_ReadsRequirementIssuesWithSubIssuesAndBlockedBy(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{"cumin/type/requirement", "cumin/status/implementing"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 10, Parent: 6, Labels: []string{"cumin/status/ready", "risk/low"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 11, Parent: 6, Labels: []string{"risk/high"}, BlockedBy: []int{10, 12}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 12, Parent: 6, Closed: true, Labels: []string{"risk/low"}})
	// Not in the snapshot: a closed requirement issue, and an open issue
	// without the requirement label.
	fake.AddIssue(repo, &githubtest.Issue{Number: 3, Closed: true, Labels: []string{"cumin/type/requirement"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 4, Parent: 3, Labels: []string{"risk/low"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 5, Labels: []string{"question"}})
	client := github.NewAppClient(server.URL, server.Client())

	snapshot, err := client.ReadSnapshot(context.Background(), githubtest.Token, "example-org", "example-repo")
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if n := fake.CountRequests(http.MethodPost, "/graphql"); n != 1 {
		t.Errorf("%d GraphQL requests, want 1", n)
	}
	if len(snapshot.RequirementIssues) != 1 {
		t.Fatalf("requirement issues = %+v, want only #6", snapshot.RequirementIssues)
	}
	issue := snapshot.RequirementIssues[0]
	if issue.Number != 6 || issue.Closed || strings.Join(issue.Labels, ",") != "cumin/type/requirement,cumin/status/implementing" {
		t.Errorf("requirement issue = %+v", issue)
	}
	if len(issue.SubIssues) != 3 {
		t.Fatalf("sub-issues = %+v, want #10, #11, #12", issue.SubIssues)
	}
	sub10, sub11, sub12 := issue.SubIssues[0], issue.SubIssues[1], issue.SubIssues[2]
	if sub10.Number != 10 || sub10.Closed || strings.Join(sub10.Labels, ",") != "cumin/status/ready,risk/low" || len(sub10.BlockedBy) != 0 {
		t.Errorf("sub-issue #10 = %+v", sub10)
	}
	if sub11.Number != 11 || fmt.Sprint(sub11.BlockedBy) != fmt.Sprint([]github.IssueRef{{Number: 10}, {Number: 12, Closed: true}}) {
		t.Errorf("sub-issue #11 = %+v", sub11)
	}
	if sub12.Number != 12 || !sub12.Closed {
		t.Errorf("sub-issue #12 = %+v", sub12)
	}
	if snapshot.RateLimit.Cost < 1 || snapshot.RateLimit.Remaining < 1 {
		t.Errorf("rate limit = %+v, want cost and remaining", snapshot.RateLimit)
	}
}

func TestReadSnapshot_ReadsTheNextPage(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	// One more than one page of requirement issues.
	for n := 1; n <= 11; n++ {
		fake.AddIssue(repo, &githubtest.Issue{Number: n, Labels: []string{"cumin/type/requirement"}})
	}
	client := github.NewAppClient(server.URL, server.Client())

	snapshot, err := client.ReadSnapshot(context.Background(), githubtest.Token, "example-org", "example-repo")
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if n := fake.CountRequests(http.MethodPost, "/graphql"); n != 2 {
		t.Errorf("%d GraphQL requests, want 2", n)
	}
	if len(snapshot.RequirementIssues) != 11 {
		t.Fatalf("%d requirement issues, want 11", len(snapshot.RequirementIssues))
	}
	for i, issue := range snapshot.RequirementIssues {
		if issue.Number != i+1 {
			t.Errorf("issue %d has number %d", i, issue.Number)
		}
	}
	// The cost of the whole read is the sum of the pages.
	if snapshot.RateLimit.Cost != 11 {
		t.Errorf("cost = %d, want the sum of two pages (11)", snapshot.RateLimit.Cost)
	}
}

func TestReadSnapshot_GraphQLErrorNamesTheMessageWithoutTheToken(t *testing.T) {
	fake, server := githubtest.New(t)
	fake.AddRepository("example-org", "example-repo")
	client := github.NewAppClient(server.URL, server.Client())

	_, err := client.ReadSnapshot(context.Background(), githubtest.Token, "example-org", "missing-repo")
	if err == nil || !strings.Contains(err.Error(), "Could not resolve to a Repository") {
		t.Fatalf("err = %v, want the GraphQL message", err)
	}
	if strings.Contains(err.Error(), githubtest.Token) {
		t.Errorf("the error holds the token: %v", err)
	}

	_, err = client.ReadSnapshot(context.Background(), "ghs_wrongToken", "example-org", "example-repo")
	if err == nil || !strings.Contains(err.Error(), "status 401") || strings.Contains(err.Error(), "ghs_wrongToken") {
		t.Errorf("wrong token: err = %v, want status 401 without the token", err)
	}
}

func TestReadSnapshot_TooManySubIssuesIsAnError(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 1, Labels: []string{"cumin/type/requirement"}})
	for n := 2; n <= 32; n++ {
		fake.AddIssue(repo, &githubtest.Issue{Number: n, Parent: 1})
	}
	client := github.NewAppClient(server.URL, server.Client())

	_, err := client.ReadSnapshot(context.Background(), githubtest.Token, "example-org", "example-repo")
	if err == nil || !strings.Contains(err.Error(), "issue #1 has more than 30 sub-issues") {
		t.Errorf("err = %v, want an error that names issue #1", err)
	}
}
