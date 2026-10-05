package github_test

import (
	"context"
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
