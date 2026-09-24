package github_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

func TestRequiredChecks_ReadsTheChecksOfTheRulesOfTheBranch(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	repo.DefaultBranch = "main"
	repo.RequiredChecks = []string{"test", "cumin-protected-paths", "test"}
	client := github.NewAppClient(server.URL, server.Client())

	checks, err := client.RequiredChecks(context.Background(), githubtest.Token, "example-org", "example-repo", "main")
	if err != nil {
		t.Fatalf("RequiredChecks: %v", err)
	}
	// Sorted, and a name that two rules require is returned once.
	if want := []string{"cumin-protected-paths", "test"}; fmt.Sprint(checks) != fmt.Sprint(want) {
		t.Errorf("checks = %v, want %v", checks, want)
	}
}

func TestRequiredChecks_ABranchWithoutRulesGivesAnEmptyList(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	repo.DefaultBranch = "main"
	client := github.NewAppClient(server.URL, server.Client())

	checks, err := client.RequiredChecks(context.Background(), githubtest.Token, "example-org", "example-repo", "main")
	if err != nil {
		t.Fatalf("RequiredChecks: %v", err)
	}
	if len(checks) != 0 {
		t.Errorf("checks = %v, want none", checks)
	}
}

func TestRequiredChecks_AFailedCallNamesTheStatusWithoutTheToken(t *testing.T) {
	fake, server := githubtest.New(t)
	fake.AddRepository("example-org", "example-repo")
	client := github.NewAppClient(server.URL, server.Client())

	_, err := client.RequiredChecks(context.Background(), githubtest.Token, "example-org", "missing-repo", "main")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v, want the status of the answer", err)
	}
	if strings.Contains(err.Error(), githubtest.Token) {
		t.Errorf("the error holds the token: %v", err)
	}
}

// TestReadSnapshot_ReadsTheBranchTheLabelsAndTheChecksOfAPullRequest covers
// what I3, I4, and I11 read of a pull request.
func TestReadSnapshot_ReadsTheBranchTheLabelsAndTheChecksOfAPullRequest(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{"cumin/type/requirement"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 10, Parent: 6, Labels: []string{"cumin/status/awaiting-checks", "risk/low"}})
	fake.AddPullRequest(repo, &githubtest.PullRequest{
		Number:     21,
		HeadCommit: "2222222222222222222222222222222222222222",
		HeadBranch: "cumin/10-add-the-login-screen",
		Author:     "example-implementer",
		Closes:     []int{10},
		Labels:     []string{"cumin/status/implementing", "risk/low"},
		Checks: []githubtest.Check{
			{Name: "test", Conclusion: "SUCCESS"},
			{Name: "cumin-protected-paths", Conclusion: "SKIPPED"},
			{Name: "lint", Conclusion: "NEUTRAL"},
			{Name: "build", Conclusion: "FAILURE"},
			{Name: "slow", Status: "IN_PROGRESS"},
			{Name: "cumin-live-status", CommitStatus: true, State: "SUCCESS"},
			{Name: "deploy", CommitStatus: true, State: "PENDING"},
			{Name: "smoke", CommitStatus: true, State: "ERROR"},
		},
	})
	client := github.NewAppClient(server.URL, server.Client())

	snapshot, err := client.ReadSnapshot(context.Background(), githubtest.Token, "example-org", "example-repo")
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	pr := snapshot.RequirementIssues[0].SubIssues[0].PullRequests[0]
	if pr.HeadBranch != "cumin/10-add-the-login-screen" {
		t.Errorf("head branch = %q", pr.HeadBranch)
	}
	if want := "[cumin/status/implementing risk/low]"; fmt.Sprint(pr.Labels) != want {
		t.Errorf("labels = %v, want %s", pr.Labels, want)
	}
	want := []github.CheckResult{
		{Name: "test", Conclusion: github.CheckPassed},
		{Name: "cumin-protected-paths", Conclusion: github.CheckPassed},
		{Name: "lint", Conclusion: github.CheckPassed},
		{Name: "build", Conclusion: github.CheckFailed},
		{Name: "slow", Conclusion: github.CheckPending},
		{Name: "cumin-live-status", Conclusion: github.CheckPassed},
		{Name: "deploy", Conclusion: github.CheckPending},
		{Name: "smoke", Conclusion: github.CheckFailed},
	}
	if fmt.Sprint(pr.Checks) != fmt.Sprint(want) {
		t.Errorf("checks = %v, want %v", pr.Checks, want)
	}
}

