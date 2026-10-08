package agent

// This file is the check of the start record of Claude Code: the init
// event of the stream. It reports context from outside the work directory
// and a missing skill of cumin, and names the shape of a JSON field
// without its values. claudecode.go runs the CLI and calls the check.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// userContext reports why the init event shows context from outside the
// work directory, or "" when it shows none. Checked: plugins that are not
// built in (builtinSuffix) and MCP servers (empty with --setting-sources project,
// measured-constraints.md row 27), and
// memory_paths (absent when auto memory is off, measured-constraints.md
// row 86; the live record of #67). plugins and mcp_servers must be present: a
// record without them cannot confirm that nothing was loaded. The init event
// lists no instruction files, so instructions cannot be checked here. The
// reason names the field, not the paths.
func userContext(e event, workDir string, wantSkills []string) string {
	// A missing field cannot confirm that nothing was loaded. Safe side.
	if e.Plugins == nil {
		return "the init event has no plugins field"
	}
	if e.MCPServers == nil {
		return "the init event has no mcp_servers field"
	}
	if reason := otherPlugins(e.Plugins); reason != "" {
		return reason
	}
	if jsonPresent(e.MCPServers) {
		return "the init event lists MCP servers"
	}
	if jsonPresent(e.MemoryPaths) {
		paths := jsonStrings(e.MemoryPaths)
		if len(paths) == 0 {
			// A shape without paths cannot be checked. Safe side.
			return "the init event has memory_paths of an unknown shape"
		}
		for _, path := range paths {
			if !underDir(path, workDir) {
				return "the init event has memory_paths outside the work directory"
			}
		}
	}
	return missingSkill(e, wantSkills)
}

// otherPlugins reports why the plugins of the init event hold one that is
// not built in, or "" when they hold none. The reason names the source of
// that plugin, so that the Operator sees what loaded; a source is an id such
// as "context7@claude-plugins-official", never a path. A list that cumin
// cannot read, or an entry without source, confirms nothing and stops the
// run, as a missing field does.
func otherPlugins(raw json.RawMessage) string {
	// Only a list is the shape of the field; null, an object, or any other
	// value is a change of Claude Code that cumin cannot read. Safe side.
	var plugins []struct {
		Source *string `json:"source"`
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &plugins) != nil {
		return "the init event has plugins of an unknown shape"
	}
	for _, plugin := range plugins {
		if plugin.Source == nil {
			return "the init event lists plugins: one has no source"
		}
		name, found := strings.CutSuffix(*plugin.Source, builtinSuffix)
		if !found || name == "" {
			return fmt.Sprintf("the init event lists plugins: %q", *plugin.Source)
		}
	}
	return ""
}

// missingSkill reports which skill of cumin the init event does not list,
// or "" when it lists them all. cumin writes the skills of a role into one
// directory and passes it with --add-dir, so a skill that is not offered
// means that the agent would write a text of GitHub from memory instead of
// from the template.
//
// The field must be present, as plugins and mcp_servers must: a record
// that cumin cannot read confirms nothing. The field is a list of the
// names of the skills, measured on 2026-09-25 with Claude Code 2.1.273
// (the record on #166); the official documentation does not describe it.
// Any other shape is unreadable and ends the run, as a missing plugins
// field does.
func missingSkill(e event, want []string) string {
	if e.Skills == nil {
		return "the init event has no skills field"
	}
	if len(want) == 0 {
		return ""
	}
	got, ok := jsonStringList(e.Skills)
	if !ok {
		// A shape that cumin cannot read cannot confirm anything.
		return "the init event has skills of an unknown shape"
	}
	for _, name := range want {
		if !slices.Contains(got, name) {
			return "the init event does not list the skill " + name
		}
	}
	return ""
}

// wantSkills are the skills that the start record of this request must
// list: those of its role, when cumin passed a directory of skills.
func wantSkills(req Request) []string {
	if req.SkillsDir == "" {
		return nil
	}
	return SkillNamesOf(req.Role)
}

// jsonPresent reports whether raw is a JSON value other than null, an
// empty array, or an empty object. A value of an unknown shape counts as
// present, on the safe side.
func jsonPresent(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return false
	}
	var array []json.RawMessage
	if err := json.Unmarshal(trimmed, &array); err == nil {
		return len(array) > 0
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err == nil {
		return len(object) > 0
	}
	return true
}

// jsonShape names the shape of raw, and never a value of it: the type,
// and for a list the type of its items with the keys of an object. A live
// run records the shape of a field that cumin reads, so that a changed
// shape is told from a changed value.
func jsonShape(raw json.RawMessage) string {
	if raw == nil {
		return "absent"
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "not JSON"
	}
	list, ok := value.([]any)
	if !ok {
		if value == nil {
			return "null"
		}
		if object, ok := value.(map[string]any); ok {
			return "object with " + strings.Join(sortedKeys(object), ",")
		}
		return fmt.Sprintf("%T", value)
	}
	if len(list) == 0 {
		return "empty list"
	}
	switch item := list[0].(type) {
	case string:
		return "list of strings"
	case map[string]any:
		return "list of objects with " + strings.Join(sortedKeys(item), ",")
	default:
		return fmt.Sprintf("list of %T", item)
	}
}

// sortedKeys returns the keys of an object in a fixed order.
func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// jsonStringList returns the strings of a JSON list of strings. The
// second value is false for any other shape, so that the caller can tell
// "cumin cannot read this" from "the list is empty".
func jsonStringList(raw json.RawMessage) ([]string, bool) {
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, false
	}
	return list, true
}

// jsonStrings collects every string in raw, at any depth.
func jsonStrings(raw json.RawMessage) []string {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case string:
			out = append(out, v)
		case []any:
			for _, item := range v {
				walk(item)
			}
		case map[string]any:
			for _, item := range v {
				walk(item)
			}
		}
	}
	walk(value)
	return out
}

// underDir reports whether path is dir or inside dir, after symbolic
// links are resolved.
func underDir(path, dir string) bool {
	path, dir = resolvePath(path), resolvePath(dir)
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

// resolvePath makes p absolute and resolves the symbolic links of its
// longest existing ancestor. A path that does not exist yet (a memory
// directory that is not created) is resolved through its parents, so that
// it compares with an existing directory on a system where a temporary
// directory is behind a link.
func resolvePath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	p = filepath.Clean(p)
	rest := ""
	for cur := p; ; {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(real, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// fieldNames returns the sorted names of the fields of the JSON object in
// line, or of its nested object at key when key is not empty.
func fieldNames(line []byte, key string) []string {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(line, &object); err != nil {
		return nil
	}
	if key != "" {
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(object[key], &nested); err != nil {
			return nil
		}
		object = nested
	}
	names := make([]string, 0, len(object))
	for name := range object {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
