package workflow

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

// toSnapshot copies every field that the rules read, and nothing of the
// platform types reaches the snapshot.
func TestToSnapshot_CopiesTitleBlockedByAndPullRequests(t *testing.T) {
	read := github.RepositorySnapshot{RequirementIssues: []github.Issue{{
		Number: 6, Labels: []string{"cumin/type/requirement"},
		SubIssues: []github.Issue{{
			Number: 10, Title: "Add the login screen", Labels: []string{"cumin/status/implementing", "risk/low"},
			BlockedBy: []github.IssueRef{{Number: 9, Closed: true}},
			PullRequests: []github.PullRequest{
				{Number: 21, HeadCommit: "2222", Author: "example-implementer[bot]", Mergeable: github.Conflicting, HeadCommittedAt: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC), Reviews: []github.Review{
					{Author: "example-reviewer[bot]", State: "APPROVED", Commit: "2222", SubmittedAt: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), URL: "https://example.com/r/1"},
				}},
			},
		}},
	}}}
	got := toSnapshot(read)
	want := Snapshot{RequirementIssues: []RequirementIssue{{
		Number: 6, Labels: []string{"cumin/type/requirement"},
		SubIssues: []SubIssue{{
			Number: 10, Title: "Add the login screen", Labels: []string{"cumin/status/implementing", "risk/low"},
			BlockedBy: []BlockedBy{{Number: 9, Closed: true}},
			PullRequests: []PullRequest{
				{Number: 21, HeadCommit: "2222", Author: "example-implementer[bot]", Mergeable: Conflicting, HeadCommittedAt: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC), Reviews: []Review{
					{Author: "example-reviewer[bot]", State: ReviewApproved, Commit: "2222", SubmittedAt: time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC), URL: "https://example.com/r/1"},
				}},
			},
		}},
	}}}
	if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", want) {
		t.Errorf("toSnapshot =\n%+v\nwant\n%+v", got, want)
	}
}

// A sub-issue that waits for its checks carries the time that
// cumin/status/checking was last added. The label times query runs
// only for a requirement issue that has such a sub-issue.
func TestReadLabelTimes_ASubIssueThatWaitsForItsChecksCarriesTheTimeOfTheLabel(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	t0 := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{"cumin/type/requirement", LabelImplementing}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 10, Parent: 6, Labels: []string{LabelChecking, "risk/low"}, LabelEvents: []githubtest.LabelEvent{
		// The issue waited for its checks before; the newest event counts.
		{Label: LabelChecking, At: t0},
		{Label: LabelImplementing, At: t0.Add(time.Minute)},
		{Label: LabelChecking, At: t0.Add(2 * time.Minute)},
	}})
	// No sub-issue of #7 waits for its checks, so its label times are not read.
	fake.AddIssue(repo, &githubtest.Issue{Number: 7, Labels: []string{"cumin/type/requirement", LabelImplementing}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 11, Parent: 7, Labels: []string{LabelReviewing, "risk/low"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 12, Parent: 7, Closed: true, Labels: []string{LabelChecking, "risk/low"}})
	service := &Service{GitHub: github.NewAppClient(server.URL, server.Client())}
	target := Target{Repository: config.Repository{Owner: "example-org", Name: "example-repo"}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	read, err := service.GitHub.ReadSnapshot(context.Background(), githubtest.Token, "example-org", "example-repo")
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	snapshot := toSnapshot(read)
	service.readLabelTimes(context.Background(), log, githubtest.Token, target, &snapshot)

	sub, ok := snapshot.SubIssue(10)
	if !ok {
		t.Fatal("issue #10 is not in the snapshot")
	}
	if want := t0.Add(2 * time.Minute); !sub.CheckingAt.Equal(want) {
		t.Errorf("CheckingAt of #10 = %v, want %v", sub.CheckingAt, want)
	}
	if waiting, _ := snapshot.RequirementIssue(6); !waiting.LabelTimesRead {
		t.Error("the label times of #6 were not read")
	}
	if other, _ := snapshot.RequirementIssue(7); other.LabelTimesRead {
		t.Error("the label times of #7 were read, but no sub-issue of #7 waits for its checks")
	}
	// The poll query, and the label times query of #6 only.
	if n := fake.CountRequests(http.MethodPost, "/graphql"); n != 2 {
		t.Errorf("%d GraphQL requests, want 2", n)
	}
}
