package cuminmaintainer

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This file tests the skill merge-decision: its check script with a fake gh
// on PATH, and the fixed texts of its SKILL.md.

const (
	headCommit  = "1111111111111111111111111111111111111111"
	olderCommit = "2222222222222222222222222222222222222222"
)

// fakeGh answers each read of the script from a file of $FAKE_GH_DIR, named
// for what the script reads. The file holds the text after the jq filter,
// because the fake runs no jq. A file <name>.fail makes that read fail with
// its text. A missing file gives an empty read with the exit code 0. The
// fake records its arguments in the file "calls".
const fakeGh = `#!/bin/sh
echo "$*" >>"$FAKE_GH_DIR/calls"
name=unknown
for arg in "$@"; do
	case $arg in
	repos/*/pulls/*/reviews*) name=reviews ;;
	repos/*/pulls/*/files*) name=files ;;
	repos/*/pulls/*) name=pull ;;
	repos/*/rules/branches/*) name=rules ;;
	repos/*/check-runs*) name=runs ;;
	repos/*/commits/*/status*) name=statuses ;;
	repos/*/contents/*) name=config ;;
	esac
done
if [ -f "$FAKE_GH_DIR/sleep" ]; then
	sleep "$(cat "$FAKE_GH_DIR/sleep")"
fi
if [ -f "$FAKE_GH_DIR/$name.fail" ]; then
	cat "$FAKE_GH_DIR/$name.fail" >&2
	exit 1
fi
if [ -f "$FAKE_GH_DIR/$name" ]; then
	cat "$FAKE_GH_DIR/$name"
fi
exit 0
`

// readyPullRequest returns the answers of gh for a pull request with an
// approval on the head commit and two required checks that passed. App 7
// reports the checks. The check "lint" failed, and no rule requires it.
func readyPullRequest() map[string]string {
	return map[string]string{
		"pull":     headCommit + "\tmain\t2\topen\n",
		"reviews":  "page\nthe-reviewer\t" + headCommit + "\n",
		"rules":    "page\nci\t7\npaths\t7\n",
		"runs":     "page\nci\t7\tcompleted\tsuccess\npaths\t7\tcompleted\tsuccess\nlint\t7\tcompleted\tfailure\n",
		"statuses": "page\n",
		"config":   "protected_paths = [\"CLAUDE.md\"]\n",
		"files":    "page\nmodified\tsrc/main.go\t\nadded\tsrc/main_test.go\t\n",
	}
}

// runCheck runs the check script for pull request 12 with a fake gh that
// gives the answers of readyPullRequest, changed by changed. An answer "-"
// removes the file, so that the read is empty. It returns the output, the
// exit code, and the directory of the fake.
func runCheck(t *testing.T, changed map[string]string, env ...string) (string, int, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	data := filepath.Join(dir, "data")
	for _, d := range []string{bin, data} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(fakeGh), 0o755); err != nil {
		t.Fatal(err)
	}
	answers := readyPullRequest()
	for name, text := range changed {
		answers[name] = text
	}
	for name, text := range answers {
		if text == "-" {
			continue
		}
		if err := os.WriteFile(filepath.Join(data, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(filepath.Join("skills", "merge-decision", "check-pull-request.sh"), "some-owner/some-repo", "12")
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "FAKE_GH_DIR="+data)
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0, data
	case errors.As(err, &exit):
		return string(out), exit.ExitCode(), data
	}
	t.Fatalf("the script did not run: %v\n%s", err, out)
	return "", 0, ""
}

