package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

// The rows of the round (issue-states.md, the text on rounds): the reviews
// of cumin-reviewer after the later of the last cumin/status/ready of the
// issue and the last APPROVE of cumin-reviewer.
func TestReviewRounds_CountsTheReviewsOfTheReviewerSinceTheStart(t *testing.T) {
	const reviewer = "example-reviewer[bot]"
	at := func(minute int) time.Time { return time.Date(2026, 9, 30, 10, minute, 0, 0, time.UTC) }
	review := func(author string, state ReviewState, minute int, commit string) Review {
		return Review{Author: author, State: state, SubmittedAt: at(minute), Commit: commit}
	}
	tests := []struct {
		name       string
		reviews    []Review
		readyAt    time.Time
		wantRounds int
		wantCommit string
	}{
		{"no review is round 0", nil, at(0), 0, ""},
		{"one request for changes is round 1", []Review{review(reviewer, ReviewChangesRequested, 5, "c1")}, at(0), 1, "c1"},
		{"two requests are round 2, the last commit is the second", []Review{
			review(reviewer, ReviewChangesRequested, 5, "c1"),
			review(reviewer, ReviewChangesRequested, 9, "c2"),
		}, at(0), 2, "c2"},
		{"a review before cumin/status/ready does not count", []Review{
			review(reviewer, ReviewChangesRequested, 5, "c1"),
			review(reviewer, ReviewChangesRequested, 9, "c2"),
		}, at(7), 1, "c2"},
		{"the count starts again after an APPROVE", []Review{
			review(reviewer, ReviewChangesRequested, 5, "c1"),
			review(reviewer, ReviewApproved, 7, "c2"),
			review(reviewer, ReviewChangesRequested, 9, "c3"),
		}, at(0), 1, "c3"},
		{"an APPROVE before cumin/status/ready does not move the start", []Review{
			review(reviewer, ReviewApproved, 2, "c1"),
			review(reviewer, ReviewChangesRequested, 3, "c1"),
			review(reviewer, ReviewChangesRequested, 9, "c2"),
		}, at(5), 1, "c2"},
		{"a person and another App do not count", []Review{
			review("octocat", ReviewChangesRequested, 5, "c1"),
			review("other-app[bot]", ReviewChangesRequested, 6, "c1"),
			review("octocat", ReviewApproved, 7, "c1"),
		}, at(0), 0, ""},
		{"a review with only COMMENT and a pending review do not count", []Review{
			review(reviewer, ReviewCommented, 5, "c1"),
			{Author: reviewer, State: ReviewPending, Commit: "c1"},
		}, at(0), 0, ""},
		{"a dismissed review starts the count again, as an APPROVE does", []Review{
			review(reviewer, ReviewChangesRequested, 3, "c1"),
			// A former APPROVE that a ruleset dismissed after a new push.
			review(reviewer, ReviewDismissed, 5, "c2"),
			review(reviewer, ReviewChangesRequested, 9, "c3"),
		}, at(0), 1, "c3"},
		{"the order of the list does not matter", []Review{
			review(reviewer, ReviewChangesRequested, 9, "c2"),
			review(reviewer, ReviewChangesRequested, 5, "c1"),
		}, at(0), 2, "c2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ReviewRounds(tt.reviews, reviewer, tt.readyAt); got != tt.wantRounds {
				t.Errorf("ReviewRounds = %d, want %d", got, tt.wantRounds)
			}
			if got := LastReviewedCommit(tt.reviews, reviewer, tt.readyAt); got != tt.wantCommit {
				t.Errorf("LastReviewedCommit = %q, want %q", got, tt.wantCommit)
			}
		})
	}
}

