package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

var (
	ownerPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)
	clientIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	// A line that starts with the key github_apps: a dotted key or an inline table.
	otherGitHubAppsForm = regexp.MustCompile(`(?m)^\s*"?github_apps"?\s*[.=]`)
)

// ReadGitHubApps returns the github_apps tables of the Host settings file:
// organization, then App name, then Client ID. A missing file gives an empty
// map.
//
// Load checks the whole file. This function does not, because
// "cumin setup github-apps" runs while the other settings can be incomplete.
func ReadGitHubApps(path string) (map[string]map[string]string, error) {
	text, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the Host settings: %w", err)
	}
	var f struct {
		GitHubApps map[string]map[string]string `toml:"github_apps"`
	}
	if _, err := toml.Decode(string(text), &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if f.GitHubApps == nil {
		f.GitHubApps = map[string]map[string]string{}
	}
	return f.GitHubApps, nil
}

// SetGitHubAppClientID writes one Client ID into the table
// [github_apps.<organization>] of the Host settings file. It creates the file
// and the table when they do not exist.
//
// A person edits this file, so the function changes the text of one line and
// keeps every comment and every other line. A TOML encoder would drop the
// comments. Before it writes, the function parses the new text and checks
// that only this one value differs. It refuses a file that it cannot change
// safely, and the person then writes the line by hand.
func SetGitHubAppClientID(path, org, app, clientID string) error {
	switch app {
	case AppCuminCore, string(RoleChiefEngineer), string(RoleImplementer), string(RoleReviewer):
	default:
		return fmt.Errorf("set github_apps: unknown GitHub App name %q", app)
	}
	if !ownerPattern.MatchString(org) {
		return fmt.Errorf("set github_apps: %q is not a name of an organization", org)
	}
	if !clientIDPattern.MatchString(clientID) {
		return errors.New("set github_apps: the Client ID has an unexpected character")
	}

	// A person can keep the settings in another place and link to them. Write
	// the file behind the link, and keep the link.
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}

	mode := fs.FileMode(0o600)
	old, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		old = nil
	case err != nil:
		return fmt.Errorf("read the Host settings: %w", err)
	default:
		if info, err := os.Stat(path); err == nil {
			mode = info.Mode().Perm()
		}
	}

	updated := setClientIDLine(string(old), org, app, clientID)
	err = checkOnlyThisValueChanged(string(old), updated, org, app, clientID)
	if err == nil && otherGitHubAppsForm.MatchString(string(old)) {
		// The parser accepts a [github_apps.<organization>] table after a
		// dotted key, but TOML does not allow to define a table two times.
		err = errors.New("the file writes github_apps as a dotted key or as an inline table")
	}
	if err != nil {
		return fmt.Errorf("%s: cannot write github_apps.%s.%s safely (%w). Write this line by hand in the table [github_apps.%s]: %s = %q", path, org, app, err, org, app, clientID)
	}
	return writeFileAtomically(path, []byte(updated), mode)
}

// setClientIDLine returns the text with the one line set. It knows only the
// table form [github_apps.<organization>] from docs/ja/development/configuration.md.
func setClientIDLine(text, org, app, clientID string) string {
	line := fmt.Sprintf("%s = %q", app, clientID)
	header := regexp.MustCompile(`^\s*\[\s*github_apps\s*\.\s*"?` + regexp.QuoteMeta(org) + `"?\s*\]\s*(#.*)?$`)
	anyHeader := regexp.MustCompile(`^\s*\[`)
	key := regexp.MustCompile(`^\s*"?` + regexp.QuoteMeta(app) + `"?\s*=`)

	lines := strings.Split(text, "\n")
	start := -1
	for i, l := range lines {
		if header.MatchString(l) {
			start = i
			break
		}
	}
	if start < 0 {
		var b strings.Builder
		b.WriteString(text)
		if text != "" && !strings.HasSuffix(text, "\n") {
			b.WriteString("\n")
		}
		if text != "" {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "[github_apps.%s]\n%s\n", org, line)
		return b.String()
	}

	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if anyHeader.MatchString(lines[i]) {
			end = i
			break
		}
	}
	// Replace only the value, and keep the text around it, such as a comment
	// at the end of the line.
	value := regexp.MustCompile(`^(\s*"?` + regexp.QuoteMeta(app) + `"?\s*=\s*)(?:"[^"]*"|'[^']*')(\s*(?:#.*)?)$`)
	for i := start + 1; i < end; i++ {
		if !key.MatchString(lines[i]) {
			continue
		}
		if m := value.FindStringSubmatch(lines[i]); m != nil {
			lines[i] = fmt.Sprintf("%s%q%s", m[1], clientID, m[2])
		} else {
			lines[i] = line // an unusual value; the check after this finds a problem
		}
		return strings.Join(lines, "\n")
	}
	// Put the new line after the last line of the table that is not blank.
	insert := start + 1
	for i := start + 1; i < end; i++ {
		if strings.TrimSpace(lines[i]) != "" {
			insert = i + 1
		}
	}
	lines = append(lines[:insert], append([]string{line}, lines[insert:]...)...)
	return strings.Join(lines, "\n")
}

// checkOnlyThisValueChanged parses both texts and compares every value.
func checkOnlyThisValueChanged(old, updated, org, app, clientID string) error {
	var before, after map[string]any
	if _, err := toml.Decode(old, &before); err != nil {
		return fmt.Errorf("the file is not valid TOML: %w", err)
	}
	if _, err := toml.Decode(updated, &after); err != nil {
		return errors.New("the file uses a form of github_apps that this command does not know")
	}
	apps, _ := after["github_apps"].(map[string]any)
	table, _ := apps[org].(map[string]any)
	if table[app] != clientID {
		return errors.New("the new value is not where it must be")
	}

	// Make the same change in the parsed old file. The rest must be equal.
	if before == nil {
		before = map[string]any{}
	}
	oldApps, _ := before["github_apps"].(map[string]any)
	if oldApps == nil {
		oldApps = map[string]any{}
		before["github_apps"] = oldApps
	}
	oldTable, _ := oldApps[org].(map[string]any)
	if oldTable == nil {
		oldTable = map[string]any{}
		oldApps[org] = oldTable
	}
	oldTable[app] = clientID
	if !reflect.DeepEqual(before, after) {
		return errors.New("another setting would change")
	}
	return nil
}

// writeFileAtomically writes a temporary file in the same directory and
// renames it, so a stop in the middle leaves the old file as it was.
func writeFileAtomically(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create the settings directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.toml")
	if err != nil {
		return fmt.Errorf("write the Host settings: %w", err)
	}
	defer os.Remove(tmp.Name()) // no effect after the rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write the Host settings: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("write the Host settings: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write the Host settings: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write the Host settings: %w", err)
	}
	return nil
}
