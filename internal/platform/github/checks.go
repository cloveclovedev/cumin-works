package github

// This file reads the checks that the rules of a branch require. The rows
// I3 and I4 of docs/ja/requirements/workflow/issue-states.md compare that
// list with the results of the checks on the head commit of a pull request,
// which come with the poll snapshot (snapshot.go), so that one poll stays
// one query.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
)

// RequiredChecks returns the names of the checks that the rules of a branch
// require, sorted and without a repeat. A branch with no ruleset, or one
// whose rules require no check, gives an empty list; the rows I3 and I4
// then treat the checks as passed at once (issue-states.md, the text on
// required checks).
//
// Official: REST "Get rules for a branch"
// (GET /repos/{owner}/{repo}/rules/branches/{branch}, 200). An installation
// token may call it with Metadata: read-only (row 34, measured in row 53).
func (c *AppClient) RequiredChecks(ctx context.Context, token, owner, repo, branch string) ([]string, error) {
	path := fmt.Sprintf("/repos/%s/%s/rules/branches/%s",
		url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(branch))
	// Every rule of every ruleset that applies to the branch. Only the
	// rules of the type required_status_checks carry a check.
	var rules []struct {
		Type       string `json:"type"`
		Parameters struct {
			RequiredStatusChecks []struct {
				Context string `json:"context"`
			} `json:"required_status_checks"`
		} `json:"parameters"`
	}
	if err := c.do(ctx, token, http.MethodGet, path, path, nil, http.StatusOK, &rules); err != nil {
		return nil, fmt.Errorf("github: read the required checks of %s/%s on %s: %w", owner, repo, branch, err)
	}
	names := []string{}
	for _, rule := range rules {
		for _, check := range rule.Parameters.RequiredStatusChecks {
			if check.Context != "" && !slices.Contains(names, check.Context) {
				names = append(names, check.Context)
			}
		}
	}
	slices.Sort(names)
	return names, nil
}
