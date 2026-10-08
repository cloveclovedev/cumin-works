package github_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

// The event that last put each label on the issue wins: the same label
// goes on and off an issue more than once.
func TestReadLabelTimes_TheEventThatLastPutEachLabel(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	t0 := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{"cumin/type/requirement", "cumin/status/awaiting-plan-review"}, LabelEvents: []githubtest.LabelEvent{
		{Label: "cumin/status/awaiting-plan-review", At: t0},
		{Label: "cumin/status/awaiting-plan-review", At: t0.Add(time.Hour), Removed: true},
		{Label: "cumin/status/implementing", At: t0.Add(time.Hour)},
		{Label: "cumin/status/implementing", At: t0.Add(2 * time.Hour), Removed: true},
		{Label: "cumin/status/awaiting-plan-review", At: t0.Add(2 * time.Hour)},
	}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 10, Parent: 6, Labels: []string{"cumin/status/ready"}, LabelEvents: []githubtest.LabelEvent{
		{Label: "cumin/status/ready", At: t0.Add(3 * time.Hour)},
	}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 11, Parent: 6})
	client := github.NewAppClient(server.URL, server.Client())

	times, rate, err := client.ReadLabelTimes(context.Background(), githubtest.Token, "example-org", "example-repo", 6)
	if err != nil {
		t.Fatalf("ReadLabelTimes: %v", err)
	}
	if got, want := times[6]["cumin/status/awaiting-plan-review"], t0.Add(2*time.Hour); !got.Equal(want) {
		t.Errorf("awaiting-plan-review of #6 at %v, want %v", got, want)
	}
	if got, want := times[10]["cumin/status/ready"], t0.Add(3*time.Hour); !got.Equal(want) {
		t.Errorf("ready of #10 at %v, want %v", got, want)
	}
	if len(times[11]) != 0 {
		t.Errorf("#11 has label times %v, want none", times[11])
	}
	if rate.Cost == 0 {
		t.Error("the rate limit of the call was not read")
	}
}

// The time of a label is the time of the event that put it on the issue.
// A later event that adds the label again, with no event that removed it
// in between, does not move the time (measured on 2026-10-05: GitHub can
// record such an event). A label that was removed keeps the time of the
// event that last put it on the issue.
func TestReadLabelTimes_ARepeatedEventOfALabelDoesNotMoveItsTime(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	t0 := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{"cumin/type/requirement"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 10, Parent: 6, Labels: []string{"cumin/status/checking"}, LabelEvents: []githubtest.LabelEvent{
		{Label: "cumin/status/ready", At: t0},
		{Label: "cumin/status/ready", At: t0.Add(time.Minute)},
		{Label: "cumin/status/ready", At: t0.Add(time.Hour), Removed: true},
		{Label: "cumin/status/checking", At: t0.Add(time.Hour)},
		{Label: "cumin/status/checking", At: t0.Add(2 * time.Hour)},
	}})
	client := github.NewAppClient(server.URL, server.Client())

	times, _, err := client.ReadLabelTimes(context.Background(), githubtest.Token, "example-org", "example-repo", 6)
	if err != nil {
		t.Fatalf("ReadLabelTimes: %v", err)
	}
	if got, want := times[10]["cumin/status/ready"], t0; !got.Equal(want) {
		t.Errorf("ready of #10 at %v, want %v", got, want)
	}
	if got, want := times[10]["cumin/status/checking"], t0.Add(time.Hour); !got.Equal(want) {
		t.Errorf("checking of #10 at %v, want %v", got, want)
	}
}

func TestReadLabelTimes_AnUnknownIssueIsAnError(t *testing.T) {
	fake, server := githubtest.New(t)
	fake.AddRepository("example-org", "example-repo")
	client := github.NewAppClient(server.URL, server.Client())
	if _, _, err := client.ReadLabelTimes(context.Background(), githubtest.Token, "example-org", "example-repo", 99); err == nil {
		t.Error("ReadLabelTimes of an unknown issue returned no error")
	}
}

