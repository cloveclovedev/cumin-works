package github

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// AppInfo is what GitHub says about the authenticated App.
type AppInfo struct {
	Slug    string
	HTMLURL string // the public page of the App
	Owner   string // the login of the account that owns the App
	// Permissions are the repository permissions of the App, without
	// "metadata", which GitHub adds by itself.
	Permissions map[string]string
}

// InstallURL returns the page where a person installs the App on an account.
func (a AppInfo) InstallURL() string {
	return strings.TrimRight(a.HTMLURL, "/") + "/installations/new"
}

// GetApp reads the authenticated App: GET /app with a JWT.
func (c *AppClient) GetApp(ctx context.Context, cred AppCredentials) (AppInfo, error) {
	jwt, err := c.jwtFor(cred)
	if err != nil {
		return AppInfo{}, err
	}
	var app struct {
		Slug    string `json:"slug"`
		HTMLURL string `json:"html_url"`
		Owner   struct {
			Login string `json:"login"`
		} `json:"owner"`
		Permissions map[string]string `json:"permissions"`
	}
	if err := c.do(ctx, jwt, http.MethodGet, "/app", "/app", nil, http.StatusOK, &app); err != nil {
		return AppInfo{}, fmt.Errorf("github: read the App with client ID %s: %w", cred.ClientID, err)
	}
	if app.Slug == "" || app.HTMLURL == "" {
		return AppInfo{}, fmt.Errorf("github: read the App with client ID %s: the response has no slug or no address", cred.ClientID)
	}
	delete(app.Permissions, "metadata")
	if app.Permissions == nil {
		app.Permissions = map[string]string{}
	}
	return AppInfo{Slug: app.Slug, HTMLURL: app.HTMLURL, Owner: app.Owner.Login, Permissions: app.Permissions}, nil
}

// IsInstalledOn reports if the App has an installation on the account (an
// organization or a user): GET /app/installations with a JWT, every page.
func (c *AppClient) IsInstalledOn(ctx context.Context, cred AppCredentials, account string) (bool, error) {
	jwt, err := c.jwtFor(cred)
	if err != nil {
		return false, err
	}
	const perPage = 100
	for page := 1; ; page++ {
		var installations []struct {
			Account struct {
				Login string `json:"login"`
			} `json:"account"`
		}
		path := fmt.Sprintf("/app/installations?per_page=%d&page=%d", perPage, page)
		if err := c.do(ctx, jwt, http.MethodGet, path, "/app/installations", nil, http.StatusOK, &installations); err != nil {
			return false, fmt.Errorf("github: list the installations of the App with client ID %s: %w", cred.ClientID, err)
		}
		for _, installation := range installations {
			// GitHub account names ignore case.
			if strings.EqualFold(installation.Account.Login, account) {
				return true, nil
			}
		}
		if len(installations) < perPage {
			return false, nil
		}
	}
}

func (c *AppClient) jwtFor(cred AppCredentials) (string, error) {
	if cred.ClientID == "" || cred.PrivateKey == nil {
		return "", fmt.Errorf("github: the App has no client ID or no private key")
	}
	return signJWT(cred, c.now())
}
