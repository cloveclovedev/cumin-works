package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Page sizes of the snapshot query. GitHub scores a query by the `first`
// arguments along each path (official: Rate limits and node limits for the
// GraphQL API). With these sizes one page cost 9 points on the sandbox on
// 2026-09-22 (6 points before the pull requests were read), against 5,000
// points per hour for one installation. The sizes are wide enough for the
// limits of the sizing policies (12 sub-issues for one requirement issue).
const (
	// Requirement issues are read in pages of this size, with a cursor.
	snapshotIssuePage = 10
	// Sub-issues, labels, blocked-by issues, and closing pull requests are
	// read once, up to this many for one issue. More is an error.
	snapshotSubIssues    = 30
	snapshotLabels       = 10
	snapshotBlockedBy    = 20
	snapshotPullRequests = 5
)

// RepositorySnapshot is what one poll reads of one repository: the open
// requirement issues with their sub-issues, and the rate limit of the call.
// docs/ja/designs/cumin-core.md, topic "What one poll reads".
type RepositorySnapshot struct {
	RequirementIssues []Issue
	RateLimit         RateLimit
}

// Issue is one issue as the snapshot sees it. A requirement issue has
// SubIssues. A sub-issue has Title, BlockedBy, and PullRequests.
type Issue struct {
	Number    int
	Closed    bool
	Labels    []string
	SubIssues []Issue
	// Title is read for sub-issues only; the branch name of a request is
	// made from it.
	Title     string
	BlockedBy []IssueRef
	// PullRequests are the pull requests that close the sub-issue (the
	// link that "Closes #N" makes), open, closed, and merged.
	PullRequests []PullRequest
}

// PullRequest is a pull request that closes an issue, as much of it as the
// rules need.
type PullRequest struct {
	Number int
	// Closed is true for a closed and for a merged pull request.
	Closed bool
	Merged bool
	// HeadCommit is the full SHA of the head of the pull request.
	HeadCommit string
	// Author is the login of the author as the REST API shows it: a GitHub
	// App is "<slug>[bot]", the form of the identity that an agent commits
	// with. GraphQL gives the login of a Bot without "[bot]" (measured on
	// the sandbox on 2026-09-22), so it is added here. Empty when the
	// author is gone (a deleted account).
	Author string
}

// IssueRef is an issue that another issue points to: only its number and
// whether it is closed.
type IssueRef struct {
	Number int
	Closed bool
}

// RateLimit is the cost of one call and what is left of the hourly points of
// the installation. The caller logs it.
type RateLimit struct {
	Cost      int
	Remaining int
}

// snapshotQuery reads the open issues with the requirement label, their
// sub-issues with the title, the state of the blocked-by issues, and the
// pull requests that close each sub-issue. The field names come from the
// design note and measured-constraints.md row 55, and were checked against
// the schema by introspection on 2026-09-21 and on the sandbox on
// 2026-09-22.
const snapshotQuery = `query($owner: String!, $name: String!, $first: Int!, $after: String, $subIssues: Int!, $labels: Int!, $blockedBy: Int!, $pullRequests: Int!) {
  repository(owner: $owner, name: $name) {
    issues(states: [OPEN], labels: ["cumin/type/requirement"], first: $first, after: $after) {
      pageInfo { hasNextPage endCursor }
      nodes {
        number
        state
        labels(first: $labels) { pageInfo { hasNextPage } nodes { name } }
        subIssues(first: $subIssues) {
          pageInfo { hasNextPage }
          nodes {
            number
            title
            state
            labels(first: $labels) { pageInfo { hasNextPage } nodes { name } }
            blockedBy(first: $blockedBy) { pageInfo { hasNextPage } nodes { number state } }
            closedByPullRequestsReferences(includeClosedPrs: true, first: $pullRequests) {
              pageInfo { hasNextPage }
              nodes { number state merged headRefOid author { __typename login } }
            }
          }
        }
      }
    }
  }
  rateLimit { cost remaining }
}`

