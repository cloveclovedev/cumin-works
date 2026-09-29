package github

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// issueCommentsLast is the page size of the comments of an issue or a pull
// request. The comment that cumin looks for is among the newest (the
// acceptance check after the last sub-issue closed, the decision request of
// I8 after the last review), and one page is the usual read.
const issueCommentsLast = 50

// The query of the newest comments of one issue or pull request. It runs
// only for a requirement issue whose sub-issues are all closed (R4, R7), and
// after a Reviewer run that explained the cause (I8), so the poll query
// keeps its cost (docs/ja/designs/poll.md, the topic on the comments of a
// requirement issue). issueOrPullRequest answers for both; issue(number:)
// does not resolve the number of a pull request (checked on 2026-09-30 on
// cumin-works: NOT_FOUND). The cost stays 1 point.
const issueCommentsQuery = `query($owner: String!, $name: String!, $number: Int!, $last: Int!, $before: String) {
  repository(owner: $owner, name: $name) {
    issueOrPullRequest(number: $number) {
      ... on Issue { comments(last: $last, before: $before) { ...commentPage } }
      ... on PullRequest { comments(last: $last, before: $before) { ...commentPage } }
    }
  }
  rateLimit { cost remaining }
}

fragment commentPage on IssueCommentConnection {
  pageInfo { hasPreviousPage startCursor }
  nodes { createdAt body url author { __typename login } }
}`

// Comment is one comment of an issue or a pull request, as much as R4, R7,
// and I8 need.
type Comment struct {
	// Author is the login as the REST API shows it: a GitHub App is
	// "<slug>[bot]". Empty when the author is gone.
	Author    string
	CreatedAt time.Time
	Body      string
	// URL is the address of the comment, for a notification.
	URL string
}

// ReadIssueComments reads the comments of an issue or a pull request that
// were written after since, newest page first, and returns them oldest
// first. It stops at the first page that reaches since, so every comment
// after since is read
// however many there are: an acceptance check comment that the Planner edits
// keeps its place and its time, and must not fall out of a fixed window.
func (c *AppClient) ReadIssueComments(ctx context.Context, token, owner, repo string, number int, since time.Time) ([]Comment, RateLimit, error) {
	var comments []Comment
	var rate RateLimit
	var before *string
	for {
		variables := map[string]any{"owner": owner, "name": repo, "number": number, "last": issueCommentsLast, "before": before}
		var resp issueCommentsResponse
		request := map[string]any{"query": issueCommentsQuery, "variables": variables}
		if err := c.do(ctx, token, http.MethodPost, "/graphql", "/graphql", request, http.StatusOK, &resp); err != nil {
			return nil, rate, fmt.Errorf("github: read the comments of %s/%s#%d: %w", owner, repo, number, err)
		}
		if len(resp.Errors) > 0 {
			var messages []string
			for _, e := range resp.Errors {
				messages = append(messages, e.Message)
			}
			return nil, rate, fmt.Errorf("github: read the comments of %s/%s#%d: %s", owner, repo, number, strings.Join(messages, "; "))
		}
		rate.Cost += resp.Data.RateLimit.Cost
		rate.Remaining = resp.Data.RateLimit.Remaining
		if resp.Data.Repository == nil || resp.Data.Repository.Issue == nil {
			return nil, rate, fmt.Errorf("github: read the comments of %s/%s#%d: the response has no issue or pull request", owner, repo, number)
		}
		page := resp.Data.Repository.Issue.Comments
		var read []Comment
		reached := false
		for _, node := range page.Nodes {
			// A comment at the same second as since is kept: the times of
			// GitHub have a resolution of one second and cannot order two
			// events inside it.
			if node.CreatedAt.Before(since) {
				reached = true
				continue
			}
			comment := Comment{CreatedAt: node.CreatedAt, Body: node.Body, URL: node.URL}
			if node.Author != nil {
				comment.Author = restLogin(node.Author.TypeName, node.Author.Login)
			}
			read = append(read, comment)
		}
		comments = append(read, comments...)
		if reached || !page.PageInfo.HasPreviousPage {
			return comments, rate, nil
		}
		cursor := page.PageInfo.StartCursor
		before = &cursor
	}
}

// The GraphQL response. It stops in this package.
type issueCommentsResponse struct {
	Data struct {
		Repository *struct {
			Issue *struct {
				Comments struct {
					PageInfo struct {
						HasPreviousPage bool   `json:"hasPreviousPage"`
						StartCursor     string `json:"startCursor"`
					} `json:"pageInfo"`
					Nodes []struct {
						CreatedAt time.Time `json:"createdAt"`
						Body      string    `json:"body"`
						URL       string    `json:"url"`
						Author    *struct {
							TypeName string `json:"__typename"`
							Login    string `json:"login"`
						} `json:"author"`
					} `json:"nodes"`
				} `json:"comments"`
			} `json:"issueOrPullRequest"`
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