// The approved commit of the request (agents/reviewer.md, round 1 after an
// approval): the commit of the newest APPROVED review of the Reviewer.
func TestLastApprovedCommit_IsTheCommitOfTheNewestApprovalOfTheReviewer(t *testing.T) {
	const reviewer = "example-reviewer[bot]"
	at := func(minute int) time.Time { return time.Date(2026, 9, 30, 10, minute, 0, 0, time.UTC) }
	review := func(author string, state ReviewState, minute int, commit string) Review {
		return Review{Author: author, State: state, SubmittedAt: at(minute), Commit: commit}
	}
	tests := []struct {
		name    string
		reviews []Review
		want    string
	}{
		{"no review names no commit", nil, ""},
		{"a request for changes is not an approval", []Review{review(reviewer, ReviewChangesRequested, 1, "c1")}, ""},
		{"one approval names its commit", []Review{review(reviewer, ReviewApproved, 1, "c1")}, "c1"},
		{"two approvals name the newer one, in any order", []Review{
			review(reviewer, ReviewApproved, 5, "c2"),
			review(reviewer, ReviewApproved, 1, "c1"),
		}, "c2"},
		{"a later request for changes keeps the approval", []Review{
			review(reviewer, ReviewApproved, 1, "c1"),
			review(reviewer, ReviewChangesRequested, 5, "c2"),
		}, "c1"},
		{"a dismissed review does not count", []Review{
			review(reviewer, ReviewApproved, 1, "c1"),
			review(reviewer, ReviewDismissed, 5, "c2"),
		}, "c1"},
		{"an approval of another author does not count", []Review{
			review(reviewer, ReviewApproved, 1, "c1"),
			review("octocat", ReviewApproved, 5, "c2"),
		}, "c1"},
		{"a pending review does not count", []Review{
			review(reviewer, ReviewApproved, 1, "c1"),
			{Author: reviewer, State: ReviewPending, Commit: "c2"},
		}, "c1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LastApprovedCommit(tt.reviews, reviewer); got != tt.want {
				t.Errorf("LastApprovedCommit = %q, want %q", got, tt.want)
			}
		})
	}
}

// The request text with no approved commit is the text of round 1 and of
// round 2 as before; the approved commit adds one line after the round, and
// in round 1 the sentence on the diff.
func TestReviewRequestText_NamesTheApprovedCommitAfterTheRound(t *testing.T) {
	base := ReviewRequest{Repository: "example-org/example-repo", Issue: 10, PullRequest: 21, HeadCommit: "c3", Round: 1, Limit: 3, WorkDir: "/work"}
	const tail = " The work directory is a checkout of the head commit c3 with no branch; change nothing in it. Invoke the skill cumin-review, then submit one review on that commit with APPROVE or REQUEST_CHANGES. Then return the result.\n"
	const head = "Request: review\nRepository: example-org/example-repo\nImplementation issue: #10\nPull request: #21\nHead commit: c3\n"

	want := head + "Round: 1 of 3\nWork directory: /work\n\nReview the pull request #21 against the implementation issue #10. This is round 1: find as much as you can." + tail
	if got := ReviewRequestText(base); got != want {
		t.Errorf("with no approved commit:\n%s\nwant:\n%s", got, want)
	}

	approved := base
	approved.Approved = "c1"
	want = head + "Round: 1 of 3\nApproved commit: c1\nWork directory: /work\n\nReview the pull request #21 against the implementation issue #10. This is round 1 after your approval of c1: review only the diff from that commit to the head commit, with the depth of round 1." + tail
	if got := ReviewRequestText(approved); got != want {
		t.Errorf("round 1 after an approval:\n%s\nwant:\n%s", got, want)
	}

	// Round 2 and later keep their text; only the line is new.
	approved.Round, approved.LastReviewed = 2, "c2"
	want = head + "Round: 2 of 3\nApproved commit: c1\nLast reviewed commit: c2\nWork directory: /work\n\nReview the pull request #21 again. This is round 2: check that your earlier blocking comments are fixed, in the diff from c2 to the head commit." + tail
	if got := ReviewRequestText(approved); got != want {
		t.Errorf("round 2 after an approval:\n%s\nwant:\n%s", got, want)
	}
}

// The latest review of the Reviewer is the one that the check after a run
// reads, in any state, but never a pending one.
func TestLatestReview_IsTheLastSubmittedReviewOfTheReviewer(t *testing.T) {
	const reviewer = "example-reviewer[bot]"
	at := func(minute int) time.Time { return time.Date(2026, 9, 30, 10, minute, 0, 0, time.UTC) }
	reviews := []Review{
		{Author: reviewer, State: ReviewChangesRequested, SubmittedAt: at(1), Commit: "c1"},
		{Author: reviewer, State: ReviewCommented, SubmittedAt: at(3), Commit: "c2"},
		{Author: "octocat", State: ReviewApproved, SubmittedAt: at(4), Commit: "c2"},
		{Author: reviewer, State: ReviewPending, Commit: "c2"},
	}
	got, ok := LatestReview(reviews, reviewer)
	if !ok || got.State != ReviewCommented || got.Commit != "c2" {
		t.Errorf("LatestReview = %+v, %v, want the COMMENTED review on c2", got, ok)
	}
	if _, ok := LatestReview(reviews[2:], reviewer); ok {
		t.Error("LatestReview found a review, want none: a pending review is not submitted")
	}
}

