package state_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
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
