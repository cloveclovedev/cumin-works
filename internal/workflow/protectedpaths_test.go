package workflow_test

import (
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/core/state"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

// protectedPathRules are the five rules of matching that every start
// request holds after the entries: any depth, a fixed position, a
// directory, no wildcard, no case.
const protectedPathRules = "- Rules of matching of the protected paths:\n" +
	"  - An entry with no \"/\" other than a trailing \"/\" matches at any depth. \"CLAUDE.md\" also matches \"sub/CLAUDE.md\".\n" +
	"  - An entry with a leading \"/\" or an inner \"/\" matches only at that position from the top of the repository.\n" +
	"  - An entry with a trailing \"/\" is a directory. It matches everything below that directory.\n" +
	"  - Wildcards do not work.\n" +
	"  - Upper case and lower case are the same. \"claude.md\" matches \"CLAUDE.md\".\n"

// The start request of every role carries the protected paths that apply
// in the repository, then the rules of matching
// (docs/ja/requirements/agents/common.md, the facts of the start request).
// With protected_paths in .cumin/config.toml the list is exactly the one of
// the file. With no file, or a file without the key, it is the default
// list.
func TestStartRequest_EveryRoleReceivesTheProtectedPathsAndTheRulesOfMatching(t *testing.T) {
	roles := []struct {
		name string
		// start makes the scene and runs the polls up to the request. It
		// returns the scene, whose record holds the last run of the CLI.
		start func(t *testing.T, config *string) *scene
		kind  string
	}{
		{"the Planner", func(t *testing.T, config *string) *scene {
			sc := newPlanScene(t)
			sc.setConfig(config)
			sc.pollAndWait(t, sc.service())
			return sc
		}, "Request: plan"},
		{"the Implementer", func(t *testing.T, config *string) *scene {
			sc := newScene(t)
			sc.setConfig(config)
			sc.addUnlinkedPullRequest(21, sc.remoteHead)
			sc.pollAndWait(t, sc.service())
			return sc
		}, "Request: implement"},
		{"the Reviewer", func(t *testing.T, config *string) *scene {
			sc := newScene(t, cliOptions{reviews: []string{"APPROVE"}})
			sc.setConfig(config)
			service := sc.service()
			sc.reviewing(t, service, state.Issue{SessionID: "implementer-session"})
			sc.pollAndWait(t, service)
			return sc
		}, "Request: review"},
		{"the Reviewer that explains the cause", func(t *testing.T, config *string) *scene {
			sc := newScene(t, cliOptions{reviews: []string{"REQUEST_CHANGES", "NONE"}, comments: []string{"NONE", "DECISION"}})
			sc.setConfig(config)
			service := sc.service()
			sc.reviewing(t, service, state.Issue{SessionID: "implementer-session"})
			sc.atTheLimit(t)
			sc.pollAndWait(t, service)
			return sc
		}, "Request: explain the cause"},
	}

	const defaultEntries = "  - `.cumin/`\n  - `CLAUDE.md`\n  - `AGENTS.md`\n  - `.claude/`\n"
	withKey := "max_review_rounds = 3\nprotected_paths = [\".cumin/\", \"/docs/requirements/\"]\n"
	withoutKey := "max_review_rounds = 3\n"
	files := []struct {
		name    string
		config  *string
		entries string
	}{
		{"with the key", &withKey, "  - `.cumin/`\n  - `/docs/requirements/`\n"},
		{"with no file", nil, defaultEntries},
		{"with no key", &withoutKey, defaultEntries},
	}

	for _, role := range roles {
		for _, file := range files {
			t.Run(role.name+" "+file.name, func(t *testing.T) {
				sc := role.start(t, file.config)
				text := promptOf(t, sc.record(t, "agent.args"))
				if !strings.Contains(text, role.kind) {
					t.Fatalf("the last request is not %q:\n%s", role.kind, text)
				}
				// The rules follow the last entry, so the list holds
				// exactly these entries.
				want := "- Protected paths (agents keep these paths unchanged):\n" + file.entries + protectedPathRules
				facts, _, _ := strings.Cut(text, "\n\n")
				if !strings.Contains(facts, want) {
					t.Errorf("the facts of the run do not hold %q:\n%s", want, text)
				}
			})
		}
	}
}

// setConfig writes the .cumin/config.toml of the repository. Nil leaves the
// repository without the file.
func (sc *scene) setConfig(content *string) {
	if content != nil {
		sc.fake.SetFile(sc.repo, ".cumin/config.toml", githubtest.File{Content: *content})
	}
}
