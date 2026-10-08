package setup

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
)

// The client IDs of two roles are swapped. Every key is valid, the IDs differ,
// and the owner is right, but the implementer would act as cumin-core.
func TestSetupGitHubApps_SwappedClientIDsStop(t *testing.T) {
	var out bytes.Buffer
	browser := &fakeBrowser{org: "example-org"}
	service := newService(t, browser, &memoryStore{}, &out)
	if err := service.Run(context.Background(), "example-org", ""); err != nil {
		t.Fatalf("first run: %v", err)
	}
	apps, _ := config.ReadGitHubApps(service.ConfigPath)
	core, implementer := apps["example-org"]["cumin-core"], apps["example-org"]["implementer"]
	for app, clientID := range map[string]string{"cumin-core": implementer, "implementer": core} {
		if err := config.SetGitHubAppClientID(service.ConfigPath, "example-org", app, clientID); err != nil {
			t.Fatal(err)
		}
	}

	err := service.Run(context.Background(), "example-org", "")
	if err == nil || !strings.Contains(err.Error(), `app "cumin-core"`) || !strings.Contains(err.Error(), "belongs to another role") {
		t.Fatalf("err = %v, want an error about the role of the client ID", err)
	}
	if len(browser.manifests) != 4 || len(browser.installed) != 4 {
		t.Errorf("the second run changed something: %d manifests, %d install pages", len(browser.manifests), len(browser.installed))
	}
}

// A valid Client ID and key of an App that another account owns is not a
// registration for this organization.
func TestSetupGitHubApps_AppOfAnotherMaintainerStops(t *testing.T) {
	var out bytes.Buffer
	browser := &fakeBrowser{org: "example-org"}
	service := newService(t, browser, &memoryStore{}, &out)
	if err := service.Run(context.Background(), "example-org", ""); err != nil {
		t.Fatalf("first run: %v", err)
	}

	browser.gitHub.mu.Lock()
	browser.gitHub.owner = "another-org"
	browser.gitHub.mu.Unlock()
	err := service.Run(context.Background(), "example-org", "")
	if err == nil || !strings.Contains(err.Error(), `belongs to "another-org"`) {
		t.Fatalf("err = %v, want an error that names the owner", err)
	}
}

// GitHub account names ignore case. A second run with another spelling finds
// the Apps of the first run, and the file keeps one table.
func TestSetupGitHubApps_OrganizationNameIgnoresCase(t *testing.T) {
	var out bytes.Buffer
	browser := &fakeBrowser{org: "Example-Org"}
	service := newService(t, browser, &memoryStore{}, &out)
	if err := service.Run(context.Background(), "Example-Org", ""); err != nil {
		t.Fatalf("first run: %v", err)
	}

	if err := service.Run(context.Background(), "example-org", ""); err != nil {
		t.Fatalf("second run with another spelling: %v", err)
	}
	if len(browser.manifests) != 4 {
		t.Errorf("the browser saw %d manifests over both runs, want 4", len(browser.manifests))
	}
	apps, _ := config.ReadGitHubApps(service.ConfigPath)
	if len(apps) != 1 || len(apps["Example-Org"]) != 4 {
		t.Errorf("github_apps = %v, want one table with four Apps", apps)
	}

	// Two tables that differ only in case: the command does not choose one.
	text, _ := os.ReadFile(service.ConfigPath)
	text = append(text, []byte("\n[github_apps.EXAMPLE-ORG]\nreviewer = \"Iv23liOTHER\"\n")...)
	if err := os.WriteFile(service.ConfigPath, text, 0o600); err != nil {
		t.Fatal(err)
	}
	err := service.Run(context.Background(), "example-org", "")
	if err == nil || !strings.Contains(err.Error(), "EXAMPLE-ORG, Example-Org") {
		t.Errorf("err = %v, want an error that names both tables", err)
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
		"https://github.example/apps/cumin-planner/installations/new",
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
	// "cumin-implementer" has 17 characters, the longest of the four App
	// names, so a prefix of 18 is too long.
	for name, c := range map[string]struct{ org, prefix string }{
		"name longer than 34 characters": {"example-org", "seventeen-chars---"},
		"prefix with a space":            {"example-org", "my org-"},
		"organization with a slash":      {"example/org", ""},
		"empty organization":             {"", ""},
	} {
		if err := CheckNames(c.org, c.prefix); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if got := len(AppName("seventeen-chars--", "implementer")); got != 34 {
		t.Errorf("the longest valid name has %d characters, want 34", got)
	}
	if err := CheckNames("example-org", "seventeen-chars--"); err != nil {
		t.Errorf("a name of exactly 34 characters: %v", err)
	}
}
