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
	"strings"
)

// RequiredCheck is one check that the rules of a branch require. Integration
// is the database id of the GitHub App that must report it, or 0 when the
// rule names no App. GitHub counts a check of another App as missing, so the
// rows I3 and I4 must match the App as well (the ruleset of the sandbox pins
// cumin-protected-paths to GitHub Actions).
type RequiredCheck struct {
	Name        string
	Integration int64
}

// RequiredChecks returns the checks that the rules of a branch require,
// sorted by name and without a repeat. A branch with no ruleset, or one
// whose rules require no check, gives an empty list; the rows I3 and I4
// then treat the checks as passed at once (issue-states.md, the text on
// required checks).
//
// Official: REST "Get rules for a branch"
// (GET /repos/{owner}/{repo}/rules/branches/{branch}, 200). An installation
// token may call it with Metadata: read-only (row 34, measured in row 53).
func (c *AppClient) RequiredChecks(ctx context.Context, token, owner, repo, branch string) ([]RequiredCheck, error) {
	path := fmt.Sprintf("/repos/%s/%s/rules/branches/%s",
		url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(branch))
	// Every rule of every ruleset that applies to the branch. Only the
	// rules of the type required_status_checks carry a check.
	var rules []struct {
		Type       string `json:"type"`
		Parameters struct {
			RequiredStatusChecks []struct {
				Context string `json:"context"`
				// IntegrationID is the App that must report the check.
				// It is absent when the rule names no App.
				IntegrationID int64 `json:"integration_id"`
			} `json:"required_status_checks"`
		} `json:"parameters"`
	}
	if err := c.do(ctx, token, http.MethodGet, path, path, nil, http.StatusOK, &rules); err != nil {
		return nil, fmt.Errorf("github: read the required checks of %s/%s on %s: %w", owner, repo, branch, err)
	}
	checks := []RequiredCheck{}
	for _, rule := range rules {
		for _, check := range rule.Parameters.RequiredStatusChecks {
			required := RequiredCheck{Name: check.Context, Integration: check.IntegrationID}
			if check.Context != "" && !slices.Contains(checks, required) {
				checks = append(checks, required)
			}
		}
	}
	slices.SortFunc(checks, func(a, b RequiredCheck) int {
		if a.Name != b.Name {
			return strings.Compare(a.Name, b.Name)
		}
		return int(a.Integration - b.Integration)
	})
	return checks, nil
}