// The round comes from GitHub only: two clients that share nothing but the
// fake GitHub, as cumin before and after a restart, count the same round.
func TestReviewRounds_TheCountComesFromGitHubAndSurvivesARestart(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	t0 := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	fake.AddIssue(repo, &githubtest.Issue{Number: 6, Labels: []string{"cumin/type/requirement", "cumin/status/implementing"}})
	fake.AddIssue(repo, &githubtest.Issue{Number: 10, Parent: 6, Labels: []string{"cumin/status/reviewing", "risk/low"}, LabelEvents: []githubtest.LabelEvent{
		{Label: "cumin/status/ready", At: t0.Add(time.Minute)},
	}})
	fake.AddPullRequest(repo, &githubtest.PullRequest{Number: 20, HeadCommit: "3333333333333333333333333333333333333333", Closes: []int{10}, Reviews: []githubtest.Review{
		// Before the last cumin/status/ready: not counted.
		{Author: "example-reviewer", AuthorIsBot: true, State: "CHANGES_REQUESTED", Commit: "1111111111111111111111111111111111111111", SubmittedAt: t0},
		{Author: "example-reviewer", AuthorIsBot: true, State: "CHANGES_REQUESTED", Commit: "2222222222222222222222222222222222222222", SubmittedAt: t0.Add(2 * time.Minute)},
		{Author: "example-reviewer", AuthorIsBot: true, State: "CHANGES_REQUESTED", Commit: "3333333333333333333333333333333333333333", SubmittedAt: t0.Add(3 * time.Minute)},
	}})

	for _, run := range []string{"before the restart", "after the restart"} {
		client := github.NewAppClient(server.URL, server.Client())
		_, snapshot, err := (&Service{GitHub: client}).readSnapshot(context.Background(), githubtest.Token, "example-org", "example-repo")
		if err != nil {
			t.Fatalf("%s: readSnapshot: %v", run, err)
		}
		times, _, err := client.ReadLabelTimes(context.Background(), githubtest.Token, "example-org", "example-repo", 10)
		if err != nil {
			t.Fatalf("%s: ReadLabelTimes: %v", run, err)
		}
		sub, ok := snapshot.SubIssue(10)
		if !ok {
			t.Fatalf("%s: issue #10 is not in the snapshot", run)
		}
		pr, _ := sub.LatestPullRequest()
		readyAt := times[10][LabelReady]
		if got := ReviewRounds(pr.Reviews, "example-reviewer[bot]", readyAt); got != 2 {
			t.Errorf("%s: round = %d, want 2", run, got)
		}
		if got := LastReviewedCommit(pr.Reviews, "example-reviewer[bot]", readyAt); got != "3333333333333333333333333333333333333333" {
			t.Errorf("%s: last reviewed commit = %q", run, got)
		}
	}
}

// I8 counts only a decision request of the Reviewer that is not older than
// its last review.
func TestExplanationOf_IsANewDecisionRequestOfTheReviewer(t *testing.T) {
	const reviewer = "example-reviewer[bot]"
	since := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	decision := "## Decision needed: Which error does the handler return?\n\nType: Unresolved after 3 review rounds"
	tests := []struct {
		name    string
		comment Comment
		want    bool
	}{
		{"a decision request after the review", Comment{Author: reviewer, CreatedAt: since.Add(time.Minute), Body: decision, URL: "u"}, true},
		{"one at the same second as the review", Comment{Author: reviewer, CreatedAt: since, Body: decision}, true},
		{"one before the review", Comment{Author: reviewer, CreatedAt: since.Add(-time.Minute), Body: decision}, false},
		{"another author", Comment{Author: "octocat", CreatedAt: since.Add(time.Minute), Body: decision}, false},
		{"another heading", Comment{Author: reviewer, CreatedAt: since.Add(time.Minute), Body: "Looks fine to me."}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := ExplanationOf([]Comment{tt.comment}, reviewer, since); ok != tt.want {
				t.Errorf("found = %v, want %v", ok, tt.want)
			}
		})
	}
}
