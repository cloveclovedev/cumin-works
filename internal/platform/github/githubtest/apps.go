package githubtest

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Token is the installation token that the fake accepts. It is also the
// token that the fake creates for an App, so that a test can see it reach
// the place that uses it.
const Token = "ghs_fakeInstallationToken"

// App is the one GitHub App that the fake knows. The endpoints of an agent
// start (GET /app, GET /users/<slug>[bot]) answer from it.
type App struct {
	Slug string
	// Owner is the login of the account that owns the App.
	Owner string
	// BotID is the id of the bot user "<slug>[bot]".
	BotID int64
}

// Permission is the answer of "Get repository permissions for a user".
type Permission struct {
	Permission string
	UserType   string
}

// SetPermission sets the permission of an account on every repository of
// the fake. An account without one reads "read", as any account reads on a
// public repository (measured in #286, M1).
func (f *Fake) SetPermission(login, permission, userType string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.permissions == nil {
		f.permissions = map[string]Permission{}
	}
	f.permissions[login] = Permission{Permission: permission, UserType: userType}
}

// AddApp registers the App of the fake. The fake then answers GET /app
// with it, and GET /users/<slug>[bot] with its bot user.
func (f *Fake) AddApp(app App) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.app = &app
}

// AddUser registers a user for GET /users/{login}.
func (f *Fake) AddUser(login string, id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[login] = id
}

// TokenRequest is the body of one token request: the repositories and the
// permissions that the token is limited to.
type TokenRequest struct {
	Repositories []string          `json:"repositories"`
	Permissions  map[string]string `json:"permissions"`
}

// TokenRequests returns the bodies of the token requests that the fake
// received (POST /app/installations/{id}/access_tokens), in order.
func (f *Fake) TokenRequests() []TokenRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var requests []TokenRequest
	for _, r := range f.requests {
		if r.Method != http.MethodPost || !accessTokensPath.MatchString(r.Path) {
			continue
		}
		var body TokenRequest
		_ = json.Unmarshal(r.Body, &body)
		requests = append(requests, body)
	}
	return requests
}

// serveApp answers GET /app with the registered App. Official: "Get the
// authenticated app".
func (f *Fake) serveApp(w http.ResponseWriter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.app == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"slug":        f.app.Slug,
		"html_url":    "https://github.com/apps/" + f.app.Slug,
		"owner":       map[string]any{"login": f.app.Owner},
		"permissions": map[string]any{"contents": "write", "metadata": "read"},
	})
}

// serveInstallation answers GET /repos/{owner}/{repo}/installation with
// the installation of the App on the repository. Official: "Get a
// repository installation for the authenticated app".
func (f *Fake) serveInstallation(w http.ResponseWriter, owner, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	repo, ok := f.repository(w, owner, name)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": repo.installationID})
}

// serveAccessToken answers POST /app/installations/{id}/access_tokens with
// Token, which expires in one hour. The body is recorded (TokenRequests).
// Official: "Create an installation access token for an app".
func (f *Fake) serveAccessToken(w http.ResponseWriter, id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	found := false
	for _, repo := range f.repositories {
		if repo.installationID == id {
			found = true
		}
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return
	}
	// The client compares the expiry with the real clock and takes no clock
	// from a test, so the expiry is one hour after the real time.
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":      Token,
		"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
}

// serveUser answers GET /users/{login} for the bot of the App and for the
// registered users. Official: "Get a user".
func (f *Fake) serveUser(w http.ResponseWriter, login string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	login, _ = url.PathUnescape(login)
	if f.app != nil && login == f.app.Slug+"[bot]" {
		writeJSON(w, http.StatusOK, map[string]any{"id": f.app.BotID, "login": login, "type": "Bot"})
		return
	}
	if id, ok := f.users[login]; ok {
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "login": login, "type": "User"})
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
}

// servePermission answers GET .../collaborators/{username}/permission.
func (f *Fake) servePermission(w http.ResponseWriter, login string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.permissions[login]
	if !ok {
		p = Permission{Permission: "read", UserType: "User"}
		if strings.HasSuffix(login, "[bot]") {
			p = Permission{Permission: "none", UserType: "Bot"}
		}
		if login == SeedActor {
			p = Permission{Permission: "admin", UserType: "User"}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"permission": p.Permission, "role_name": p.Permission,
		"user": map[string]any{"login": login, "type": p.UserType}})
}
