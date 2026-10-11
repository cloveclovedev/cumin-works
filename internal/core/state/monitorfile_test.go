package state_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/state"
)

// monitorFileOf is a monitor file with every field set.
func monitorFileOf(at time.Time) state.MonitorFile {
	next := at.Add(2 * time.Hour)
	return state.MonitorFile{
		Version: state.MonitorFileVersion,
		LastPoll: state.MonitorLastPoll{At: at, Errors: []state.MonitorPollError{
			{Repository: "example/app", Message: "read the snapshot: GitHub returned 502"},
		}},
		StopRequested: true,
		Quota:         state.MonitorQuota{State: state.MonitorQuotaStopped, StoppedWindows: []string{"5h"}, NextTryAt: &next},
	}
}

// The monitor file has the mode of the state file, and its directory holds
// no temporary file after the write.
func TestMonitorFile_TheWriteLeavesOneFileThatOnlyTheUserReads(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub")
	path := filepath.Join(dir, state.MonitorFileName)
	for range 3 {
		if err := state.WriteMonitorFile(path, monitorFileOf(time.Date(2026, 10, 4, 7, 0, 5, 0, time.UTC))); err != nil {
			t.Fatalf("WriteMonitorFile: %v", err)
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, %v, want 0600", info.Mode().Perm(), err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != state.MonitorFileName {
		t.Errorf("the directory holds %v, want only %s", entries, state.MonitorFileName)
	}
}

// A reader that reads while cumin writes gets a whole file every time: the
// write is a rename of a temporary file.
func TestMonitorFile_AReaderNeverSeesAPartialFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), state.MonitorFileName)
	at := time.Date(2026, 10, 4, 7, 0, 5, 0, time.UTC)
	done := make(chan struct{})
	written := make(chan error, 1)
	go func() {
		defer close(done)
		for i := range 200 {
			if err := state.WriteMonitorFile(path, monitorFileOf(at.Add(time.Duration(i)*time.Second))); err != nil {
				written <- err
				return
			}
		}
	}()
	reads := 0
	for writing := true; writing; {
		select {
		case <-done:
			writing = false
		default:
		}
		raw, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatalf("read the monitor file: %v", err)
		}
		var read state.MonitorFile
		if err := json.Unmarshal(raw, &read); err != nil {
			t.Fatalf("a reader saw a file that is not whole: %v\n%q", err, raw)
		}
		if read.Version != state.MonitorFileVersion || read.Quota.State != state.MonitorQuotaStopped || len(read.LastPoll.Errors) != 1 {
			t.Fatalf("a reader saw a file that is not whole:\n%s", raw)
		}
		reads++
	}
	select {
	case err := <-written:
		t.Fatalf("WriteMonitorFile: %v", err)
	default:
	}
	if reads == 0 {
		t.Fatal("the reader read no file")
	}
}

// An array with no element is written as [], a time in UTC, and a missing
// next try time as no key.
func TestMonitorFile_EmptyArraysAreWrittenAndAMissingNextTryIsNot(t *testing.T) {
	path := filepath.Join(t.TempDir(), state.MonitorFileName)
	at := time.Date(2026, 10, 4, 16, 0, 5, 0, time.FixedZone("UTC+09:00", 9*3600))
	m := state.MonitorFile{Version: state.MonitorFileVersion, LastPoll: state.MonitorLastPoll{At: at}, Quota: state.MonitorQuota{State: state.MonitorQuotaOpen}}
	if err := state.WriteMonitorFile(path, m); err != nil {
		t.Fatalf("WriteMonitorFile: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "version": 1,
  "last_poll": {
    "at": "2026-10-04T07:00:05Z",
    "errors": []
  },
  "stop_requested": false,
  "quota": {
    "state": "open",
    "stopped_windows": []
  },
  "agents": [],
  "waiting": []
}
`
	if string(raw) != want {
		t.Errorf("the monitor file is\n%s\nwant\n%s", raw, want)
	}
}

// A reader gets back what cumin run wrote, and a missing file is no file
// and no error.
func TestMonitorFile_TheReaderReturnsWhatWasWritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), state.MonitorFileName)
	if _, found, err := state.ReadMonitorFile(path); found || err != nil {
		t.Fatalf("a missing file: found = %v, err = %v, want no file and no error", found, err)
	}
	at := time.Date(2026, 10, 4, 7, 0, 5, 0, time.UTC)
	written := monitorFileOf(at)
	written.Agents = []state.MonitorAgent{{Repository: "example/app", Issue: 7, Role: "implementer", Request: "implement", Title: "a title"}}
	if err := state.WriteMonitorFile(path, written); err != nil {
		t.Fatalf("WriteMonitorFile: %v", err)
	}
	read, found, err := state.ReadMonitorFile(path)
	if err != nil || !found {
		t.Fatalf("ReadMonitorFile: found = %v, err = %v", found, err)
	}
	if !read.LastPoll.At.Equal(at) || len(read.LastPoll.Errors) != 1 || read.LastPoll.Errors[0] != written.LastPoll.Errors[0] ||
		len(read.Agents) != 1 || read.Agents[0] != written.Agents[0] || len(read.Waiting) != 0 {
		t.Errorf("the reader returned %+v, want what was written", read)
	}
}

// A file of a version above the one that this cumin knows, or of another
// shape, is an error that names the path and the reason.
func TestMonitorFile_TheReaderRejectsANewerVersionAndAnotherShape(t *testing.T) {
	for name, tc := range map[string]struct{ content, want string }{
		"a newer version":   {`{"version": 2, "last_poll": {"at": "2026-10-04T07:00:05Z", "errors": []}, "agents": [], "waiting": []}`, "version 2, and this cumin knows version 1"},
		"no version":        {`{"last_poll": {"at": "2026-10-04T07:00:05Z", "errors": []}, "agents": [], "waiting": []}`, "no version"},
		"not JSON":          {`{"version": 1,`, "unexpected end of JSON input"},
		"no last_poll.at":   {`{"version": 1, "last_poll": {"errors": []}, "agents": [], "waiting": []}`, "no last_poll.at"},
		"no list of errors": {`{"version": 1, "last_poll": {"at": "2026-10-04T07:00:05Z"}, "agents": [], "waiting": []}`, "no last_poll.errors"},
		"no list of agents": {`{"version": 1, "last_poll": {"at": "2026-10-04T07:00:05Z", "errors": []}, "waiting": []}`, "no agents"},
		"no list of waits":  {`{"version": 1, "last_poll": {"at": "2026-10-04T07:00:05Z", "errors": []}, "agents": []}`, "no waiting"},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), state.MonitorFileName)
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, found, err := state.ReadMonitorFile(path)
			if found || err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), path) {
				t.Errorf("found = %v, err = %v, want an error with %q and the path", found, err, tc.want)
			}
		})
	}
}