// The GraphQL response. It stops in this package.
type snapshotResponse struct {
	Data struct {
		Repository *struct {
			Issues struct {
				PageInfo pageInfo    `json:"pageInfo"`
				Nodes    []issueNode `json:"nodes"`
			} `json:"issues"`
		} `json:"repository"`
		RateLimit struct {
			Cost      int `json:"cost"`
			Remaining int `json:"remaining"`
		} `json:"rateLimit"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type pageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type issueNode struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	State  string `json:"state"`
	Labels struct {
		PageInfo pageInfo `json:"pageInfo"`
		Nodes    []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	SubIssues struct {
		PageInfo pageInfo    `json:"pageInfo"`
		Nodes    []issueNode `json:"nodes"`
	} `json:"subIssues"`
	BlockedBy struct {
		PageInfo pageInfo `json:"pageInfo"`
		Nodes    []struct {
			Number int    `json:"number"`
			State  string `json:"state"`
		} `json:"nodes"`
	} `json:"blockedBy"`
	PullRequests struct {
		PageInfo pageInfo          `json:"pageInfo"`
		Nodes    []pullRequestNode `json:"nodes"`
	} `json:"closedByPullRequestsReferences"`
}

type pullRequestNode struct {
	Number     int    `json:"number"`
	State      string `json:"state"`
	Merged     bool   `json:"merged"`
	HeadRefOid string `json:"headRefOid"`
	Author     *struct {
		TypeName string `json:"__typename"`
		Login    string `json:"login"`
	} `json:"author"`
}

// pullRequest converts one node. GitHub gives the state OPEN, CLOSED, or
// MERGED (the schema: PullRequestState).
func (n pullRequestNode) pullRequest() (PullRequest, error) {
	if n.State != "OPEN" && n.State != "CLOSED" && n.State != "MERGED" {
		return PullRequest{}, fmt.Errorf("pull request #%d has the unknown state %q", n.Number, n.State)
	}
	pr := PullRequest{Number: n.Number, Closed: n.State != "OPEN", Merged: n.Merged, HeadCommit: n.HeadRefOid}
	if n.Author != nil {
		pr.Author = n.Author.Login
		if n.Author.TypeName == "Bot" {
			pr.Author += "[bot]"
		}
	}
	return pr, nil
}

// ReadSnapshot reads the snapshot of one repository with the installation
// token: one GraphQL query for each page of requirement issues. Closed
// requirement issues are not read (issue-states.md, principle 6).
func (c *AppClient) ReadSnapshot(ctx context.Context, token, owner, repo string) (RepositorySnapshot, error) {
	var snapshot RepositorySnapshot
	var after *string
	for {
		variables := map[string]any{
			"owner": owner, "name": repo, "first": snapshotIssuePage, "after": after,
			"subIssues": snapshotSubIssues, "labels": snapshotLabels, "blockedBy": snapshotBlockedBy,
			"pullRequests": snapshotPullRequests,
		}
		var resp snapshotResponse
		request := map[string]any{"query": snapshotQuery, "variables": variables}
		if err := c.do(ctx, token, http.MethodPost, "/graphql", "/graphql", request, http.StatusOK, &resp); err != nil {
			return RepositorySnapshot{}, fmt.Errorf("github: read the snapshot of %s/%s: %w", owner, repo, err)
		}
		if len(resp.Errors) > 0 {
			var messages []string
			for _, e := range resp.Errors {
				messages = append(messages, e.Message)
			}
			return RepositorySnapshot{}, fmt.Errorf("github: read the snapshot of %s/%s: %s", owner, repo, strings.Join(messages, "; "))
		}
		if resp.Data.Repository == nil {
			return RepositorySnapshot{}, fmt.Errorf("github: read the snapshot of %s/%s: the response has no repository", owner, repo)
		}
		// The last page reports the cost of the whole read only for itself;
		// sum the cost, and keep the remaining points of the last call.
		snapshot.RateLimit.Cost += resp.Data.RateLimit.Cost
		snapshot.RateLimit.Remaining = resp.Data.RateLimit.Remaining
		for _, node := range resp.Data.Repository.Issues.Nodes {
			issue, err := node.issue()
			if err != nil {
				return RepositorySnapshot{}, fmt.Errorf("github: read the snapshot of %s/%s: %w", owner, repo, err)
			}
			snapshot.RequirementIssues = append(snapshot.RequirementIssues, issue)
		}
		page := resp.Data.Repository.Issues.PageInfo
		if !page.HasNextPage {
			return snapshot, nil
		}
		cursor := page.EndCursor
		after = &cursor
	}
}

// issue converts one node. A connection with more nodes than the page size
// is an error, so that a rule never decides on a partial issue.
func (n issueNode) issue() (Issue, error) {
	if n.Labels.PageInfo.HasNextPage {
		return Issue{}, fmt.Errorf("issue #%d has more than %d labels", n.Number, snapshotLabels)
	}
	if n.SubIssues.PageInfo.HasNextPage {
		return Issue{}, fmt.Errorf("issue #%d has more than %d sub-issues", n.Number, snapshotSubIssues)
	}
	if n.BlockedBy.PageInfo.HasNextPage {
		return Issue{}, fmt.Errorf("issue #%d has more than %d blocked-by issues", n.Number, snapshotBlockedBy)
	}
	if n.PullRequests.PageInfo.HasNextPage {
		return Issue{}, fmt.Errorf("issue #%d has more than %d closing pull requests", n.Number, snapshotPullRequests)
	}
	issue := Issue{Number: n.Number, Title: n.Title, Closed: n.State == "CLOSED"}
	if n.State != "OPEN" && n.State != "CLOSED" {
		return Issue{}, fmt.Errorf("issue #%d has the unknown state %q", n.Number, n.State)
	}
	for _, label := range n.Labels.Nodes {
		issue.Labels = append(issue.Labels, label.Name)
	}
	var errs []error
	for _, sub := range n.SubIssues.Nodes {
		subIssue, err := sub.issue()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		issue.SubIssues = append(issue.SubIssues, subIssue)
	}
	for _, blocker := range n.BlockedBy.Nodes {
		issue.BlockedBy = append(issue.BlockedBy, IssueRef{Number: blocker.Number, Closed: blocker.State == "CLOSED"})
	}
	for _, node := range n.PullRequests.Nodes {
		pr, err := node.pullRequest()
		if err != nil {
			errs = append(errs, fmt.Errorf("issue #%d: %w", n.Number, err))
			continue
		}
		issue.PullRequests = append(issue.PullRequests, pr)
	}
	return issue, errors.Join(errs...)
}
