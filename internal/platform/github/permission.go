package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// RepositoryPermission reads the permission of an account on the
// repository and the type of the account. Official: "Get repository
// permissions for a user" (permission is admin, write, read, or none;
// maintain maps to write, triage to read); Permissions required for GitHub
// Apps: Metadata read. The token of cumin-core reads it for any account
// (measured in #286, M1).
func (c *AppClient) RepositoryPermission(ctx context.Context, token, owner, repo, login string) (permission, userType string, err error) {
	path := fmt.Sprintf("/repos/%s/%s/collaborators/%s/permission", owner, repo, url.PathEscape(login))
	var body struct {
		Permission string `json:"permission"`
		User       *struct {
			Type string `json:"type"`
		} `json:"user"`
	}
	if err := c.do(ctx, token, http.MethodGet, path, path, nil, http.StatusOK, &body); err != nil {
		return "", "", fmt.Errorf("github: read the permission of %s on %s/%s: %w", login, owner, repo, err)
	}
	if body.User != nil {
		userType = body.User.Type
	}
	return body.Permission, userType, nil
}
