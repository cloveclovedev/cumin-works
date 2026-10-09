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
		"| `replace-binary.sh` | A merge changed code outside the tests, and cumin must run the new binary | Yes.",
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
		"- `\"$CUMIN_SOURCE_DIR\"/scripts/replace-binary.sh`",
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
	for _, tool := range []string{"cumin-health.sh", "replace-binary.sh", "live-scenario.sh"} {
		if _, err := os.Stat(filepath.Join("..", "..", "scripts", tool)); err != nil {
			t.Errorf("the skill calls %s, which scripts/ does not hold: %v", tool, err)
		}
	}
}