func TestCheckPullRequest_ExitsZeroForAnApprovalOnTheHeadWithAllChecksPassed(t *testing.T) {
	out, code, data := runCheck(t, nil)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	for _, want := range []string{
		"Head commit: " + headCommit,
		"- the-reviewer on " + headCommit + " (the head commit)",
		"- ci: success",
		"- paths: success",
		"Changed files under a protected path (2 changed files):\n- none",
		"Result: an approval is on the head commit, and every required check passed.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the output lacks %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "lint") {
		t.Errorf("the output names a check that no rule requires\n%s", out)
	}
	// The script only reads: every call is "gh api" without a method and
	// without a field, which gh sends as GET.
	calls, err := os.ReadFile(filepath.Join(data, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
	if len(lines) != 7 {
		t.Errorf("gh ran %d times, want one read for each of 7 facts\n%s", len(lines), calls)
	}
	for _, line := range lines {
		words := strings.Fields(line)
		if words[0] != "api" {
			t.Errorf("gh ran %q, want only gh api", line)
		}
		for _, word := range words {
			switch word {
			case "-X", "--method", "-f", "-F", "--field", "--raw-field", "--input":
				t.Errorf("gh ran %q, which can write", line)
			}
		}
	}
}

func TestCheckPullRequest_ExitsNonZeroWhenThePullRequestIsNotReady(t *testing.T) {
	tests := []struct {
		name    string
		changed map[string]string
		want    []string
	}{
		{"the approval is on an older commit",
			map[string]string{"reviews": "page\nthe-reviewer\t" + olderCommit + "\n"},
			[]string{"- the-reviewer on " + olderCommit + " (not the head commit)", "no approval is on the head commit"}},
		{"no approval",
			map[string]string{"reviews": "page\n"},
			[]string{"Approvals:\n- none", "not ready. no approval."}},
		{"a cancelled check",
			map[string]string{"runs": "page\nci\t7\tcompleted\tcancelled\npaths\t7\tcompleted\tsuccess\n"},
			[]string{"- ci: cancelled", "Propose to run it again.", "the check ci is cancelled"}},
		{"a queued check",
			map[string]string{"runs": "page\nci\t7\tqueued\t\npaths\t7\tcompleted\tsuccess\n"},
			[]string{"- ci: queued", "the check ci is queued"}},
		{"a check in progress",
			map[string]string{"runs": "page\nci\t7\tin_progress\t\npaths\t7\tcompleted\tsuccess\n"},
			[]string{"the check ci is in_progress"}},
		{"a check that GitHub skipped",
			map[string]string{"runs": "page\nci\t7\tcompleted\tsuccess\npaths\t7\tcompleted\tskipped\n"},
			[]string{"the check paths is skipped"}},
		{"a failed check",
			map[string]string{"runs": "page\nci\t7\tcompleted\tfailure\npaths\t7\tcompleted\tsuccess\n"},
			[]string{"the check ci is failure"}},
		{"a required check that did not report",
			map[string]string{"runs": "page\nci\t7\tcompleted\tsuccess\n"},
			[]string{"- paths: missing", "the check paths is missing"}},
		{"a required check from another App",
			map[string]string{"runs": "page\nci\t8\tcompleted\tsuccess\npaths\t7\tcompleted\tsuccess\n"},
			[]string{"the check ci is missing"}},
		{"one of two runs of a check failed",
			map[string]string{"runs": "page\nci\t7\tcompleted\tsuccess\nci\t7\tcompleted\tfailure\npaths\t7\tcompleted\tsuccess\n"},
			[]string{"the check ci is success, failure"}},
		{"a pending commit status",
			map[string]string{"rules": "page\nci\t7\ndeploy\t0\n", "runs": "page\nci\t7\tcompleted\tsuccess\n", "statuses": "page\ndeploy\tpending\n"},
			[]string{"the check deploy is pending"}},
		{"an empty list of required checks",
			map[string]string{"rules": "page\n"},
			[]string{"- none: the rules of the branch require no check", "the list of required checks is empty"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out, code, _ := runCheck(t, tt.changed)
			if code != 1 {
				t.Fatalf("exit code = %d, want 1\n%s", code, out)
			}
			for _, want := range tt.want {
				if !strings.Contains(out, want) {
					t.Errorf("the output lacks %q\n%s", want, out)
				}
			}
		})
	}
}

// A commit status meets a rule that names no App.
func TestCheckPullRequest_CountsASuccessfulCommitStatus(t *testing.T) {
	out, code, _ := runCheck(t, map[string]string{
		"rules":    "page\nci\t7\ndeploy\t0\n",
		"runs":     "page\nci\t7\tcompleted\tsuccess\n",
		"statuses": "page\ndeploy\tsuccess\n",
	})
	if code != 0 || !strings.Contains(out, "- deploy: success") {
		t.Errorf("exit code = %d, want 0 and the check deploy\n%s", code, out)
	}
}

// A failed read and an empty read decide nothing: the exit code is 2, and
// the output holds no result.
func TestCheckPullRequest_TakesAFailedOrEmptyReadForAnError(t *testing.T) {
	tests := []struct {
		name    string
		changed map[string]string
		want    string
	}{
		{"gh fails for the pull request", map[string]string{"pull.fail": "gh: Bad credentials (HTTP 401)\n"}, "gh could not read the pull request: gh: Bad credentials (HTTP 401)"},
		{"gh fails for the reviews", map[string]string{"reviews.fail": "gh: Server Error (HTTP 502)\n"}, "gh could not read the reviews"},
		{"gh fails for the rules", map[string]string{"rules.fail": "gh: Server Error (HTTP 502)\n"}, "gh could not read the rules of the branch main"},
		{"gh fails for the check runs", map[string]string{"runs.fail": "gh: Server Error (HTTP 502)\n"}, "gh could not read the check runs"},
		{"gh fails for the commit statuses", map[string]string{"statuses.fail": "gh: Server Error (HTTP 502)\n"}, "gh could not read the commit statuses"},
		{"gh fails for the settings", map[string]string{"config.fail": "gh: Server Error (HTTP 502)\n"}, "gh could not read .cumin/config.toml"},
		{"gh fails for the changed files", map[string]string{"files.fail": "gh: Server Error (HTTP 502)\n"}, "gh could not read the changed files"},
		{"gh returns nothing for the pull request", map[string]string{"pull": "-"}, "gh returned no head commit"},
		{"gh returns nothing for the reviews", map[string]string{"reviews": "-"}, "gh returned nothing for the reviews"},
		{"gh returns nothing for the rules", map[string]string{"rules": "-"}, "gh returned nothing for the rules of the branch main"},
		{"gh returns nothing for the check runs", map[string]string{"runs": "-"}, "gh returned nothing for the check runs"},
		{"gh returns nothing for the commit statuses", map[string]string{"statuses": "-"}, "gh returned nothing for the commit statuses"},
		{"gh returns nothing for the changed files", map[string]string{"files": "-"}, "gh returned nothing for the changed files"},
		{"gh lists fewer files than the pull request changes", map[string]string{"files": "page\nmodified\tsrc/main.go\t\n"}, "gh listed 1 of 2 changed files"},
		{"an entry of the settings is a wildcard", map[string]string{"config": "protected_paths = [\"*.md\"]\n"}, "uses a wildcard"},
		{"the list of the settings is not an array", map[string]string{"config": "protected_paths = \"CLAUDE.md\"\n"}, "the value must be an array of strings"},
		{"the list of the settings does not end", map[string]string{"config": "protected_paths = [\"CLAUDE.md\",\n"}, "the array does not end"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out, code, _ := runCheck(t, tt.changed)
			if code != 2 {
				t.Fatalf("exit code = %d, want 2\n%s", code, out)
			}
			if !strings.Contains(out, "error: ") || !strings.Contains(out, tt.want) {
				t.Errorf("the output lacks the error %q\n%s", tt.want, out)
			}
			if strings.Contains(out, "Result:") {
				t.Errorf("the output holds a result after an error\n%s", out)
			}
		})
	}
}

func TestCheckPullRequest_RefusesWrongArguments(t *testing.T) {
	script := filepath.Join("skills", "merge-decision", "check-pull-request.sh")
	for _, args := range [][]string{{}, {"some-owner/some-repo"}, {"some-repo", "12"}, {"some-owner/some-repo", "twelve"}, {"some-owner/some-repo", "12", "13"}} {
		out, err := exec.Command(script, args...).CombinedOutput()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 2 {
			t.Errorf("arguments %q: err = %v, want the exit code 2\n%s", args, err, out)
		}
	}
}

// The script ends at its time limit when gh hangs, and it says so.
func TestCheckPullRequest_EndsAtTheTimeLimit(t *testing.T) {
	start := time.Now()
	out, code, _ := runCheck(t, map[string]string{"sleep": "60"}, "CUMIN_CHECK_TIME_LIMIT=1")
	if code != 124 || !strings.Contains(out, "the time limit of 1 seconds ended") {
		t.Errorf("exit code = %d, want 124 and the time limit\n%s", code, out)
	}
	if took := time.Since(start); took > 30*time.Second {
		t.Errorf("the script took %s with a time limit of 1 second", took)
	}
}

// The cases follow scripts/setup-repo/test_protected_paths.py, without the
// case of another Unicode form.
func TestCheckPullRequest_ListsAChangedFileForEachRuleOfMatching(t *testing.T) {
	tests := []struct {
		entry, path string
		want        bool
	}{
		// An entry with no inner "/" matches at any depth.
		{"CLAUDE.md", "CLAUDE.md", true},
		{"CLAUDE.md", "sub/dir/CLAUDE.md", true},
		{"CLAUDE.md", "CLAUDE.md/notes.txt", true},
		{"CLAUDE.md", "docs/CLAUDE.md.bak", false},
		{"CLAUDE.md", "docs/MY-CLAUDE.md", false},
		{"CLAUDE.md", "src/main.go", false},
		// Upper case and lower case are the same.
		{"CLAUDE.md", "sub/claude.md", true},
		{"claude.md", "CLAUDE.md", true},
		{".claude/", "app/.CLAUDE/settings.json", true},
		// An entry with a trailing "/" is a directory.
		{".claude/", ".claude/settings.json", true},
		{".claude/", "app/.claude/skills/x/SKILL.md", true},
		{".claude/", ".claude", false},
		{".claude/", "docs/.claude.md", false},
		{".cumin/", ".cumin/config.toml", true},
		// An entry with a leading "/" or an inner "/" matches at that position.
		{"/docs/requirements/", "docs/requirements/overview.md", true},
		{"/docs/requirements/", "sub/docs/requirements/overview.md", false},
		{"/CLAUDE.md", "CLAUDE.md", true},
		{"/CLAUDE.md", "sub/CLAUDE.md", false},
		{"docs/requirements/", "docs/requirements/a/b.md", true},
		{"docs/requirements/", "sub/docs/requirements/b.md", false},
		{"docs/requirements/", "docs/requirements", false},
		{"docs/requirements", "docs/requirements/b.md", true},
		{"docs/requirements", "docs/requirements", true},
		{"docs/requirements", "docs/requirements-old/b.md", false},
	}
	for _, tt := range tests {
		t.Run(tt.entry+" "+tt.path, func(t *testing.T) {
			t.Parallel()
			out, code, _ := runCheck(t, map[string]string{
				"pull":   headCommit + "\tmain\t1\topen\n",
				"config": "protected_paths = [\"" + tt.entry + "\"]\n",
				"files":  "page\nmodified\t" + tt.path + "\t\n",
			})
			// A protected path does not change the exit code. The session
			// names the file to the Maintainer.
			if code != 0 {
				t.Fatalf("exit code = %d, want 0\n%s", code, out)
			}
			listed := strings.Contains(out, "- "+tt.path+" (modified) matches \""+tt.entry+"\"")
			none := strings.Contains(out, "Changed files under a protected path (1 changed files):\n- none")
			if listed != tt.want || none == tt.want {
				t.Errorf("listed = %t, want %t\n%s", listed, tt.want, out)
			}
		})
	}
}

func TestCheckPullRequest_ReadsTheProtectedPathsOfTheSettings(t *testing.T) {
	files := "page\nmodified\tAGENTS.md\t\nrenamed\tdocs/notes.md\t.claude/notes.md\n"
	tests := []struct {
		name    string
		changed map[string]string
		want    []string
		not     []string
	}{
		{"a list of several lines, with comments and both kinds of quotes",
			map[string]string{"config": "merge_method = \"squash\"\n# The list.\nprotected_paths = [ # paths\n  \"AGENTS.md\", # one\n  '.claude/',\n]\npriority_labels = [\"docs/notes.md\"]\n"},
			[]string{"- AGENTS.md (modified) matches \"AGENTS.md\"", "- .claude/notes.md (renamed) matches \".claude/\""},
			[]string{"- docs/notes.md"}},
		{"the list replaces the default list",
			map[string]string{"config": "protected_paths = [\"/docs/\"]\n"},
			[]string{"- docs/notes.md (renamed) matches \"/docs/\""},
			[]string{"- AGENTS.md", "- .claude/notes.md"}},
		{"an empty list",
			map[string]string{"config": "protected_paths = []\n"},
			[]string{"(2 changed files):\n- none"}, nil},
		{"settings without the list give the default list",
			map[string]string{"config": "merge_method = \"squash\"\n"},
			[]string{"- AGENTS.md (modified) matches \"AGENTS.md\"", "- .claude/notes.md (renamed) matches \".claude/\""}, nil},
		{"a repository without the settings gives the default list",
			map[string]string{"config": "-", "config.fail": "gh: Not Found (HTTP 404)\n"},
			[]string{"- AGENTS.md (modified) matches \"AGENTS.md\"", "- .claude/notes.md (renamed) matches \".claude/\""}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.changed["files"] = files
			out, code, _ := runCheck(t, tt.changed)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0\n%s", code, out)
			}
			for _, want := range tt.want {
				if !strings.Contains(out, want) {
					t.Errorf("the output lacks %q\n%s", want, out)
				}
			}
			for _, not := range tt.not {
				if strings.Contains(out, not) {
					t.Errorf("the output holds %q\n%s", not, out)
				}
			}
		})
	}
}

