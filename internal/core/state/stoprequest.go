package state

// This file is the stop request file of the Host: `cumin stop
// --after-current-runs` writes it, and `cumin run` reads it at every poll.
// `cumin run` removes it when it starts and when that stop ends, so a
// request never reaches the next start (designs/cumin-core.md, the topic on
// the stop).

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// StopRequestFileName is the name of the stop request file in the state
// directory.
const StopRequestFileName = "stop-request.json"

// StopRequest asks the running cumin to start no new work, to let the agent runs
// that are going on end, and then to exit.
type StopRequest struct {
	// RequestedAt is when the Operator asked, for cumin status.
	RequestedAt time.Time `json:"requested_at"`
}

type stopRequestFile struct {
	Version int `json:"version"`
	StopRequest
}

// ReadStopRequest reads the stop request file. A missing file is no request and
// no error. A file that cannot be read, or of another shape or version, is
// an error that names the path; the caller treats it as no request.
func ReadStopRequest(path string) (StopRequest, bool, error) {
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return StopRequest{}, false, nil
	case err != nil:
		return StopRequest{}, false, fmt.Errorf("stop request: read %s: %w", path, err)
	}
	var read stopRequestFile
	if err := json.Unmarshal(raw, &read); err != nil {
		return StopRequest{}, false, fmt.Errorf("stop request: read %s: %w", path, err)
	}
	if read.Version != Version {
		return StopRequest{}, false, fmt.Errorf("stop request: read %s: version %d, want %d", path, read.Version, Version)
	}
	if read.RequestedAt.IsZero() {
		return StopRequest{}, false, fmt.Errorf("stop request: read %s: no requested_at", path)
	}
	return read.StopRequest, true, nil
}

// WriteStopRequest writes the stop request file through a temporary file and a
// rename, so that cumin run never reads half a file.
func WriteStopRequest(path string, d StopRequest) error {
	raw, err := json.MarshalIndent(stopRequestFile{Version: Version, StopRequest: d}, "", "  ")
	if err != nil {
		return fmt.Errorf("stop request: write %s: %w", path, err)
	}
	return writeFile(path, append(raw, '\n'))
}

// RemoveStopRequest removes the stop request file, and says whether there was
// one. A missing file is no error.
func RemoveStopRequest(path string) (bool, error) {
	err := os.Remove(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("stop request: remove %s: %w", path, err)
	}
	return true, nil
}
