package workflow

// This file converts the types of the GitHub client (internal/platform/github)
// to the types of the pure rules (domain.go). The types of the client stop
// here and in the files that call the client.

import (
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
)

// toComments converts the comments that the REST call read. The types of the
// platform package stop here.
func toComments(read []github.Comment) []Comment {
	comments := make([]Comment, 0, len(read))
	for _, c := range read {
		comments = append(comments, Comment{Author: c.Author, CreatedAt: c.CreatedAt, Body: c.Body, URL: c.URL})
	}
	return comments
}

// toRequiredChecks converts the required checks that the REST call read.
// The types of the platform package stop here.
func toRequiredChecks(read []github.RequiredCheck) []RequiredCheck {
	checks := make([]RequiredCheck, 0, len(read))
	for _, check := range read {
		checks = append(checks, RequiredCheck{Name: check.Name, Integration: check.Integration})
	}
	return checks
}

// toChecks converts the checks of one pull request.
func toChecks(read []github.CheckResult) []CheckResult {
	checks := make([]CheckResult, 0, len(read))
	for _, check := range read {
		checks = append(checks, CheckResult{
			Name:        check.Name,
			Conclusion:  toConclusion(check.Conclusion),
			Integration: check.Integration,
		})
	}
	return checks
}

// toReviews converts the reviews of one pull request.
func toReviews(read []github.Review) []Review {
	var reviews []Review
	for _, r := range read {
		reviews = append(reviews, Review{Author: r.Author, State: ReviewState(r.State), Commit: r.Commit, SubmittedAt: r.SubmittedAt, URL: r.URL})
	}
	return reviews
}

// toConclusion folds the conclusion of the client into the one of the rules.
func toConclusion(c github.CheckConclusion) CheckConclusion {
	switch c {
	case github.CheckPassed:
		return CheckPassed
	case github.CheckFailed:
		return CheckFailed
	}
	return CheckPending
}

// toSnapshot converts what the GitHub client read to the snapshot of the
// rules. The types of the platform package stop here.
func toSnapshot(read github.RepositorySnapshot) Snapshot {
	snapshot := Snapshot{DefaultBranch: read.DefaultBranch}
	for _, issue := range read.RequirementIssues {
		snapshot.RequirementIssues = append(snapshot.RequirementIssues, toRequirementIssue(issue))
	}
	for _, issue := range read.UnreadRequirementIssues {
		snapshot.Unread = append(snapshot.Unread, toRequirementIssue(issue))
	}
	return snapshot
}

// toRequirementIssue converts one requirement issue of the GitHub client,
// from the poll or from the read of one issue.
func toRequirementIssue(issue github.Issue) RequirementIssue {
	requirement := RequirementIssue{Number: issue.Number, Title: issue.Title, Labels: issue.Labels, LabelsUnread: issue.LabelsOverLimit}
	for _, blocker := range issue.BlockedBy {
		requirement.BlockedBy = append(requirement.BlockedBy, BlockedBy{Number: blocker.Number, Closed: blocker.Closed})
	}
	for _, sub := range issue.SubIssues {
		requirement.SubIssues = append(requirement.SubIssues, toSubIssue(sub))
	}
	return requirement
}

// toSubIssue converts one sub-issue of the GitHub client, from the poll or
// from the read of one issue.
func toSubIssue(sub github.Issue) SubIssue {
	subIssue := SubIssue{Number: sub.Number, NodeID: sub.NodeID, Title: sub.Title, Closed: sub.Closed, ClosedAt: sub.ClosedAt, Labels: sub.Labels, LabelsUnread: sub.LabelsOverLimit}
	for _, blocker := range sub.BlockedBy {
		subIssue.BlockedBy = append(subIssue.BlockedBy, BlockedBy{Number: blocker.Number, Closed: blocker.Closed})
	}
	subIssue.PullRequests = toPullRequests(sub.PullRequests)
	return subIssue
}

// toPullRequests converts the pull requests of one sub-issue of the GitHub
// client, from the second query of the poll or from the read of one issue.
func toPullRequests(read []github.PullRequest) []PullRequest {
	var pullRequests []PullRequest
	for _, pr := range read {
		pullRequests = append(pullRequests, PullRequest{
			Number:     pr.Number,
			HeadCommit: pr.HeadCommit,
			HeadBranch: pr.HeadBranch,
			Author:     pr.Author,
			Labels:     pr.Labels,
			Checks:     toChecks(pr.Checks),
			Reviews:    toReviews(pr.Reviews),
			// The three values of GitHub pass as they are; the
			// client refuses any other value.
			Mergeable:       MergeableState(pr.Mergeable),
			HeadCommittedAt: pr.HeadCommittedAt,
		})
	}
	return pullRequests
}
