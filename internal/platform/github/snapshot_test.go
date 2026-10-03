package github_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

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

	snapshot, err := readTwoQueries(client)
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	// One page of the poll query, and one call of the second query.
	if n := fake.CountRequests(http.MethodPost, "/graphql"); n != 2 {
		t.Errorf("%d GraphQL requests, want 2", n)
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
		{Number: 21, HeadCommit: "2222222222222222222222222222222222222222", Author: "example-implementer[bot]", Mergeable: github.Mergeable},
		{Number: 23, HeadCommit: "4444444444444444444444444444444444444444", Author: "octocat", Mergeable: github.Mergeable},
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
	// One more than the page size. The requirement allows 12 sub-issues
	// for one requirement issue (requirement-sizing.md), so this never
	// happens in normal use.
	for n := 2; n <= 18; n++ {
		fake.AddIssue(repo, &githubtest.Issue{Number: n, Parent: 1})
	}
	client := github.NewAppClient(server.URL, server.Client())

	_, err := client.ReadSnapshot(context.Background(), githubtest.Token, "example-org", "example-repo")
	if err == nil || !strings.Contains(err.Error(), "issue #1 has more than 15 sub-issues") {
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

	snapshot, err := readTwoQueries(client)
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
	// Three open pull requests are an error. Closed ones do not count.
	for n := 10; n <= 12; n++ {
		fake.AddPullRequest(repo, &githubtest.PullRequest{Number: n, Author: "octocat", Closes: []int{2}})
	}
	for n := 16; n <= 25; n++ {
		fake.AddPullRequest(repo, &githubtest.PullRequest{Number: n, Closed: true, Author: "octocat", Closes: []int{2}})
	}
	client := github.NewAppClient(server.URL, server.Client())

	_, err := readTwoQueries(client)
	if err == nil || !strings.Contains(err.Error(), "issue #2 has more than 2 open closing pull requests") {
		t.Errorf("err = %v, want an error that names issue #2", err)
	}
	if err := fake.ClosePullRequest(repo, 12); err != nil {
		t.Fatal(err)
	}
	if _, err := readTwoQueries(client); err != nil {
		t.Errorf("with two open pull requests: %v", err)
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

// The reviews of a pull request come with the author in the REST form, the
// state, the commit, the time, and the address. A pending review has no
// time, and a review whose commit is gone has no commit.
func TestReadSnapshot_ReadsTheReviewsOfAPullRequest(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 1, Labels: []string{"cumin/type/requirement"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 2, Parent: 1})
	at := time.Date(2026, 9, 30, 1, 2, 3, 0, time.UTC)
	fake.AddPullRequest(repo, &githubtest.PullRequest{Number: 3, HeadCommit: "5555555555555555555555555555555555555555", Closes: []int{2}, Reviews: []githubtest.Review{
		{Author: "example-reviewer", AuthorIsBot: true, State: "CHANGES_REQUESTED", Commit: "4444444444444444444444444444444444444444", SubmittedAt: at, URL: "https://github.com/example-org/example-repo/pull/3#pullrequestreview-1"},
		{Author: "octocat", State: "COMMENTED", SubmittedAt: at.Add(time.Minute)},
		{Author: "example-reviewer", AuthorIsBot: true, State: "PENDING", Commit: "5555555555555555555555555555555555555555"},
	}})
	client := github.NewAppClient(server.URL, server.Client())

	snapshot, err := readTwoQueries(client)
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	want := []github.Review{
		{Author: "example-reviewer[bot]", State: "CHANGES_REQUESTED", Commit: "4444444444444444444444444444444444444444", SubmittedAt: at, URL: "https://github.com/example-org/example-repo/pull/3#pullrequestreview-1"},
		{Author: "octocat", State: "COMMENTED", SubmittedAt: at.Add(time.Minute)},
		{Author: "example-reviewer[bot]", State: "PENDING", Commit: "5555555555555555555555555555555555555555"},
	}
	got := snapshot.RequirementIssues[0].SubIssues[0].PullRequests[0].Reviews
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("reviews = %+v, want %+v", got, want)
	}
}

// More reviews than one read holds are an error, as for every other
// connection: a rule never counts the rounds on a part of the reviews.
func TestReadSnapshot_TooManyReviewsIsAnError(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 1, Labels: []string{"cumin/type/requirement"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 2, Parent: 1})
	reviews := make([]githubtest.Review, 101)
	for i := range reviews {
		reviews[i] = githubtest.Review{Author: "octocat", State: "COMMENTED", SubmittedAt: time.Date(2026, 9, 30, 0, i, 0, 0, time.UTC)}
	}
	fake.AddPullRequest(repo, &githubtest.PullRequest{Number: 3, Closes: []int{2}, Reviews: reviews})
	client := github.NewAppClient(server.URL, server.Client())

	_, err := readTwoQueries(client)
	if err == nil || !strings.Contains(err.Error(), "pull request #3 has more than 100 reviews") {
		t.Errorf("err = %v, want an error that names pull request #3", err)
	}
}

