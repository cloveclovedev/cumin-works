// Package github talks to GitHub for cumin. The types of GitHub (REST bodies,
// GraphQL responses) stop in this package.
package github

import (
	"maps"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
)

// Permission levels of a GitHub App repository permission.
const (
	permissionRead  = "read"
	permissionWrite = "write"
)

// appPermissions is the one table of the repository permissions of each
// GitHub App. It must match the table in
// docs/ja/development/github-app-setup.md. The setup command builds the App
// manifests from this table, and every installation token is limited to it.
//
// GitHub adds "metadata: read" by itself, so the table does not list it. No
// App has the Administration or the Workflows permission.
var appPermissions = map[string]map[string]string{
	config.AppCuminCore: {
		"contents":      permissionWrite, // merging a pull request needs contents: write
		"pull_requests": permissionWrite,
		"issues":        permissionWrite,
	},
	string(config.RolePlanner): {
		"issues":   permissionWrite,
		"contents": permissionRead,
	},
	string(config.RoleImplementer): {
		"contents":      permissionWrite,
		"pull_requests": permissionWrite,
		"issues":        permissionRead,
	},
	string(config.RoleReviewer): {
		"pull_requests": permissionWrite,
		"contents":      permissionRead,
		"issues":        permissionRead,
	},
}

// AppPermissions returns a copy of the repository permissions of one GitHub
// App. The name is config.AppCuminCore or an agent role. ok is false for an
// unknown name.
func AppPermissions(app string) (permissions map[string]string, ok bool) {
	p, ok := appPermissions[app]
	if !ok {
		return nil, false
	}
	return maps.Clone(p), true
}
