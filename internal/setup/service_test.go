package setup

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"html"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/platform/github"
)

const (
	fakeClientSecret  = "fake-client-secret-0123456789"
	fakeWebhookSecret = "fake-webhook-secret-0123456789"
)

// fakeGitHub has the one API endpoint that the flow uses. It gives each code a
// generated private key, as GitHub does.
type fakeGitHub struct {
	t *testing.T

	mu    sync.Mutex
	codes map[string]string // code -> App name from the manifest
	pems  map[string][]byte // client ID -> PEM that the fake returned
}

func newFakeGitHub(t *testing.T) (*fakeGitHub, *httptest.Server) {
	t.Helper()
	fake := &fakeGitHub{t: t, codes: map[string]string{}, pems: map[string][]byte{}}
	server := httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(server.Close)
	return fake, server
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	match := regexp.MustCompile(`^/app-manifests/([^/]+)/conversions$`).FindStringSubmatch(r.URL.Path)
	if r.Method != http.MethodPost || match == nil {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		return
	}
	if r.Header.Get("Authorization") != "" {
		f.t.Errorf("the conversion call has an Authorization header")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	name, ok := f.codes[match[1]]
	if !ok {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		return
	}
	delete(f.codes, match[1]) // a code works once

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		f.t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	clientID := "Iv23li" + name
	f.pems[clientID] = pemBytes
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": len(f.pems), "slug": name, "client_id": clientID,
		"html_url": "https://github.com/apps/" + name, "pem": string(pemBytes),
		"client_secret": fakeClientSecret, "webhook_secret": fakeWebhookSecret,
	})
}

// newCode plays the part of GitHub after the person selects "Create GitHub App".
func (f *fakeGitHub) newCode(appName string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	code := fmt.Sprintf("code-%s-%d", appName, len(f.codes)+len(f.pems))
	f.codes[code] = appName
	return code
}

// fakeBrowser plays the part of the person and the browser: it loads the local
// page, reads the form, and calls the callback the way GitHub redirects.
type fakeBrowser struct {
	t         *testing.T
	gitHub    *fakeGitHub
	org       string
	wrongFor  string // an App name that gets a callback with a wrong state
	stopAfter int    // when > 0, the browser does nothing after this many Apps

	mu        sync.Mutex
	manifests []Manifest
	codes     []string
}

var (
	formAction   = regexp.MustCompile(`<form action="([^"]+)" method="post">`)
	formManifest = regexp.MustCompile(`name="manifest" value="([^"]+)"`)
)

func (b *fakeBrowser) open(startURL string) error {
	b.mu.Lock()
	if b.stopAfter > 0 && len(b.manifests) >= b.stopAfter {
		b.mu.Unlock()
		return nil
	}
	b.mu.Unlock()

	resp, err := http.Get(startURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		b.t.Errorf("GET the local page: status %d: %s", resp.StatusCode, body)
		return nil
	}
	if host := resp.Request.URL.Hostname(); host != "127.0.0.1" {
		b.t.Errorf("the local page is on %q, want 127.0.0.1", host)
	}

	action := formAction.FindSubmatch(body)
	rawManifest := formManifest.FindSubmatch(body)
	if action == nil || rawManifest == nil {
		b.t.Errorf("the local page has no form: %s", body)
		return nil
	}
	target, err := url.Parse(html.UnescapeString(string(action[1])))
	if err != nil {
		b.t.Errorf("form action: %v", err)
		return nil
	}
	if want := "/organizations/" + b.org + "/settings/apps/new"; target.Path != want {
		b.t.Errorf("form action path = %q, want %q", target.Path, want)
	}
	var manifest Manifest
	if err := json.Unmarshal([]byte(html.UnescapeString(string(rawManifest[1]))), &manifest); err != nil {
		b.t.Errorf("manifest: %v", err)
		return nil
	}

	code := b.gitHub.newCode(manifest.Name)
	b.mu.Lock()
	b.manifests = append(b.manifests, manifest)
	b.codes = append(b.codes, code)
	b.mu.Unlock()

	state := target.Query().Get("state")
	if manifest.Name == b.wrongFor {
		state = "a-state-from-another-page"
	}
	go func() {
		callbackURL := manifest.RedirectURL + "?code=" + url.QueryEscape(code) + "&state=" + url.QueryEscape(state)
		if resp, err := http.Get(callbackURL); err == nil {
			resp.Body.Close()
		}
	}()
	return nil
}

