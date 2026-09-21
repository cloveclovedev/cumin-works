package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// User is a GitHub user, as much of it as cumin needs. The bot user of a
// GitHub App has the login "<slug>[bot]".
type User struct {
	ID    int64
	Login string
}

// GetUser reads one user: GET /users/{username} with an installation
// token (official: "Get a user"). cumin calls it once for each role to
// learn the id of the bot user, which the commit email of an agent needs
// (measured-constraints.md row 49).
func (c *AppClient) GetUser(ctx context.Context, token, login string) (User, error) {
	if token == "" || login == "" {
		return User{}, fmt.Errorf("github: read a user: the token or the login is empty")
	}
	var user struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	path := "/users/" + url.PathEscape(login)
	if err := c.do(ctx, token, http.MethodGet, path, path, nil, http.StatusOK, &user); err != nil {
		return User{}, fmt.Errorf("github: read the user %s: %w", login, err)
	}
	if user.ID == 0 || user.Login == "" {
		return User{}, fmt.Errorf("github: read the user %s: the response has no id or no login", login)
	}
	return User{ID: user.ID, Login: user.Login}, nil
}
