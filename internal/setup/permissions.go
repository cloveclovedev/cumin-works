package setup

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/platform/github"
)

// DefaultPollInterval is how often the command reads GitHub again while it
// waits for a change that the person makes in the browser.
const DefaultPollInterval = 5 * time.Second

// permissionChange is a registered App whose permissions differ from the
// permission table of its role.
type permissionChange struct {
	app  string
	slug string
	cred github.AppCredentials
	have map[string]string
	want map[string]string
}

// roleOf returns the role whose permission table equals the permissions, or ""
// when no role has them.
func roleOf(permissions map[string]string) string {
	for _, app := range Apps {
		if want, _ := github.AppPermissions(app); maps.Equal(permissions, want) {
			return app
		}
	}
	return ""
}

// adds reports whether want grants more than have: a new permission, or write
// in place of read. GitHub applies a removed permission at once, and an added
// one only after the account of the installation approves it.
func adds(have, want map[string]string) bool {
	for name, level := range want {
		if have[name] == "" || (have[name] == "read" && level == "write") {
			return true
		}
	}
	return false
}

// describe returns the difference in one line, for example
// "contents: read -> write".
func describe(have, want map[string]string) string {
	names := slices.Sorted(maps.Keys(want))
	for name := range have {
		if _, ok := want[name]; !ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	var parts []string
	for _, name := range names {
		if have[name] != want[name] {
			parts = append(parts, fmt.Sprintf("%s: %s -> %s", name, level(have[name]), level(want[name])))
		}
	}
	return strings.Join(parts, ", ")
}

func level(value string) string {
	if value == "" {
		return "none"
	}
	return value
}

// AppPermissionsURL returns the page where the owner of an App changes its
// permissions.
func AppPermissionsURL(gitHubURL, org, slug string) string {
	return strings.TrimRight(gitHubURL, "/") + "/organizations/" + org + "/settings/apps/" + slug + "/permissions"
}

// changePermissions leads the person through the change of one App: first the
// registration of the App, then, for an added permission, the approval of the
// installation on the organization. GitHub has no API for either, so the
// command opens each page and reads GitHub until the change is there.
func (s *Service) changePermissions(ctx context.Context, org string, change permissionChange) error {
	page := AppPermissionsURL(s.GitHubURL, org, change.slug)
	fmt.Fprintf(s.Out, "The App %s needs other permissions: %s. Change them on this page and save: %s\n", change.slug, describe(change.have, change.want), page)
	s.open(page)
	err := s.waitFor(ctx, "change of the permissions of "+change.slug, func() (bool, error) {
		info, err := s.GitHub.GetApp(ctx, change.cred)
		return maps.Equal(info.Permissions, change.want), err
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(s.Out, "changed the permissions of %s\n", change.slug)

	if !adds(change.have, change.want) {
		return nil
	}
	// An App that is not installed asks for its permissions on the
	// installation page, which the command opens later.
	installed, err := s.GitHub.IsInstalledOn(ctx, change.cred, org)
	if err != nil || !installed {
		return err
	}
	installation, err := s.GitHub.GetOrgInstallation(ctx, change.cred, org)
	if err != nil {
		return err
	}
	if !maps.Equal(installation.Permissions, change.want) {
		fmt.Fprintf(s.Out, "Approve the new permissions of %s on %s: %s\n", change.slug, org, installation.HTMLURL)
		s.open(installation.HTMLURL)
		err := s.waitFor(ctx, "approval of the permissions of "+change.slug+" on "+org, func() (bool, error) {
			installation, err := s.GitHub.GetOrgInstallation(ctx, change.cred, org)
			return maps.Equal(installation.Permissions, change.want), err
		})
		if err != nil {
			return err
		}
	}
	fmt.Fprintf(s.Out, "the installation of %s on %s has the new permissions\n", change.slug, org)
	return nil
}

// waitFor calls done until it reports true, an error, the confirmation
// timeout, or the end of ctx.
func (s *Service) waitFor(ctx context.Context, what string, done func() (bool, error)) error {
	timeout := s.ConfirmTimeout
	if timeout <= 0 {
		timeout = DefaultConfirmTimeout
	}
	interval := s.PollInterval
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	deadline := time.After(timeout)
	for {
		ok, err := done()
		if err != nil || ok {
			return err
		}
		select {
		case <-time.After(interval):
		case <-deadline:
			return fmt.Errorf("no %s within %s", what, timeout)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// open opens a page in the browser. The address is in the output already, so
// a browser that does not open is not an error.
func (s *Service) open(page string) {
	if err := s.OpenBrowser(page); err != nil {
		fmt.Fprintf(s.Out, "The browser did not open (%v). Open the address above by hand.\n", err)
	}
}
