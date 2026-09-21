package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Page sizes of the snapshot query. GitHub scores a query by the product of
// the `first` arguments along each path, divided by 100 (official: Rate
// limits and node limits for the GraphQL API). With these sizes one page
// costs 1 + 10 + 10 + 300 + 300 = 621 requests, about 6 points, against 5,000
// points per hour for one installation. The sizes are wide enough for the
// limits of the sizing policies (12 sub-issues for one requirement issue).
const (
	// Requirement issues are read in pages of this size, with a cursor.
	snapshotIssuePage = 10
	// Sub-issues, labels, and blocked-by issues are read once, up to this
	// many for one issue. More is an error.
	snapshotSubIssues = 30
	snapshotLabels    = 10
	snapshotBlockedBy = 20
)

// RepositorySnapshot is what one poll reads of one repository: the open
// requirement issues with their sub-issues, and the rate limit of the call.
// docs/ja/designs/cumin-core.md, topic "What one poll reads".
type RepositorySnapshot struct {
	RequirementIssues []Issue
	RateLimit         RateLimit
}

// Issue is one issue as the snapshot sees it. A requirement issue has
// SubIssues. A sub-issue has BlockedBy.
type Issue struct {
	Number    int
	Closed    bool
	Labels    []string
	SubIssues []Issue
	BlockedBy []IssueRef
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
// sub-issues, and the state of the blocked-by issues. The field names come
// from the design note and measured-constraints.md row 55, and were checked
// against the schema by introspection on 2026-09-21.
const snapshotQuery = `query($owner: String!, $name: String!, $first: Int!, $after: String, $subIssues: Int!, $labels: Int!, $blockedBy: Int!) {
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
            state
            labels(first: $labels) { pageInfo { hasNextPage } nodes { name } }
            blockedBy(first: $blockedBy) { pageInfo { hasNextPage } nodes { number state } }
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
	issue := Issue{Number: n.Number, Closed: n.State == "CLOSED"}
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
	return issue, errors.Join(errs...)
}
