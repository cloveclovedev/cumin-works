package github_test

import (
	"context"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

const readyLabel = "cumin/status/ready"

// The actor of the newest event of the label wins: an older event of the
// same label by another account, and a newer event of another label, do
// not.
func TestReadLabelActor_TheActorOfTheNewestEventOfTheLabel(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	t0 := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	fake.AddIssue(repo, &githubtest.Issue{Number: 10, LabelEvents: []githubtest.LabelEvent{
		{Label: readyLabel, At: t0, Actor: "first-owner", ActorType: "User"},
		{Label: readyLabel, At: t0.Add(time.Hour), Actor: "second-owner", ActorType: "User"},
		{Label: "cumin/status/implementing", At: t0.Add(2 * time.Hour), Actor: "cumin-core", ActorType: "Bot"},
	}})
	client := github.NewAppClient(server.URL, server.Client())

	actor, rate, err := client.ReadLabelActor(context.Background(), githubtest.Token, "example-org", "example-repo", 10, readyLabel)
	if err != nil {
		t.Fatalf("ReadLabelActor: %v", err)
	}
	if want := (github.LabelActor{Login: "second-owner", Type: "User", At: t0.Add(time.Hour)}); actor != want {
		t.Errorf("actor = %+v, want %+v", actor, want)
	}
	if rate.Cost == 0 {
		t.Error("the rate limit of the call was not read")
	}
}

// A requirement issue that never got the label takes the newest event of
// the label among its sub-issues.
func TestReadLabelActor_ARequirementIssueWithoutTheEventReadsItsSubIssues(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	t0 := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{"cumin/type/requirement"}, LabelEvents: []githubtest.LabelEvent{
		{Label: "cumin/status/implementing", At: t0.Add(5 * time.Hour), Actor: "cumin-core", ActorType: "Bot"},
	}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 10, Parent: 6, LabelEvents: []githubtest.LabelEvent{
		{Label: readyLabel, At: t0.Add(2 * time.Hour), Actor: "newer-owner", ActorType: "User"},
	}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 11, Parent: 6, LabelEvents: []githubtest.LabelEvent{
		{Label: readyLabel, At: t0.Add(time.Hour), Actor: "older-owner", ActorType: "User"},
	}})
	client := github.NewAppClient(server.URL, server.Client())

	actor, _, err := client.ReadLabelActor(context.Background(), githubtest.Token, "example-org", "example-repo", 6, readyLabel)
	if err != nil {
		t.Fatalf("ReadLabelActor: %v", err)
	}
	if actor.Login != "newer-owner" {
		t.Errorf("actor = %+v, want newer-owner", actor)
	}
}

// The event of the issue itself wins over a newer event of a sub-issue.
func TestReadLabelActor_TheEventOfTheIssueWinsOverItsSubIssues(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	t0 := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{"cumin/type/requirement"}, LabelEvents: []githubtest.LabelEvent{
		{Label: readyLabel, At: t0, Actor: "requirement-owner", ActorType: "User"},
	}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 10, Parent: 6, LabelEvents: []githubtest.LabelEvent{
		{Label: readyLabel, At: t0.Add(time.Hour), Actor: "sub-issue-owner", ActorType: "User"},
	}})
	client := github.NewAppClient(server.URL, server.Client())

	actor, _, err := client.ReadLabelActor(context.Background(), githubtest.Token, "example-org", "example-repo", 6, readyLabel)
	if err != nil {
		t.Fatalf("ReadLabelActor: %v", err)
	}
	if actor.Login != "requirement-owner" {
		t.Errorf("actor = %+v, want requirement-owner", actor)
	}
}

// With no event of the label, and with an event whose account no longer
// exists, the actor has no login and no type.
func TestReadLabelActor_NoEventIsNoActor(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	t0 := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	fake.AddIssue(repo, &githubtest.Issue{Number: 10})
	fake.AddIssue(repo, &githubtest.Issue{Number: 11, LabelEvents: []githubtest.LabelEvent{{Label: readyLabel, At: t0}}})
	client := github.NewAppClient(server.URL, server.Client())

	for _, number := range []int{10, 11} {
		actor, _, err := client.ReadLabelActor(context.Background(), githubtest.Token, "example-org", "example-repo", number, readyLabel)
		if err != nil {
			t.Fatalf("ReadLabelActor of #%d: %v", number, err)
		}
		if actor.Login != "" || actor.Type != "" {
			t.Errorf("actor of #%d = %+v, want none", number, actor)
		}
	}
}

func TestReadLabelActor_AnUnknownIssueIsAnError(t *testing.T) {
	fake, server := githubtest.New(t)
	fake.AddRepository("example-org", "example-repo")
	client := github.NewAppClient(server.URL, server.Client())
	if _, _, err := client.ReadLabelActor(context.Background(), githubtest.Token, "example-org", "example-repo", 99, readyLabel); err == nil {
		t.Error("ReadLabelActor of an unknown issue returned no error")
	}
}
