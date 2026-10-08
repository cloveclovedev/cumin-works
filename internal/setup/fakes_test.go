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
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

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
	codes     map[string]string            // code -> App name from the manifest
	pems      map[string][]byte            // client ID -> PEM that the fake returned
	keys      map[string]*rsa.PrivateKey   // client ID -> key of the App
	slugs     map[string]string            // client ID -> slug
	perms     map[string]map[string]string // App name -> permissions from the manifest
	installed map[string]string            // slug -> account that has an installation
	approved  map[string]map[string]string // slug -> permissions that the installation approved; perms when missing
	owner     string                       // the account that owns every App; "example-org" when empty
}

func newFakeGitHub(t *testing.T) (*fakeGitHub, *httptest.Server) {
	t.Helper()
	fake := &fakeGitHub{t: t, codes: map[string]string{}, pems: map[string][]byte{}, keys: map[string]*rsa.PrivateKey{}, slugs: map[string]string{}, perms: map[string]map[string]string{}, installed: map[string]string{}, approved: map[string]map[string]string{}}
	server := httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(server.Close)
	return fake, server
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/app" || r.URL.Path == "/app/installations" || strings.HasPrefix(r.URL.Path, "/orgs/") {
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
		owner := f.owner
		if owner == "" {
			owner = "example-org"
		}
		permissions := map[string]string{"metadata": "read"} // GitHub adds this one
		maps.Copy(permissions, f.perms[slug])
		_ = json.NewEncoder(w).Encode(map[string]any{"slug": slug, "html_url": "https://github.example/apps/" + slug, "owner": map[string]any{"login": owner}, "permissions": permissions})
		return
	}
	if org, ok := strings.CutPrefix(r.URL.Path, "/orgs/"); ok {
		org = strings.TrimSuffix(org, "/installation")
		if !strings.EqualFold(f.installed[slug], org) {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		approved, ok := f.approved[slug]
		if !ok {
			approved = f.perms[slug]
		}
		permissions := map[string]string{"metadata": "read"}
		maps.Copy(permissions, approved)
		_ = json.NewEncoder(w).Encode(map[string]any{"html_url": "https://github.example/organizations/" + org + "/settings/installations/7", "permissions": permissions})
		return
	}
	installations := []map[string]any{}
	if account, ok := f.installed[slug]; ok {
		installations = append(installations, map[string]any{"account": map[string]any{"login": account}})
	}
	_ = json.NewEncoder(w).Encode(installations)
}

// newCode plays the part of GitHub after the person selects "Create GitHub App".
func (f *fakeGitHub) newCode(appName string, permissions map[string]string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.perms[appName] = permissions
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
	wrongFor  string               // an App name that gets a callback with a wrong state
	reloadFor string               // an App name whose callback tab the person loads again
	stopAfter int                  // when > 0, the browser does nothing after this many Apps
	onPage    func(address string) // the person acts on a GitHub page that the command opened

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
		onPage := b.onPage
		b.mu.Unlock()
		if onPage != nil {
			onPage(startURL)
		}
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

	code := b.gitHub.newCode(manifest.Name, manifest.DefaultPermissions)
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
		PollInterval:   10 * time.Millisecond,
	}
}
