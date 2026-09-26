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

// rulePage is the page size of "Get rules for a branch" (1 to 100). The
// answer is paginated: with per_page=1 the sandbox returned a Link header
// with four pages on 2026-09-25. A rule that a later page holds must not be
// missed, because a missed required_status_checks rule would let I3 treat a
// missing check as passed.
const rulePage = 100

// RequiredChecks returns the checks that the rules of a branch require,
// sorted by name and without a repeat. Every page of the rules is read. A
// branch with no ruleset, or one whose rules require no check, gives an
// empty list; the rows I3 and I4 then treat the checks as passed at once
// (issue-states.md, the text on required checks).
//
// Official: REST "Get rules for a branch"
// (GET /repos/{owner}/{repo}/rules/branches/{branch}, 200). An installation
// token may call it with Metadata: read-only (row 34, measured in row 53).
func (c *AppClient) RequiredChecks(ctx context.Context, token, owner, repo, branch string) ([]RequiredCheck, error) {
	base := fmt.Sprintf("/repos/%s/%s/rules/branches/%s",
		url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(branch))
	checks := []RequiredCheck{}
	for page := 1; ; page++ {
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
		path := fmt.Sprintf("%s?per_page=%d&page=%d", base, rulePage, page)
		if err := c.do(ctx, token, http.MethodGet, path, base, nil, http.StatusOK, &rules); err != nil {
			return nil, fmt.Errorf("github: read the required checks of %s/%s on %s: %w", owner, repo, branch, err)
		}
		for _, rule := range rules {
			for _, check := range rule.Parameters.RequiredStatusChecks {
				required := RequiredCheck{Name: check.Context, Integration: check.IntegrationID}
				if check.Context != "" && !slices.Contains(checks, required) {
					checks = append(checks, required)
				}
			}
		}
		if len(rules) < rulePage {
			break
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
