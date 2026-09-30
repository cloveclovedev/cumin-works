package github

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// closerPageSize is the page size of the review threads of a pull request
// and of the comments of one thread. A connection over it is an error, so
// that I9 never lists a part of the comments as all of them.
const closerPageSize = 100

// linkedLimit is the page size of the linked pull requests of one issue.
// More is an error, so that I9 never misses a merged one.
const linkedLimit = 10

// The query of the pull requests that are linked to close one issue, open,
// closed, and merged (I9). Who closed the issue does not matter: since
// 2026-09-30 GitHub may not close an issue at the merge, and cumin or the
// Owner closes it then (docs/ja/designs/poll.md, the topic on the
// follow-up notes).
//
// Official, GraphQL schema: Issue.closedByPullRequestsReferences with
// includeClosedPrs. Checked on 2026-09-30; one query cost 1 point.
const linkedQuery = `query($owner: String!, $name: String!, $number: Int!, $linked: Int!) {
  repository(owner: $owner, name: $name) {
    issue(number: $number) {
      closedByPullRequestsReferences(first: $linked, includeClosedPrs: true) {
        pageInfo { hasNextPage }
        nodes { number merged }
      }
    }
  }
  rateLimit { cost remaining }
}`

// The query of what the follow-up note copies from one pull request: the
// description and the review threads. Official, GraphQL schema:
// PullRequest.reviewThreads and PullRequestReviewThread.comments are
// connections. Checked by introspection on 2026-09-30; one query cost 1
// point.
const pullRequestNoteQuery = `query($owner: String!, $name: String!, $number: Int!, $threads: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      number
      merged
      body
      reviewThreads(first: $threads) {
        pageInfo { hasNextPage }
        nodes {
          path
          line
          originalLine
          comments(first: $threads) {
            pageInfo { hasNextPage }
            nodes { body url author { __typename login } }
          }
        }
      }
    }
  }
  rateLimit { cost remaining }
}`

// LinkedPullRequest is one pull request that is linked to close an issue.
type LinkedPullRequest struct {
	Number int
	Merged bool
}

// ReadLinkedPullRequests reads the pull requests that are linked to close
// an issue, whatever their state.
func (c *AppClient) ReadLinkedPullRequests(ctx context.Context, token, owner, repo string, number int) ([]LinkedPullRequest, RateLimit, error) {
	fail := func(err error) ([]LinkedPullRequest, RateLimit, error) {
		return nil, RateLimit{}, fmt.Errorf("github: read the linked pull requests of %s/%s#%d: %w", owner, repo, number, err)
	}
	var resp linkedResponse
	request := map[string]any{"query": linkedQuery, "variables": map[string]any{"owner": owner, "name": repo, "number": number, "linked": linkedLimit}}
	if err := c.do(ctx, token, http.MethodPost, "/graphql", "/graphql", request, http.StatusOK, &resp); err != nil {
		return fail(err)
	}
	if len(resp.Errors) > 0 {
		return fail(graphQLErrors(resp.Errors))
	}
	rate := RateLimit{Cost: resp.Data.RateLimit.Cost, Remaining: resp.Data.RateLimit.Remaining}
	if resp.Data.Repository == nil || resp.Data.Repository.Issue == nil {
		return fail(fmt.Errorf("the response has no issue"))
	}
	refs := resp.Data.Repository.Issue.ClosedByPullRequestsReferences
	if refs.PageInfo.HasNextPage {
		return fail(fmt.Errorf("more than %d linked pull requests", linkedLimit))
	}
	linked := make([]LinkedPullRequest, 0, len(refs.Nodes))
	for _, n := range refs.Nodes {
		linked = append(linked, LinkedPullRequest{Number: n.Number, Merged: n.Merged})
	}
	return linked, rate, nil
}

// ClosingPullRequest is a pull request that is linked to close an issue,
// as much as the follow-up note needs.
type ClosingPullRequest struct {
	Number int
	Merged bool
	// Body is the description of the pull request.
	Body    string
	Threads []ReviewThread
}

// ReviewThread is one thread of review comments on a line of a pull
// request, first comment first.
type ReviewThread struct {
	Path string
	// Line is the line of the thread in the file now, or the line that it
	// was written on when the code has moved since. 0 when neither is set.
	Line     int
	Comments []ReviewComment
}

