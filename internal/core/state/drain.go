package state

// This file is the drain request file of the Host: `cumin stop
// --after-current-runs` writes it, and `cumin run` reads it at every poll.
// `cumin run` removes it when it starts and when the drain ends, so a
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

// DrainFileName is the name of the drain request file in the state
// directory.
const DrainFileName = "drain-request.json"

// Drain asks the running cumin to start no new work, to let the agent runs
// that are going on end, and then to exit.
type Drain struct {
	// RequestedAt is when the Owner asked, for cumin status.
	RequestedAt time.Time `json:"requested_at"`
}

type drainFile struct {
	Version int `json:"version"`
	Drain
}

// ReadDrain reads the drain request file. A missing file is no request and
// no error. A file that cannot be read, or of another shape or version, is
// an error that names the path; the caller treats it as no request.
func ReadDrain(path string) (Drain, bool, error) {
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Drain{}, false, nil
	case err != nil:
		return Drain{}, false, fmt.Errorf("drain: read %s: %w", path, err)
	}
	var read drainFile
	if err := json.Unmarshal(raw, &read); err != nil {
		return Drain{}, false, fmt.Errorf("drain: read %s: %w", path, err)
	}
	if read.Version != Version {
		return Drain{}, false, fmt.Errorf("drain: read %s: version %d, want %d", path, read.Version, Version)
	}
	if read.RequestedAt.IsZero() {
		return Drain{}, false, fmt.Errorf("drain: read %s: no requested_at", path)
	}
	return read.Drain, true, nil
}

// WriteDrain writes the drain request file through a temporary file and a
// rename, so that cumin run never reads half a file.
func WriteDrain(path string, d Drain) error {
	raw, err := json.MarshalIndent(drainFile{Version: Version, Drain: d}, "", "  ")
	if err != nil {
		return fmt.Errorf("drain: write %s: %w", path, err)
	}
	return writeFile(path, append(raw, '\n'))
}

// RemoveDrain removes the drain request file, and says whether there was
// one. A missing file is no error.
func RemoveDrain(path string) (bool, error) {
	err := os.Remove(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("drain: remove %s: %w", path, err)
	}
	return true, nil
}
