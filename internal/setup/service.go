package setup

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/keychain"
)

// DefaultGitHubURL is the address of the GitHub web pages.
const DefaultGitHubURL = "https://github.com"

// DefaultConfirmTimeout is how long the command waits for one confirmation in
// the browser. The code of the Manifest flow is valid for one hour.
const DefaultConfirmTimeout = 10 * time.Minute

// SecretStore stores the private keys. *keychain.Keychain is the real one. The
// interface exists because the acceptance tests must run without macOS.
type SecretStore interface {
	SetBase64(ctx context.Context, service, account string, value []byte) error
}

// Converter exchanges the code of the Manifest flow. *github.AppClient is the
// real one.
type Converter interface {
	ConvertManifestCode(ctx context.Context, code string) (github.AppRegistration, error)
}

// Service runs "cumin setup github-apps".
type Service struct {
	GitHubURL      string // the GitHub web pages; DefaultGitHubURL outside of tests
	Converter      Converter
	Secrets        SecretStore
	OpenBrowser    func(url string) error
	Out            io.Writer // messages for the person; never a secret
	ConfirmTimeout time.Duration
}

// Registered is one App after the registration. It holds no secret.
type Registered struct {
	App      string
	Slug     string
	ClientID string
}

// callback is what the local page hands to the flow. The code is a secret for
// one hour: it never goes to Out, to a log, or into an error.
type callback struct {
	state string
	code  string
}

// RegisterApps registers every App in Apps for the organization, one by one.
// The person confirms each App in the browser. The private key of each App
// goes directly into the secret store.
func (s *Service) RegisterApps(ctx context.Context, org, prefix string) ([]Registered, error) {
	if err := CheckNames(org, prefix); err != nil {
		return nil, err
	}

	// The operating system assigns the port. It is open only while the command runs.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("setup: open the local page: %w", err)
	}
	callbacks := make(chan callback)
	page := &localPage{gitHubURL: s.GitHubURL, org: org, prefix: prefix, baseURL: "http://" + listener.Addr().String(), callbacks: callbacks}
	server := &http.Server{Handler: page.handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()

	var done []Registered
	for _, app := range Apps {
		registered, err := s.registerOne(ctx, page, callbacks, app)
		if err != nil {
			return done, fmt.Errorf("setup: app %q: %w", app, err)
		}
		done = append(done, registered)
		fmt.Fprintf(s.Out, "registered %s: slug %s, client ID %s\n", app, registered.Slug, registered.ClientID)
	}
	return done, nil
}

func (s *Service) registerOne(ctx context.Context, page *localPage, callbacks <-chan callback, app string) (Registered, error) {
	state, err := randomState()
	if err != nil {
		return Registered{}, err
	}
	page.expect(app, state)
	defer page.expect("", "")

	start := page.baseURL + "/start?app=" + url.QueryEscape(app)
	fmt.Fprintf(s.Out, "Confirm the App %q in the browser: %s\n", AppName(page.prefix, app), start)
	if err := s.OpenBrowser(start); err != nil {
		fmt.Fprintf(s.Out, "The browser did not open (%v). Open the address above by hand.\n", err)
	}

	timeout := s.ConfirmTimeout
	if timeout <= 0 {
		timeout = DefaultConfirmTimeout
	}
	var got callback
	select {
	case got = <-callbacks:
	case <-time.After(timeout):
		return Registered{}, fmt.Errorf("no confirmation in the browser within %s", timeout)
	case <-ctx.Done():
		return Registered{}, ctx.Err()
	}
	if got.state != state {
		return Registered{}, errors.New("the callback has a wrong state. Nothing is stored")
	}

	registration, err := s.Converter.ConvertManifestCode(ctx, got.code)
	if err != nil {
		return Registered{}, err
	}
	account := keychain.PrivateKeyAccount(registration.ClientID)
	if err := s.Secrets.SetBase64(ctx, keychain.Service, account, registration.PrivateKeyPEM); err != nil {
		return Registered{}, fmt.Errorf("GitHub registered the App %s (client ID %s), but the private key is not stored: %w. Delete the App on GitHub and run the command again", registration.Slug, registration.ClientID, err)
	}
	return Registered{App: app, Slug: registration.Slug, ClientID: registration.ClientID}, nil
}

func randomState() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("make the state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
