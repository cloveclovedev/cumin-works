package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/platform/github"
)

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

	wantNames := []string{"acme-cumin-core", "acme-cumin-planner", "acme-cumin-implementer", "acme-cumin-reviewer"}
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
