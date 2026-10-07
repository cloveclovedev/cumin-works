package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// The check after the Implementer ends finds the pull request of an
// implementation issue by the branch that cumin chose and by its author, and
// cumin-core adds the closing link when GitHub did not make it from
// "Closes #N" (docs/ja/designs/poll.md, the topic on the end of a run).

// branchPageSize is the page size of the open pull requests of one branch.
// A branch has one open pull request in normal work; a full page is an
// error, so that the check after the Implementer ends never decides on a
// part of the list.
const branchPageSize = 100

// BranchPullRequest is an open pull request of one branch, as much of it as
// the check after the Implementer ends needs.
type BranchPullRequest struct {
	Number int
	// NodeID is the GraphQL ID; the closing link takes it.
	NodeID string
	// Author is the login as the REST API shows it: a GitHub App is
	// "<slug>[bot]".
	Author     string
	HeadCommit string
	HeadBranch string
}

// ListOpenPullRequestsOfBranch lists the open pull requests whose head is
// the branch of the repository itself. Official: "List pull requests"
// (state, head as "owner:branch"); Permissions required for GitHub Apps:
// Pull requests read.
func (c *AppClient) ListOpenPullRequestsOfBranch(ctx context.Context, token, owner, repo, branch string) ([]BranchPullRequest, error) {
	query := url.Values{"state": {"open"}, "head": {owner + ":" + branch}, "per_page": {fmt.Sprint(branchPageSize)}}
	path := fmt.Sprintf("/repos/%s/%s/pulls?%s", owner, repo, query.Encode())
	var pulls []struct {
		Number int    `json:"number"`
		NodeID string `json:"node_id"`
		User   *struct {
			Login string `json:"login"`
		} `json:"user"`
		Head struct {
			SHA string `json:"sha"`
			Ref string `json:"ref"`
		} `json:"head"`
	}
	if err := c.do(ctx, token, http.MethodGet, path, path, nil, http.StatusOK, &pulls); err != nil {
		return nil, fmt.Errorf("github: list the open pull requests of the branch %s: %w", branch, err)
	}
	if len(pulls) == branchPageSize {
		return nil, fmt.Errorf("github: the branch %s has %d or more open pull requests", branch, branchPageSize)
	}
	list := make([]BranchPullRequest, 0, len(pulls))
	for _, p := range pulls {
		pr := BranchPullRequest{Number: p.Number, NodeID: p.NodeID, HeadCommit: p.Head.SHA, HeadBranch: p.Head.Ref}
		if p.User != nil {
			pr.Author = p.User.Login
		}
		list = append(list, pr)
	}
	return list, nil
}

// addClosingLinkMutation adds a pull request as a closing reference of an
// issue. Official, GraphQL reference, Issues: addCloseIssueReferences
// "adds one or more pull requests as manually linked closing references on
// an issue" (input issueId, pullRequestIds). The token of cumin-core calls
// it (measured on the sandbox on 2026-09-30, #276).
const addClosingLinkMutation = `mutation($issueId: ID!, $pullRequestIds: [ID!]!) {
  addCloseIssueReferences(input: {issueId: $issueId, pullRequestIds: $pullRequestIds}) {
    issue { number }
  }
}`

// AddClosingLink links the pull request to the issue as a closing
// reference. The IDs are GraphQL node IDs. An error holds the answer of
// GitHub.
func (c *AppClient) AddClosingLink(ctx context.Context, token, issueID, pullRequestID string) error {
	request := map[string]any{
		"query":     addClosingLinkMutation,
		"variables": map[string]any{"issueId": issueID, "pullRequestIds": []string{pullRequestID}},
	}
	var resp struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := c.do(ctx, token, http.MethodPost, "/graphql", "/graphql", request, http.StatusOK, &resp); err != nil {
		return fmt.Errorf("github: add the closing link: %w", err)
	}
	if len(resp.Errors) > 0 {
		var messages []string
		for _, e := range resp.Errors {
			messages = append(messages, e.Message)
		}
		return fmt.Errorf("github: add the closing link: %s", strings.Join(messages, "; "))
	}
	return nil
}
