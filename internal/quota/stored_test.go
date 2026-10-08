package quota

import (
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/state"
)

// The usage that the state file keeps comes back as the same usage, and the
// time of the read stays with it.
func TestStoredUsageIsTheUsageThatWasKept(t *testing.T) {
	readAt := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	usage := Usage{
		FiveHour: Window{Utilization: 0.25, ResetsAt: readAt.Add(2 * time.Hour)},
		Weekly:   Window{Utilization: 0.5, ResetsAt: readAt.Add(72 * time.Hour)},
	}
	stored := ToStored(usage, readAt)
	want := state.Quota{
		FiveHour: state.QuotaWindow{Utilization: 0.25, ResetsAt: readAt.Add(2 * time.Hour)},
		Weekly:   state.QuotaWindow{Utilization: 0.5, ResetsAt: readAt.Add(72 * time.Hour)},
		ReadAt:   readAt,
	}
	if stored != want {
		t.Errorf("ToStored = %+v, want %+v", stored, want)
	}
	if got := StoredUsage(stored); got != usage {
		t.Errorf("StoredUsage = %+v, want %+v", got, usage)
	}
}
