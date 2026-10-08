// The conversions between the usage of the rules and the usage that the
// state file keeps. They are the only place that copies the two windows, so
// that a new field of a window changes one file.

package quota

import (
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/state"
)

// StoredUsage converts the usage that the state file keeps.
func StoredUsage(stored state.Quota) Usage {
	return Usage{
		FiveHour: Window{Utilization: stored.FiveHour.Utilization, ResetsAt: stored.FiveHour.ResetsAt},
		Weekly:   Window{Utilization: stored.Weekly.Utilization, ResetsAt: stored.Weekly.ResetsAt},
	}
}

// ToStored converts a usage that was read at readAt to the form that the
// state file keeps.
func ToStored(usage Usage, readAt time.Time) state.Quota {
	return state.Quota{
		FiveHour: state.QuotaWindow{Utilization: usage.FiveHour.Utilization, ResetsAt: usage.FiveHour.ResetsAt},
		Weekly:   state.QuotaWindow{Utilization: usage.Weekly.Utilization, ResetsAt: usage.Weekly.ResetsAt},
		ReadAt:   readAt,
	}
}
