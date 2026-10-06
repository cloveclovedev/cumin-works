// Package quota holds the pure rules of the quota limits (Q1 to Q3 of
// docs/ja/requirements/workflow/issue-states.md): the pace limit of the
// weekly window, the limit of the 5h window by time band, whether a
// window stops the start of an agent, and whether a stored usage is new enough to
// decide a start. docs/ja/designs/quota.md records the design.
//
// Nothing here reads a clock, a file, or a CLI. The caller gives the usage,
// the settings, the time, and the location of the clock time, so that a test
// can try any time of a week in any time zone.
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

// Allowance is the Owner's allowance to use the rest of one 5h window (Q2):
// the 5h limit is 100% before FiveHourUntil, the reset of that window. It
// never raises the weekly limit. The zero value is no allowance.
type Allowance struct {
	FiveHourUntil time.Time
}

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

// Allows reports whether an agent may start.
func (d Decision) Allows() bool { return len(d.Stopped) == 0 }

// Decide compares the usage with the limit of each window at now. The time
// bands of the 5h window use the clock time of now in loc; cumin passes the
// time zone of the Host. A window whose reset time has passed stops
// nothing: its usage is from before the reset. An allowance that holds at
// now makes the 5h limit 100%.
func Decide(usage Usage, settings config.QuotaSettings, allowance Allowance, now time.Time, loc *time.Location) Decision {
	fiveHourLimit := FiveHourLimit(settings.FiveHour, now, loc)
	if now.Before(allowance.FiveHourUntil) {
		fiveHourLimit = 100
	}
	d := Decision{
		FiveHourLimit: fiveHourLimit,
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

// FiveHourLimit is the threshold of the time band that holds the clock time
// of now in loc, or the default threshold outside every band, in percent.
func FiveHourLimit(settings config.FiveHourQuota, now time.Time, loc *time.Location) float64 {
	local := now.In(loc)
	at := config.TimeOfDay(local.Hour()*60 + local.Minute())
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

// retryMargin is added to the time at which the pace limit meets the
// usage: at that very time the usage is still at the limit, and a try then
// would stop again.
const retryMargin = time.Minute

// NextTry is the earliest time at which the stored usage could pass the
// limits that stop it now (Q3 of issue-states.md). Usage only rises until
// a reset, so no earlier try can pass. For each window that stops:
//
//   - 5h: its reset, or the start of the first time band whose threshold
//     is above the usage, whichever comes first. The bands start at
//     their clock time in loc.
//   - weekly: the time at which the pace limit passes the usage, or its
//     reset, whichever comes first.
//
// When both windows stop, the later of the two times counts. The second
// value is false when nothing stops at now, an allowance included: a new
// allowance ends the wait at once.
func NextTry(usage Usage, settings config.QuotaSettings, allowance Allowance, now time.Time, loc *time.Location) (time.Time, bool) {
	d := Decide(usage, settings, allowance, now, loc)
	if d.Allows() {
		return time.Time{}, false
	}
	var next time.Time
	for _, window := range d.Stopped {
		var t time.Time
		switch window {
		case FiveHour:
			t = fiveHourNextTry(usage.FiveHour, settings.FiveHour, now, loc)
		case Weekly:
			t = weeklyNextTry(usage.Weekly, settings.Weekly)
		}
		if t.After(next) {
			next = t
		}
	}
	return next, true
}

func fiveHourNextTry(w Window, settings config.FiveHourQuota, now time.Time, loc *time.Location) time.Time {
	next := w.ResetsAt
	// The limit changes only where a band starts or ends. Each boundary
	// comes once in the next 24 hours.
	for _, band := range settings.Bands {
		for _, boundary := range []config.TimeOfDay{band.From, band.To} {
			t := nextClockTime(now, boundary, loc)
			if t.Before(next) && w.Utilization*100 < FiveHourLimit(settings, t, loc)-epsilon {
				next = t
			}
		}
	}
	return next
}

// nextClockTime is the first time after now at the clock time t in loc.
func nextClockTime(now time.Time, t config.TimeOfDay, loc *time.Location) time.Time {
	y, m, d := now.In(loc).Date()
	at := time.Date(y, m, d, int(t)/60, int(t)%60, 0, 0, loc)
	if !at.After(now) {
		at = time.Date(y, m, d+1, int(t)/60, int(t)%60, 0, 0, loc)
	}
	return at
}

func weeklyNextTry(w Window, settings config.WeeklyQuota) time.Time {
	percent := w.Utilization * 100
	if settings.Target <= 0 || percent >= float64(settings.Target)-epsilon {
		return w.ResetsAt
	}
	// target x (e + lead) / 7 days = percent, so e = percent / target x 7 days - lead.
	elapsed := time.Duration(percent/float64(settings.Target)*float64(week)) - settings.Lead
	t := w.ResetsAt.Add(-week).Add(elapsed).Add(retryMargin)
	if t.After(w.ResetsAt) {
		return w.ResetsAt
	}
	return t
}

// FreshFor is how long a stored usage is new enough to decide a start
// without a minimal run. It is a fixed value, not a setting
// (docs/ja/designs/quota.md, the section on the check before a start).
const FreshFor = 5 * time.Minute

// Fresh reports whether a usage that cumin read at readAt is new enough at
// now to decide a start without a minimal run. A missing time of the read,
// and a time of the read after now, are not new enough: one minimal run
// reads the usage again.
func Fresh(readAt, now time.Time) bool {
	// Round(0) removes the monotonic clock reading, so that the two times
	// compare by the wall clock. The monotonic clock can stop while the
	// Host sleeps, and a usage from before the sleep would stay new enough.
	readAt, now = readAt.Round(0), now.Round(0)
	if readAt.IsZero() || readAt.After(now) {
		return false
	}
	return now.Sub(readAt) <= FreshFor
}

// Newer returns the newer of two readings of the same window, whatever
// order they came in: runs that overlap can end in any order. A later
// reset time is a newer window. In the same window, usage only rises, so
// the higher usage is the newer reading.
func Newer(a, b Window) Window {
	switch {
	case a.ResetsAt.After(b.ResetsAt):
		return a
	case b.ResetsAt.After(a.ResetsAt):
		return b
	case a.Utilization >= b.Utilization:
		return a
	default:
		return b
	}
}
