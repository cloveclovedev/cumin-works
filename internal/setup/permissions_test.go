package setup

import (
	"bytes"
	"context"
	"io"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/platform/github"
)

// installedAfterOneRun registers the four Apps, installs each on the
// organization, and returns the service with the browser and the fake.
func installedAfterOneRun(t *testing.T, out io.Writer) (*Service, *fakeBrowser, *fakeGitHub) {
	t.Helper()
	browser := &fakeBrowser{org: "example-org"}
	service := newService(t, browser, &memoryStore{}, out)
	if err := service.Run(context.Background(), "example-org", ""); err != nil {
		t.Fatalf("first run: %v", err)
	}
	fake := browser.gitHub
	fake.mu.Lock()
	for _, slug := range fake.slugs {
		fake.installed[slug] = "example-org"
	}
	fake.mu.Unlock()
	browser.mu.Lock()
	browser.installed = nil
	browser.mu.Unlock()
	return service, browser, fake
}

// setPermissions plays GitHub after a change of the registration (perms) or
// an approval of the installation (approved).
func setPermissions(fake *fakeGitHub, table map[string]map[string]string, slug string, permissions map[string]string) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	table[slug] = maps.Clone(permissions)
}

// The permission table grew: the App page, then the installation page, and
// the command waits for each change on GitHub.
func TestSetupGitHubApps_LeadsThroughAnAddedPermission(t *testing.T) {
	var out bytes.Buffer
	service, browser, fake := installedAfterOneRun(t, &out)
	want, _ := github.AppPermissions("planner")
	old := map[string]string{"issues": "write"} // the table of no role
	setPermissions(fake, fake.perms, "cumin-planner", old)
	setPermissions(fake, fake.approved, "cumin-planner", old)

	appPage := AppPermissionsURL("https://github.example", "example-org", "cumin-planner")
	installationPage := "https://github.example/organizations/example-org/settings/installations/7"
	browser.onPage = func(address string) {
		switch address {
		case appPage:
			setPermissions(fake, fake.perms, "cumin-planner", want)
		case installationPage:
			setPermissions(fake, fake.approved, "cumin-planner", want)
		}
	}
	if err := service.Run(context.Background(), "example-org", ""); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	if got := strings.Join(browser.installed, " "); got != appPage+" "+installationPage {
		t.Errorf("opened %q, want the App page, then the installation page", got)
	}
	for _, line := range []string{"contents: none -> " + want["contents"], appPage, installationPage, "the installation of cumin-planner on example-org has the new permissions"} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("the output does not show %q:\n%s", line, out.String())
		}
	}

	// Nothing to change: a second run opens no page.
	browser.installed = nil
	if err := service.Run(context.Background(), "example-org", ""); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(browser.installed) != 0 {
		t.Errorf("the second run opened %q", browser.installed)
	}
}

// GitHub applies a removed permission at once: no approval to wait for.
func TestSetupGitHubApps_RemovedPermissionNeedsNoApproval(t *testing.T) {
	var out bytes.Buffer
	service, browser, fake := installedAfterOneRun(t, &out)
	want, _ := github.AppPermissions("planner")
	old := maps.Clone(want)
	old["pull_requests"] = "read"
	setPermissions(fake, fake.perms, "cumin-planner", old)
	setPermissions(fake, fake.approved, "cumin-planner", old)
	appPage := AppPermissionsURL("https://github.example", "example-org", "cumin-planner")
	browser.onPage = func(address string) {
		if address == appPage {
			setPermissions(fake, fake.perms, "cumin-planner", want)
		}
	}
	if err := service.Run(context.Background(), "example-org", ""); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.Join(browser.installed, " "); got != appPage {
		t.Errorf("opened %q, want only the App page", got)
	}
	if !strings.Contains(out.String(), "pull_requests: read -> none") {
		t.Errorf("the output does not name the removed permission:\n%s", out.String())
	}
}

func TestSetupGitHubApps_StopsWhenThePermissionsDoNotChange(t *testing.T) {
	var out bytes.Buffer
	service, _, fake := installedAfterOneRun(t, &out)
	setPermissions(fake, fake.perms, "cumin-planner", map[string]string{"issues": "write"})
	service.ConfirmTimeout = 50 * time.Millisecond

	err := service.Run(context.Background(), "example-org", "")
	if err == nil || !strings.Contains(err.Error(), `app "planner"`) || !strings.Contains(err.Error(), "no change of the permissions of cumin-planner") {
		t.Fatalf("err = %v, want a timeout that names the App", err)
	}
}

func TestDescribe_NamesEachChangedPermission(t *testing.T) {
	have := map[string]string{"contents": "read", "pull_requests": "write"}
	want := map[string]string{"contents": "write", "issues": "write"}
	if got := describe(have, want); got != "contents: read -> write, issues: none -> write, pull_requests: write -> none" {
		t.Errorf("describe = %q", got)
	}
	if !adds(have, want) || adds(want, map[string]string{"contents": "read"}) {
		t.Error("adds does not tell an added permission from a removed one")
	}
}

// The permission table of cumin-core grew by the three read permissions of
// the checks. An App that is registered with the table before them gets the
// guided change: the App page, then the installation page. The command
// writes no permission itself: GitHub holds the old ones until the person
// changes them on each page.
func TestSetupGitHubApps_LeadsCuminCoreThroughTheReadPermissionsOfTheChecks(t *testing.T) {
	var out bytes.Buffer
	service, browser, fake := installedAfterOneRun(t, &out)
	want, _ := github.AppPermissions("cumin-core")
	old := map[string]string{"contents": "write", "pull_requests": "write", "issues": "write"}
	setPermissions(fake, fake.perms, "cumin-core", old)
	setPermissions(fake, fake.approved, "cumin-core", old)

	appPage := AppPermissionsURL("https://github.example", "example-org", "cumin-core")
	pages := 0
	browser.onPage = func(address string) {
		fake.mu.Lock()
		registered, approved := maps.Clone(fake.perms["cumin-core"]), maps.Clone(fake.approved["cumin-core"])
		fake.mu.Unlock()
		pages++
		switch {
		case address == appPage:
			if !maps.Equal(registered, old) || !maps.Equal(approved, old) {
				t.Errorf("before the App page: registered %v, approved %v, want the old permissions", registered, approved)
			}
			setPermissions(fake, fake.perms, "cumin-core", want)
		case strings.Contains(address, "/settings/installations/"):
			if !maps.Equal(approved, old) {
				t.Errorf("before the installation page: approved %v, want the old permissions", approved)
			}
			setPermissions(fake, fake.approved, "cumin-core", want)
		}
	}
	if err := service.Run(context.Background(), "example-org", ""); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	if pages != 2 || len(browser.installed) != 2 || browser.installed[0] != appPage {
		t.Errorf("opened %q, want the App page of cumin-core, then its installation page", browser.installed)
	}
	for _, line := range []string{"actions: none -> read, checks: none -> read, statuses: none -> read",
		"the installation of cumin-core on example-org has the new permissions"} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("the output does not show %q:\n%s", line, out.String())
		}
	}
}
