// Package cuminmaintainer holds the plugin of Claude Code for the session of
// a Maintainer or the Operator, and a test of the form of its files.
package cuminmaintainer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The names of the plugin and of the marketplace are part of the install
// command in docs/ja/guides/maintainer-skills.md.
const (
	pluginName      = "cumin-maintainer"
	marketplaceName = "cumin-works"
)

// descriptionLimit is the length after which Claude Code cuts the description
// of a skill in its listing ("Extend Claude with skills", read 2026-10-09).
const descriptionLimit = 1536

// organizationFacts match a fact of one Organization: the name of an
// Organization or of its product, a repository, an App, a person, or the
// number of an issue. A placeholder such as <owner>/<repository> or
// {owner}/{repo} matches none of them.
var organizationFacts = []struct {
	what    string
	pattern *regexp.Regexp
}{
	{"the name of an Organization", regexp.MustCompile(`(?i)cloveclove|peppercheck`)},
	{"a repository", regexp.MustCompile(`(?i)github\.com[/:][a-z0-9][a-z0-9-]*/[a-z0-9._-]+`)},
	{"an issue of a repository", regexp.MustCompile(`(?i)\b[a-z0-9][a-z0-9-]*/[a-z0-9._-]+#[0-9]+`)},
	{"the number of an issue", regexp.MustCompile(`(^|[\s(])#[0-9]+\b`)},
	{"an App", regexp.MustCompile(`(?i)\[bot\]`)},
	{"a person", regexp.MustCompile(`(^|[\s(])@[A-Za-z0-9][A-Za-z0-9-]*`)},
}

// section returns the text under the heading "## <title>" of a Markdown
// text, up to the next heading of the same level, and whether the heading
// exists. A "## " line inside a code block ends the section too: a skill
// keeps such lines out of these two sections.
func section(text, title string) (string, bool) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "## "+title {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "## ") {
				end = j
				break
			}
		}
		return strings.TrimSpace(strings.Join(lines[i+1:end], "\n")), true
	}
	return "", false
}

