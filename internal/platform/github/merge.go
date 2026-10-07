package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// The merge step of "start the merge" (docs/ja/designs/poll.md, the topic
// on the merge step): merge the pull request at the approved head commit,
// tell a conflict apart from the other failures, and close the
// implementation issue once when GitHub did not.

// ErrHeadMoved is the answer of a merge whose sha is not the head of the
// pull request: 409 (official: "Merge a pull request"; measured in #286,
// M2). Nothing is merged.
var ErrHeadMoved = errors.New("the head of the pull request is not the approved commit")

// ErrConflict is the answer of a merge that conflicts with the base branch:
// 405, and the pull request then reads mergeable false. A ruleset refusal
// is 405 as well (measured-constraints.md row 62), and only mergeable tells
// them apart (measured in #286, M4 and M5).
var ErrConflict = errors.New("the pull request has merge conflicts")

// ErrBaseModified is the answer of a merge that GitHub refuses right after
// another merge moved the base branch: 405 "Base branch was modified.
// Review and try the merge again." (issue-states.md, what cumin does inside
// merging). The state ends by itself, so the caller sends the merge again.
var ErrBaseModified = errors.New("the base branch was modified")

// baseModifiedMessage starts the message of GitHub for ErrBaseModified.
const baseModifiedMessage = "Base branch was modified"

// MergePullRequest merges the pull request with the method (squash, merge,
// or rebase) when its head is sha. Official: "Merge a pull request"
// (merge_method, sha); "Get a pull request" (mergeable). A conflict returns
// an error that wraps ErrConflict, a moved head one that wraps
// ErrHeadMoved, a base branch that just moved one that wraps
// ErrBaseModified; any other failure holds the answer of GitHub.
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
	case errors.As(err, &status) && status.Status == http.StatusMethodNotAllowed && strings.HasPrefix(status.Message, baseModifiedMessage):
		return fmt.Errorf("github: merge %s/%s#%d: %w (%s)", owner, repo, number, ErrBaseModified, status.Message)
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

// PullRequestIsMerged reads whether a pull request is merged. Official:
// "Get a pull request" (merged). A merge whose answer got lost shows here,
// so the merge step reads it before it sends the merge again.
func (c *AppClient) PullRequestIsMerged(ctx context.Context, token, owner, repo string, number int) (bool, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls/%d", owner, repo, number)
	var pull struct {
		Merged bool `json:"merged"`
	}
	if err := c.do(ctx, token, http.MethodGet, path, path, nil, http.StatusOK, &pull); err != nil {
		return false, fmt.Errorf("github: read whether %s/%s#%d is merged: %w", owner, repo, number, err)
	}
	return pull.Merged, nil
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