// The round of the review needs the last cumin/status/ready of one
// implementation issue. The same query reads it when it is given the
// number of that issue, which has no sub-issues of its own.
func TestReadLabelTimes_ReadsOneImplementationIssue(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	t0 := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{"cumin/type/requirement"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 10, Parent: 6, Labels: []string{"cumin/status/reviewing"}, LabelEvents: []githubtest.LabelEvent{
		{Label: "cumin/status/ready", At: t0},
		{Label: "cumin/status/ready", At: t0.Add(30 * time.Minute), Removed: true},
		{Label: "cumin/status/ready", At: t0.Add(time.Hour)},
	}})
	client := github.NewAppClient(server.URL, server.Client())

	times, _, err := client.ReadLabelTimes(context.Background(), githubtest.Token, "example-org", "example-repo", 10)
	if err != nil {
		t.Fatalf("ReadLabelTimes: %v", err)
	}
	if got, want := times[10]["cumin/status/ready"], t0.Add(time.Hour); !got.Equal(want) {
		t.Errorf("ready of #10 at %v, want %v", got, want)
	}
}

// A requirement issue that is split again keeps the closed sub-issues of
// its earlier split: the label times of 20 sub-issues are read in full,
// with one more call for the second page.
func TestReadLabelTimes_ReadsTwentySubIssuesInTwoPages(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	t0 := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{"cumin/type/requirement"}})
	for n := 10; n < 30; n++ {
		fake.AddIssue(repo, &githubtest.Issue{Number: n, Parent: 6, LabelEvents: []githubtest.LabelEvent{
			{Label: "cumin/status/ready", At: t0.Add(time.Duration(n) * time.Minute)},
		}})
	}
	client := github.NewAppClient(server.URL, server.Client())

	times, rate, err := client.ReadLabelTimes(context.Background(), githubtest.Token, "example-org", "example-repo", 6)
	if err != nil {
		t.Fatalf("ReadLabelTimes: %v", err)
	}
	for n := 10; n < 30; n++ {
		if got, want := times[n]["cumin/status/ready"], t0.Add(time.Duration(n)*time.Minute); !got.Equal(want) {
			t.Errorf("ready of #%d at %v, want %v", n, got, want)
		}
	}
	if len(times) != 21 {
		t.Errorf("label times of %d issues, want 21: the issue and its 20 sub-issues", len(times))
	}
	if n := fake.CountRequests(http.MethodPost, "/graphql"); n != 2 {
		t.Errorf("%d GraphQL requests, want 2: the first page and one next page of sub-issues", n)
	}
	if rate.Cost != 2 {
		t.Errorf("cost = %d, want 2: the cost of both calls", rate.Cost)
	}
}

// An issue with at most one page of sub-issues costs one call. One more
// than three pages is an error that names the issue and the limit.
func TestReadLabelTimes_ReadsTheNextPageOnlyWhenThereIsOne(t *testing.T) {
	for _, tc := range []struct{ subIssues, calls int }{{0, 1}, {12, 1}, {13, 2}, {36, 3}} {
		fake, server := githubtest.New(t)
		repo := fake.AddRepository("example-org", "example-repo")
		fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{"cumin/type/requirement"}})
		for n := 0; n < tc.subIssues; n++ {
			fake.AddIssue(repo, &githubtest.Issue{Number: 10 + n, Parent: 6})
		}
		client := github.NewAppClient(server.URL, server.Client())

		times, _, err := client.ReadLabelTimes(context.Background(), githubtest.Token, "example-org", "example-repo", 6)
		if err != nil {
			t.Fatalf("%d sub-issues: ReadLabelTimes: %v", tc.subIssues, err)
		}
		if len(times) != tc.subIssues+1 {
			t.Errorf("%d sub-issues: label times of %d issues, want %d", tc.subIssues, len(times), tc.subIssues+1)
		}
		if n := fake.CountRequests(http.MethodPost, "/graphql"); n != tc.calls {
			t.Errorf("%d sub-issues: %d GraphQL requests, want %d", tc.subIssues, n, tc.calls)
		}
	}
}

func TestReadLabelTimes_MoreThanThreePagesOfSubIssuesIsAnError(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{"cumin/type/requirement"}})
	for n := 0; n < 37; n++ {
		fake.AddIssue(repo, &githubtest.Issue{Number: 10 + n, Parent: 6})
	}
	client := github.NewAppClient(server.URL, server.Client())

	_, _, err := client.ReadLabelTimes(context.Background(), githubtest.Token, "example-org", "example-repo", 6)
	if err == nil || !strings.Contains(err.Error(), "issue #6 has more than 36 sub-issues") {
		t.Errorf("ReadLabelTimes of 37 sub-issues: %v, want an error that names #6 and 36", err)
	}
	if n := fake.CountRequests(http.MethodPost, "/graphql"); n != 3 {
		t.Errorf("%d GraphQL requests, want 3: no call after the third page", n)
	}
}
