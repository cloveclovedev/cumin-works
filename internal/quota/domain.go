// Package quota holds the pure rules of the quota limits (Q1 to Q3 of
// docs/ja/requirements/workflow/issue-states.md): the pace limit of the
// weekly window, the limit of the 5h window by time band, and whether a
// window stops a new start. docs/ja/designs/quota.md records the design.
//
// Nothing here reads a clock, a file, or a CLI. The caller gives the usage,
// the settings, and the time, so that a test can try any time of a week.
package quota

import (
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
)

// week is the length of the weekly window. Its start is its reset time
// minus one week; no setting holds the day or the hour of the reset.
const week = 7 * 24 * time.Hour

// Window is the usage of one quota window.
type Window struct {
	// Utilization is from 0 to 1 (measured-constraints.md row 1).
	Utilization float64
	// ResetsAt is when the window resets.
	ResetsAt time.Time
}

// Usage is the usage of the two quota windows.
type Usage struct {
	FiveHour Window
	Weekly   Window
}

// Name names a quota window in logs and notifications.
type Name string

// The quota windows, in the order in which Decide reports them.
const (
	FiveHour Name = "5h"
	Weekly   Name = "weekly"
)

// Decision is the result of Decide.
type Decision struct {
	// FiveHourLimit and WeeklyLimit are the limits at the time of the
	// decision, in percent.
	FiveHourLimit float64
	WeeklyLimit   float64
	// Stopped are the windows whose usage is at or above their limit. It
	// is empty when a start may go on.
	Stopped []Name
}

// Allows reports whether a new start may go on.
func (d Decision) Allows() bool { return len(d.Stopped) == 0 }

// Decide compares the usage with the limit of each window at now. The time
// bands of the 5h window use the clock time of now, so now carries the
// local time zone of the Host. A window whose reset time has passed stops
// nothing: its usage is from before the reset.
func Decide(usage Usage, settings config.QuotaSettings, now time.Time) Decision {
	d := Decision{
		FiveHourLimit: FiveHourLimit(settings.FiveHour, now),
		WeeklyLimit:   PaceLimit(settings.Weekly, usage.Weekly.ResetsAt, now),
	}
	if reached(usage.FiveHour, d.FiveHourLimit, now) {
		d.Stopped = append(d.Stopped, FiveHour)
	}
	if reached(usage.Weekly, d.WeeklyLimit, now) {
		d.Stopped = append(d.Stopped, Weekly)
	}
	return d
}

// FiveHourLimit is the threshold of the time band that holds now, or the
// default threshold outside every band, in percent.
func FiveHourLimit(settings config.FiveHourQuota, now time.Time) float64 {
	at := config.TimeOfDay(now.Hour()*60 + now.Minute())
	for _, band := range settings.Bands {
		if band.Contains(at) {
			return float64(band.Threshold)
		}
	}
	return float64(settings.Threshold)
}

// PaceLimit is the weekly limit at now, in percent:
// target x min(1, (elapsed + lead) / 7 days). The elapsed time counts from
// the start of the window, resetsAt minus 7 days, and stays within the
// window.
func PaceLimit(settings config.WeeklyQuota, resetsAt, now time.Time) float64 {
	elapsed := now.Sub(resetsAt.Add(-week))
	elapsed = max(0, min(week, elapsed))
	fraction := min(1, float64(elapsed+settings.Lead)/float64(week))
	return float64(settings.Target) * fraction
}

// epsilon absorbs the rounding of a utilization such as 0.29 that becomes
// 28.999999999999996 in percent.
const epsilon = 1e-9

// reached reports whether a window is at or above its limit.
func reached(w Window, limit float64, now time.Time) bool {
	if !now.Before(w.ResetsAt) {
		return false
	}
	return w.Utilization*100 >= limit-epsilon
}