func TestReadSnapshot_APullRequestWithoutAnyCheckHasNoChecks(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{"cumin/type/requirement"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 10, Parent: 6})
	fake.AddPullRequest(repo, &githubtest.PullRequest{Number: 21, HeadBranch: "cumin/10-x", Closes: []int{10}})
	client := github.NewAppClient(server.URL, server.Client())

	snapshot, err := client.ReadSnapshot(context.Background(), githubtest.Token, "example-org", "example-repo")
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if checks := snapshot.RequirementIssues[0].SubIssues[0].PullRequests[0].Checks; len(checks) != 0 {
		t.Errorf("checks = %v, want none", checks)
	}
}

// TestReadSnapshot_TooManyChecksOrLabelsOnAPullRequestIsAnError: a rule must
// never decide on a part of the facts, as it must not for an issue.
func TestReadSnapshot_TooManyChecksOrLabelsOnAPullRequestIsAnError(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pull    githubtest.PullRequest
		message string
	}{
		{name: "checks", pull: githubtest.PullRequest{Number: 21, Closes: []int{10}, Checks: manyChecks(21)}, message: "more than 20 checks"},
		{name: "labels", pull: githubtest.PullRequest{Number: 21, Closes: []int{10}, Labels: manyLabels(11)}, message: "more than 10 labels"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake, server := githubtest.New(t)
			repo := fake.AddRepository("example-org", "example-repo")
			fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{"cumin/type/requirement"}})
			fake.AddIssue(repo, &githubtest.Issue{Number: 10, Parent: 6})
			pull := tc.pull
			fake.AddPullRequest(repo, &pull)
			client := github.NewAppClient(server.URL, server.Client())

			_, err := client.ReadSnapshot(context.Background(), githubtest.Token, "example-org", "example-repo")
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("err = %v, want %q", err, tc.message)
			}
			if !strings.Contains(err.Error(), "issue #10") || !strings.Contains(err.Error(), "pull request #21") {
				t.Errorf("err = %v, want the issue and the pull request", err)
			}
		})
	}
}

// TestReadSnapshot_AnUnknownKindOfCheckIsAnError: GitHub has two kinds of
// check in the rollup today. A third one must stop the poll instead of
// letting I3 pass an issue whose check cumin cannot read.
func TestReadSnapshot_AnUnknownKindOfCheckIsAnError(t *testing.T) {
	const answer = `{"data":{"repository":{"defaultBranchRef":{"name":"main","target":{"oid":"abc"}},
	  "cuminConfig":null,"cuminRiskCriteria":null,
	  "issues":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[
	    {"number":6,"state":"OPEN","labels":{"pageInfo":{"hasNextPage":false},"nodes":[]},
	     "subIssues":{"pageInfo":{"hasNextPage":false},"nodes":[
	       {"number":10,"title":"x","state":"OPEN","labels":{"pageInfo":{"hasNextPage":false},"nodes":[]},
	        "blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[]},
	        "closedByPullRequestsReferences":{"pageInfo":{"hasNextPage":false},"nodes":[
	          {"number":21,"headRefOid":"222","headRefName":"cumin/10-x","author":null,
	           "labels":{"pageInfo":{"hasNextPage":false},"nodes":[]},
	           "statusCheckRollup":{"contexts":{"pageInfo":{"hasNextPage":false},"nodes":[
	             {"__typename":"DeploymentGate","name":"a-new-kind"}]}}}]}}]}}]}},
	  "rateLimit":{"cost":9,"remaining":4991}}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, answer)
	}))
	defer server.Close()
	client := github.NewAppClient(server.URL, server.Client())

	_, err := client.ReadSnapshot(context.Background(), githubtest.Token, "example-org", "example-repo")
	if err == nil || !strings.Contains(err.Error(), "unknown type \"DeploymentGate\"") {
		t.Fatalf("err = %v, want the unknown type", err)
	}
}

func manyChecks(n int) []githubtest.Check {
	checks := make([]githubtest.Check, 0, n)
	for i := range n {
		checks = append(checks, githubtest.Check{Name: fmt.Sprintf("check-%d", i), Conclusion: "SUCCESS"})
	}
	return checks
}

func manyLabels(n int) []string {
	labels := make([]string, 0, n)
	for i := range n {
		labels = append(labels, fmt.Sprintf("label-%d", i))
	}
	return labels
}
