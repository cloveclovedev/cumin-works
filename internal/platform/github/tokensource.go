package github

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

// tokenRenewMargin is how long before the expiry a token counts as expired.
// GitHub fixes the expiry at one hour after the token is created, and a poll
// runs every 60 seconds by default, so a token serves about 55 polls. The
// margin covers one poll and the clock drift between the Host and GitHub. The
// settings table does not list it, so it is a constant.
const tokenRenewMargin = 5 * time.Minute

// TokenSource keeps one installation access token for one App and one
// repository, and creates a new one only when the token is close to its
// expiry. It is safe for concurrent use.
//
// Agents do not use a source: an agent run lasts up to 55 minutes, so it
// needs a token that was just created.
type TokenSource struct {
	client *AppClient
	cred   AppCredentials
	app    string
	owner  string
	repo   string
	now    func() time.Time

	mu    sync.Mutex
	token InstallationToken
}

// NewTokenSource returns a source with no token. The first Token call
// creates one. app is config.AppCuminCore or an agent role.
func NewTokenSource(client *AppClient, cred AppCredentials, app, owner, repo string) *TokenSource {
	return &TokenSource{client: client, cred: cred, app: app, owner: owner, repo: repo, now: time.Now}
}

// Token returns a token that stays valid for at least tokenRenewMargin. After
// a failed creation the source keeps no token, so the next call tries again.
func (s *TokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token.Token != "" && s.now().Before(s.token.ExpiresAt.Add(-tokenRenewMargin)) {
		return s.token.Token, nil
	}
	s.token = InstallationToken{}
	token, err := s.client.CreateInstallationToken(ctx, s.cred, s.app, s.owner, s.repo)
	if err != nil {
		return "", err
	}
	s.token = token
	return token.Token, nil
}

// String keeps the token out of formatted output.
func (s *TokenSource) String() string {
	return "TokenSource{" + s.app + " on " + s.owner + "/" + s.repo + "}"
}

// Format keeps the token out of every fmt verb. Without it, verbs such as
// %#v print the fields and do not call String.
func (s *TokenSource) Format(f fmt.State, verb rune) {
	_, _ = io.WriteString(f, s.String())
}
