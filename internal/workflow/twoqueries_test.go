package workflow

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

// The second query of the poll reads the pull requests of the open
// sub-issues with a cumin/status/* label, and of no other sub-issue.
func TestSubIssuesWithPullRequestRules_SelectsTheOpenSubIssuesWithAStatusLabel(t *testing.T) {
	snapshot := Snapshot{RequirementIssues: []RequirementIssue{
		{Number: 6, Labels: []string{LabelRequirement, LabelImplementing}, SubIssues: []SubIssue{
			{Number: 10, Labels: []string{LabelReady, "risk/low"}},
			{Number: 11, Labels: []string{"risk/low"}},
			{Number: 12, Closed: true, Labels: []string{LabelAwaitingChecks}},
			{Number: 13, Labels: []string{LabelAwaitingOwnerDecision}},
		}},
		// A requirement issue with no status label still has its sub-issues read.
		{Number: 7, Labels: []string{LabelRequirement}, SubIssues: []SubIssue{
			{Number: 14, Labels: []string{LabelAwaitingOwnerReview}},
			{Number: 15},
		}},
	}}
	var got []int
	for _, sub := range snapshot.SubIssuesWithPullRequestRules() {
		got = append(got, sub.Number)
	}
	if want := []int{10, 13, 14}; !slices.Equal(got, want) {
		t.Errorf("selected sub-issues = %v, want %v", got, want)
	}
	if got := (Snapshot{}).SubIssuesWithPullRequestRules(); len(got) != 0 {
		t.Errorf("selected sub-issues of an empty snapshot = %v, want none", got)
	}
}

// The pull requests of the second read go to their sub-issues, and the
// snapshot of the first read stays as it was.
func TestWithPullRequests_PutsThePullRequestsInTheirSubIssues(t *testing.T) {
	first := Snapshot{DefaultBranch: "main", RequirementIssues: []RequirementIssue{
		{Number: 6, SubIssues: []SubIssue{{Number: 10, Labels: []string{LabelReady}}, {Number: 11}}},
	}}
	snapshot := first.WithPullRequests(map[int][]PullRequest{10: {{Number: 20}}})
	sub, _ := snapshot.SubIssue(10)
	if len(sub.PullRequests) != 1 || sub.PullRequests[0].Number != 20 {
		t.Errorf("pull requests of #10 = %+v, want #20", sub.PullRequests)
	}
	if other, _ := snapshot.SubIssue(11); len(other.PullRequests) != 0 {
		t.Errorf("pull requests of #11 = %+v, want none", other.PullRequests)
	}
	if before, _ := first.SubIssue(10); len(before.PullRequests) != 0 || snapshot.DefaultBranch != "main" {
		t.Errorf("the first snapshot changed: %+v", before)
	}
}

// pullRequestQueries counts the second queries of a poll that the fake
// GitHub received, and returns the ids of the last one.
func pullRequestQueries(t *testing.T, fake *githubtest.Fake) (int, []string) {
	t.Helper()
	n := 0
	var ids []string
	for _, r := range fake.Requests() {
		if r.Method != http.MethodPost || r.Path != "/graphql" {
			continue
		}
		var body struct {
			Variables struct {
				IDs []string `json:"ids"`
			} `json:"variables"`
		}
		if err := json.Unmarshal(r.Body, &body); err != nil {
			t.Fatal(err)
		}
		if body.Variables.IDs != nil {
			n++
			ids = body.Variables.IDs
		}
	}
	return n, ids
}

