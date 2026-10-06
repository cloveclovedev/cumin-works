package github

import (
	"context"
	"fmt"
	"net/http"
)

// RequestReview requests the review of one account on a pull request, so
// that GitHub lists the pull request under the review requests of that
// account (I7; docs/ja/designs/poll.md, the topic on the merge step).
// Official: "Request reviewers for a pull request" (reviewers; 201);
// Permissions required for GitHub Apps: Pull requests write. The same
// request for an account that is already requested does not fail, and an
// account that is not a collaborator answers 422 (measured in #510, V2 and
// V3).
func (c *AppClient) RequestReview(ctx context.Context, token, owner, repo string, number int, login string) error {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/requested_reviewers", owner, repo, number)
	body := map[string]any{"reviewers": []string{login}}
	var pull struct {
		Number int `json:"number"`
	}
	if err := c.do(ctx, token, http.MethodPost, path, path, body, http.StatusCreated, &pull); err != nil {
		return fmt.Errorf("github: request the review of %s on %s/%s#%d: %w", login, owner, repo, number, err)
	}
	return nil
}
