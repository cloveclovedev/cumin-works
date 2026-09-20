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
	path, updated, mode, err := prepareClientIDWrite(path, org, app, clientID)
	if err != nil {
		return err
	}
	return writeFileAtomically(path, []byte(updated), mode)
}

// CheckGitHubAppClientIDWritable reports if SetGitHubAppClientID can write a
// Client ID for the App, without a change to the file. The setup command calls
// it before it registers an App on GitHub, so that a settings file in a form
// that the command does not know stops the run before any change.
func CheckGitHubAppClientIDWritable(path, org, app string) error {
	_, _, _, err := prepareClientIDWrite(path, org, app, "Iv00placeholder")
	return err
}

// prepareClientIDWrite returns the file to write (after symbolic links), its
// new text, and its mode. It writes nothing.
func prepareClientIDWrite(path, org, app, clientID string) (string, string, fs.FileMode, error) {
	switch app {
	case AppCuminCore, string(RoleChiefEngineer), string(RoleImplementer), string(RoleReviewer):
	default:
		return "", "", 0, fmt.Errorf("set github_apps: unknown GitHub App name %q", app)
	}
	if !ownerPattern.MatchString(org) {
		return "", "", 0, fmt.Errorf("set github_apps: %q is not a name of an organization", org)
	}
	if !clientIDPattern.MatchString(clientID) {
		return "", "", 0, errors.New("set github_apps: the Client ID has an unexpected character")
	}

	// A person can keep the settings in another place and link to them. Write
	// the file behind the link, and keep the link.
	path, err := followLinks(path)
	if err != nil {
		return "", "", 0, err
	}

	mode := fs.FileMode(0o600)
	old, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		old = nil
	case err != nil:
		return "", "", 0, fmt.Errorf("read the Host settings: %w", err)
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
		return "", "", 0, fmt.Errorf("%s: cannot write github_apps.%s.%s safely (%w). Write this line by hand in the table [github_apps.%s]: %s = %q", path, org, app, err, org, app, clientID)
	}
	return path, updated, mode, nil
}

// followLinks returns the file that the path names after every symbolic link.
// It reads each link by itself, so it also works when the file behind the last
// link does not exist yet. filepath.EvalSymlinks fails in that case.
func followLinks(path string) (string, error) {
	// More links than any system follows. A loop of links ends here too.
	const maxLinks = 255
	for range maxLinks {
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&fs.ModeSymlink == 0 {
			return path, nil
		}
		target, err := os.Readlink(path)
		if err != nil {
			return "", fmt.Errorf("read the link of the Host settings: %w", err)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		path = target
	}
	// Never write to a path that is still a link: the write would replace the link.
	return "", errors.New("the Host settings file is behind too many symbolic links, or the links make a loop")
}

// setClientIDLine returns the text with the one line set. It knows only the
// table form [github_apps.<organization>] from docs/ja/development/configuration.md.
func setClientIDLine(text, org, app, clientID string) string {
	// Keep the line endings of the file. With CRLF, every line ends in "\r"
	// after the split on "\n".
	eol, cr := "\n", ""
	if strings.Contains(text, "\r\n") {
		eol, cr = "\r\n", "\r"
	}
	line := fmt.Sprintf("%s = %q", app, clientID)
	header := regexp.MustCompile(`^\s*\[\s*github_apps\s*\.\s*"?` + regexp.QuoteMeta(org) + `"?\s*\]\s*(#.*)?$`)
	anyHeader := regexp.MustCompile(`^\s*\[`)
	key := regexp.MustCompile(`^\s*"?` + regexp.QuoteMeta(app) + `"?\s*=`)

	lines := strings.Split(text, "\n")
	bare := func(i int) string { return strings.TrimSuffix(lines[i], "\r") }
	start := -1
	for i := range lines {
		if header.MatchString(bare(i)) {
			start = i
			break
		}
	}
	if start < 0 {
		var b strings.Builder
		b.WriteString(text)
		if text != "" && !strings.HasSuffix(text, "\n") {
			b.WriteString(eol)
		}
		if text != "" {
			b.WriteString(eol)
		}
		fmt.Fprintf(&b, "[github_apps.%s]%s%s%s", org, eol, line, eol)
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
		ending := strings.TrimPrefix(lines[i], bare(i)) // "\r" or nothing
		if m := value.FindStringSubmatch(bare(i)); m != nil {
			lines[i] = fmt.Sprintf("%s%q%s%s", m[1], clientID, m[2], ending)
		} else {
			lines[i] = line + ending // an unusual value; the check after this finds a problem
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
	// The last line of a file with no final line break has no "\r" of its own.
	if insert == len(lines) && cr != "" && !strings.HasSuffix(lines[insert-1], "\r") {
		lines[insert-1] += cr
		lines = append(lines, line)
		return strings.Join(lines, "\n")
	}
	lines = append(lines[:insert], append([]string{line + cr}, lines[insert:]...)...)
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
