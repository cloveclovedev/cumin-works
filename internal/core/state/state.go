// Package state keeps the small amount of state that cumin holds on the
// Host: for each implementation issue, the session of the last agent run and
// the number of check fix requests (I4). The requirement allows only what
// cumin can lose without losing work (cumin-core.md, the section on what
// cumin keeps): a lost file starts a new session and a count of zero.
//
// Only `cumin run` writes the file (designs/cumin-core.md, the topic on the
// files of the Host), so no lock is needed between processes.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// Version is the version of the file format. cumin reads its own version
// only; a file of a later version is treated as missing, because a newer
// cumin may have written keys that this one would drop.
const Version = 1

// Issue is what cumin keeps for one implementation issue.
type Issue struct {
	// SessionID is the session of the last agent run of the issue. A
	// request in the same session resumes it (I4, I5).
	SessionID string `json:"session_id,omitempty"`
	// CheckFixRequests is how many check fixes cumin has asked for since
	// the Owner last added cumin/status/ready (I4).
	CheckFixRequests int `json:"check_fix_requests,omitempty"`
}

// empty reports whether the entry holds nothing, so that Set removes it.
func (i Issue) empty() bool { return i == Issue{} }

// file is the content of the state file.
type file struct {
	Version int `json:"version"`
	// Issues are the entries, by "<owner>/<repo>#<number>".
	Issues map[string]Issue `json:"issues,omitempty"`
}

// Store reads and writes the state file. Every method is safe for
// concurrent use, and every method of a nil Store does nothing: a caller
// without a state file then behaves as if the file had just been lost.
//
// A read and a write are two calls, so a caller that raises a count must be
// the only one working on that issue. cumin is: one issue has one agent at a
// time, and its status label says so.
type Store struct {
	path string

	mu   sync.Mutex
	data file
}

// Open reads the state file at path. A file that is missing, unreadable, or
// of another shape gives an empty state and one warning that names the path,
// never its content: losing the file costs a session and a count, nothing
// else. The directory is created when the first write needs it.
func Open(path string, logger *slog.Logger) *Store {
	store := &Store{path: path, data: file{Version: Version, Issues: map[string]Issue{}}}
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return store
	case err != nil:
		warn(logger, path, "the state file was not read", err)
		return store
	}
	var read file
	if err := json.Unmarshal(raw, &read); err != nil {
		warn(logger, path, "the state file is not valid JSON", err)
		return store
	}
	if read.Version != Version {
		warn(logger, path, "the state file has another version",
			fmt.Errorf("version %d, want %d", read.Version, Version))
		return store
	}
	for key, issue := range read.Issues {
		if !issue.empty() {
			store.data.Issues[key] = issue
		}
	}
	return store
}

func warn(logger *slog.Logger, path, message string, err error) {
	if logger == nil {
		logger = slog.Default()
	}
	logger.Warn(message+"; cumin goes on without it", "path", path, "error", err.Error())
}

// Issues is how many entries the store holds. `cumin run` logs it at start.
func (s *Store) Issues() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.data.Issues)
}

// Issue returns the entry of one implementation issue, or an empty entry.
// repository is "<owner>/<repo>".
func (s *Store) Issue(repository string, number int) Issue {
	if s == nil {
		return Issue{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data.Issues[key(repository, number)]
}

// Set writes the entry of one implementation issue and saves the file. An
// empty entry removes it, as Clear does.
func (s *Store) Set(repository string, number int, issue Issue) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if issue.empty() {
		delete(s.data.Issues, key(repository, number))
	} else {
		s.data.Issues[key(repository, number)] = issue
	}
	return s.save()
}

// Clear removes the entry of one implementation issue and saves the file.
// A claim (I1) calls it: after the Owner adds cumin/status/ready, the next
// request starts a new session and the count starts at zero.
func (s *Store) Clear(repository string, number int) error {
	return s.Set(repository, number, Issue{})
}

// key names one implementation issue: "<owner>/<repo>#<number>".
func key(repository string, number int) string {
	return fmt.Sprintf("%s#%d", repository, number)
}

// save writes the whole file through a temporary file in the same directory
// and a rename, so that an interrupted write leaves no broken file. The
// caller holds the lock.
func (s *Store) save() error {
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("state: write %s: %w", s.path, err)
	}
	raw = append(raw, '\n')
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("state: write %s: %w", s.path, err)
	}
	temp, err := os.CreateTemp(dir, filepath.Base(s.path)+".*")
	if err != nil {
		return fmt.Errorf("state: write %s: %w", s.path, err)
	}
	// Every path out of here removes the temporary file, except the rename,
	// which moves it.
	defer os.Remove(temp.Name())
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("state: write %s: %w", s.path, err)
	}
	if _, err := temp.Write(raw); err != nil {
		temp.Close()
		return fmt.Errorf("state: write %s: %w", s.path, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("state: write %s: %w", s.path, err)
	}
	if err := os.Rename(temp.Name(), s.path); err != nil {
		return fmt.Errorf("state: write %s: %w", s.path, err)
	}
	return nil
}
