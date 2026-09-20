package setup

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"html"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/keychain"
)

const (
	fakeClientSecret  = "fake-client-secret-0123456789"
	fakeWebhookSecret = "fake-webhook-secret-0123456789"
)

// fakeGitHub has the one API endpoint that the flow uses. It gives each code a
// generated private key, as GitHub does.
type fakeGitHub struct {
	t *testing.T

	mu        sync.Mutex
	codes     map[string]string          // code -> App name from the manifest
	pems      map[string][]byte          // client ID -> PEM that the fake returned
	keys      map[string]*rsa.PrivateKey // client ID -> key of the App
	slugs     map[string]string          // client ID -> slug
	installed map[string]string          // slug -> account that has an installation
}

func newFakeGitHub(t *testing.T) (*fakeGitHub, *httptest.Server) {
	t.Helper()
	fake := &fakeGitHub{t: t, codes: map[string]string{}, pems: map[string][]byte{}, keys: map[string]*rsa.PrivateKey{}, slugs: map[string]string{}, installed: map[string]string{}}
	server := httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(server.Close)
	return fake, server
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/app" || r.URL.Path == "/app/installations" {
		f.serveAsApp(w, r)
		return
	}
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
	f.keys[clientID] = key
	f.slugs[clientID] = name
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": len(f.pems), "slug": name, "client_id": clientID,
		"html_url": "https://github.com/apps/" + name, "pem": string(pemBytes),
		"client_secret": fakeClientSecret, "webhook_secret": fakeWebhookSecret,
	})
}

// serveAsApp answers the two calls that an App makes with a JWT. It checks the
// signature with the key that the fake gave to that App.
func (f *fakeGitHub) serveAsApp(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	jwt, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		http.Error(w, `{"message":"A JSON web token could not be decoded"}`, http.StatusUnauthorized)
		return
	}
	var claims struct{ Iss string }
	rawClaims, _ := base64.RawURLEncoding.DecodeString(parts[1])
	_ = json.Unmarshal(rawClaims, &claims)
	key, ok := f.keys[claims.Iss]
	signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ok || rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], signature) != nil {
		http.Error(w, `{"message":"A JSON web token could not be decoded"}`, http.StatusUnauthorized)
		return
	}
	slug := f.slugs[claims.Iss]
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/app" {
		_ = json.NewEncoder(w).Encode(map[string]any{"slug": slug, "html_url": "https://github.example/apps/" + slug})
		return
	}
	installations := []map[string]any{}
	if account, ok := f.installed[slug]; ok {
		installations = append(installations, map[string]any{"account": map[string]any{"login": account}})
	}
	_ = json.NewEncoder(w).Encode(installations)
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
	reloadFor string // an App name whose callback tab the person loads again
	stopAfter int    // when > 0, the browser does nothing after this many Apps

	mu        sync.Mutex
	manifests []Manifest
	codes     []string
	statuses  []int    // the status of every callback request
	installed []string // the installation pages that the command opened
	pending   sync.WaitGroup
}

// callbackStatuses waits for every callback request and returns the statuses.
func (b *fakeBrowser) callbackStatuses() []int {
	b.pending.Wait()
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]int(nil), b.statuses...)
}

var (
	formAction   = regexp.MustCompile(`<form action="([^"]+)" method="post">`)
	formManifest = regexp.MustCompile(`name="manifest" value="([^"]+)"`)
)

