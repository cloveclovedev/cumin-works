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
	fake.AddIssue(repo, &githubtest.Issue{Number: 10, Title: "Add the login screen", Parent: 6, Labels: []string{"cumin/status/ready", "risk/low"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 11, Parent: 6, Labels: []string{"risk/high"}, BlockedBy: []int{10, 12}})
	// #10 has three pull requests: a closed one from a person (not read),
	// an open one from the Implementer App, and an open one from a person.
	// #11 has none. #12 has a merged one (not read).
	fake.AddPullRequest(repo, &githubtest.PullRequest{Number: 20, Closed: true, HeadCommit: "1111111111111111111111111111111111111111", Author: "octocat", Closes: []int{10}})
	fake.AddPullRequest(repo, &githubtest.PullRequest{Number: 21, HeadCommit: "2222222222222222222222222222222222222222", Author: "example-implementer", AuthorIsBot: true, Closes: []int{10}})
	fake.AddPullRequest(repo, &githubtest.PullRequest{Number: 23, HeadCommit: "4444444444444444444444444444444444444444", Author: "octocat", Closes: []int{10}})
	fake.AddPullRequest(repo, &githubtest.PullRequest{Number: 22, Merged: true, Closed: true, HeadCommit: "3333333333333333333333333333333333333333", Author: "octocat", Closes: []int{12}})
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
	if sub10.Number != 10 || sub10.Title != "Add the login screen" || sub10.Closed || strings.Join(sub10.Labels, ",") != "cumin/status/ready,risk/low" || len(sub10.BlockedBy) != 0 {
		t.Errorf("sub-issue #10 = %+v", sub10)
	}
	wantPulls := []github.PullRequest{
		{Number: 21, HeadCommit: "2222222222222222222222222222222222222222", Author: "example-implementer[bot]"},
		{Number: 23, HeadCommit: "4444444444444444444444444444444444444444", Author: "octocat"},
	}
	if fmt.Sprint(sub10.PullRequests) != fmt.Sprint(wantPulls) {
		t.Errorf("pull requests of #10 = %+v, want %+v", sub10.PullRequests, wantPulls)
	}
	if sub11.Number != 11 || fmt.Sprint(sub11.BlockedBy) != fmt.Sprint([]github.IssueRef{{Number: 10}, {Number: 12, Closed: true}}) || len(sub11.PullRequests) != 0 {
		t.Errorf("sub-issue #11 = %+v", sub11)
	}
	if sub12.Number != 12 || !sub12.Closed {
		t.Errorf("sub-issue #12 = %+v", sub12)
	}
	if len(sub12.PullRequests) != 0 {
		t.Errorf("pull requests of #12 = %+v, want none: the merged one is not read", sub12.PullRequests)
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

// A pull request whose author account is gone has an empty author.
func TestReadSnapshot_PullRequestWithoutAuthorHasAnEmptyAuthor(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 1, Labels: []string{"cumin/type/requirement"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 2, Parent: 1})
	fake.AddPullRequest(repo, &githubtest.PullRequest{Number: 3, HeadCommit: "4444444444444444444444444444444444444444", Closes: []int{2}})
	client := github.NewAppClient(server.URL, server.Client())

	snapshot, err := client.ReadSnapshot(context.Background(), githubtest.Token, "example-org", "example-repo")
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	pulls := snapshot.RequirementIssues[0].SubIssues[0].PullRequests
	if len(pulls) != 1 || pulls[0].Author != "" {
		t.Errorf("pull requests = %+v, want one pull request with an empty author", pulls)
	}
}

func TestReadSnapshot_TooManyPullRequestsIsAnError(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 1, Labels: []string{"cumin/type/requirement"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 2, Parent: 1})
	// Six open pull requests are an error. Closed ones do not count.
	for n := 10; n <= 15; n++ {
		fake.AddPullRequest(repo, &githubtest.PullRequest{Number: n, Author: "octocat", Closes: []int{2}})
	}
	for n := 16; n <= 25; n++ {
		fake.AddPullRequest(repo, &githubtest.PullRequest{Number: n, Closed: true, Author: "octocat", Closes: []int{2}})
	}
	client := github.NewAppClient(server.URL, server.Client())

	_, err := client.ReadSnapshot(context.Background(), githubtest.Token, "example-org", "example-repo")
	if err == nil || !strings.Contains(err.Error(), "issue #2 has more than 5 open closing pull requests") {
		t.Errorf("err = %v, want an error that names issue #2", err)
	}
	if err := fake.ClosePullRequest(repo, 15); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ReadSnapshot(context.Background(), githubtest.Token, "example-org", "example-repo"); err != nil {
		t.Errorf("with five open pull requests: %v", err)
	}
}