// The snapshot carries the mergeable value of a pull request as GitHub
// answers it, and the commit time of the head commit.
func TestReadSnapshot_ReadsTheMergeableValueAndTheHeadCommitTime(t *testing.T) {
	at := time.Date(2026, 10, 3, 1, 2, 3, 0, time.UTC)
	tests := []struct {
		name string
		set  func(fake *githubtest.Fake, repo *githubtest.Repository)
		want github.MergeableState
	}{
		{"a pull request without a conflict is mergeable", func(*githubtest.Fake, *githubtest.Repository) {}, github.Mergeable},
		{"a conflict is conflicting", func(fake *githubtest.Fake, repo *githubtest.Repository) {
			fake.SetPullRequestConflict(repo, 3)
		}, github.Conflicting},
		{"a value that GitHub still calculates is unknown", func(fake *githubtest.Fake, repo *githubtest.Repository) {
			fake.SetPullRequestMergeable(repo, 3, "UNKNOWN")
		}, github.MergeableUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake, server := githubtest.New(t)
			repo := fake.AddRepository("example-org", "example-repo")
			fake.AddIssue(repo, &githubtest.Issue{Number: 1, Labels: []string{"cumin/type/requirement"}})
			fake.AddIssue(repo, &githubtest.Issue{Number: 2, Parent: 1})
			fake.AddPullRequest(repo, &githubtest.PullRequest{Number: 3, HeadCommit: "5555555555555555555555555555555555555555", Closes: []int{2}})
			fake.SetPullRequestHeadCommitTime(repo, 3, at)
			tt.set(fake, repo)
			client := github.NewAppClient(server.URL, server.Client())

			snapshot, err := readTwoQueries(client)
			if err != nil {
				t.Fatalf("ReadSnapshot: %v", err)
			}
			pr := snapshot.RequirementIssues[0].SubIssues[0].PullRequests[0]
			if pr.Mergeable != tt.want {
				t.Errorf("mergeable = %q, want %q", pr.Mergeable, tt.want)
			}
			if !pr.HeadCommittedAt.Equal(at) {
				t.Errorf("head commit time = %v, want %v", pr.HeadCommittedAt, at)
			}
		})
	}
}

// A mergeable value that cumin does not know is an error of the poll: no
// rule decides on a value whose meaning is not known.
func TestReadSnapshot_AnUnknownMergeableValueIsAnError(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 1, Labels: []string{"cumin/type/requirement"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 2, Parent: 1})
	fake.AddPullRequest(repo, &githubtest.PullRequest{Number: 3, HeadCommit: "5555555555555555555555555555555555555555", Closes: []int{2}})
	fake.SetPullRequestMergeable(repo, 3, "BLOCKED")
	client := github.NewAppClient(server.URL, server.Client())

	_, err := readTwoQueries(client)
	if err == nil || !strings.Contains(err.Error(), `pull request #3 has the unknown mergeable value "BLOCKED"`) {
		t.Errorf("ReadSnapshot = %v, want an error that names pull request #3 and the value", err)
	}
}

// The time of the head commit is zero when the last commit that GitHub
// lists is not the head commit (a push came between the two reads), and
// when GitHub lists no commit: a later rule must not count a wait from the
// time of another commit.
func TestReadSnapshot_TheHeadCommitTimeIsZeroWhenTheLastCommitIsNotTheHead(t *testing.T) {
	tests := []struct {
		name    string
		commits string
		want    time.Time
	}{
		{"the last commit is the head commit", `[{"commit":{"oid":"222","committedDate":"2026-10-03T01:02:03Z"}}]`, time.Date(2026, 10, 3, 1, 2, 3, 0, time.UTC)},
		{"the last commit is another commit", `[{"commit":{"oid":"111","committedDate":"2026-10-03T01:02:03Z"}}]`, time.Time{}},
		{"no commit", `[]`, time.Time{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			answer := `{"data":{"nodes":[
	       {"__typename":"Issue","number":10,
	        "closedByPullRequestsReferences":{"pageInfo":{"hasNextPage":false},"nodes":[
			          {"number":21,"headRefOid":"222","headRefName":"cumin/10-x","mergeable":"MERGEABLE","author":null,
			           "commits":{"nodes":` + tt.commits + `},
			           "labels":{"pageInfo":{"hasNextPage":false},"nodes":[]},
			           "statusCheckRollup":null,
			           "reviews":{"pageInfo":{"hasNextPage":false},"nodes":[]}}]}}],
			  "rateLimit":{"cost":17,"remaining":4983}}}`
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, answer)
			}))
			defer server.Close()
			client := github.NewAppClient(server.URL, server.Client())

			read, err := client.ReadPullRequests(context.Background(), githubtest.Token, "example-org", "example-repo", []string{"I_10"})
			if err != nil {
				t.Fatalf("ReadPullRequests: %v", err)
			}
			pr := read.PullRequests[10][0]
			if !pr.HeadCommittedAt.Equal(tt.want) {
				t.Errorf("head commit time = %v, want %v", pr.HeadCommittedAt, tt.want)
			}
		})
	}
}

