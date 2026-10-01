package workflow_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
	"github.com/cloveclovedev/cumin-works/internal/workflow"
)

const theOwner = "the-owner"

// awaitingOwner puts issue #10 in cumin/status/awaiting-owner-review after
// I7, with risk/medium, the pull request #21 with one passed required
// check, and the Owner as an admin of the repository.
func awaitingOwner(t *testing.T) *scene {
	t.Helper()
	sc := newScene(t)
	sc.awaitingChecks(t, []string{"ci"}, []githubtest.Check{{Name: "ci", Conclusion: "SUCCESS"}})
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{
		Number: 10, Parent: 6, Title: subIssueTitle,
		Labels: []string{"risk/medium", workflow.LabelAwaitingOwnerReview},
	})
	sc.fake.SetPermission(theOwner, "admin", "User")
	return sc
}

func (sc *scene) review(author string, bot bool, state, commit string, minutesAgo int) {
	pr := sc.repo.PullRequests[21]
	pr.Reviews = append(pr.Reviews, githubtest.Review{Author: author, AuthorIsBot: bot, State: state, Commit: commit,
		SubmittedAt: time.Now().Add(-time.Duration(minutesAgo) * time.Minute)})
}

func permissionReads(sc *scene) int {
	n := 0
	for _, r := range sc.fake.Requests() {
		if r.Method == http.MethodGet && strings.HasSuffix(r.Path, "/permission") {
			n++
		}
	}
	return n
}

// I12 (issue-states.md): the Owner approves the head commit with a review,
// and cumin-core merges the pull request once, across polls, then closes
// the issue that GitHub left open.
func TestI12_AnApprovalOfTheOwnerOnTheHeadIsMergedOnce(t *testing.T) {
	sc := awaitingOwner(t)
	sc.review(implementerSlug, true, "APPROVED", sc.remoteHead, 30)
	sc.review(theOwner, false, "APPROVED", sc.remoteHead, 5)
	service := sc.service()

	for range 2 {
		sc.pollAndWait(t, service)
	}

	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 1 {
		t.Errorf("%d merge requests, want 1", n)
	}
	if got := sc.mergeMethod(t); got != "squash" {
		t.Errorf("merge method = %q, want squash", got)
	}
	if issue := sc.fake.Issue(sc.repo, 10); !issue.Closed || issue.StateReason != "completed" {
		t.Errorf("issue #10: closed %v, reason %q; want closed as completed", issue.Closed, issue.StateReason)
	}
	// Only the person is read: the bot of the Reviewer App is never an Owner.
	if n := permissionReads(sc); n != 1 {
		t.Errorf("%d permission reads, want 1 (the Owner)", n)
	}
	for _, want := range []string{`"msg":"I12: the Owner approved the head commit"`, `"msg":"I12: merged the pull request"`} {
		if !strings.Contains(sc.logs.String(), want) {
			t.Errorf("the log has no %s", want)
		}
	}
}

// Approvals that do not count are not merged.
func TestI12_ApprovalsThatDoNotCountAreNotMerged(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(sc *scene)
	}{
		{"an approval on an older commit", func(sc *scene) {
			sc.review(theOwner, false, "APPROVED", "0000000000000000000000000000000000000000", 5)
		}},
		{"an approval of a bot", func(sc *scene) {
			sc.review(implementerSlug, true, "APPROVED", sc.remoteHead, 5)
		}},
		{"an approval of an account without write permission", func(sc *scene) {
			sc.review("someone", false, "APPROVED", sc.remoteHead, 5)
		}},
		{"an approval of a bot account that has write permission", func(sc *scene) {
			sc.fake.SetPermission("writer-bot", "write", "Bot")
			sc.review("writer-bot", false, "APPROVED", sc.remoteHead, 5)
		}},
		{"an approval that the Owner took back with REQUEST_CHANGES", func(sc *scene) {
			sc.review(theOwner, false, "APPROVED", sc.remoteHead, 10)
			sc.review(theOwner, false, "CHANGES_REQUESTED", sc.remoteHead, 5)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := awaitingOwner(t)
			tc.setup(sc)
			service := sc.service()

			sc.pollAndWait(t, service)

			if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
				t.Errorf("%d merge requests, want none", n)
			}
			if got := sc.fake.Issue(sc.repo, 10).Labels; !slices.Contains(got, workflow.LabelAwaitingOwnerReview) {
				t.Errorf("labels of #10 = %v, want cumin/status/awaiting-owner-review to stay", got)
			}
		})
	}
}

// An approval of the Owner after a comment of another person, and a comment
// of the Owner after the approval, still count: comments decide nothing.
func TestI12_ACommentOfTheOwnerKeepsTheApproval(t *testing.T) {
	sc := awaitingOwner(t)
	sc.review(theOwner, false, "APPROVED", sc.remoteHead, 10)
	sc.review(theOwner, false, "COMMENTED", sc.remoteHead, 5)
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 1 {
		t.Errorf("%d merge requests, want 1", n)
	}
}