func TestReadSnapshot_ReadsTheDefaultBranchAndTheCuminFiles(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.SetFile(repo, ".cumin/config.toml", githubtest.File{Content: "max_review_rounds = 2\n"})
	fake.SetFile(repo, ".cumin/risk-criteria.md", githubtest.File{Content: "# Risk criteria\n"})
	client := github.NewAppClient(server.URL, server.Client())

	snapshot, err := client.ReadSnapshot(context.Background(), githubtest.Token, "example-org", "example-repo")
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if snapshot.DefaultBranch != "main" || snapshot.DefaultBranchOID == "" {
		t.Errorf("default branch = %q at %q, want main at a commit", snapshot.DefaultBranch, snapshot.DefaultBranchOID)
	}
	if snapshot.CuminConfig == nil || snapshot.CuminConfig.Text != "max_review_rounds = 2\n" {
		t.Fatalf("config = %+v", snapshot.CuminConfig)
	}
	if snapshot.CuminConfig.Path != github.CuminConfigPath || snapshot.CuminConfig.OID == "" {
		t.Errorf("config = %+v, want the path and the blob oid", snapshot.CuminConfig)
	}
	if snapshot.CuminRiskCriteria == nil || snapshot.CuminRiskCriteria.Text != "# Risk criteria\n" ||
		snapshot.CuminRiskCriteria.Path != github.CuminRiskCriteriaPath {
		t.Errorf("risk criteria = %+v", snapshot.CuminRiskCriteria)
	}
	// The oid of a file changes when its content changes, so that a caller
	// parses the file again only after a change.
	before := snapshot.CuminConfig.OID
	fake.SetFile(repo, ".cumin/config.toml", githubtest.File{Content: "max_review_rounds = 5\n"})
	snapshot, err = client.ReadSnapshot(context.Background(), githubtest.Token, "example-org", "example-repo")
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if snapshot.CuminConfig.OID == before {
		t.Errorf("the blob oid did not change with the content")
	}
}

func TestReadSnapshot_WithoutTheCuminFilesIsNotAnError(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 1, Labels: []string{"cumin/type/requirement"}})
	client := github.NewAppClient(server.URL, server.Client())

	snapshot, err := client.ReadSnapshot(context.Background(), githubtest.Token, "example-org", "example-repo")
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if snapshot.CuminConfig != nil || snapshot.CuminRiskCriteria != nil {
		t.Errorf("config = %+v, risk criteria = %+v, want both absent", snapshot.CuminConfig, snapshot.CuminRiskCriteria)
	}
	if len(snapshot.RequirementIssues) != 1 {
		t.Errorf("%d requirement issues, want 1", len(snapshot.RequirementIssues))
	}
}

func TestReadSnapshot_ACuminFileThatCannotBeReadWholeIsAnError(t *testing.T) {
	tests := []struct {
		name string
		path string
		file githubtest.File
		want string
	}{
		{"binary settings", ".cumin/config.toml", githubtest.File{Content: "\x00", Binary: true}, ".cumin/config.toml is not text"},
		{"truncated settings", ".cumin/config.toml", githubtest.File{Content: "max_review_rounds = 2", Truncated: true}, ".cumin/config.toml is too large"},
		{"binary risk criteria", ".cumin/risk-criteria.md", githubtest.File{Content: "\x00", Binary: true}, ".cumin/risk-criteria.md is not text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake, server := githubtest.New(t)
			repo := fake.AddRepository("example-org", "example-repo")
			fake.SetFile(repo, tt.path, tt.file)
			client := github.NewAppClient(server.URL, server.Client())

			_, err := client.ReadSnapshot(context.Background(), githubtest.Token, "example-org", "example-repo")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want an error that names %s", err, tt.path)
			}
		})
	}
}

func TestReadSnapshot_AsksForTheFilesOnTheFirstPageOnly(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.SetFile(repo, ".cumin/config.toml", githubtest.File{Content: "merge_method = \"merge\"\n"})
	// One more than one page of requirement issues.
	for n := 1; n <= 11; n++ {
		fake.AddIssue(repo, &githubtest.Issue{Number: n, Labels: []string{"cumin/type/requirement"}})
	}
	client := github.NewAppClient(server.URL, server.Client())

	snapshot, err := client.ReadSnapshot(context.Background(), githubtest.Token, "example-org", "example-repo")
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if snapshot.CuminConfig == nil || snapshot.CuminConfig.Text != "merge_method = \"merge\"\n" {
		t.Errorf("config = %+v, want the file of the first page", snapshot.CuminConfig)
	}
	requests := fake.Requests()
	if len(requests) != 2 {
		t.Fatalf("%d requests, want 2", len(requests))
	}
	if !strings.Contains(string(requests[0].Body), `"repositoryFiles":true`) {
		t.Errorf("the first page does not ask for the files: %s", requests[0].Body)
	}
	if !strings.Contains(string(requests[1].Body), `"repositoryFiles":false`) {
		t.Errorf("the second page asks for the files again: %s", requests[1].Body)
	}
}
