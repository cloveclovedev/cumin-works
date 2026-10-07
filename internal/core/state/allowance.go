package state

// This file is the allowance file of the Host: `cumin quota allow` writes
// it, and `cumin run` reads it at every poll ("resume agent starts" of
// issue-states.md). Each file has one writer, so the two processes need no
// lock (designs/cumin-core.md, the topic on the files of the Host).

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// AllowanceFileName is the name of the allowance file in the state
// directory.
const AllowanceFileName = "quota-allowance.json"

// Allowance lets cumin use the rest of one 5h window: the 5h limit is 100%
// until that window resets. It never raises the weekly limit.
type Allowance struct {
	// FiveHourUntil is the reset time of the 5h window that the
	// Operator allowed.
	FiveHourUntil time.Time `json:"five_hour_until"`
}

type allowanceFile struct {
	Version int `json:"version"`
	Allowance
}

// ReadAllowance reads the allowance file. A missing file is no allowance
// and no error. A file that cannot be read, or of another shape or
// version, is an error that names the path; the caller treats it as no
// allowance.
func ReadAllowance(path string) (Allowance, error) {
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Allowance{}, nil
	case err != nil:
		return Allowance{}, fmt.Errorf("allowance: read %s: %w", path, err)
	}
	var read allowanceFile
	if err := json.Unmarshal(raw, &read); err != nil {
		return Allowance{}, fmt.Errorf("allowance: read %s: %w", path, err)
	}
	if read.Version != Version {
		return Allowance{}, fmt.Errorf("allowance: read %s: version %d, want %d", path, read.Version, Version)
	}
	if read.FiveHourUntil.IsZero() {
		return Allowance{}, fmt.Errorf("allowance: read %s: no five_hour_until", path)
	}
	return read.Allowance, nil
}

// WriteAllowance writes the allowance file through a temporary file and a
// rename, so that cumin run never reads half a file.
func WriteAllowance(path string, a Allowance) error {
	raw, err := json.MarshalIndent(allowanceFile{Version: Version, Allowance: a}, "", "  ")
	if err != nil {
		return fmt.Errorf("allowance: write %s: %w", path, err)
	}
	return writeFile(path, append(raw, '\n'))
}

// writeFile writes a file of the state directory through a temporary file
// in the same directory and a rename, so that an interrupted write leaves
// no broken file.
func writeFile(path string, raw []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("state: write %s: %w", path, err)
	}
	temp, err := os.CreateTemp(dir, filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("state: write %s: %w", path, err)
	}
	// Every path out of here removes the temporary file, except the rename,
	// which moves it.
	defer os.Remove(temp.Name())
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("state: write %s: %w", path, err)
	}
	if _, err := temp.Write(raw); err != nil {
		temp.Close()
		return fmt.Errorf("state: write %s: %w", path, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("state: write %s: %w", path, err)
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return fmt.Errorf("state: write %s: %w", path, err)
	}
	return nil
}