// The read of one sub-issue returns the facts that the poll returns for the
// same sub-issue, in one query.
func TestReadSubIssue_ReturnsTheFactsOfThePollForOneIssue(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{"cumin/type/requirement", "cumin/status/implementing"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 10, Title: "Add the login screen", Parent: 6, Labels: []string{"cumin/status/reviewing", "risk/low"}, BlockedBy: []int{11}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 11, Parent: 6, Closed: true, Labels: []string{"risk/high"}})
	fake.AddPullRequest(repo, &githubtest.PullRequest{
		Number: 21, HeadCommit: "2222222222222222222222222222222222222222", HeadBranch: "cumin/10-add-the-login-screen",
		Author: "example-implementer", AuthorIsBot: true, Closes: []int{10}, Labels: []string{"risk/low"},
		Checks:  []githubtest.Check{{Name: "ci", Conclusion: "SUCCESS"}},
		Reviews: []githubtest.Review{{Author: "example-reviewer", AuthorIsBot: true, State: "APPROVED", Commit: "2222222222222222222222222222222222222222"}},
	})
	// Many other requirement issues: the read of one issue does not page.
	for n := 30; n <= 55; n++ {
		fake.AddIssue(repo, &githubtest.Issue{Number: n, Labels: []string{"cumin/type/requirement"}})
	}
	client := github.NewAppClient(server.URL, server.Client())
	ctx := context.Background()

	snapshot, err := readTwoQueries(client)
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	before := fake.CountRequests(http.MethodPost, "/graphql")
	read, err := client.ReadSubIssue(ctx, githubtest.Token, "example-org", "example-repo", 10)
	if err != nil {
		t.Fatalf("ReadSubIssue: %v", err)
	}
	if n := fake.CountRequests(http.MethodPost, "/graphql") - before; n != 1 {
		t.Errorf("%d GraphQL requests, want 1", n)
	}
	want := snapshot.RequirementIssues[0].SubIssues[0]
	if len(want.PullRequests) != 1 || len(want.PullRequests[0].Checks) != 1 || len(want.PullRequests[0].Reviews) != 1 {
		t.Fatalf("the poll read %+v, want one pull request with one check and one review", want)
	}
	if !reflect.DeepEqual(read.Issue, want) {
		t.Errorf("issue = %+v, want the sub-issue of the poll %+v", read.Issue, want)
	}
	if read.DefaultBranch != "main" || read.DefaultBranch != snapshot.DefaultBranch {
		t.Errorf("default branch = %q, want main as in the poll (%q)", read.DefaultBranch, snapshot.DefaultBranch)
	}
	if read.RateLimit.Cost != 1 {
		t.Errorf("cost = %d, want 1", read.RateLimit.Cost)
	}
}

// The read of one requirement issue returns the facts that the poll returns
// for the same requirement issue, with its sub-issues.
func TestReadRequirementIssue_ReturnsTheFactsOfThePollForOneIssue(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 5, Closed: true})
	fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{"cumin/type/requirement", "cumin/status/planning"}, BlockedBy: []int{5}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 10, Title: "First", Parent: 6, Labels: []string{"risk/low"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 11, Title: "Second", Parent: 6, Closed: true, Labels: []string{"risk/high"}, BlockedBy: []int{10}})
	fake.AddPullRequest(repo, &githubtest.PullRequest{Number: 21, HeadCommit: "2222222222222222222222222222222222222222", Author: "octocat", Closes: []int{10}})
	client := github.NewAppClient(server.URL, server.Client())
	ctx := context.Background()

	snapshot, err := readTwoQueries(client)
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	before := fake.CountRequests(http.MethodPost, "/graphql")
	read, err := client.ReadRequirementIssue(ctx, githubtest.Token, "example-org", "example-repo", 6)
	if err != nil {
		t.Fatalf("ReadRequirementIssue: %v", err)
	}
	if n := fake.CountRequests(http.MethodPost, "/graphql") - before; n != 1 {
		t.Errorf("%d GraphQL requests, want 1", n)
	}
	want := snapshot.RequirementIssues[0]
	if len(want.SubIssues) != 2 || len(want.SubIssues[0].PullRequests) != 1 {
		t.Fatalf("the poll read %+v, want two sub-issues and one pull request", want)
	}
	if !reflect.DeepEqual(read.Issue, want) {
		t.Errorf("issue = %+v, want the requirement issue of the poll %+v", read.Issue, want)
	}
}

