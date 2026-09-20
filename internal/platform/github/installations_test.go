package github

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// appServer answers GET /app and GET /app/installations for an App that has
// `count` installations. The last one is on the account "Example-Org".
func appServer(t *testing.T, count int) (*httptest.Server, *[]string) {
	t.Helper()
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("%s has no JWT", r.URL.Path)
		}
		requests = append(requests, r.URL.RequestURI())
		switch r.URL.Path {
		case "/app":
			writeJSON(w, http.StatusOK, map[string]any{
				"slug": "acme-cumin-core", "html_url": "https://github.com/apps/acme-cumin-core",
				"owner":       map[string]any{"login": "Example-Org"},
				"permissions": map[string]any{"metadata": "read", "contents": "write", "issues": "write", "pull_requests": "write"},
			})
		case "/app/installations":
			var page int
			fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
			installations := []map[string]any{}
			for i := (page - 1) * 100; i < page*100 && i < count; i++ {
				login := fmt.Sprintf("account-%d", i)
				if i == count-1 {
					login = "Example-Org"
				}
				installations = append(installations, map[string]any{"account": map[string]any{"login": login}})
			}
			writeJSON(w, http.StatusOK, installations)
		default:
			writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		}
	}))
	t.Cleanup(server.Close)
	return server, &requests
}

func TestGetApp_ReturnsTheSlugAndTheInstallPage(t *testing.T) {
	server, _ := appServer(t, 0)
	client := NewAppClient(server.URL, server.Client())

	info, err := client.GetApp(context.Background(), testCredentials())
	if err != nil {
		t.Fatalf("GetApp: %v", err)
	}
	if info.Slug != "acme-cumin-core" || info.InstallURL() != "https://github.com/apps/acme-cumin-core/installations/new" {
		t.Errorf("info = %+v, install page = %s", info, info.InstallURL())
	}
	// "metadata" is not in the table of cumin, so GetApp drops it.
	want, _ := AppPermissions("cumin-core")
	if info.Owner != "Example-Org" || !maps.Equal(info.Permissions, want) {
		t.Errorf("owner = %q, permissions = %v, want %v", info.Owner, info.Permissions, want)
	}
}

func TestIsInstalledOn_ReadsEveryPageAndIgnoresCase(t *testing.T) {
	// 150 installations: the account is on the second page.
	server, requests := appServer(t, 150)
	client := NewAppClient(server.URL, server.Client())

	installed, err := client.IsInstalledOn(context.Background(), testCredentials(), "example-org")
	if err != nil || !installed {
		t.Errorf("installed = %v, err = %v, want true", installed, err)
	}
	if len(*requests) != 2 || !strings.Contains((*requests)[1], "page=2") {
		t.Errorf("requests = %v, want two pages", *requests)
	}

	installed, err = client.IsInstalledOn(context.Background(), testCredentials(), "missing-org")
	if err != nil || installed {
		t.Errorf("missing account: installed = %v, err = %v, want false", installed, err)
	}
}

func TestIsInstalledOn_NoInstallation(t *testing.T) {
	server, _ := appServer(t, 0)
	client := NewAppClient(server.URL, server.Client())

	installed, err := client.IsInstalledOn(context.Background(), testCredentials(), "example-org")
	if err != nil || installed {
		t.Errorf("installed = %v, err = %v, want false", installed, err)
	}
}
