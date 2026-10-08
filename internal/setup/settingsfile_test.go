package setup

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/core/testenv"
)

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
	if want := "cumin-core cumin-planner cumin-implementer cumin-reviewer"; strings.Join(names, " ") != want {
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

// A key that exists is not enough: it must be a key, and GitHub must accept it
// for the Client ID. The check comes before any registration.
func TestSetupGitHubApps_BadStoredKeyStopsBeforeAnyRegistration(t *testing.T) {
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	for name, stored := range map[string][]byte{
		"not a key":            []byte("this is not PEM"),
		"a key of another App": pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(otherKey)}),
	} {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			browser := &fakeBrowser{org: "example-org"}
			store := &memoryStore{}
			service := newService(t, browser, store, &out)
			settings := "[github_apps.example-org]\nimplementer = \"Iv23liSTALE\"\n"
			if err := os.WriteFile(service.ConfigPath, []byte(settings), 0o600); err != nil {
				t.Fatal(err)
			}
			_ = store.SetBase64(context.Background(), "cumin-works", "github-app-private-key/Iv23liSTALE", stored)

			err := service.Run(context.Background(), "example-org", "")
			if err == nil || !strings.Contains(err.Error(), `"implementer"`) || !strings.Contains(err.Error(), "Iv23liSTALE") {
				t.Fatalf("err = %v, want an error that names the App and the client ID", err)
			}
			if strings.Contains(err.Error(), "PRIVATE KEY") {
				t.Errorf("the error holds the key")
			}
			if len(browser.manifests) != 0 || len(store.items) != 1 {
				t.Errorf("the run registered %d Apps and the store has %d items, want 0 and 1", len(browser.manifests), len(store.items))
			}
			if text, _ := os.ReadFile(service.ConfigPath); string(text) != settings {
				t.Errorf("the settings file changed: %s", text)
			}
		})
	}
}

// One App for two roles would give an agent the identity of another role, for
// example the identity that may bypass the ruleset of the default branch.
func TestSetupGitHubApps_SameClientIDForTwoRolesStops(t *testing.T) {
	var out bytes.Buffer
	browser := &fakeBrowser{org: "example-org"}
	service := newService(t, browser, &memoryStore{}, &out)
	if err := service.Run(context.Background(), "example-org", ""); err != nil {
		t.Fatalf("first run: %v", err)
	}
	apps, _ := config.ReadGitHubApps(service.ConfigPath)
	core := apps["example-org"]["cumin-core"]
	if err := config.SetGitHubAppClientID(service.ConfigPath, "example-org", "implementer", core); err != nil {
		t.Fatal(err)
	}

	err := service.Run(context.Background(), "example-org", "")
	if err == nil || !strings.Contains(err.Error(), `"cumin-core" and "implementer"`) || !strings.Contains(err.Error(), core) {
		t.Fatalf("err = %v, want an error that names both Apps and the client ID", err)
	}
	if len(browser.manifests) != 4 {
		t.Errorf("the second run registered an App: %d manifests", len(browser.manifests))
	}
}

// The command cannot write into this form of the settings file. It must find
// that out before GitHub registers an App, not after.
func TestSetupGitHubApps_SettingsFileThatCannotBeWrittenStopsBeforeAnyRegistration(t *testing.T) {
	var out bytes.Buffer
	browser := &fakeBrowser{org: "example-org"}
	store := &memoryStore{}
	service := newService(t, browser, store, &out)
	settings := "work_dir = \"/tmp/w\"\ngithub_apps = { other-org = { reviewer = \"Iv23liOTHER\" } }\n"
	if err := os.WriteFile(service.ConfigPath, []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}

	err := service.Run(context.Background(), "example-org", "")
	if err == nil || !strings.Contains(err.Error(), "nothing is changed") {
		t.Fatalf("err = %v, want an error before any change", err)
	}
	if len(browser.manifests) != 0 || len(store.items) != 0 {
		t.Errorf("the run registered %d Apps and stored %d keys, want none", len(browser.manifests), len(store.items))
	}
	if text, _ := os.ReadFile(service.ConfigPath); string(text) != settings {
		t.Errorf("the settings file changed: %s", text)
	}
}

// The command cannot write the Client ID into a settings directory that is
// not writable. It must find that out before GitHub registers an App.
func TestSetupGitHubApps_SettingsDirectoryThatCannotBeWrittenStopsBeforeAnyRegistration(t *testing.T) {
	if os.Geteuid() == 0 {
		testenv.SkipOrFail(t, "the test user is root, and root ignores the permission bits of a directory")
	}
	var out bytes.Buffer
	browser := &fakeBrowser{org: "example-org"}
	store := &memoryStore{}
	service := newService(t, browser, store, &out)
	dir := filepath.Dir(service.ConfigPath)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	err := service.Run(context.Background(), "example-org", "")
	if err == nil || !strings.Contains(err.Error(), "nothing is changed") || !strings.Contains(err.Error(), dir) {
		t.Fatalf("err = %v, want an error before any change that names the directory", err)
	}
	if len(browser.manifests) != 0 || len(store.items) != 0 {
		t.Errorf("the run registered %d Apps and stored %d keys, want none", len(browser.manifests), len(store.items))
	}
}
