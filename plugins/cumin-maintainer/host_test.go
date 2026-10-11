package cuminmaintainer

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// This file tests the skill host: the fixed texts of its SKILL.md. The skill
// has no script of its own, and the tests of the tools are in scripts/.

// The skill calls the three general tools, and it sends the reader to the
// guide for the options of the tools.
func TestHostSkill_NamesEveryCommandAndLinksToTheGuide(t *testing.T) {
	text := readText(filepath.Join("skills", "host"), "SKILL.md")
	for _, want := range []string{
		"The skill adds no step of its own",
		"When `CUMIN_SOURCE_DIR` is not set, ask the Maintainer for the path",
		// The shell expands "$CUMIN_SOURCE_DIR" before an assignment in front
		// of the command, so the skill sets the variable first.
		"set the variable first in the same command: `export CUMIN_SOURCE_DIR=<path>; <command>`",
		"| `cumin-health.sh` | The Maintainer asks \"is it running\", or a watch reports an old last poll | No.",
		"| `install.sh --after-current-runs` | A merge changed code outside the tests, and cumin must run the new binary | Yes.",
		"| `live-scenario.sh` | The Maintainer asks for the live scenario E2E-1 | Yes.",
		"## The new binary before the next approval\n",
		"replace the binary before the session approves the next pull request",
		"Only the exit code 0 is a success.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("SKILL.md lacks %q", want)
		}
	}

	if strings.Contains(text, "`CUMIN_SOURCE_DIR=<path>") {
		t.Error("SKILL.md gives the assignment in front of the command, which the shell applies after it expands the command")
	}

	commands, _ := section(text, "Commands")
	if !strings.Contains(commands, "`$CUMIN_SOURCE_DIR/docs/ja/guides/host-tools.md`") {
		t.Error("the section \"## Commands\" does not link to host-tools.md for the options of the tools")
	}
	read, changes, ok := strings.Cut(commands, "Changes the Host, so ask first:")
	if !ok {
		t.Fatal("the section \"## Commands\" does not mark the commands that change the Host")
	}
	if !strings.Contains(read, "- `\"$CUMIN_SOURCE_DIR\"/scripts/cumin-health.sh`") {
		t.Error("cumin-health.sh must stand under the commands that only read")
	}
	for _, command := range []string{
		"- `\"$CUMIN_SOURCE_DIR\"/scripts/install.sh --after-current-runs`",
		"- `\"$CUMIN_SOURCE_DIR\"/scripts/live-scenario.sh --repo <owner>/<sandbox> --config <file>`",
	} {
		if strings.Contains(read, command) || !strings.Contains(changes, command) {
			t.Errorf("%q must stand only under the commands that change the Host", command)
		}
	}
	// Every call of a tool and of git in the text of the skill
	// stands under "## Commands".
	for _, command := range regexp.MustCompile("`((?:\"\\$CUMIN_SOURCE_DIR\"|git )[^`]*)`").FindAllStringSubmatch(text, -1) {
		if !strings.Contains(commands, "- `"+command[1]+"`") {
			t.Errorf("the section \"## Commands\" lacks %q", command[1])
		}
	}
	// Each tool that the skill calls is a tool of the repository.
	for _, tool := range []string{"cumin-health.sh", "install.sh", "live-scenario.sh"} {
		if _, err := os.Stat(filepath.Join("..", "..", "scripts", tool)); err != nil {
			t.Errorf("the skill calls %s, which scripts/ does not hold: %v", tool, err)
		}
	}
}

// The guide of the tools names the new form of the tool that replaces the
// binary, with its six steps. Each option that the
// skill or the guide names for a script is an option of that script.
func TestHostSkill_NamesOnlyOptionsThatTheScriptsHave(t *testing.T) {
	root := filepath.Join("..", "..")
	guide := readText(filepath.Join(root, "docs", "ja", "guides"), "host-tools.md")
	for _, want := range []string{
		"| `scripts/install.sh --after-current-runs` |",
		"| `scripts/cumin-health.sh` |",
		"step <n> of 6",
		"| `--after-current-runs` |",
		"| `--stop-timeout <seconds>` |",
	} {
		if !strings.Contains(guide, want) {
			t.Errorf("host-tools.md lacks %q", want)
		}
	}
	// The diagram of the guide is rendered again after its source.
	for _, name := range []string{"host-tools.puml", "host-tools.svg"} {
		if !strings.Contains(readText(filepath.Join(root, "docs", "ja", "guides"), name), "install.sh --after-current-runs") {
			t.Errorf("%s lacks the tool install.sh --after-current-runs", name)
		}
	}

	health, _ := section(guide, "cuminが動いているか確かめる")
	replace, _ := section(guide, "バイナリを入れ替える")
	commands, _ := section(readText(filepath.Join("skills", "host"), "SKILL.md"), "Commands")
	option := regexp.MustCompile("--[a-z][a-z-]*")
	for script, texts := range map[string][]string{
		"cumin-health.sh": {health, linesWith(commands, "/scripts/cumin-health.sh")},
		"install.sh":      {linesWith(replace, "| `--"), linesWith(replace, "scripts/install.sh"), linesWith(commands, "/scripts/install.sh")},
	} {
		source := readText(filepath.Join(root, "scripts"), script)
		names := option.FindAllString(strings.Join(texts, "\n"), -1)
		if len(names) == 0 {
			t.Errorf("no option of %s was found in the skill and the guide", script)
		}
		for _, name := range names {
			if !strings.Contains(source, name+")") {
				t.Errorf("the skill or the guide names %s of %s, which the script does not have", name, script)
			}
		}
	}
}

// linesWith returns the lines of a text that hold a word.
func linesWith(text, word string) string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, word) {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
