package github_test

import (
	"context"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

// The newest event of each label wins: the same label goes on and off an
// issue more than once.
func TestReadLabelTimes_TheNewestEventOfEachLabel(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	t0 := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{"cumin/type/requirement", "cumin/status/awaiting-owner-review"}, LabelEvents: []githubtest.LabelEvent{
		{Label: "cumin/status/awaiting-owner-review", At: t0},
		{Label: "cumin/status/implementing", At: t0.Add(time.Hour)},
		{Label: "cumin/status/awaiting-owner-review", At: t0.Add(2 * time.Hour)},
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
	if got, want := times[6]["cumin/status/awaiting-owner-review"], t0.Add(2*time.Hour); !got.Equal(want) {
		t.Errorf("awaiting-owner-review of #6 at %v, want %v", got, want)
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