// An issue over a limit of the query is an error that names the issue, as
// in the poll.
func TestReadOneIssue_AnIssueOverALimitIsAnErrorThatNamesTheIssue(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 1, Labels: []string{"cumin/type/requirement"}})
	// One more sub-issue than the page size, and one of them with three
	// open closing pull requests.
	for n := 2; n <= 18; n++ {
		fake.AddIssue(repo, &githubtest.Issue{Number: n, Parent: 1})
	}
	for n := 30; n <= 32; n++ {
		fake.AddPullRequest(repo, &githubtest.PullRequest{Number: n, Author: "octocat", Closes: []int{2}})
	}
	client := github.NewAppClient(server.URL, server.Client())
	ctx := context.Background()

	_, err := client.ReadRequirementIssue(ctx, githubtest.Token, "example-org", "example-repo", 1)
	if err == nil || !strings.Contains(err.Error(), "read issue #1 of example-org/example-repo: issue #1 has more than 15 sub-issues") {
		t.Errorf("err = %v, want an error that names issue #1", err)
	}
	_, err = client.ReadSubIssue(ctx, githubtest.Token, "example-org", "example-repo", 2)
	if err == nil || !strings.Contains(err.Error(), "read issue #2 of example-org/example-repo: issue #2 has more than 2 open closing pull requests") {
		t.Errorf("err = %v, want an error that names issue #2", err)
	}
}

// An issue that does not exist is an error that names the issue.
func TestReadSubIssue_AnIssueThatDoesNotExistIsAnError(t *testing.T) {
	fake, server := githubtest.New(t)
	fake.AddRepository("example-org", "example-repo")
	client := github.NewAppClient(server.URL, server.Client())

	_, err := client.ReadSubIssue(context.Background(), githubtest.Token, "example-org", "example-repo", 7)
	if err == nil || !strings.Contains(err.Error(), "read issue #7 of example-org/example-repo: Could not resolve to an Issue with the number of 7.") {
		t.Errorf("err = %v, want an error that names issue #7", err)
	}
}

// The read of one issue returns an issue only when a poll reads it too
// (issue-states.md, principle 6): a sub-issue needs an open parent with the
// requirement label, and a requirement issue is open and has that label.
func TestReadOneIssue_AnIssueThatThePollDoesNotReadIsAnError(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	// #3 is a closed requirement issue, #5 has no requirement label, and
	// #9 has no parent.
	fake.AddIssue(repo, &githubtest.Issue{Number: 3, Closed: true, Labels: []string{"cumin/type/requirement"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 4, Parent: 3, Labels: []string{"risk/low"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 5, Labels: []string{"question"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 7, Parent: 5, Labels: []string{"risk/low"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 9, Labels: []string{"risk/low"}})
	client := github.NewAppClient(server.URL, server.Client())
	ctx := context.Background()

	for number, want := range map[int]string{
		4: "read issue #4 of example-org/example-repo: the parent of issue #4 is not a requirement issue of the poll: issue #3 is not open",
		7: "read issue #7 of example-org/example-repo: the parent of issue #7 is not a requirement issue of the poll: issue #5 has no label cumin/type/requirement",
		9: "read issue #9 of example-org/example-repo: issue #9 has no parent issue",
	} {
		if _, err := client.ReadSubIssue(ctx, githubtest.Token, "example-org", "example-repo", number); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ReadSubIssue(%d): err = %v, want %q", number, err, want)
		}
	}
	for number, want := range map[int]string{
		3: "read issue #3 of example-org/example-repo: issue #3 is not open",
		5: "read issue #5 of example-org/example-repo: issue #5 has no label cumin/type/requirement",
	} {
		if _, err := client.ReadRequirementIssue(ctx, githubtest.Token, "example-org", "example-repo", number); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ReadRequirementIssue(%d): err = %v, want %q", number, err, want)
		}
	}
	snapshot, err := client.ReadSnapshot(ctx, githubtest.Token, "example-org", "example-repo")
	if err != nil || len(snapshot.RequirementIssues) != 0 {
		t.Errorf("ReadSnapshot = %+v, %v; want no requirement issue, as the reads of one issue say", snapshot.RequirementIssues, err)
	}
}
