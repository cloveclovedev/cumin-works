package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// The merge step of I6 and I12 (docs/ja/designs/poll.md, the topic on the
// merge step): merge the pull request at the approved head commit, tell a
// conflict apart from the other failures, and close the implementation
// issue once when GitHub did not.

// ErrHeadMoved is the answer of a merge whose sha is not the head of the
// pull request: 409 (official: "Merge a pull request"; measured in #286,
// M2). Nothing is merged.
var ErrHeadMoved = errors.New("the head of the pull request is not the approved commit")

// ErrConflict is the answer of a merge that conflicts with the base branch:
// 405, and the pull request then reads mergeable false. A ruleset refusal
// is 405 as well (measured row 62), and only mergeable tells them apart
// (measured in #286, M4 and M5).
var ErrConflict = errors.New("the pull request has merge conflicts")

// MergePullRequest merges the pull request with the method (squash, merge,
// or rebase) when its head is sha. Official: "Merge a pull request"
// (merge_method, sha); "Get a pull request" (mergeable). A conflict returns
// an error that wraps ErrConflict, a moved head one that wraps
// ErrHeadMoved; any other failure holds the answer of GitHub.
func (c *AppClient) MergePullRequest(ctx context.Context, token, owner, repo string, number int, sha, method string) error {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d/merge", owner, repo, number)
	body := map[string]any{"merge_method": method, "sha": sha}
	var merged struct {
		Merged bool `json:"merged"`
	}
	err := c.do(ctx, token, http.MethodPut, path, path, body, http.StatusOK, &merged)
	var status *StatusError
	switch {
	case err == nil && merged.Merged:
		return nil
	case err == nil:
		return fmt.Errorf("github: merge %s/%s#%d: GitHub answered 200 without a merge", owner, repo, number)
	case errors.As(err, &status) && status.Status == http.StatusConflict:
		return fmt.Errorf("github: merge %s/%s#%d: %w (%s)", owner, repo, number, ErrHeadMoved, status.Message)
	case errors.As(err, &status) && status.Status == http.StatusMethodNotAllowed:
		mergeable, readErr := c.mergeable(ctx, token, owner, repo, number)
		if readErr == nil && mergeable != nil && !*mergeable {
			return fmt.Errorf("github: merge %s/%s#%d: %w (%s)", owner, repo, number, ErrConflict, status.Message)
		}
	}
	return fmt.Errorf("github: merge %s/%s#%d: %w", owner, repo, number, err)
}

// mergeable reads the mergeable field of a pull request: nil while GitHub
// computes the test merge commit. After a merge that failed, GitHub has
// computed it (measured in #286, M5).
func (c *AppClient) mergeable(ctx context.Context, token, owner, repo string, number int) (*bool, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, number)
	var pull struct {
		Mergeable *bool `json:"mergeable"`
	}
	if err := c.do(ctx, token, http.MethodGet, path, path, nil, http.StatusOK, &pull); err != nil {
		return nil, err
	}
	return pull.Mergeable, nil
}

// IssueIsOpen reads whether an issue is open. Official: "Get an issue"
// (state).
func (c *AppClient) IssueIsOpen(ctx context.Context, token, owner, repo string, number int) (bool, error) {
	path := fmt.Sprintf("/repos/%s/%s/issues/%d", owner, repo, number)
	var issue struct {
		State string `json:"state"`
	}
	if err := c.do(ctx, token, http.MethodGet, path, path, nil, http.StatusOK, &issue); err != nil {
		return false, fmt.Errorf("github: read the state of %s/%s#%d: %w", owner, repo, number, err)
	}
	return issue.State == "open", nil
}

// CloseIssueAsCompleted closes an issue with the reason completed.
// Official: "Update an issue" (state, state_reason); Permissions required
// for GitHub Apps: Issues write. Closing a closed issue changes nothing
// (measured in #276, C4).
func (c *AppClient) CloseIssueAsCompleted(ctx context.Context, token, owner, repo string, number int) error {
	path := fmt.Sprintf("/repos/%s/%s/issues/%d", owner, repo, number)
	body := map[string]any{"state": "closed", "state_reason": "completed"}
	var issue struct {
		State string `json:"state"`
	}
	if err := c.do(ctx, token, http.MethodPatch, path, path, body, http.StatusOK, &issue); err != nil {
		return fmt.Errorf("github: close %s/%s#%d: %w", owner, repo, number, err)
	}
	return nil
}

// PullRequestURL is the address of one pull request on GitHub, for a
// notification.
func PullRequestURL(owner, repo string, number int) string {
	return fmt.Sprintf("%s/pull/%d", RepositoryURL(owner, repo), number)
}
