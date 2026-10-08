package github_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

const readyLabel = "cumin/status/ready"

// The actor of the event that last put the label on the issue wins: an
// event of the same label before the label was removed, and a newer event
// of another label, do not.
func TestReadLabelActor_TheActorOfTheEventThatLastPutTheLabel(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	t0 := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	fake.AddIssue(repo, &githubtest.Issue{Number: 10, LabelEvents: []githubtest.LabelEvent{
		{Label: readyLabel, At: t0, Actor: "first-owner", ActorType: "User"},
		{Label: readyLabel, At: t0.Add(30 * time.Minute), Removed: true},
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

// GitHub can record an event that adds a label again while the label is on
// the issue, late and with another account (measured on 2026-10-05). The
// event that put the label on the issue answers, with its time.
func TestReadLabelActor_ARepeatedEventOfTheLabelDoesNotAnswer(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	t0 := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	fake.AddIssue(repo, &githubtest.Issue{Number: 10, Labels: []string{readyLabel}, LabelEvents: []githubtest.LabelEvent{
		{Label: readyLabel, At: t0, Actor: "example-owner", ActorType: "User"},
		{Label: readyLabel, At: t0.Add(time.Minute), Actor: "cumin-planner", ActorType: "Bot"},
	}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 11, Labels: []string{readyLabel}, LabelEvents: []githubtest.LabelEvent{
		{Label: readyLabel, At: t0, Actor: "example-owner", ActorType: "User"},
		{Label: readyLabel, At: t0.Add(time.Minute), Actor: "cumin-planner", ActorType: "Bot"},
		{Label: readyLabel, At: t0.Add(2 * time.Minute), Removed: true},
		{Label: readyLabel, At: t0.Add(3 * time.Minute), Actor: "example-triager", ActorType: "User"},
		{Label: readyLabel, At: t0.Add(4 * time.Minute), Actor: "cumin-planner", ActorType: "Bot"},
	}})
	client := github.NewAppClient(server.URL, server.Client())

	for number, want := range map[int]github.LabelActor{
		10: {Login: "example-owner", Type: "User", At: t0},
		11: {Login: "example-triager", Type: "User", At: t0.Add(3 * time.Minute)},
	} {
		for name, read := range map[string]func(context.Context, string, string, string, int, string) (github.LabelActor, github.RateLimit, error){
			"ReadLabelActor": client.ReadLabelActor, "ReadOwnLabelActor": client.ReadOwnLabelActor,
		} {
			actor, _, err := read(context.Background(), githubtest.Token, "example-org", "example-repo", number, readyLabel)
			if err != nil {
				t.Fatalf("%s of #%d: %v", name, number, err)
			}
			if actor != want {
				t.Errorf("%s of #%d = %+v, want %+v", name, number, actor, want)
			}
		}
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

// A requirement issue that is split again keeps the closed sub-issues of
// its earlier split: the newest event among 20 sub-issues answers, also
// when it is on the second page.
func TestReadLabelActor_ReadsTwentySubIssuesInTwoPages(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	t0 := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{"cumin/type/requirement"}})
	for n := 10; n < 30; n++ {
		fake.AddIssue(repo, &githubtest.Issue{Number: n, Parent: 6, LabelEvents: []githubtest.LabelEvent{
			{Label: readyLabel, At: t0.Add(time.Duration(n) * time.Minute), Actor: fmt.Sprintf("owner-%d", n), ActorType: "User"},
		}})
	}
	client := github.NewAppClient(server.URL, server.Client())

	actor, rate, err := client.ReadLabelActor(context.Background(), githubtest.Token, "example-org", "example-repo", 6, readyLabel)
	if err != nil {
		t.Fatalf("ReadLabelActor: %v", err)
	}
	if actor.Login != "owner-29" {
		t.Errorf("actor = %+v, want owner-29: the newest event is on the second page", actor)
	}
	if n := fake.CountRequests(http.MethodPost, "/graphql"); n != 2 {
		t.Errorf("%d GraphQL requests, want 2: the first page and one next page of sub-issues", n)
	}
	if rate.Cost != 2 {
		t.Errorf("cost = %d, want 2: the cost of both calls", rate.Cost)
	}
}

// An issue with at most one page of sub-issues costs one call. An issue
// that carries the event itself costs one call with any number of
// sub-issues: its sub-issues do not answer.
func TestReadLabelActor_ReadsTheNextPageOnlyWhenItNeedsOne(t *testing.T) {
	for _, tc := range []struct {
		subIssues int
		own       bool
		calls     int
	}{{0, false, 1}, {12, false, 1}, {13, false, 2}, {36, false, 3}, {20, true, 1}} {
		fake, server := githubtest.New(t)
		repo := fake.AddRepository("example-org", "example-repo")
		issue := &githubtest.Issue{Number: 6, Labels: []string{"cumin/type/requirement"}}
		if tc.own {
			issue.LabelEvents = []githubtest.LabelEvent{{Label: readyLabel, At: time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC), Actor: "requirement-owner", ActorType: "User"}}
		}
		fake.AddIssue(repo, issue)
		for n := 0; n < tc.subIssues; n++ {
			fake.AddIssue(repo, &githubtest.Issue{Number: 10 + n, Parent: 6})
		}
		client := github.NewAppClient(server.URL, server.Client())

		if _, _, err := client.ReadLabelActor(context.Background(), githubtest.Token, "example-org", "example-repo", 6, readyLabel); err != nil {
			t.Fatalf("%d sub-issues: ReadLabelActor: %v", tc.subIssues, err)
		}
		if n := fake.CountRequests(http.MethodPost, "/graphql"); n != tc.calls {
			t.Errorf("%d sub-issues, own event %v: %d GraphQL requests, want %d", tc.subIssues, tc.own, n, tc.calls)
		}
	}
}

func TestReadLabelActor_MoreThanThreePagesOfSubIssuesIsAnError(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{"cumin/type/requirement"}})
	for n := 0; n < 37; n++ {
		fake.AddIssue(repo, &githubtest.Issue{Number: 10 + n, Parent: 6})
	}
	client := github.NewAppClient(server.URL, server.Client())

	_, _, err := client.ReadLabelActor(context.Background(), githubtest.Token, "example-org", "example-repo", 6, readyLabel)
	if err == nil || !strings.Contains(err.Error(), "issue #6 has more than 36 sub-issues") {
		t.Errorf("ReadLabelActor of 37 sub-issues: %v, want an error that names #6 and 36", err)
	}
	if n := fake.CountRequests(http.MethodPost, "/graphql"); n != 3 {
		t.Errorf("%d GraphQL requests, want 3: no call after the third page", n)
	}
}