// memoryStore is the secret store of the tests. The real one needs macOS.
type memoryStore struct {
	mu    sync.Mutex
	items map[string][]byte
}

func (m *memoryStore) SetBase64(_ context.Context, service, account string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.items == nil {
		m.items = map[string][]byte{}
	}
	m.items[service+" "+account] = bytes.Clone(value)
	return nil
}

func newService(t *testing.T, browser *fakeBrowser, store *memoryStore, out io.Writer) *Service {
	t.Helper()
	fake, api := newFakeGitHub(t)
	browser.t, browser.gitHub = t, fake
	return &Service{
		GitHubURL:      "https://github.example",
		Converter:      github.NewAppClient(api.URL, api.Client()),
		Secrets:        store,
		OpenBrowser:    browser.open,
		Out:            out,
		ConfirmTimeout: 5 * time.Second,
	}
}

func TestSetupGitHubApps_RegistersFourAppsAndStoresTheKeys(t *testing.T) {
	var out bytes.Buffer
	browser := &fakeBrowser{org: "example-org"}
	store := &memoryStore{}
	service := newService(t, browser, store, &out)

	registered, err := service.RegisterApps(context.Background(), "example-org", "acme-")
	if err != nil {
		t.Fatalf("RegisterApps: %v", err)
	}
	if len(registered) != 4 || len(store.items) != 4 {
		t.Fatalf("registered %d Apps and stored %d keys, want 4 and 4", len(registered), len(store.items))
	}

	wantNames := []string{"acme-cumin-core", "acme-cumin-chief-engineer", "acme-cumin-implementer", "acme-cumin-reviewer"}
	for i, manifest := range browser.manifests {
		app := Apps[i]
		if manifest.Name != wantNames[i] {
			t.Errorf("manifest %d name = %q, want %q", i, manifest.Name, wantNames[i])
		}
		wantPermissions, _ := github.AppPermissions(app)
		if !maps.Equal(manifest.DefaultPermissions, wantPermissions) {
			t.Errorf("%s: permissions = %v, want %v", app, manifest.DefaultPermissions, wantPermissions)
		}
		if manifest.Public {
			t.Errorf("%s: the manifest is public", app)
		}
		if manifest.URL != "https://github.com/example-org" {
			t.Errorf("%s: url = %q", app, manifest.URL)
		}

		// The key of each App is stored under its Client ID, with the same bytes.
		clientID := registered[i].ClientID
		stored := store.items["cumin-works github-app-private-key/"+clientID]
		if !bytes.Equal(stored, browser.gitHub.pems[clientID]) || len(stored) == 0 {
			t.Errorf("%s: the stored key differs from the key that GitHub returned", app)
		}
		if registered[i].App != app || registered[i].Slug != wantNames[i] {
			t.Errorf("registered[%d] = %+v", i, registered[i])
		}
		if !strings.Contains(out.String(), "client ID "+clientID) {
			t.Errorf("the output does not show the client ID of %s", app)
		}
	}
}

// The manifest on the page has no webhook and asks for no user authorization.
func TestSetupGitHubApps_ManifestHasNoWebhook(t *testing.T) {
	manifest, err := BuildManifest("example-org", "", "implementer", "http://127.0.0.1:1/callback")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(manifest)
	var fields map[string]any
	_ = json.Unmarshal(encoded, &fields)
	for _, forbidden := range []string{"hook_attributes", "default_events", "callback_urls", "request_oauth_on_install", "setup_url"} {
		if _, has := fields[forbidden]; has {
			t.Errorf("the manifest has %q: %s", forbidden, encoded)
		}
	}
	if fields["public"] != false {
		t.Errorf("public = %v, want false", fields["public"])
	}
}

func TestSetupGitHubApps_WrongStateStoresNothing(t *testing.T) {
	var out bytes.Buffer
	browser := &fakeBrowser{org: "example-org", wrongFor: "cumin-core"}
	store := &memoryStore{}
	service := newService(t, browser, store, &out)

	registered, err := service.RegisterApps(context.Background(), "example-org", "")
	if err == nil || !strings.Contains(err.Error(), "wrong state") {
		t.Fatalf("err = %v, want a wrong state error", err)
	}
	if len(registered) != 0 || len(store.items) != 0 {
		t.Errorf("registered %d Apps and stored %d keys, want none", len(registered), len(store.items))
	}
	if len(browser.gitHub.pems) != 0 {
		t.Error("the flow exchanged the code of a callback with a wrong state")
	}
}

