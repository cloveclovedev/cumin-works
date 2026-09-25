package state_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/core/state"
)

func TestOpen_AMissingFileIsAnEmptyState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "state.json")
	logs := &bytes.Buffer{}

	store := state.Open(path, logger(logs))

	if store.Issues() != 0 {
		t.Errorf("%d issues, want none", store.Issues())
	}
	if logs.Len() != 0 {
		t.Errorf("a missing file warned: %s", logs)
	}
	// The first write makes the directory.
	if err := store.Set("example-org/example-repo", 12, state.Issue{SessionID: "s-1"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the file was not written: %v", err)
	}
}

func TestSetAndClear_SurviveANewStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	const repo = "example-org/example-repo"
	store := state.Open(path, nil)

	if err := store.Set(repo, 12, state.Issue{SessionID: "s-1", CheckFixRequests: 2}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := store.Set(repo, 13, state.Issue{SessionID: "s-2"}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// A new process reads the file back: the session and the count are kept.
	again := state.Open(path, nil)
	if got := again.Issue(repo, 12); got.SessionID != "s-1" || got.CheckFixRequests != 2 {
		t.Errorf("issue #12 = %+v, want the session and the count", got)
	}
	if got := again.Issue(repo, 13); got.SessionID != "s-2" {
		t.Errorf("issue #13 = %+v", got)
	}
	// Another repository with the same number is another entry.
	if got := again.Issue("other-org/other-repo", 12); got != (state.Issue{}) {
		t.Errorf("issue of another repository = %+v, want empty", got)
	}

	if err := again.Clear(repo, 12); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if got := state.Open(path, nil).Issue(repo, 12); got != (state.Issue{}) {
		t.Errorf("after Clear: %+v, want empty", got)
	}
	if n := state.Open(path, nil).Issues(); n != 1 {
		t.Errorf("%d issues after Clear, want 1", n)
	}
}

// TestSave_WritesTheVersionAndOnlyTheAllowedKeys keeps the file to what the
// requirement allows: the session and the count of an issue, and nothing
// else. A quota number, a token, or a path must never be in it.
func TestSave_WritesTheVersionAndOnlyTheAllowedKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store := state.Open(path, nil)
	if err := store.Set("example-org/example-repo", 12, state.Issue{SessionID: "s-1", CheckFixRequests: 1}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var read struct {
		Version int                       `json:"version"`
		Issues  map[string]map[string]any `json:"issues"`
	}
	if err := json.Unmarshal(raw, &read); err != nil {
		t.Fatalf("the file is not valid JSON: %v\n%s", err, raw)
	}
	if read.Version != state.Version {
		t.Errorf("version = %d, want %d", read.Version, state.Version)
	}
	entry, ok := read.Issues["example-org/example-repo#12"]
	if !ok {
		t.Fatalf("the file has no entry of the issue: %s", raw)
	}
	for name := range entry {
		if name != "session_id" && name != "check_fix_requests" {
			t.Errorf("the entry has the key %q", name)
		}
	}
	// The file is the Host's own; other users have no reason to read it.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("mode = %o, want 600", mode)
	}
	// The write left no temporary file behind.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("the directory holds %d files, want only the state file", len(entries))
	}
}

// TestOpen_ABrokenFileIsAnEmptyStateWithOneWarning: cumin must start even
// when the file is broken. It loses a session and a count, nothing else.
func TestOpen_ABrokenFileIsAnEmptyStateWithOneWarning(t *testing.T) {
	for _, tc := range []struct {
		name, content, message string
	}{
		{name: "not JSON", content: "{oops", message: "not valid JSON"},
		{name: "another version", content: `{"version":99,"issues":{"a#1":{"session_id":"s"}}}`, message: "another version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			logs := &bytes.Buffer{}

			store := state.Open(path, logger(logs))

			if store.Issues() != 0 {
				t.Errorf("%d issues, want none", store.Issues())
			}
			if !strings.Contains(logs.String(), tc.message) || !strings.Contains(logs.String(), "WARN") {
				t.Errorf("logs = %s, want one warning with %q", logs, tc.message)
			}
			if strings.Contains(logs.String(), "session_id") {
				t.Errorf("the warning holds the content of the file: %s", logs)
			}
		})
	}
}

// TestNilStore_DoesNothing lets a caller run without a state file: it then
// behaves as if the file had just been lost.
func TestNilStore_DoesNothing(t *testing.T) {
	var store *state.Store

	if store.Issues() != 0 || store.Issue("example-org/example-repo", 12) != (state.Issue{}) {
		t.Error("a nil store returned state")
	}
	if err := store.Set("example-org/example-repo", 12, state.Issue{SessionID: "s-1"}); err != nil {
		t.Errorf("Set on a nil store: %v", err)
	}
	if err := store.Clear("example-org/example-repo", 12); err != nil {
		t.Errorf("Clear on a nil store: %v", err)
	}
}

func logger(out *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