// ReviewComment is one comment of a review thread.
type ReviewComment struct {
	// Author is the login as the REST API shows it: a GitHub App is
	// "<slug>[bot]". Empty when the author is gone.
	Author string
	Body   string
	URL    string
}

// ReadPullRequestNote reads the description and the review threads of one
// pull request, as much as the follow-up note needs.
func (c *AppClient) ReadPullRequestNote(ctx context.Context, token, owner, repo string, number int) (*ClosingPullRequest, RateLimit, error) {
	variables := map[string]any{"owner": owner, "name": repo, "number": number, "threads": closerPageSize}
	var resp pullRequestNoteResponse
	request := map[string]any{"query": pullRequestNoteQuery, "variables": variables}
	fail := func(err error) (*ClosingPullRequest, RateLimit, error) {
		return nil, RateLimit{}, fmt.Errorf("github: read pull request %s/%s#%d: %w", owner, repo, number, err)
	}
	if err := c.do(ctx, token, http.MethodPost, "/graphql", "/graphql", request, http.StatusOK, &resp); err != nil {
		return fail(err)
	}
	if len(resp.Errors) > 0 {
		return fail(graphQLErrors(resp.Errors))
	}
	rate := RateLimit{Cost: resp.Data.RateLimit.Cost, Remaining: resp.Data.RateLimit.Remaining}
	if resp.Data.Repository == nil || resp.Data.Repository.PullRequest == nil {
		return fail(fmt.Errorf("the response has no pull request"))
	}
	closer := resp.Data.Repository.PullRequest
	if closer.ReviewThreads.PageInfo.HasNextPage {
		return fail(fmt.Errorf("more than %d review threads", closerPageSize))
	}
	pr := &ClosingPullRequest{Number: closer.Number, Merged: closer.Merged, Body: closer.Body}
	for _, t := range closer.ReviewThreads.Nodes {
		if t.Comments.PageInfo.HasNextPage {
			return fail(fmt.Errorf("a review thread has more than %d comments", closerPageSize))
		}
		thread := ReviewThread{Path: t.Path}
		switch {
		case t.Line != nil:
			thread.Line = *t.Line
		case t.OriginalLine != nil:
			thread.Line = *t.OriginalLine
		}
		for _, n := range t.Comments.Nodes {
			comment := ReviewComment{Body: n.Body, URL: n.URL}
			if n.Author != nil {
				comment.Author = restLogin(n.Author.TypeName, n.Author.Login)
			}
			thread.Comments = append(thread.Comments, comment)
		}
		pr.Threads = append(pr.Threads, thread)
	}
	return pr, rate, nil
}

// graphQLErrors joins the messages of a GraphQL response.
func graphQLErrors(errs []struct {
	Message string `json:"message"`
}) error {
	var messages []string
	for _, e := range errs {
		messages = append(messages, e.Message)
	}
	return fmt.Errorf("%s", strings.Join(messages, "; "))
}

// The GraphQL responses. They stop in this package.
type linkedResponse struct {
	Data struct {
		Repository *struct {
			Issue *struct {
				ClosedByPullRequestsReferences struct {
					PageInfo struct {
						HasNextPage bool `json:"hasNextPage"`
					} `json:"pageInfo"`
					Nodes []struct {
						Number int  `json:"number"`
						Merged bool `json:"merged"`
					} `json:"nodes"`
				} `json:"closedByPullRequestsReferences"`
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

type pullRequestNoteResponse struct {
	Data struct {
		Repository *struct {
			PullRequest *struct {
				Number        int    `json:"number"`
				Merged        bool   `json:"merged"`
				Body          string `json:"body"`
				ReviewThreads struct {
					PageInfo struct {
						HasNextPage bool `json:"hasNextPage"`
					} `json:"pageInfo"`
					Nodes []struct {
						Path         string `json:"path"`
						Line         *int   `json:"line"`
						OriginalLine *int   `json:"originalLine"`
						Comments     struct {
							PageInfo struct {
								HasNextPage bool `json:"hasNextPage"`
							} `json:"pageInfo"`
							Nodes []struct {
								Body   string `json:"body"`
								URL    string `json:"url"`
								Author *struct {
									TypeName string `json:"__typename"`
									Login    string `json:"login"`
								} `json:"author"`
							} `json:"nodes"`
						} `json:"comments"`
					} `json:"nodes"`
				} `json:"reviewThreads"`
			} `json:"pullRequest"`
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
