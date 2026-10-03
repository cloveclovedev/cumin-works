package github_test

import (
	"context"
	"slices"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

// readTwoQueries reads the fake repository as a poll does, in two queries:
// the poll query, then the pull requests of every sub-issue. It returns one
// snapshot with the pull requests in their sub-issues.
func readTwoQueries(client *github.AppClient) (github.RepositorySnapshot, error) {
	ctx := context.Background()
	snapshot, err := client.ReadSnapshot(ctx, githubtest.Token, "example-org", "example-repo")
	if err != nil {
		return github.RepositorySnapshot{}, err
	}
	var ids []string
	for _, requirement := range snapshot.RequirementIssues {
		for _, sub := range requirement.SubIssues {
			ids = append(ids, sub.NodeID)
		}
	}
	read, err := client.ReadPullRequests(ctx, githubtest.Token, "example-org", "example-repo", ids)
	if err != nil {
		return github.RepositorySnapshot{}, err
	}
	for i, requirement := range snapshot.RequirementIssues {
		for j, sub := range requirement.SubIssues {
			snapshot.RequirementIssues[i].SubIssues[j].PullRequests = read.PullRequests[sub.Number]
		}
	}
	return snapshot, nil
}

// The poll query stops at the sub-issue: it names no pull request, and its
// answer holds none, also for a sub-issue with an open closing pull request.
func TestReadSnapshot_ReadsNoPullRequest(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{"cumin/type/requirement"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 10, Parent: 6, Labels: []string{"cumin/status/awaiting-checks"}})
	fake.AddPullRequest(repo, &githubtest.PullRequest{Number: 21, Closes: []int{10}})
	client := github.NewAppClient(server.URL, server.Client())

	snapshot, err := client.ReadSnapshot(context.Background(), githubtest.Token, "example-org", "example-repo")
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if got := snapshot.RequirementIssues[0].SubIssues[0].PullRequests; len(got) != 0 {
		t.Errorf("pull requests of #10 = %+v, want none from the poll query", got)
	}
	requests := fake.Requests()
	if len(requests) != 1 {
		t.Fatalf("%d requests, want 1", len(requests))
	}
	for _, field := range []string{"closedByPullRequestsReferences", "statusCheckRollup", "reviews", "$pullRequests"} {
		if containsField(requests[0].Body, field) {
			t.Errorf("the poll query names %s", field)
		}
	}
}

func containsField(body []byte, field string) bool {
	return slices.Contains(splitWords(string(body)), field)
}

// splitWords cuts a query at every character that is not part of a name.
func splitWords(text string) []string {
	var words []string
	word := []rune{}
	for _, r := range text {
		if r == '$' || r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			word = append(word, r)
			continue
		}
		if len(word) > 0 {
			words = append(words, string(word))
			word = word[:0]
		}
	}
	return words
}

// The second query returns the open closing pull requests of the named
// sub-issues only, by the number of the sub-issue, in one request.
func TestReadPullRequests_ReadsThePullRequestsOfTheNamedIssues(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{"cumin/type/requirement"}})
	for n := 10; n <= 12; n++ {
		fake.AddIssue(repo, &githubtest.Issue{Number: n, Parent: 6, Labels: []string{"cumin/status/awaiting-checks"}})
		fake.AddPullRequest(repo, &githubtest.PullRequest{Number: n + 10, Author: "example-implementer", AuthorIsBot: true, Closes: []int{n}})
	}
	fake.AddPullRequest(repo, &githubtest.PullRequest{Number: 30, Closed: true, Closes: []int{10}})
	client := github.NewAppClient(server.URL, server.Client())

	ids := []string{githubtest.IssueNodeID(repo, 10), githubtest.IssueNodeID(repo, 12)}
	read, err := client.ReadPullRequests(context.Background(), githubtest.Token, "example-org", "example-repo", ids)
	if err != nil {
		t.Fatalf("ReadPullRequests: %v", err)
	}
	if len(read.PullRequests) != 2 || len(read.PullRequests[10]) != 1 || len(read.PullRequests[12]) != 1 {
		t.Fatalf("pull requests = %+v, want one for #10 and one for #12", read.PullRequests)
	}
	if pr := read.PullRequests[10][0]; pr.Number != 20 || pr.Author != "example-implementer[bot]" {
		t.Errorf("pull request of #10 = %+v, want #20 of example-implementer[bot]", pr)
	}
	if read.RateLimit.Cost != 1 {
		t.Errorf("cost = %d, want the cost of one call", read.RateLimit.Cost)
	}
	if n := len(fake.Requests()); n != 1 {
		t.Errorf("%d requests, want 1", n)
	}
}

// No sub-issue to read means no request at all.
func TestReadPullRequests_NoIssueSendsNoQuery(t *testing.T) {
	fake, server := githubtest.New(t)
	fake.AddRepository("example-org", "example-repo")
	client := github.NewAppClient(server.URL, server.Client())

	read, err := client.ReadPullRequests(context.Background(), githubtest.Token, "example-org", "example-repo", nil)
	if err != nil || len(read.PullRequests) != 0 {
		t.Errorf("ReadPullRequests = %+v, %v; want nothing", read, err)
	}
	if n := len(fake.Requests()); n != 0 {
		t.Errorf("%d requests, want none", n)
	}
}

// GitHub takes at most 100 ids in one call, so more sub-issues are read in
// calls of 100, and the costs add up.
func TestReadPullRequests_ReadsMoreThanAHundredIssuesInPages(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	var ids []string
	for n := 1; n <= 101; n++ {
		fake.AddIssue(repo, &githubtest.Issue{Number: n})
		ids = append(ids, githubtest.IssueNodeID(repo, n))
	}
	fake.AddPullRequest(repo, &githubtest.PullRequest{Number: 200, Closes: []int{101}})
	client := github.NewAppClient(server.URL, server.Client())

	read, err := client.ReadPullRequests(context.Background(), githubtest.Token, "example-org", "example-repo", ids)
	if err != nil {
		t.Fatalf("ReadPullRequests: %v", err)
	}
	if len(read.PullRequests) != 101 || len(read.PullRequests[101]) != 1 {
		t.Errorf("%d issues read, pull requests of #101 = %+v; want 101 issues and #200", len(read.PullRequests), read.PullRequests[101])
	}
	if n := len(fake.Requests()); n != 2 {
		t.Errorf("%d requests, want 2", n)
	}
	if read.RateLimit.Cost != 2 {
		t.Errorf("cost = %d, want the sum of the two calls", read.RateLimit.Cost)
	}
}

// An issue that is gone between the two reads stops the poll: a rule must
// not decide on a sub-issue whose pull requests were not read.
func TestReadPullRequests_AnIDThatIsNoIssueIsAnError(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 10})
	client := github.NewAppClient(server.URL, server.Client())

	ids := []string{githubtest.IssueNodeID(repo, 10), githubtest.IssueNodeID(repo, 99)}
	if _, err := client.ReadPullRequests(context.Background(), githubtest.Token, "example-org", "example-repo", ids); err == nil {
		t.Error("ReadPullRequests returned no error for an issue that is gone")
	}
}
