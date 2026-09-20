// Package setup holds the setup commands of cumin. "cumin setup github-apps"
// registers the GitHub App of cumin itself and of each agent role with the
// GitHub App Manifest flow.
package setup

import (
	"fmt"
	"regexp"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/platform/github"
)

// maxAppNameLength is the limit of GitHub for the name of a GitHub App.
const maxAppNameLength = 34

// Apps lists the GitHub Apps in the order in which the command registers them.
var Apps = []string{
	config.AppCuminCore,
	string(config.RoleChiefEngineer),
	string(config.RoleImplementer),
	string(config.RoleReviewer),
}

var (
	orgPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)
	prefixPattern = regexp.MustCompile(`^[A-Za-z0-9-]*$`)
)

// Manifest is the GitHub App manifest that the browser sends to GitHub.
// It has no webhook: cumin polls GitHub. It asks for no user authorization:
// cumin never uses a token of a person.
type Manifest struct {
	Name               string            `json:"name"`
	URL                string            `json:"url"`
	RedirectURL        string            `json:"redirect_url"`
	Public             bool              `json:"public"`
	DefaultPermissions map[string]string `json:"default_permissions"`
}

// AppName returns the name of the GitHub App of one app key. App names are
// unique on all of GitHub, so an organization chooses its own prefix.
func AppName(prefix, app string) string {
	if app == config.AppCuminCore {
		return prefix + app // the key already starts with "cumin-"
	}
	return prefix + "cumin-" + app
}

// CheckNames reports the first name that GitHub would reject, before the
// command opens a browser page.
func CheckNames(org, prefix string) error {
	if !orgPattern.MatchString(org) {
		return fmt.Errorf("setup: %q is not a name of an organization", org)
	}
	if !prefixPattern.MatchString(prefix) {
		return fmt.Errorf("setup: the name prefix %q must have only letters, digits, and '-'", prefix)
	}
	for _, app := range Apps {
		if name := AppName(prefix, app); len(name) > maxAppNameLength {
			return fmt.Errorf("setup: the App name %q has %d characters, and GitHub allows %d. Use a shorter --name-prefix", name, len(name), maxAppNameLength)
		}
	}
	return nil
}

// BuildManifest returns the manifest of one App. The permissions come from the
// one permission table, so the registration and the tokens cannot differ.
func BuildManifest(org, prefix, app, redirectURL string) (Manifest, error) {
	permissions, ok := github.AppPermissions(app)
	if !ok {
		return Manifest{}, fmt.Errorf("setup: unknown app %q", app)
	}
	return Manifest{
		Name:               AppName(prefix, app),
		URL:                "https://github.com/" + org,
		RedirectURL:        redirectURL,
		Public:             false,
		DefaultPermissions: permissions,
	}, nil
}