// A repository with no open sub-issue with a status label is read in one
// query. With such a sub-issue, the second query names that sub-issue only.
func TestReadSnapshot_SendsTheSecondQueryOnlyForSelectedSubIssues(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{"cumin/type/requirement", LabelImplementing}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 10, Parent: 6, Labels: []string{"risk/low"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 11, Parent: 6, Closed: true, Labels: []string{LabelAwaitingChecks, "risk/low"}})
	fake.AddPullRequest(repo, &githubtest.PullRequest{Number: 20, Closes: []int{10}})
	service := &Service{GitHub: github.NewAppClient(server.URL, server.Client())}

	read, snapshot, err := service.readSnapshot(context.Background(), githubtest.Token, "example-org", "example-repo")
	if err != nil {
		t.Fatalf("readSnapshot: %v", err)
	}
	if n := fake.CountRequests(http.MethodPost, "/graphql"); n != 1 {
		t.Errorf("%d GraphQL requests with no selected sub-issue, want 1", n)
	}
	if n, _ := pullRequestQueries(t, fake); n != 0 {
		t.Errorf("%d second queries with no selected sub-issue, want none", n)
	}
	if sub, _ := snapshot.SubIssue(10); len(sub.PullRequests) != 0 {
		t.Errorf("pull requests of #10 = %+v, want none: no rule reads them", sub.PullRequests)
	}
	firstCost := read.RateLimit.Cost

	if err := fake.SetLabels(repo, 10, []string{LabelReady, "risk/low"}); err != nil {
		t.Fatal(err)
	}
	read, snapshot, err = service.readSnapshot(context.Background(), githubtest.Token, "example-org", "example-repo")
	if err != nil {
		t.Fatalf("readSnapshot: %v", err)
	}
	if n := fake.CountRequests(http.MethodPost, "/graphql"); n != 3 {
		t.Errorf("%d GraphQL requests after the second read, want 3", n)
	}
	n, ids := pullRequestQueries(t, fake)
	if want := []string{githubtest.IssueNodeID(repo, 10)}; n != 1 || !slices.Equal(ids, want) {
		t.Errorf("%d second queries for %v, want 1 for %v", n, ids, want)
	}
	if sub, _ := snapshot.SubIssue(10); len(sub.PullRequests) != 1 || sub.PullRequests[0].Number != 20 {
		t.Errorf("pull requests of #10 = %+v, want #20", sub.PullRequests)
	}
	if read.RateLimit.Cost != firstCost+1 {
		t.Errorf("cost = %d, want the cost of both queries (%d)", read.RateLimit.Cost, firstCost+1)
	}
}