// frontMatter returns the keys of the front matter of a skill: the lines
// "key: value" between the first line "---" and the next one. Claude Code
// reads the front matter only when "---" is the first line of the file.
func frontMatter(text string) (map[string]string, bool) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if lines[0] != "---" {
		return nil, false
	}
	keys := map[string]string{}
	for _, line := range lines[1:] {
		if line == "---" {
			return keys, true
		}
		if key, value, ok := strings.Cut(line, ":"); ok {
			keys[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return nil, false
}

// checkSkill returns the problems of one SKILL.md. rules is the text that the
// section "Rules of the session" must hold.
func checkSkill(rel, name, text, rules string) []string {
	var problems []string
	keys, ok := frontMatter(text)
	switch {
	case !ok:
		problems = append(problems, fmt.Sprintf("%s: the file must start with a front matter between two lines \"---\"", rel))
	default:
		if keys["name"] != name {
			problems = append(problems, fmt.Sprintf("%s: the front matter must hold \"name: %s\", the name of the directory", rel, name))
		}
		// ">" and "|" start a text of several lines in YAML, which this check
		// cannot measure.
		if d := keys["description"]; d == "" || len(d) > descriptionLimit || strings.HasPrefix(d, ">") || strings.HasPrefix(d, "|") {
			problems = append(problems, fmt.Sprintf("%s: the front matter must hold a description of 1 to %d characters on one line", rel, descriptionLimit))
		}
	}
	got, ok := section(text, "Rules of the session")
	switch {
	case !ok:
		problems = append(problems, fmt.Sprintf("%s: the section \"## Rules of the session\" is missing", rel))
	case got != rules:
		problems = append(problems, fmt.Sprintf("%s: the section \"## Rules of the session\" differs from rules.md: copy the text below the title of rules.md", rel))
	}
	commands, ok := section(text, "Commands")
	switch {
	case !ok:
		problems = append(problems, fmt.Sprintf("%s: the section \"## Commands\" is missing", rel))
	case !strings.Contains(commands, "`"):
		problems = append(problems, fmt.Sprintf("%s: the section \"## Commands\" names no command in backticks", rel))
	}
	return problems
}

// The skill hand-over writes the note that the skill session-start reads. Both
// list the headings of the note under the same section.
const (
	startSkill         = "session-start"
	handOverSkill      = "hand-over"
	noteHeadingSection = "Headings of the hand-over note"
	noteHeadingCount   = 4
)

// noteHeading matches one heading of the hand-over note in a list item of a
// skill: "1. `## State`".
var noteHeading = regexp.MustCompile("(?m)^[0-9]+\\. `(## [^`]+)`$")

// noteHeadings returns the headings of the hand-over note that a skill lists,
// in their order.
func noteHeadings(text string) []string {
	list, _ := section(text, noteHeadingSection)
	var headings []string
	for _, m := range noteHeading.FindAllStringSubmatch(list, -1) {
		headings = append(headings, m[1])
	}
	return headings
}

// checkNoteHeadings returns the problems of the headings of the hand-over
// note. texts holds the SKILL.md of every skill by its name. A plugin with
// neither of the two skills has no note.
func checkNoteHeadings(texts map[string]string) []string {
	start, hasStart := texts[startSkill]
	handOver, hasHandOver := texts[handOverSkill]
	if !hasStart && !hasHandOver {
		return nil
	}
	var problems []string
	read, written := noteHeadings(start), noteHeadings(handOver)
	for _, skill := range []struct {
		name     string
		headings []string
	}{{startSkill, read}, {handOverSkill, written}} {
		if len(skill.headings) != noteHeadingCount {
			problems = append(problems, fmt.Sprintf("skills/%s/SKILL.md: the section \"## %s\" lists %d headings of the note, want %d", skill.name, noteHeadingSection, len(skill.headings), noteHeadingCount))
		}
	}
	// The skill hand-over names each heading again in the table of what goes
	// under it.
	for _, h := range written {
		if !strings.Contains(handOver, "\n| `"+h+"` |") {
			problems = append(problems, fmt.Sprintf("skills/%s/SKILL.md: no row of a table starts with the heading `%s` of the note", handOverSkill, h))
		}
	}
	if strings.Join(read, "\n") != strings.Join(written, "\n") {
		problems = append(problems, fmt.Sprintf("the headings of the hand-over note differ: %s reads %q, %s writes %q", startSkill, read, handOverSkill, written))
	}
	return problems
}

// checkPlugin returns one problem for each broken rule of the plugin under
// root/plugins/cumin-maintainer and of the marketplace file of root:
//   - the marketplace lists the plugin under its name, with a relative source;
//   - every skill has a front matter, the rules of the session as rules.md
//     has them, and the commands that it runs;
//   - the skills session-start and hand-over list the same four headings of
//     the hand-over note;
//   - no file of the plugin holds a fact of one Organization.
func checkPlugin(root string) ([]string, error) {
	var problems []string
	dir := filepath.Join(root, "plugins", pluginName)

	var manifest struct {
		Name string `json:"name"`
	}
	if err := readJSON(filepath.Join(dir, ".claude-plugin", "plugin.json"), &manifest); err != nil {
		return nil, err
	}
	if manifest.Name != pluginName {
		problems = append(problems, fmt.Sprintf("plugin.json: name = %q, want %q", manifest.Name, pluginName))
	}
	var marketplace struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name   string `json:"name"`
			Source any    `json:"source"`
		} `json:"plugins"`
	}
	if err := readJSON(filepath.Join(root, ".claude-plugin", "marketplace.json"), &marketplace); err != nil {
		return nil, err
	}
	if marketplace.Name != marketplaceName {
		problems = append(problems, fmt.Sprintf("marketplace.json: name = %q, want %q", marketplace.Name, marketplaceName))
	}
	// A relative source is a path from the root of the marketplace, the
	// directory that holds .claude-plugin/ ("Create a marketplace").
	source := "./plugins/" + pluginName
	if len(marketplace.Plugins) != 1 || marketplace.Plugins[0].Name != pluginName || marketplace.Plugins[0].Source != source {
		problems = append(problems, fmt.Sprintf("marketplace.json: plugins must hold one entry with the name %q and the source %q", pluginName, source))
	}

	title, rules, ok := strings.Cut(strings.ReplaceAll(readText(dir, "rules.md"), "\r\n", "\n"), "\n")
	if !ok || title != "# Rules of the session" {
		return nil, errors.New("rules.md must start with the line \"# Rules of the session\"")
	}
	rules = strings.TrimSpace(rules)
	if strings.Contains(rules, "\n## ") {
		problems = append(problems, "rules.md: a heading \"## \" would end the section of a skill: use \"### \"")
	}

	skills, err := os.ReadDir(filepath.Join(dir, "skills"))
	if err != nil {
		return nil, err
	}
	var count int
	texts := map[string]string{}
	for _, skill := range skills {
		if !skill.IsDir() {
			continue
		}
		count++
		rel := filepath.Join("skills", skill.Name(), "SKILL.md")
		text, err := os.ReadFile(filepath.Join(dir, rel))
		if errors.Is(err, fs.ErrNotExist) {
			problems = append(problems, fmt.Sprintf("%s is missing", rel))
			continue
		}
		if err != nil {
			return nil, err
		}
		texts[skill.Name()] = string(text)
		problems = append(problems, checkSkill(rel, skill.Name(), string(text), rules)...)
	}
	if count == 0 {
		return nil, errors.New("found no skill under skills/")
	}
	problems = append(problems, checkNoteHeadings(texts)...)

	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		// This file names the facts that it looks for, and it is not a file
		// that a session reads.
		if err != nil || d.IsDir() || strings.HasSuffix(path, "_test.go") {
			return err
		}
		text, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		for n, line := range strings.Split(string(text), "\n") {
			for _, fact := range organizationFacts {
				if found := fact.pattern.FindString(line); found != "" {
					problems = append(problems, fmt.Sprintf("%s:%d holds %s (%q): a file of the plugin holds no fact of one Organization", rel, n+1, fact.what, strings.TrimSpace(found)))
				}
			}
		}
		return nil
	})
	return problems, err
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// readText returns the text of a file of the plugin, or "" when it cannot be
// read: the caller reports the missing first line.
func readText(dir, name string) string {
	data, _ := os.ReadFile(filepath.Join(dir, name))
	return string(data)
}

