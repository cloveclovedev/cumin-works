package github_test

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// diagramsBranch is the one branch that the Planner App may write: the
// diagrams of its sub-issues.
const diagramsBranch = "cumin/diagrams"

// TestLiveDiagramsBranch checks that the Planner App writes the branch
// cumin/diagrams and nothing else. It needs the rulesets of
// scripts/setup-repo.sh with --core-app and --implementer-app, and the
// Planner App with Contents: Read & write, approved on the installation.
func TestLiveDiagramsBranch(t *testing.T) {
	l := newLive(t)
	defer func() { t.Log("\n" + l.table()) }()

	planner := l.token(t, "planner")
	implementer := l.token(t, "implementer")
	core := l.token(t, "cumin-core")
	path := "live/" + l.runID + ".svg"

	// Add one file on cumin/diagrams, as the Planner does: an orphan commit
	// when the branch does not exist yet, a child of its head when it does.
	var head string
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if r := l.api(t, planner, http.MethodGet, "/repos/{repo}/git/ref/heads/"+diagramsBranch, nil); r.status == http.StatusOK {
		r.json(t, &ref)
		head = ref.Object.SHA
	}
	commit := l.plannerCommit(t, planner, head, path)
	var status int
	if head == "" {
		status = l.api(t, planner, http.MethodPost, "/repos/{repo}/git/refs", map[string]string{"ref": "refs/heads/" + diagramsBranch, "sha": commit}).status
	} else {
		status = l.api(t, planner, http.MethodPatch, "/repos/{repo}/git/refs/heads/"+diagramsBranch, map[string]any{"sha": commit}).status
	}
	l.expectAllowed(t, "D1", "The Planner App adds a file to cumin/diagrams", status)

	// Everything else is refused for the Planner App.
	l.expectRefused(t, "D2", "The Planner App creates another branch",
		l.api(t, planner, http.MethodPost, "/repos/{repo}/git/refs", map[string]string{"ref": "refs/heads/live/planner-" + l.runID, "sha": commit}))
	l.expectRefused(t, "D3", "The Planner App moves the default branch",
		l.api(t, planner, http.MethodPatch, "/repos/{repo}/git/refs/heads/"+l.branch, map[string]any{"sha": commit, "force": true}))
	l.expectRefused(t, "D4", "The Planner App creates a tag",
		l.api(t, planner, http.MethodPost, "/repos/{repo}/git/refs", map[string]string{"ref": "refs/tags/live-planner-" + l.runID, "sha": commit}))
	// A commit with no shared history, so that moving the branch to it is a
	// force push also on the first run, when the branch did not exist.
	unrelated := l.plannerCommit(t, planner, "", "live/"+l.runID+"-unrelated.svg")
	l.expectRefused(t, "D5", "The Planner App force-pushes cumin/diagrams to an unrelated commit",
		l.api(t, planner, http.MethodPatch, "/repos/{repo}/git/refs/heads/"+diagramsBranch, map[string]any{"sha": unrelated, "force": true}))
	l.expectRefused(t, "D6", "The Planner App deletes cumin/diagrams",
		l.api(t, planner, http.MethodDelete, "/repos/{repo}/git/refs/heads/"+diagramsBranch, nil))
	l.expectRefused(t, "D7", "The cumin-core App deletes cumin/diagrams",
		l.api(t, core, http.MethodDelete, "/repos/{repo}/git/refs/heads/"+diagramsBranch, nil))

	// The Implementer App still creates and deletes its branches.
	var main struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	l.api(t, implementer, http.MethodGet, "/repos/{repo}/git/ref/heads/"+l.branch, nil).mustJSON(t, http.StatusOK, &main)
	branch := "live/implementer-" + l.runID
	l.expectAllowed(t, "D8", "The Implementer App creates a branch",
		l.api(t, implementer, http.MethodPost, "/repos/{repo}/git/refs", map[string]string{"ref": "refs/heads/" + branch, "sha": main.Object.SHA}).status)
	l.expectAllowed(t, "D9", "The Implementer App deletes its branch",
		l.api(t, implementer, http.MethodDelete, "/repos/{repo}/git/refs/heads/"+branch, nil).status)
}

// plannerCommit creates a commit that adds one small SVG at path, on top of
// parent, or with no parent when parent is empty. It returns the commit SHA.
func (l *live) plannerCommit(t *testing.T, token, parent, path string) string {
	t.Helper()
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"/>`
	var sha struct {
		SHA  string `json:"sha"`
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	l.api(t, token, http.MethodPost, "/repos/{repo}/git/blobs", map[string]string{"content": base64.StdEncoding.EncodeToString([]byte(svg)), "encoding": "base64"}).mustJSON(t, http.StatusCreated, &sha)
	tree := map[string]any{"tree": []map[string]string{{"path": path, "mode": "100644", "type": "blob", "sha": sha.SHA}}}
	parents := []string{}
	if parent != "" {
		var parentCommit struct {
			Tree struct {
				SHA string `json:"sha"`
			} `json:"tree"`
		}
		l.api(t, token, http.MethodGet, "/repos/{repo}/git/commits/"+parent, nil).mustJSON(t, http.StatusOK, &parentCommit)
		tree["base_tree"] = parentCommit.Tree.SHA
		parents = append(parents, parent)
	}
	l.api(t, token, http.MethodPost, "/repos/{repo}/git/trees", tree).mustJSON(t, http.StatusCreated, &sha)
	l.api(t, token, http.MethodPost, "/repos/{repo}/git/commits", map[string]any{"message": "Add " + path, "tree": sha.SHA, "parents": parents}).mustJSON(t, http.StatusCreated, &sha)
	return sha.SHA
}

func (l *live) expectAllowed(t *testing.T, number, what string, status int) {
	t.Helper()
	l.record(number, what, "Allowed", fmt.Sprintf("Status %d", status))
	if status < 200 || status > 299 {
		t.Errorf("%s: %s: status %d, want success", number, what, status)
	}
}

// expectRefused requires a refusal by a ruleset: an error status with a
// rule violation in the message, not any other failure.
func (l *live) expectRefused(t *testing.T, number, what string, r response) {
	t.Helper()
	l.record(number, what, "Refused by a ruleset", fmt.Sprintf("Status %d: %s", r.status, r.message()))
	if r.status < 400 || !strings.Contains(strings.ToLower(r.message()), "rule") {
		t.Errorf("%s: %s: status %d: %q, want a refusal by a ruleset", number, what, r.status, r.message())
	}
}
