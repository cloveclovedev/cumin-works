package state_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/state"
)

func TestAllowance_WrittenAndReadBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", state.AllowanceFileName)
	until := time.Date(2026, 10, 1, 15, 0, 0, 0, time.UTC)
	if err := state.WriteAllowance(path, state.Allowance{FiveHourUntil: until}); err != nil {
		t.Fatalf("WriteAllowance: %v", err)
	}
	got, err := state.ReadAllowance(path)
	if err != nil || !got.FiveHourUntil.Equal(until) {
		t.Errorf("ReadAllowance = %+v, %v, want until %v", got, err, until)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, %v, want 0600", info.Mode().Perm(), err)
	}
}

func TestAllowance_AMissingFileIsNoAllowance(t *testing.T) {
	got, err := state.ReadAllowance(filepath.Join(t.TempDir(), state.AllowanceFileName))
	if err != nil || !got.FiveHourUntil.IsZero() {
		t.Errorf("ReadAllowance = %+v, %v, want no allowance and no error", got, err)
	}
}

func TestAllowance_ABrokenFileIsAnErrorThatNamesThePath(t *testing.T) {
	for name, content := range map[string]string{
		"not JSON":      "{",
		"other version": `{"version":99,"five_hour_until":"2026-10-01T15:00:00Z"}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), state.AllowanceFileName)
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := state.ReadAllowance(path)
			if err == nil || !strings.Contains(err.Error(), path) {
				t.Errorf("err = %v, want an error that names %s", err, path)
			}
		})
	}
}