// TestPlugin_EverySkillFollowsTheForm checks the committed plugin.
func TestPlugin_EverySkillFollowsTheForm(t *testing.T) {
	problems, err := checkPlugin(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}

const testRules = "### One part\n\n- Ask before an action."

// writePlugin writes a plugin with one good skill under a new root, then the
// files of changed, and returns the root.
func writePlugin(t *testing.T, changed map[string]string) string {
	t.Helper()
	root := t.TempDir()
	plugin := filepath.Join("plugins", pluginName)
	files := map[string]string{
		filepath.Join(".claude-plugin", "marketplace.json"):        `{"name":"cumin-works","owner":{"name":"x"},"plugins":[{"name":"cumin-maintainer","source":"./plugins/cumin-maintainer"}]}`,
		filepath.Join(plugin, ".claude-plugin", "plugin.json"):     `{"name":"cumin-maintainer"}`,
		filepath.Join(plugin, "rules.md"):                          "# Rules of the session\n\n" + testRules + "\n",
		filepath.Join(plugin, "skills", "good", "SKILL.md"):        testSkill("good", "## Rules of the session\n\n"+testRules+"\n\n## Commands\n\n- `gh issue view <number>`\n"),
		filepath.Join(plugin, "skills", "good", "scripts", "x.sh"): "#!/bin/sh\ngh api repos/{owner}/{repo}/issues/\"$1\"\necho \"<plugin>@<marketplace>\"\n",
		filepath.Join(plugin, "skills", "README.md"):               "A file next to the skills is not a skill.\n",
		filepath.Join(plugin, "skills", "good", "reference.md"):    "Install from <owner>/<repository>. See https://github.com/<owner>/<repository>.\n",
		filepath.Join(plugin, "skills", "good", "colors.md"):       "Take step 1. The color is #1D76DB.\n",
	}
	for name, text := range changed {
		files[filepath.Join(plugin, name)] = text
	}
	for name, text := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func testSkill(name, body string) string {
	return "---\nname: " + name + "\ndescription: Use when a test needs a skill.\n---\n\n# A skill\n\n" + body
}

func TestCheckPlugin_FindsEachProblem(t *testing.T) {
	rulesSection := "## Rules of the session\n\n" + testRules + "\n\n"
	commands := "## Commands\n\n- `gh issue view <number>`\n"
	skill := func(name string) string { return filepath.Join("skills", name, "SKILL.md") }
	tests := []struct {
		name    string
		changed map[string]string
		want    []string
	}{
		{"a good plugin has no problem", nil, nil},
		{"a skill lacks the rules section",
			map[string]string{skill("bad"): testSkill("bad", commands)},
			[]string{`the section "## Rules of the session" is missing`}},
		{"a skill holds other rules than rules.md",
			map[string]string{skill("bad"): testSkill("bad", "## Rules of the session\n\n### One part\n\n- Act without asking.\n\n"+commands)},
			[]string{`the section "## Rules of the session" differs from rules.md`}},
		{"a skill lacks the commands section",
			map[string]string{skill("bad"): testSkill("bad", rulesSection)},
			[]string{`the section "## Commands" is missing`}},
		{"a skill names no command",
			map[string]string{skill("bad"): testSkill("bad", rulesSection+"## Commands\n\nNone.\n")},
			[]string{`the section "## Commands" names no command`}},
		{"a skill has no front matter",
			map[string]string{skill("bad"): "# A skill\n\n" + rulesSection + commands},
			[]string{"must start with a front matter"}},
		{"the name of a skill differs from its directory",
			map[string]string{skill("bad"): testSkill("other", rulesSection+commands)},
			[]string{`must hold "name: bad"`}},
		{"a skill has no description",
			map[string]string{skill("bad"): "---\nname: bad\n---\n\n" + rulesSection + commands},
			[]string{"must hold a description"}},
		{"the description of a skill covers several lines",
			map[string]string{skill("bad"): "---\nname: bad\ndescription: >-\n  Use when a test needs a skill.\n---\n\n" + rulesSection + commands},
			[]string{"must hold a description"}},
		{"a skill directory has no SKILL.md",
			map[string]string{filepath.Join("skills", "bad", "notes.md"): "Notes.\n"},
			[]string{"SKILL.md is missing"}},
		{"a skill holds an organization name",
			map[string]string{skill("bad"): testSkill("bad", "Ask the team of cloveclovedev.\n\n"+rulesSection+commands)},
			[]string{"holds the name of an Organization"}},
		{"a script holds a repository",
			map[string]string{filepath.Join("skills", "good", "scripts", "y.sh"): "git clone https://github.com/some-org/some-repo\n"},
			[]string{"holds a repository"}},
		{"a file holds an issue of a repository",
			map[string]string{"notes.md": "See some-org/some-repo#12.\n"},
			[]string{"holds an issue of a repository"}},
		{"a file holds the number of an issue",
			map[string]string{"notes.md": "This follows #385.\n"},
			[]string{"holds the number of an issue"}},
		{"a file holds an App",
			map[string]string{"notes.md": "The author is some-app[bot].\n"},
			[]string{"holds an App"}},
		{"a file holds a person",
			map[string]string{"notes.md": "Ask @someone first.\n"},
			[]string{"holds a person"}},
		{"the rules hold a heading of the level of a section",
			map[string]string{"rules.md": "# Rules of the session\n\n" + testRules + "\n\n## More\n\n- A rule.\n"},
			[]string{`rules.md: a heading "## "`, `differs from rules.md`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			problems, err := checkPlugin(writePlugin(t, tt.changed))
			if err != nil {
				t.Fatal(err)
			}
			if len(problems) != len(tt.want) {
				t.Fatalf("problems = %q, want one for each of %q", problems, tt.want)
			}
			for i, w := range tt.want {
				if !strings.Contains(problems[i], w) {
					t.Errorf("problem %d = %q, want %q", i, problems[i], w)
				}
			}
		})
	}
}

func TestCheckPlugin_ChecksTheMarketplaceFile(t *testing.T) {
	root := writePlugin(t, nil)
	path := filepath.Join(root, ".claude-plugin", "marketplace.json")
	if err := os.WriteFile(path, []byte(`{"name":"other","plugins":[{"name":"cumin-maintainer","source":"../plugins/cumin-maintainer"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	problems, err := checkPlugin(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 2 || !strings.Contains(problems[0], `name = "other"`) || !strings.Contains(problems[1], "plugins must hold one entry") {
		t.Errorf("problems = %q, want the name and the source of the marketplace", problems)
	}
}

// TestCheckPlugin_ComparesTheHeadingsOfTheHandOverNote proves that the note
// which the skill hand-over writes has the four headings that the skill
// session-start reads.
func TestCheckPlugin_ComparesTheHeadingsOfTheHandOverNote(t *testing.T) {
	rest := "## Rules of the session\n\n" + testRules + "\n\n## Commands\n\n- `cumin status`\n"
	four := []string{"State", "Open decisions", "Waiting for the Maintainer", "What the session learned"}
	withHeadings := func(name string, headings ...string) string {
		body := "## " + noteHeadingSection + "\n\n"
		for i, h := range headings {
			body += fmt.Sprintf("%d. `## %s`\n", i+1, h)
		}
		// The table of the skill hand-over, with one row for each heading.
		for _, h := range headings {
			body += "\n| `## " + h + "` | Text. |"
		}
		return testSkill(name, body+"\n\n"+rest)
	}
	skill := func(name string) string { return filepath.Join("skills", name, "SKILL.md") }
	tests := []struct {
		name    string
		changed map[string]string
		want    []string
	}{
		{"both skills list the same four headings",
			map[string]string{skill(startSkill): withHeadings(startSkill, four...), skill(handOverSkill): withHeadings(handOverSkill, four...)},
			nil},
		{"one heading differs",
			map[string]string{skill(startSkill): withHeadings(startSkill, four...), skill(handOverSkill): withHeadings(handOverSkill, "State", "Decisions", "Waiting for the Maintainer", "What the session learned")},
			[]string{"the headings of the hand-over note differ"}},
		{"the order differs",
			map[string]string{skill(startSkill): withHeadings(startSkill, four...), skill(handOverSkill): withHeadings(handOverSkill, four[1], four[0], four[2], four[3])},
			[]string{"the headings of the hand-over note differ"}},
		{"the table of the skill hand-over lacks a heading of its list",
			map[string]string{skill(startSkill): withHeadings(startSkill, four...), skill(handOverSkill): strings.Replace(withHeadings(handOverSkill, four...), "| `## Open decisions` |", "| `## Decisions` |", 1)},
			[]string{"no row of a table starts with the heading `## Open decisions`"}},
		{"both skills list three headings",
			map[string]string{skill(startSkill): withHeadings(startSkill, four[:3]...), skill(handOverSkill): withHeadings(handOverSkill, four[:3]...)},
			[]string{"session-start/SKILL.md", "hand-over/SKILL.md"}},
		{"the skill hand-over is missing",
			map[string]string{skill(startSkill): withHeadings(startSkill, four...)},
			[]string{"hand-over/SKILL.md", "the headings of the hand-over note differ"}},
		{"the skill session-start lacks the section",
			map[string]string{skill(startSkill): testSkill(startSkill, rest), skill(handOverSkill): withHeadings(handOverSkill, four...)},
			[]string{"session-start/SKILL.md", "the headings of the hand-over note differ"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			problems, err := checkPlugin(writePlugin(t, tt.changed))
			if err != nil {
				t.Fatal(err)
			}
			if len(problems) != len(tt.want) {
				t.Fatalf("problems = %q, want one for each of %q", problems, tt.want)
			}
			for i, w := range tt.want {
				if !strings.Contains(problems[i], w) {
					t.Errorf("problem %d = %q, want %q", i, problems[i], w)
				}
			}
		})
	}
}

// guidePath is the guide that holds the table of the skills and the list of
// the permission rules of Claude Code.
var guidePath = filepath.Join("docs", "ja", "guides", "maintainer-skills.md")

// The marks of a rule in the list of the guide: GitHub records the command,
// or the command changes the Host. The Operator may leave such a rule out.
var ruleMarks = map[string]bool{"GitHub": true, "Host": true}

// guideRule is one row of the list of the permission rules: the tool, the
// text between the parentheses, and whether the row has a mark.
type guideRule struct {
	tool, pattern string
	marked        bool
}

// ruleRow matches a row of a table whose first cell is a permission rule
// ("Configure permissions", read 2026-10-09: a rule is "Tool(specifier)").
// It also takes the second cell, the mark.
var ruleRow = regexp.MustCompile("^\\| `(Bash|Edit)\\((.+)\\)` \\| ([^|]*) \\|")

// guideRules returns the rules of the list of the guide.
func guideRules(guide string) []guideRule {
	var rules []guideRule
	for _, line := range strings.Split(guide, "\n") {
		if m := ruleRow.FindStringSubmatch(line); m != nil {
			rules = append(rules, guideRule{m[1], m[2], ruleMarks[strings.TrimSpace(m[3])]})
		}
	}
	return rules
}

// matches reports whether a rule covers a text, as the page "Configure
// permissions" says: "*" stands for any text, a rule without "*" matches one
// exact text, the suffix ":*" is the same as " *", and a rule whose only "*"
// is at the end after a space also matches the text without that part.
func (r guideRule) matches(text string) bool {
	pattern := r.pattern
	if strings.HasSuffix(pattern, ":*") {
		pattern = strings.TrimSuffix(pattern, ":*") + " *"
	}
	if strings.Count(pattern, "*") == 1 && strings.HasSuffix(pattern, " *") && text == strings.TrimSuffix(pattern, " *") {
		return true
	}
	parts := strings.Split(pattern, "*")
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	return regexp.MustCompile("(?s)^" + strings.Join(parts, ".*") + "$").MatchString(text)
}

// skillCommand is one command that a skill names under "## Commands". A file
// is a path that the session writes with its own tool, not a shell command.
type skillCommand struct {
	text       string
	file       bool
	askedFirst bool
}

var backticks = regexp.MustCompile("`([^`]+)`")

// skillCommands returns the commands of the list items under "## Commands"
// of a skill. The line before a list names its group: only the group "Read,
// without asking" is not asked first. A text in backticks that starts with
// "--" is an option of the command before it. Claude Code replaces
// ${CLAUDE_SKILL_DIR} in the text of a skill with the directory of the skill
// ("Extend Claude with skills", read 2026-10-09), so the rule sees that path.
func skillCommands(name, text string) []skillCommand {
	list, _ := section(text, "Commands")
	var commands []skillCommand
	var askedFirst bool
	for _, line := range strings.Split(list, "\n") {
		if !strings.HasPrefix(line, "- ") {
			if strings.HasSuffix(strings.TrimSpace(line), ":") {
				askedFirst = !strings.HasPrefix(line, "Read, without asking")
			}
			continue
		}
		for _, m := range backticks.FindAllStringSubmatch(line, -1) {
			c := m[1]
			if strings.HasPrefix(c, "--") {
				continue
			}
			c = strings.ReplaceAll(c, "${CLAUDE_SKILL_DIR}", "/plugin/skills/"+name)
			commands = append(commands, skillCommand{c, strings.HasPrefix(c, "~/"), askedFirst})
		}
	}
	return commands
}

// checkGuide returns one problem for each broken rule of the guide of root:
//   - the guide holds a row of a table for each skill directory;
//   - every command under "## Commands" of a skill has a rule in the list;
//   - a command that a skill asks first has a rule with a mark, and no rule
//     without a mark: the Operator who leaves the marked rules out is asked.
func checkGuide(root string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(root, guidePath))
	if err != nil {
		return nil, err
	}
	guide := strings.ReplaceAll(string(data), "\r\n", "\n")
	rules := guideRules(guide)
	dir := filepath.Join(root, "plugins", pluginName, "skills")
	skills, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var problems []string
	for _, skill := range skills {
		if !skill.IsDir() {
			continue
		}
		name := skill.Name()
		if !strings.Contains(guide, "\n| `"+name+"` | ") {
			problems = append(problems, fmt.Sprintf("the guide holds no row of a table for the skill `%s`", name))
		}
		text, err := os.ReadFile(filepath.Join(dir, name, "SKILL.md"))
		if err != nil {
			return nil, err
		}
		for _, c := range skillCommands(name, string(text)) {
			tool := "Bash"
			if c.file {
				tool = "Edit"
			}
			var marked, unmarked []string
			for _, r := range rules {
				if r.tool != tool || !r.matches(c.text) {
					continue
				}
				rule := r.tool + "(" + r.pattern + ")"
				if r.marked {
					marked = append(marked, rule)
				} else {
					unmarked = append(unmarked, rule)
				}
			}
			switch {
			case len(marked)+len(unmarked) == 0:
				problems = append(problems, fmt.Sprintf("skills/%s/SKILL.md: the command `%s` has no rule %s(...) in the list of the guide", name, c.text, tool))
			case c.askedFirst && len(unmarked) > 0:
				problems = append(problems, fmt.Sprintf("skills/%s/SKILL.md: the skill asks before `%s`, but the rule `%s` of the guide has no mark", name, c.text, unmarked[0]))
			case c.askedFirst && len(marked) == 0:
				problems = append(problems, fmt.Sprintf("skills/%s/SKILL.md: the skill asks before `%s`, but no rule with a mark covers it", name, c.text))
			}
		}
	}
	return problems, nil
}

// TestGuide_HoldsEverySkillAndARuleForEveryCommand checks the committed
// guide against the committed skills.
func TestGuide_HoldsEverySkillAndARuleForEveryCommand(t *testing.T) {
	problems, err := checkGuide(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}

func TestGuideRule_MatchesAsClaudeCodeDoes(t *testing.T) {
	tests := []struct {
		pattern, text string
		want          bool
	}{
		{"npm run build", "npm run build", true},
		{"npm run build", "npm run build --watch", false},
		{"npm run *", "npm run test --watch", true},
		{"npm run *", "npm run", true},
		{"npm run *", "npm install", false},
		{"ls *", "lsof", false},
		{"ls*", "lsof", true},
		{"ls:*", "ls -la", true},
		{"git log * main", "git log main", false},
		{"* --help *", "npm --help", false},
		{"*/skills/watch/wait.sh *", "/plugin/skills/watch/wait.sh --seen x", true},
		{"gh api repos/*", "gh api --method POST repos/o/r/issues", false},
	}
	for _, tt := range tests {
		if got := (guideRule{tool: "Bash", pattern: tt.pattern}).matches(tt.text); got != tt.want {
			t.Errorf("the rule Bash(%s) matches %q = %v, want %v", tt.pattern, tt.text, got, tt.want)
		}
	}
}

// TestCheckGuide_FindsEachProblem proves that the check fails when the guide
// lacks a skill, lacks the rule of a command, or holds a rule without a mark
// for a command that a skill asks first.
func TestCheckGuide_FindsEachProblem(t *testing.T) {
	skill := "## Commands\n\nRead, without asking:\n\n- `gh issue view <number> --comments`\n- `${CLAUDE_SKILL_DIR}/check.sh <number>`\n\n" +
		"Recorded by GitHub, so ask first:\n\n- `gh pr merge <number>`, with `--merge` or `--squash`\n\n" +
		"Changes the Host, so ask first:\n\n- The write of the draft to `~/note.tmp`\n\nThe text below names `no command`.\n"
	row := "| `good` | When a test needs a skill. |\n"
	read := "| `Bash(gh issue view *)` | - | `good` |\n| `Bash(*/skills/good/check.sh *)` | - | `good` |\n"
	merge := "| `Bash(gh pr merge *)` | GitHub | `good` |\n"
	write := "| `Edit(~/note.tmp)` | Host | `good` |\n"
	tests := []struct {
		name  string
		guide string
		want  []string
	}{
		{"a good guide has no problem", row + read + merge + write, nil},
		{"the guide lacks the row of a skill", read + merge + write, []string{"no row of a table for the skill `good`"}},
		{"the guide lacks the rule of a command",
			row + "| `Bash(gh issue view *)` | - | `good` |\n" + merge + write,
			[]string{"the command `/plugin/skills/good/check.sh <number>` has no rule Bash(...)"}},
		{"the guide lacks the rule of a written file",
			row + read + merge,
			[]string{"the command `~/note.tmp` has no rule Edit(...)"}},
		{"a rule of another tool does not cover a command",
			row + read + "| `Edit(gh pr merge *)` | GitHub | `good` |\n" + write,
			[]string{"the command `gh pr merge <number>` has no rule Bash(...)"}},
		{"the rule of a command that is asked first has no mark",
			row + read + "| `Bash(gh pr merge *)` | - | `good` |\n" + write,
			[]string{"the skill asks before `gh pr merge <number>`, but the rule `Bash(gh pr merge *)` of the guide has no mark"}},
		{"a wide rule without a mark covers a command that is asked first",
			row + read + merge + "| `Bash(gh *)` | - | `good` |\n" + write,
			[]string{"the rule `Bash(gh *)` of the guide has no mark"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writePlugin(t, map[string]string{filepath.Join("skills", "good", "SKILL.md"): testSkill("good", skill)})
			path := filepath.Join(root, guidePath)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("# A guide\n\n| skill | Use |\n|---|---|\n"+tt.guide), 0o644); err != nil {
				t.Fatal(err)
			}
			problems, err := checkGuide(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(problems) != len(tt.want) {
				t.Fatalf("problems = %q, want one for each of %q", problems, tt.want)
			}
			for i, w := range tt.want {
				if !strings.Contains(problems[i], w) {
					t.Errorf("problem %d = %q, want %q", i, problems[i], w)
				}
			}
		})
	}
}