// Each rule of the poll that reads a pull request decides its action on a
// snapshot that the two queries built: the claim (I1), the review after the
// checks (I3), the fix of a failed check (I4), the label copy (I11), the
// approval and the change request of the Owner (I12, I13), the conflict
// (I14), and the stop for checks that do not report (I15).
func TestDecide_EachRuleThatReadsAPullRequestDecidesOnTheSnapshotOfTheTwoQueries(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	t0 := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	head := func(n int) string { return string(rune('a'+n-10)) + "000000000000000000000000000000000000000" }
	issue := func(n int, events []githubtest.LabelEvent, labels ...string) {
		fake.AddIssue(repo, &githubtest.Issue{Number: n, Parent: 6, Title: "x", Labels: labels, LabelEvents: events})
	}
	// pull adds the pull request #(100+n) of issue #n, with the labels of
	// the issue, so that only #17 has labels to copy.
	pull := func(n int, pr githubtest.PullRequest) {
		pr.Number, pr.Closes, pr.HeadCommit = 100+n, []int{n}, head(n)
		pr.Author, pr.AuthorIsBot = "example-implementer", true
		if pr.Labels == nil {
			pr.Labels = fake.Issue(repo, n).Labels
		}
		fake.AddPullRequest(repo, &pr)
	}
	fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{"cumin/type/requirement", LabelImplementing}})
	issue(10, nil, LabelReady, "risk/low")
	pull(10, githubtest.PullRequest{HeadBranch: "cumin/10-x"})
	issue(11, nil, LabelAwaitingChecks, "risk/low")
	pull(11, githubtest.PullRequest{Checks: []githubtest.Check{{Name: "ci", Conclusion: "SUCCESS"}}})
	issue(12, nil, LabelAwaitingChecks, "risk/low")
	pull(12, githubtest.PullRequest{Checks: []githubtest.Check{{Name: "ci", Conclusion: "FAILURE"}}})
	issue(13, nil, LabelAwaitingChecks, "risk/low")
	pull(13, githubtest.PullRequest{Conflict: true})
	issue(14, []githubtest.LabelEvent{{Label: LabelAwaitingChecks, At: t0}}, LabelAwaitingChecks, "risk/low")
	pull(14, githubtest.PullRequest{HeadCommittedAt: t0})
	issue(15, []githubtest.LabelEvent{{Label: LabelAwaitingOwnerReview, At: t0}}, LabelAwaitingOwnerReview, "risk/medium")
	pull(15, githubtest.PullRequest{Reviews: []githubtest.Review{{Author: "example-owner", State: "APPROVED", Commit: head(15), SubmittedAt: t0.Add(time.Minute)}}})
	issue(16, []githubtest.LabelEvent{{Label: LabelAwaitingOwnerReview, At: t0}}, LabelAwaitingOwnerReview, "risk/medium")
	pull(16, githubtest.PullRequest{Reviews: []githubtest.Review{{Author: "example-owner", State: "CHANGES_REQUESTED", Commit: head(16), SubmittedAt: t0.Add(time.Minute)}}})
	issue(17, nil, LabelReviewing, "risk/low")
	pull(17, githubtest.PullRequest{Labels: []string{LabelImplementing, "risk/low"}})
	// No status label: the second query does not read this pull request,
	// and no rule acts on it.
	issue(18, nil, "risk/low")
	pull(18, githubtest.PullRequest{Labels: []string{LabelReviewing, "risk/low"}})

	service := &Service{GitHub: github.NewAppClient(server.URL, server.Client())}
	_, snapshot, err := service.readSnapshot(context.Background(), githubtest.Token, "example-org", "example-repo")
	if err != nil {
		t.Fatalf("readSnapshot: %v", err)
	}
	target := Target{Repository: config.Repository{Owner: "example-org", Name: "example-repo"}}
	service.readLabelTimes(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), githubtest.Token, target, &snapshot)

	if sub, _ := snapshot.SubIssue(10); len(sub.PullRequests) != 1 || sub.PullRequests[0].HeadBranch != "cumin/10-x" {
		t.Errorf("pull requests of #10 = %+v, want the branch that the claim continues on", sub.PullRequests)
	}
	if sub, _ := snapshot.SubIssue(18); len(sub.PullRequests) != 0 {
		t.Errorf("pull requests of #18 = %+v, want none", sub.PullRequests)
	}
	required := []RequiredCheck{{Name: "ci"}}
	actions := decideReadyOfOwner(snapshot, 20, required, nil, t0.Add(2*time.Hour), time.Hour)
	want := []Action{
		ResolveConflict{Number: 13, PullRequest: 113},
		StartReview{Number: 11, PullRequest: 111},
		FixChecks{Number: 12, PullRequest: 112, Failed: required},
		StopForUnreportedChecks{Number: 14, PullRequest: 114, HeadCommit: head(14), Unreported: required, Waited: 2 * time.Hour},
		Claim{Number: 10, RequirementIssue: 6},
		MergeOwnerApproval{Number: 15, PullRequest: 115, Reviewers: []string{"example-owner"}},
		FixOwnerReview{Number: 16, PullRequest: 116, Reviewers: []string{"example-owner"}},
		CopyLabels{Issue: 17, PullRequest: 117, Labels: []string{LabelReviewing, "risk/low"}},
	}
	if len(actions) != len(want) {
		t.Fatalf("actions = %+v, want %+v", actions, want)
	}
	for i := range want {
		if !reflect.DeepEqual(actions[i], want[i]) {
			t.Errorf("action %d = %+v, want %+v", i, actions[i], want[i])
		}
	}
}
