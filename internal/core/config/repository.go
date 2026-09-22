package config

// This file applies the settings that a target repository keeps in
// `.cumin/config.toml` on its default branch over the Host settings. The
// order of strength is the defaults, the Host settings file, then the
// repository file (docs/ja/requirements/cumin-core.md, the topic on
// settings). Only the rows that the settings table marks as "can be
// overridden by the repository" may come from a repository.

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// Settings of the Host that a repository cannot set: the target
// repositories, the poll, the work directory, the quota of the account, and
// the GitHub Apps whose private keys live on the Host. A name here covers
// its whole table.
var hostOnlySettings = []string{
	"repositories", "work_dir", "poll_interval", "max_issues_in_progress",
	"request_command", "quota", "github_apps",
}

// Keys of a role that a repository cannot set: the executable is a path of
// the Host, and the time limit follows the lifetime of an installation
// token.
var hostOnlyRoleSettings = []string{"cli_path", "time_limit"}

// Reasons in the errors of a repository file.
const (
	reasonHostOnly = "belongs to the Host settings file: a repository cannot set it"
	reasonUnknown  = "unknown key"
	reasonNotARole = "unknown agent role"
)

// repositoryFile mirrors the `.cumin/config.toml` of a target repository. A
// pointer is empty when the file does not hold the key, so that the Host
// value stays.
//
// protected_paths is a valid key that cumin ignores: the check of GitHub
// Actions and the Implementer instruction use it, and the starter file of
// scripts/setup-repo.sh holds only this key.
type repositoryFile struct {
	MaxReviewRounds     *int                      `toml:"max_review_rounds"`
	MaxCheckFixRequests *int                      `toml:"max_check_fix_requests"`
	MergeMethod         *string                   `toml:"merge_method"`
	Roles               map[string]repositoryRole `toml:"roles"`
	ProtectedPaths      []string                  `toml:"protected_paths"`
}

type repositoryRole struct {
	CLI   *string `toml:"cli"`
	Model *string `toml:"model"`
}

// WithRepository returns the effective settings of one target repository:
// the Host settings with the repository file applied over them. The Host
// settings are not changed.
//
// Every error names the key. The caller adds the repository and the path of
// the file. As in Load, a syntax or type error stops at the first key,
// while unknown keys, keys of the Host, and values outside their limits are
// reported together.
func (s *Settings) WithRepository(data []byte) (*Settings, error) {
	var f repositoryFile
	md, err := toml.Decode(string(data), &f)
	if err != nil {
		return nil, err
	}

	var errs []error
	fail := func(key, format string, args ...any) {
		errs = append(errs, fmt.Errorf("%s: %s", key, fmt.Sprintf(format, args...)))
	}
	for _, key := range undecodedKeys(md) {
		fail(key.name, "%s", key.reason)
	}

	// The copy shares what a repository cannot change; only the roles are
	// cloned, because a repository may set the CLI and the model of a role.
	effective := *s
	effective.Roles = maps.Clone(s.Roles)

	if f.MaxReviewRounds != nil {
		if *f.MaxReviewRounds < 1 {
			fail("max_review_rounds", limitAtLeastOne)
		} else {
			effective.MaxReviewRounds = *f.MaxReviewRounds
		}
	}
	if f.MaxCheckFixRequests != nil {
		if *f.MaxCheckFixRequests < 1 {
			fail("max_check_fix_requests", limitAtLeastOne)
		} else {
			effective.MaxCheckFixRequests = *f.MaxCheckFixRequests
		}
	}
	if f.MergeMethod != nil {
		switch method := MergeMethod(*f.MergeMethod); method {
		case MergeSquash, MergeMerge, MergeRebase:
			effective.MergeMethod = method
		default:
			fail("merge_method", limitMergeMethod, *f.MergeMethod)
		}
	}
	// Sorted, so that the same file always gives the same error text.
	for _, name := range slices.Sorted(maps.Keys(f.Roles)) {
		role, key := Role(name), "roles."+name
		if !slices.Contains(allRoles, role) {
			fail(key, reasonNotARole)
			continue
		}
		settings := effective.Roles[role]
		if cli := f.Roles[name].CLI; cli != nil {
			if *cli != CLIClaudeCode {
				fail(key+".cli", limitCLI, *cli, CLIClaudeCode)
			} else {
				settings.CLI = *cli
			}
		}
		if model := f.Roles[name].Model; model != nil {
			settings.Model = *model
		}
		effective.Roles[role] = settings
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return &effective, nil
}

// badKey is one key of a repository file that cumin does not accept.
type badKey struct {
	name   string
	reason string
}

// undecodedKeys tells the keys of the Host from the unknown ones. A key of
// the Host that names a table (quota, github_apps) is reported once, by the
// name of the table, because the parser reports the table and every key
// below it.
func undecodedKeys(md toml.MetaData) []badKey {
	var keys []badKey
	seen := map[string]bool{}
	add := func(name, reason string) {
		if !seen[name] {
			seen[name] = true
			keys = append(keys, badKey{name: name, reason: reason})
		}
	}
	for _, key := range md.Undecoded() {
		parts := []string(key)
		switch {
		case slices.Contains(hostOnlySettings, parts[0]):
			add(parts[0], reasonHostOnly)
		case parts[0] == "roles" && len(parts) >= 3 && slices.Contains(hostOnlyRoleSettings, parts[2]):
			add(strings.Join(parts[:3], "."), reasonHostOnly)
		case parts[0] == "roles" && len(parts) < 3:
			// The table of a role, not a setting of its own.
		default:
			add(key.String(), reasonUnknown)
		}
	}
	return keys
}