func (b *fakeBrowser) open(startURL string) error {
	if !strings.HasPrefix(startURL, "http://127.0.0.1:") {
		b.mu.Lock()
		b.installed = append(b.installed, startURL)
		b.mu.Unlock()
		return nil
	}
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
	requests := 1
	if manifest.Name == b.reloadFor {
		requests = 2
	}
	b.pending.Add(1)
	go func() {
		defer b.pending.Done()
		callbackURL := manifest.RedirectURL + "?code=" + url.QueryEscape(code) + "&state=" + url.QueryEscape(state)
		for range requests {
			if resp, err := http.Get(callbackURL); err == nil {
				resp.Body.Close()
				b.mu.Lock()
				b.statuses = append(b.statuses, resp.StatusCode)
				b.mu.Unlock()
			}
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

func (m *memoryStore) GetBase64(_ context.Context, service, account string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.items[service+" "+account]
	if !ok {
		return nil, keychain.ErrNotFound
	}
	return bytes.Clone(value), nil
}

func newService(t *testing.T, browser *fakeBrowser, store *memoryStore, out io.Writer) *Service {
	t.Helper()
	fake, api := newFakeGitHub(t)
	browser.t, browser.gitHub = t, fake
	return &Service{
		GitHubURL:      "https://github.example",
		GitHub:         github.NewAppClient(api.URL, api.Client()),
		Secrets:        store,
		ConfigPath:     filepath.Join(t.TempDir(), "config.toml"),
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

	registered, err := service.RegisterApps(context.Background(), "example-org", "acme-", Apps)
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
	service.ConfirmTimeout = 500 * time.Millisecond

	// The page refuses the callback, and the flow gets no confirmation.
	registered, err := service.RegisterApps(context.Background(), "example-org", "", Apps)
	if err == nil || !strings.Contains(err.Error(), "no confirmation") {
		t.Fatalf("err = %v, want an error", err)
	}
	if statuses := browser.callbackStatuses(); len(statuses) != 1 || statuses[0] != http.StatusBadRequest {
		t.Errorf("callback statuses = %v, want one 400", statuses)
	}
	if len(registered) != 0 || len(store.items) != 0 {
		t.Errorf("registered %d Apps and stored %d keys, want none", len(registered), len(store.items))
	}
	if len(browser.gitHub.pems) != 0 {
		t.Error("the flow exchanged the code of a callback with a wrong state")
	}
}

// The person loads the callback tab of the first App again. The second request
// must not wait in line and reach the flow of the next App.
func TestSetupGitHubApps_ReloadedCallbackDoesNotReachTheNextApp(t *testing.T) {
	var out bytes.Buffer
	browser := &fakeBrowser{org: "example-org", reloadFor: "cumin-core"}
	store := &memoryStore{}
	service := newService(t, browser, store, &out)

	registered, err := service.RegisterApps(context.Background(), "example-org", "", Apps)
	if err != nil {
		t.Fatalf("RegisterApps: %v", err)
	}
	if len(registered) != 4 || len(store.items) != 4 {
		t.Errorf("registered %d Apps and stored %d keys, want 4 and 4", len(registered), len(store.items))
	}
	statuses := browser.callbackStatuses()
	refused := 0
	for _, status := range statuses {
		if status == http.StatusBadRequest {
			refused++
		}
	}
	if len(statuses) != 5 || refused != 1 {
		t.Errorf("callback statuses = %v, want five requests with exactly one 400 for the reload", statuses)
	}
}

func TestSetupGitHubApps_StopsWhenThePersonDoesNotConfirm(t *testing.T) {
	var out bytes.Buffer
	browser := &fakeBrowser{org: "example-org", stopAfter: 2}
	store := &memoryStore{}
	service := newService(t, browser, store, &out)
	service.ConfirmTimeout = 300 * time.Millisecond

	registered, err := service.RegisterApps(context.Background(), "example-org", "", Apps)
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

	registered, err := service.RegisterApps(context.Background(), "example-org", "", Apps)
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

	if _, err := service.RegisterApps(context.Background(), "example-org", "", Apps); err != nil {
		t.Fatalf("RegisterApps: %v", err)
	}
	callbackURL := browser.manifests[0].RedirectURL
	if resp, err := http.Get(callbackURL); err == nil {
		resp.Body.Close()
		t.Errorf("the local page still answers on %s after the command ended", callbackURL)
	}
}

const hostSettings = `# Host settings. A person edits this file.
repositories = ["example-org/example-repo"]  # the target
work_dir = "/tmp/cumin-work"

[roles.implementer]
time_limit = "45m"
`

func TestSetupGitHubApps_WritesTheClientIDsAndKeepsTheOtherSettings(t *testing.T) {
	var out bytes.Buffer
	browser := &fakeBrowser{org: "example-org"}
	store := &memoryStore{}
	service := newService(t, browser, store, &out)
	if err := os.WriteFile(service.ConfigPath, []byte(hostSettings), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := service.Run(context.Background(), "example-org", ""); err != nil {
		t.Fatalf("Run: %v", err)
	}

	text, _ := os.ReadFile(service.ConfigPath)
	if !strings.HasPrefix(string(text), hostSettings) {
		t.Errorf("the old lines of the settings file changed:\n%s", text)
	}
	for _, secret := range []string{"PRIVATE KEY", fakeClientSecret, fakeWebhookSecret} {
		if strings.Contains(string(text), secret) {
			t.Errorf("the settings file holds a secret: %q", secret)
		}
	}
	settings, err := config.Load(service.ConfigPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, app := range Apps {
		clientID := settings.GitHubApps["example-org"][app]
		if _, ok := browser.gitHub.pems[clientID]; !ok {
			t.Errorf("%s: the settings hold the client ID %q, which GitHub did not return", app, clientID)
		}
	}
	if got := settings.Roles[config.RoleImplementer].TimeLimit; got != 45*time.Minute {
		t.Errorf("another setting changed: time limit = %v", got)
	}
}

// The run stops after the second App. The next run registers only the rest.
func TestSetupGitHubApps_SecondRunRegistersOnlyTheMissingApps(t *testing.T) {
	var out bytes.Buffer
	browser := &fakeBrowser{org: "example-org", stopAfter: 2}
	store := &memoryStore{}
	service := newService(t, browser, store, &out)
	service.ConfirmTimeout = 300 * time.Millisecond

	if err := service.Run(context.Background(), "example-org", ""); err == nil {
		t.Fatal("the first run gave no error")
	}
	apps, _ := config.ReadGitHubApps(service.ConfigPath)
	if len(apps["example-org"]) != 2 {
		t.Fatalf("the settings hold %d client IDs after the stopped run, want 2", len(apps["example-org"]))
	}

	browser.stopAfter = 0
	service.ConfirmTimeout = 5 * time.Second
	if err := service.Run(context.Background(), "example-org", ""); err != nil {
		t.Fatalf("second run: %v", err)
	}
	var names []string
	for _, manifest := range browser.manifests {
		names = append(names, manifest.Name)
	}
	if want := "cumin-core cumin-chief-engineer cumin-implementer cumin-reviewer"; strings.Join(names, " ") != want {
		t.Errorf("registered %q over both runs, want each App one time: %q", names, want)
	}
	apps, _ = config.ReadGitHubApps(service.ConfigPath)
	if len(apps["example-org"]) != 4 || len(store.items) != 4 {
		t.Errorf("after the second run: %d client IDs and %d keys, want 4 and 4", len(apps["example-org"]), len(store.items))
	}
}

func TestSetupGitHubApps_RunAfterAFullRunRegistersNothing(t *testing.T) {
	var out bytes.Buffer
	browser := &fakeBrowser{org: "example-org"}
	service := newService(t, browser, &memoryStore{}, &out)
	if err := service.Run(context.Background(), "example-org", ""); err != nil {
		t.Fatalf("first run: %v", err)
	}

	out.Reset()
	if err := service.Run(context.Background(), "example-org", ""); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(browser.manifests) != 4 {
		t.Errorf("the browser saw %d manifests over both runs, want 4", len(browser.manifests))
	}
	if got := strings.Count(out.String(), "already registered "); got != 4 {
		t.Errorf("the second run reports %d registered Apps, want 4:\n%s", got, out.String())
	}
}

func TestSetupGitHubApps_ClientIDWithoutKeyStopsBeforeAnyRegistration(t *testing.T) {
	var out bytes.Buffer
	browser := &fakeBrowser{org: "example-org"}
	store := &memoryStore{}
	service := newService(t, browser, store, &out)
	settings := "[github_apps.example-org]\nimplementer = \"Iv23liNOKEY\"\n"
	if err := os.WriteFile(service.ConfigPath, []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}

	err := service.Run(context.Background(), "example-org", "")
	if err == nil || !strings.Contains(err.Error(), `"implementer"`) || !strings.Contains(err.Error(), "Iv23liNOKEY") {
		t.Fatalf("err = %v, want an error that names the App and the client ID", err)
	}
	if len(browser.manifests) != 0 || len(store.items) != 0 {
		t.Errorf("the run registered %d Apps and stored %d keys, want none", len(browser.manifests), len(store.items))
	}
	if text, _ := os.ReadFile(service.ConfigPath); string(text) != settings {
		t.Errorf("the settings file changed: %s", text)
	}
}

func TestSetupGitHubApps_OpensTheInstallPageOnlyForAppsThatAreNotInstalled(t *testing.T) {
	var out bytes.Buffer
	browser := &fakeBrowser{org: "example-org"}
	service := newService(t, browser, &memoryStore{}, &out)
	// GitHub account names ignore case.
	browser.gitHub.installed["cumin-core"] = "Example-Org"
	browser.gitHub.installed["cumin-reviewer"] = "example-org"
	browser.gitHub.installed["cumin-implementer"] = "another-org"

	if err := service.Run(context.Background(), "example-org", ""); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{
		"https://github.example/apps/cumin-chief-engineer/installations/new",
		"https://github.example/apps/cumin-implementer/installations/new",
	}
	if strings.Join(browser.installed, " ") != strings.Join(want, " ") {
		t.Errorf("opened %q, want %q", browser.installed, want)
	}
	for _, address := range want {
		if !strings.Contains(out.String(), address) {
			t.Errorf("the output does not show %s", address)
		}
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