// The script reads the rules of the base branch of the pull request, with
// the name of the branch as one part of the path.
func TestCheckPullRequest_ReadsTheRulesOfTheBaseBranch(t *testing.T) {
	out, code, data := runCheck(t, map[string]string{"pull": headCommit + "\trelease/1.x\t2\topen\n"})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	calls, err := os.ReadFile(filepath.Join(data, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"repos/some-owner/some-repo/pulls/12 ",
		"repos/some-owner/some-repo/rules/branches/release%2F1.x?per_page=100",
		"repos/some-owner/some-repo/commits/" + headCommit + "/check-runs?per_page=100",
		"repos/some-owner/some-repo/commits/" + headCommit + "/status?per_page=100",
		"repos/some-owner/some-repo/contents/.cumin/config.toml",
	} {
		if !strings.Contains(string(calls), want) {
			t.Errorf("gh did not read %q\n%s", want, calls)
		}
	}
}

// The report of the skill and the question of the approval have a fixed
// form. A Maintainer learns them once.
func TestMergeDecisionSkill_HoldsTheFourHeadingsAndTheFixedQuestion(t *testing.T) {
	dir := filepath.Join("skills", "merge-decision")
	text := readText(dir, "SKILL.md")
	last := -1
	for _, heading := range []string{
		"### What changes\n",
		"### What was checked and how\n",
		"### What the Maintainer should look at\n",
		"### Recommendation\n",
	} {
		at := strings.Index(text, heading)
		if at <= last {
			t.Errorf("SKILL.md lacks the heading %q, or its order is wrong", strings.TrimSpace(heading))
		}
		last = at
	}
	for _, want := range []string{
		"`Approve pull request <number> (<meaning>) at commit <first 7 characters of the head commit>?`",
		"`${CLAUDE_SKILL_DIR}/check-pull-request.sh <owner>/<repository> <number>`",
		"Approve one pull request at a time.",
		"only inside the allowance that the Maintainer gave this session",
		"Propose to run it again",
		"Read the whole diff outside the tests",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("SKILL.md lacks %q", want)
		}
	}
	info, err := os.Stat(filepath.Join(dir, "check-pull-request.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Error("check-pull-request.sh is not executable")
	}
}
