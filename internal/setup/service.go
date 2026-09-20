package setup

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
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
// GetBase64 returns keychain.ErrNotFound for a missing item.
type SecretStore interface {
	SetBase64(ctx context.Context, service, account string, value []byte) error
	GetBase64(ctx context.Context, service, account string) ([]byte, error)
}

// GitHubAPI is the part of the GitHub API that the command uses.
// *github.AppClient is the real one.
type GitHubAPI interface {
	ConvertManifestCode(ctx context.Context, code string) (github.AppRegistration, error)
	GetApp(ctx context.Context, cred github.AppCredentials) (github.AppInfo, error)
	IsInstalledOn(ctx context.Context, cred github.AppCredentials, account string) (bool, error)
}

// Service runs "cumin setup github-apps".
type Service struct {
	GitHubURL      string // the GitHub web pages; DefaultGitHubURL outside of tests
	GitHub         GitHubAPI
	Secrets        SecretStore
	ConfigPath     string // the Host settings file; the command writes the Client IDs here
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

// callback is what the local page hands to the flow, after the page checked
// the state. The code is a secret for one hour: it never goes to Out, to a
// log, or into an error.
type callback struct {
	code string
}

// Run is "cumin setup github-apps". It registers every App that is not
// registered on this Host, one by one, and then opens the installation page of
// every App that has no installation on the organization.
//
// An App counts as registered when the Host settings hold its Client ID and the
// secret store holds the key for that Client ID. Run checks every App before it
// registers one, so that a broken state changes nothing.
func (s *Service) Run(ctx context.Context, org, prefix string) error {
	if err := CheckNames(org, prefix); err != nil {
		return err
	}
	apps, err := config.ReadGitHubApps(s.ConfigPath)
	if err != nil {
		return fmt.Errorf("setup: %w", err)
	}
	// Use the spelling of the settings file from here on, so that the command
	// reads and writes one table.
	if org, err = settingsKey(apps, org); err != nil {
		return err
	}
	clientIDs := apps[org]

	// Check every registered App before any change: the key exists, it is a
	// key, and GitHub accepts it for this Client ID.
	var missing []string
	usedBy := map[string]string{} // client ID -> the App that uses it
	for _, app := range Apps {
		clientID := clientIDs[app]
		if clientID == "" {
			missing = append(missing, app)
			continue
		}
		// Each role has its own App. With one App for two roles, an agent
		// would act with the identity and the permissions of another role.
		if other, used := usedBy[clientID]; used {
			return fmt.Errorf("setup: the apps %q and %q have the same client ID %s in %s. Each App needs its own client ID. Nothing is changed", other, app, clientID, s.ConfigPath)
		}
		usedBy[clientID] = app
		cred, err := s.credentials(ctx, clientID)
		if errors.Is(err, keychain.ErrNotFound) {
			return fmt.Errorf("setup: app %q has the client ID %s in %s, but the Keychain has no private key for it. cumin does not guess. If the App still exists on GitHub, make a new private key by hand (docs/ja/development/github-app-setup.md, step 2). If not, remove the line from the settings file and run the command again", app, clientID, s.ConfigPath)
		}
		if err != nil {
			return fmt.Errorf("setup: app %q (client ID %s): %w", app, clientID, err)
		}
		info, err := s.GitHub.GetApp(ctx, cred)
		if err != nil {
			return fmt.Errorf("setup: app %q: GitHub does not accept the private key in the Keychain for the client ID %s. Nothing is changed: %w", app, clientID, err)
		}
		// The four Apps have four different sets of permissions, so the
		// permissions show that the client ID belongs to this role. With the
		// client IDs of two roles swapped, an agent would act as another App,
		// for example as the one that may bypass the ruleset.
		if want, _ := github.AppPermissions(app); !maps.Equal(info.Permissions, want) {
			return fmt.Errorf("setup: app %q: the App %s with the client ID %s has the permissions %v, and %q needs %v. The client ID belongs to another role, or the permissions of the App changed. Nothing is changed", app, info.Slug, clientID, info.Permissions, app, want)
		}
		// A private App can be installed only on the account that owns it.
		if !strings.EqualFold(info.Owner, org) {
			return fmt.Errorf("setup: app %q: the App %s with the client ID %s belongs to %q, not to %q. Nothing is changed", app, info.Slug, clientID, info.Owner, org)
		}
		fmt.Fprintf(s.Out, "already registered %s: client ID %s\n", app, clientID)
	}

	// GitHub registers an App for good. So check first that the command can
	// write its client ID afterwards. A settings file in a form that the
	// command does not know stops the run here, before any change.
	for _, app := range missing {
		if err := config.CheckGitHubAppClientIDWritable(s.ConfigPath, org, app); err != nil {
			return fmt.Errorf("setup: nothing is changed: %w", err)
		}
	}

	if len(missing) > 0 {
		if _, err := s.RegisterApps(ctx, org, prefix, missing); err != nil {
			return err
		}
	}
	return s.openInstallPages(ctx, org)
}

// settingsKey returns the spelling of the organization in the settings file.
// GitHub account names ignore case, so "Example-Org" and "example-org" are one
// organization. Two tables that differ only in case are an error.
func settingsKey(apps map[string]map[string]string, org string) (string, error) {
	var found []string
	for key := range apps {
		if strings.EqualFold(key, org) {
			found = append(found, key)
		}
	}
	switch len(found) {
	case 0:
		return org, nil
	case 1:
		return found[0], nil
	default:
		slices.Sort(found)
		return "", fmt.Errorf("setup: the settings file has more than one github_apps table for this organization: %s. Keep one", strings.Join(found, ", "))
	}
}

// credentials reads and parses the private key of one registered App.
func (s *Service) credentials(ctx context.Context, clientID string) (github.AppCredentials, error) {
	pemBytes, err := s.Secrets.GetBase64(ctx, keychain.Service, keychain.PrivateKeyAccount(clientID))
	if err != nil {
		return github.AppCredentials{}, err
	}
	key, err := github.ParsePrivateKey(pemBytes)
	if err != nil {
		return github.AppCredentials{}, err
	}
	return github.AppCredentials{ClientID: clientID, PrivateKey: key}, nil
}

// RegisterApps registers the given Apps for the organization, one by one. The
// person confirms each App in the browser. The private key of each App goes
// directly into the secret store, and then the Client ID goes into the Host
// settings file.
func (s *Service) RegisterApps(ctx context.Context, org, prefix string, apps []string) ([]Registered, error) {
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
	for _, app := range apps {
		registered, err := s.registerOne(ctx, page, callbacks, app)
		if err != nil {
			return done, fmt.Errorf("setup: app %q: %w", app, err)
		}
		done = append(done, registered)
		fmt.Fprintf(s.Out, "registered %s: slug %s, client ID %s\n", app, registered.Slug, registered.ClientID)
	}
	return done, nil
}

// openInstallPages opens the installation page of every App that has no
// installation on the organization. GitHub has no API to install an App.
func (s *Service) openInstallPages(ctx context.Context, org string) error {
	apps, err := config.ReadGitHubApps(s.ConfigPath)
	if err != nil {
		return fmt.Errorf("setup: %w", err)
	}
	for _, app := range Apps {
		cred, err := s.credentials(ctx, apps[org][app])
		if err != nil {
			return fmt.Errorf("setup: app %q: %w", app, err)
		}
		installed, err := s.GitHub.IsInstalledOn(ctx, cred, org)
		if err != nil {
			return fmt.Errorf("setup: app %q: %w", app, err)
		}
		if installed {
			fmt.Fprintf(s.Out, "installed %s on %s\n", app, org)
			continue
		}
		info, err := s.GitHub.GetApp(ctx, cred)
		if err != nil {
			return fmt.Errorf("setup: app %q: %w", app, err)
		}
		fmt.Fprintf(s.Out, "Install the App %s on %s with \"Only select repositories\": %s\n", info.Slug, org, info.InstallURL())
		if err := s.OpenBrowser(info.InstallURL()); err != nil {
			fmt.Fprintf(s.Out, "The browser did not open (%v). Open the address above by hand.\n", err)
		}
	}
	return nil
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
	registration, err := s.GitHub.ConvertManifestCode(ctx, got.code)
	if err != nil {
		return Registered{}, err
	}
	account := keychain.PrivateKeyAccount(registration.ClientID)
	if err := s.Secrets.SetBase64(ctx, keychain.Service, account, registration.PrivateKeyPEM); err != nil {
		return Registered{}, fmt.Errorf("GitHub registered the App %s (client ID %s), but the private key is not stored: %w. Delete the App on GitHub and run the command again", registration.Slug, registration.ClientID, err)
	}
	// The key first, then the Client ID: a Client ID in the settings always has a key.
	if err := config.SetGitHubAppClientID(s.ConfigPath, page.org, app, registration.ClientID); err != nil {
		return Registered{}, fmt.Errorf("GitHub registered the App %s and the private key is stored, but the client ID is not written: %w", registration.Slug, err)
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
