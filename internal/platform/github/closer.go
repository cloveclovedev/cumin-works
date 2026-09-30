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

// The query of the pull request that closed one issue, with what the
// follow-up note copies (I9): the description and the review threads. It
// runs only for a closed sub-issue of an open requirement issue that has no
// note yet (docs/ja/designs/poll.md, the topic on the follow-up notes).
//
// Official, GraphQL schema: ClosedEvent.closer is the union Closer
// (Commit, ProjectV2, PullRequest); PullRequest.reviewThreads and
// PullRequestReviewThread.comments are connections. Checked by
// introspection on 2026-09-30; one query cost 1 point.
const closerQuery = `query($owner: String!, $name: String!, $number: Int!, $threads: Int!) {
  repository(owner: $owner, name: $name) {
    issue(number: $number) {
      timelineItems(itemTypes: [CLOSED_EVENT], last: 1) {
        nodes {
          ... on ClosedEvent {
            closer {
              __typename
              ... on PullRequest {
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
          }
        }
      }
    }
  }
  rateLimit { cost remaining }
}`

// ClosingPullRequest is the pull request that closed an issue, as much as
// the follow-up note needs.
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

// ReadClosingPullRequest reads the pull request that closed an issue last.
// It returns nil when the issue is open, or when a commit, a project, or a
// person closed it.
func (c *AppClient) ReadClosingPullRequest(ctx context.Context, token, owner, repo string, number int) (*ClosingPullRequest, RateLimit, error) {
	variables := map[string]any{"owner": owner, "name": repo, "number": number, "threads": closerPageSize}
	var resp closerResponse
	request := map[string]any{"query": closerQuery, "variables": variables}
	fail := func(err error) (*ClosingPullRequest, RateLimit, error) {
		return nil, RateLimit{}, fmt.Errorf("github: read the pull request that closed %s/%s#%d: %w", owner, repo, number, err)
	}
	if err := c.do(ctx, token, http.MethodPost, "/graphql", "/graphql", request, http.StatusOK, &resp); err != nil {
		return fail(err)
	}
	if len(resp.Errors) > 0 {
		var messages []string
		for _, e := range resp.Errors {
			messages = append(messages, e.Message)
		}
		return fail(fmt.Errorf("%s", strings.Join(messages, "; ")))
	}
	rate := RateLimit{Cost: resp.Data.RateLimit.Cost, Remaining: resp.Data.RateLimit.Remaining}
	if resp.Data.Repository == nil || resp.Data.Repository.Issue == nil {
		return fail(fmt.Errorf("the response has no issue"))
	}
	nodes := resp.Data.Repository.Issue.TimelineItems.Nodes
	if len(nodes) == 0 || nodes[0].Closer == nil || nodes[0].Closer.TypeName != "PullRequest" {
		return nil, rate, nil
	}
	closer := nodes[0].Closer
	if closer.ReviewThreads.PageInfo.HasNextPage {
		return fail(fmt.Errorf("pull request #%d has more than %d review threads", closer.Number, closerPageSize))
	}
	pr := &ClosingPullRequest{Number: closer.Number, Merged: closer.Merged, Body: closer.Body}
	for _, t := range closer.ReviewThreads.Nodes {
		if t.Comments.PageInfo.HasNextPage {
			return fail(fmt.Errorf("a review thread of pull request #%d has more than %d comments", closer.Number, closerPageSize))
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

// The GraphQL response. It stops in this package.
type closerResponse struct {
	Data struct {
		Repository *struct {
			Issue *struct {
				TimelineItems struct {
					Nodes []struct {
						Closer *struct {
							TypeName      string `json:"__typename"`
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
						} `json:"closer"`
					} `json:"nodes"`
				} `json:"timelineItems"`
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
