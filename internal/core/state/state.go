// Package state keeps the small amount of state that cumin holds on the
// Host: for each implementation issue, the session of the last agent run and
// the number of check fix requests (I4); for each requirement issue in
// cumin/status/accepting, the session of the last Planner run and the number
// of repeated acceptance check requests; and the latest quota usage (Q3). The requirement allows only what
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
	"strings"
	"sync"
	"time"
)

// Version is the version of the file format. cumin reads its own version
// and the earlier ones, whose keys are a subset of its own; a file of a
// later version is treated as missing, because a newer cumin may have
// written keys that this one would drop. Version 2 added the session of the
// Reviewer.
const Version = 2

// Issue is what cumin keeps for one implementation issue.
type Issue struct {
	// SessionID is the session of the last Implementer run of the issue.
	// A request in the same session resumes it (I4, I5).
	SessionID string `json:"session_id,omitempty"`
	// ReviewerSessionID is the session of the last Reviewer run of the
	// issue. The Reviewer and the Implementer never share a session
	// (agents/reviewer.md); round 2 and later resume this one (I3).
	ReviewerSessionID string `json:"reviewer_session_id,omitempty"`
	// CheckFixRequests is how many check fixes cumin has asked for since
	// the Owner last added cumin/status/ready (I4).
	CheckFixRequests int `json:"check_fix_requests,omitempty"`
	// AcceptanceRequests is how many times cumin has requested the
	// acceptance check again during this stay of a requirement issue in
	// cumin/status/accepting. The entry of a requirement issue holds it,
	// with the session of the last Planner run in SessionID.
	AcceptanceRequests int `json:"acceptance_requests,omitempty"`
	// SplitRequests is how many times cumin has requested the split again
	// during this stay of a requirement issue in cumin/status/planning.
	SplitRequests int `json:"split_requests,omitempty"`
}

// empty reports whether the entry holds nothing, so that Set removes it.
func (i Issue) empty() bool { return i == Issue{} }

// QuotaWindow is the usage of one quota window.
type QuotaWindow struct {
	// Utilization is from 0 to 1.
	Utilization float64   `json:"utilization"`
	ResetsAt    time.Time `json:"resets_at"`
}

// Quota is the latest quota usage that cumin read, and when it read it.
// While new starts stop, cumin decides from it when to try again, so that
// a restart makes no minimal run before that time (Q3). The file keeps
// only the numbers of the account; it holds no token.
type Quota struct {
	FiveHour QuotaWindow `json:"five_hour"`
	Weekly   QuotaWindow `json:"weekly"`
	ReadAt   time.Time   `json:"read_at"`
}

// file is the content of the state file. A file of this version without
// the quota key is a file from before the quota was kept; it opens with no
// usage.
type file struct {
	Version int `json:"version"`
	// Issues are the entries, by "<owner>/<repo>#<number>".
	Issues map[string]Issue `json:"issues,omitempty"`
	// Quota is the latest quota usage, or nil.
	Quota *Quota `json:"quota,omitempty"`
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
	if read.Version < 1 || read.Version > Version {
		warn(logger, path, "the state file has another version",
			fmt.Errorf("version %d, want %d", read.Version, Version))
		return store
	}
	for name, issue := range read.Issues {
		if !issue.empty() {
			store.data.Issues[strings.ToLower(name)] = issue
		}
	}
	store.data.Quota = read.Quota
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
// empty entry removes it, as Clear does. When the file cannot be saved, the
// entry keeps its value from before, and the error says why.
func (s *Store) Set(repository string, number int, issue Issue) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(repository, number)
	before, had := s.data.Issues[k]
	if issue.empty() {
		delete(s.data.Issues, k)
	} else {
		s.data.Issues[k] = issue
	}
	if err := s.save(); err != nil {
		// The entry goes back to what the file holds, so that a caller that
		// tries again never builds on a change that was not saved (I4 would
		// count requests that never started).
		if had {
			s.data.Issues[k] = before
		} else {
			delete(s.data.Issues, k)
		}
		return err
	}
	return nil
}

// Quota returns the latest quota usage, and false when none is kept.
func (s *Store) Quota() (Quota, bool) {
	if s == nil {
		return Quota{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.Quota == nil {
		return Quota{}, false
	}
	return *s.data.Quota, true
}

// SetQuota keeps the latest quota usage and saves the file. When the file
// cannot be saved, the usage keeps its value from before, and the error
// says why.
func (s *Store) SetQuota(q Quota) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	before := s.data.Quota
	s.data.Quota = &q
	if err := s.save(); err != nil {
		s.data.Quota = before
		return err
	}
	return nil
}

// Clear removes the entry of one implementation issue and saves the file.
// A claim (I1) calls it: after the Owner adds cumin/status/ready, the next
// request starts a new session and the count starts at zero.
func (s *Store) Clear(repository string, number int) error {
	return s.Set(repository, number, Issue{})
}

// key names one implementation issue: "<owner>/<repo>#<number>". The
// repository is lower case, because GitHub account and repository names
// ignore case: the Host may write the same repository with another
// capitalization in its settings, and the entry must still be found.
func key(repository string, number int) string {
	return fmt.Sprintf("%s#%d", strings.ToLower(repository), number)
}

// save writes the whole file through a temporary file in the same directory
// and a rename, so that an interrupted write leaves no broken file. The
// caller holds the lock.
func (s *Store) save() error {
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("state: write %s: %w", s.path, err)
	}
	return writeFile(s.path, append(raw, '\n'))
}
