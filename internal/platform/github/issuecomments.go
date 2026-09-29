package github

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// issueCommentsLast is how many of the newest comments of a requirement
// issue R4 and R7 read. The acceptance check comment is written after the
// last sub-issue closed, so it is among the newest; the page stays small.
const issueCommentsLast = 50

// The query of the newest comments of one issue. It runs only for a
// requirement issue whose sub-issues are all closed (R4, R7), so the poll
// query keeps its cost (docs/ja/designs/poll.md, the topic on the comments
// of a requirement issue).
const issueCommentsQuery = `query($owner: String!, $name: String!, $number: Int!, $last: Int!) {
  repository(owner: $owner, name: $name) {
    issue(number: $number) {
      comments(last: $last) {
        nodes { createdAt body author { __typename login } }
      }
    }
  }
  rateLimit { cost remaining }
}`

// RequirementComment is one comment of a requirement issue, as much as R4
// and R7 need.
type RequirementComment struct {
	// Author is the login as the REST API shows it: a GitHub App is
	// "<slug>[bot]". Empty when the author is gone.
	Author    string
	CreatedAt time.Time
	Body      string
}

// ReadIssueComments reads the newest comments of an issue, oldest first.
func (c *AppClient) ReadIssueComments(ctx context.Context, token, owner, repo string, number int) ([]RequirementComment, RateLimit, error) {
	variables := map[string]any{"owner": owner, "name": repo, "number": number, "last": issueCommentsLast}
	var resp issueCommentsResponse
	request := map[string]any{"query": issueCommentsQuery, "variables": variables}
	if err := c.do(ctx, token, http.MethodPost, "/graphql", "/graphql", request, http.StatusOK, &resp); err != nil {
		return nil, RateLimit{}, fmt.Errorf("github: read the comments of %s/%s#%d: %w", owner, repo, number, err)
	}
	if len(resp.Errors) > 0 {
		var messages []string
		for _, e := range resp.Errors {
			messages = append(messages, e.Message)
		}
		return nil, RateLimit{}, fmt.Errorf("github: read the comments of %s/%s#%d: %s", owner, repo, number, strings.Join(messages, "; "))
	}
	rate := RateLimit{Cost: resp.Data.RateLimit.Cost, Remaining: resp.Data.RateLimit.Remaining}
	if resp.Data.Repository == nil || resp.Data.Repository.Issue == nil {
		return nil, rate, fmt.Errorf("github: read the comments of %s/%s#%d: the response has no issue", owner, repo, number)
	}
	var comments []RequirementComment
	for _, node := range resp.Data.Repository.Issue.Comments.Nodes {
		comment := RequirementComment{CreatedAt: node.CreatedAt, Body: node.Body}
		if node.Author != nil {
			comment.Author = restLogin(node.Author.TypeName, node.Author.Login)
		}
		comments = append(comments, comment)
	}
	return comments, rate, nil
}

// The GraphQL response. It stops in this package.
type issueCommentsResponse struct {
	Data struct {
		Repository *struct {
			Issue *struct {
				Comments struct {
					Nodes []struct {
						CreatedAt time.Time `json:"createdAt"`
						Body      string    `json:"body"`
						Author    *struct {
							TypeName string `json:"__typename"`
							Login    string `json:"login"`
						} `json:"author"`
					} `json:"nodes"`
				} `json:"comments"`
			} `json:"issue"`
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