// Without an approval of a person on the head commit, cumin reads no
// permission and no required checks.
func TestI12_ThePermissionIsReadOnlyForACandidate(t *testing.T) {
	sc := awaitingOwner(t)
	sc.review(theOwner, false, "CHANGES_REQUESTED", sc.remoteHead, 5)
	service := sc.service()

	if err := service.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	service.Wait()

	if n := permissionReads(sc); n != 0 {
		t.Errorf("%d permission reads, want none", n)
	}
	if n := sc.fake.CountRequests(http.MethodGet, branchRulesPath); n != 0 {
		t.Errorf("%d reads of the required checks, want none", n)
	}
}

// The Owner approved, but a required check does not pass on the head
// commit: cumin waits.
func TestI12_TheMergeWaitsForTheRequiredChecks(t *testing.T) {
	sc := awaitingOwner(t)
	sc.repo.PullRequests[21].Checks = []githubtest.Check{{Name: "ci", Status: "IN_PROGRESS"}}
	sc.review(theOwner, false, "APPROVED", sc.remoteHead, 5)
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
		t.Errorf("%d merge requests, want none", n)
	}
	if !strings.Contains(sc.logs.String(), `"msg":"I12: the Owner approved; the merge waits for the required checks"`) {
		t.Error("the log does not say that the merge waits")
	}
}

// The risk label is read from the issue at I12 too: not exactly one stops
// the issue with the row I12.
func TestI12_AnIssueWithoutOneRiskLabelIsStopped(t *testing.T) {
	sc := awaitingOwner(t)
	sc.fake.AddIssue(sc.repo, &githubtest.Issue{
		Number: 10, Parent: 6, Title: subIssueTitle,
		Labels: []string{"risk/medium", "risk/high", workflow.LabelAwaitingOwnerReview},
	})
	sc.review(theOwner, false, "APPROVED", sc.remoteHead, 5)
	service := sc.service()

	sc.pollAndWait(t, service)

	if n := sc.fake.CountRequests(http.MethodPut, mergePath); n != 0 {
		t.Errorf("%d merge requests, want none", n)
	}
	comments := sc.fake.Comments(sc.repo, 10)
	if len(comments) != 1 || !strings.Contains(comments[0].Body, "Row: I12") ||
		!strings.Contains(comments[0].Body, workflow.RiskLabelReason(workflow.MergeTwoRiskLabels)) {
		t.Errorf("comments of #10 = %+v, want one stop note of I12", comments)
	}
}

func TestOwnerApproved_I12(t *testing.T) {
	const head = "2222222222222222222222222222222222222222"
	at := func(minutes int) time.Time { return time.Date(2026, 10, 1, 10, minutes, 0, 0, time.UTC) }
	owners := map[string]bool{"owner": true, "owner-two": true, "app[bot]": true}
	for _, tc := range []struct {
		name    string
		reviews []workflow.Review
		want    bool
	}{
		{"approved on the head", []workflow.Review{{Author: "owner", State: workflow.ReviewApproved, Commit: head, SubmittedAt: at(1)}}, true},
		{"approved on an older commit", []workflow.Review{{Author: "owner", State: workflow.ReviewApproved, Commit: "old", SubmittedAt: at(1)}}, false},
		{"a later change request of another Owner", []workflow.Review{
			{Author: "owner", State: workflow.ReviewApproved, Commit: head, SubmittedAt: at(1)},
			{Author: "owner-two", State: workflow.ReviewChangesRequested, Commit: head, SubmittedAt: at(2)}}, false},
		{"a later approval after a change request", []workflow.Review{
			{Author: "owner", State: workflow.ReviewChangesRequested, Commit: head, SubmittedAt: at(1)},
			{Author: "owner", State: workflow.ReviewApproved, Commit: head, SubmittedAt: at(2)}}, true},
		{"a later comment does not count", []workflow.Review{
			{Author: "owner", State: workflow.ReviewApproved, Commit: head, SubmittedAt: at(1)},
			{Author: "owner", State: workflow.ReviewCommented, Commit: head, SubmittedAt: at(2)}}, true},
		{"a person who is not an Owner", []workflow.Review{{Author: "someone", State: workflow.ReviewApproved, Commit: head, SubmittedAt: at(1)}}, false},
		{"a bot never counts", []workflow.Review{{Author: "app[bot]", State: workflow.ReviewApproved, Commit: head, SubmittedAt: at(1)}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := workflow.OwnerApproved(tc.reviews, head, owners); got != tc.want {
				t.Errorf("OwnerApproved = %v, want %v", got, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		permission, userType string
		want                 bool
	}{{"admin", "User", true}, {"write", "User", true}, {"read", "User", false}, {"none", "User", false}, {"write", "Bot", false}, {"admin", "Bot", false}} {
		if got := workflow.IsOwner(tc.permission, tc.userType); got != tc.want {
			t.Errorf("IsOwner(%q, %q) = %v, want %v", tc.permission, tc.userType, got, tc.want)
		}
	}
}