func TestSetupGitHubApps_StopsWhenThePersonDoesNotConfirm(t *testing.T) {
	var out bytes.Buffer
	browser := &fakeBrowser{org: "example-org", stopAfter: 2}
	store := &memoryStore{}
	service := newService(t, browser, store, &out)
	service.ConfirmTimeout = 300 * time.Millisecond

	registered, err := service.RegisterApps(context.Background(), "example-org", "")
	if err == nil || !strings.Contains(err.Error(), "no confirmation") || !strings.Contains(err.Error(), `"implementer"`) {
		t.Fatalf("err = %v, want a timeout for the third App", err)
	}
	if len(registered) != 2 || len(store.items) != 2 {
		t.Errorf("registered %d Apps and stored %d keys, want 2 and 2", len(registered), len(store.items))
	}
}

func TestSetupGitHubApps_OutputHoldsNoSecret(t *testing.T) {
	var out bytes.Buffer
	browser := &fakeBrowser{org: "example-org"}
	store := &memoryStore{}
	service := newService(t, browser, store, &out)

	registered, err := service.RegisterApps(context.Background(), "example-org", "")
	if err != nil {
		t.Fatalf("RegisterApps: %v", err)
	}
	output := out.String() + fmt.Sprintf("%v %+v %#v", registered, registered, registered)

	secrets := []string{fakeClientSecret, fakeWebhookSecret, "PRIVATE KEY"}
	secrets = append(secrets, browser.codes...)
	for _, pemBytes := range browser.gitHub.pems {
		secrets = append(secrets, string(pemBytes))
	}
	for _, secret := range secrets {
		if strings.Contains(output, secret) {
			t.Errorf("the output holds a secret (%d characters)", len(secret))
		}
	}
	if len(browser.codes) != 4 {
		t.Errorf("the browser got %d codes, want 4", len(browser.codes))
	}
}

// A failed conversion must not show the code: an error text goes to the terminal.
func TestSetupGitHubApps_ConversionErrorHidesTheCode(t *testing.T) {
	_, api := newFakeGitHub(t)
	client := github.NewAppClient(api.URL, api.Client())

	_, err := client.ConvertManifestCode(context.Background(), "a-code-that-github-does-not-know")
	if err == nil {
		t.Fatal("no error")
	}
	if strings.Contains(err.Error(), "a-code-that-github-does-not-know") {
		t.Errorf("the error holds the code: %v", err)
	}
	if !strings.Contains(err.Error(), "status 404") {
		t.Errorf("err = %v, want the status code", err)
	}
}

// The port is open only while the command runs.
func TestSetupGitHubApps_ClosesTheLocalPage(t *testing.T) {
	var out bytes.Buffer
	browser := &fakeBrowser{org: "example-org"}
	service := newService(t, browser, &memoryStore{}, &out)

	if _, err := service.RegisterApps(context.Background(), "example-org", ""); err != nil {
		t.Fatalf("RegisterApps: %v", err)
	}
	callbackURL := browser.manifests[0].RedirectURL
	if resp, err := http.Get(callbackURL); err == nil {
		resp.Body.Close()
		t.Errorf("the local page still answers on %s after the command ended", callbackURL)
	}
}

func TestCheckNames(t *testing.T) {
	if err := CheckNames("example-org", "acme-"); err != nil {
		t.Errorf("valid names: %v", err)
	}
	// "cumin-chief-engineer" has 20 characters, so a prefix of 15 is too long.
	for name, c := range map[string]struct{ org, prefix string }{
		"name longer than 34 characters": {"example-org", "fifteen-chars--"},
		"prefix with a space":            {"example-org", "my org-"},
		"organization with a slash":      {"example/org", ""},
		"empty organization":             {"", ""},
	} {
		if err := CheckNames(c.org, c.prefix); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if got := len(AppName("fourteen-chars", "chief-engineer")); got != 34 {
		t.Errorf("the longest valid name has %d characters, want 34", got)
	}
	if err := CheckNames("example-org", "fourteen-chars"); err != nil {
		t.Errorf("a name of exactly 34 characters: %v", err)
	}
}
