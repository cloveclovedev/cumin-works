package quota

import (
	"math"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
)

// defaults are the settings of the settings table: 5h threshold 85, weekly
// target 85, lead one day.
var defaults = config.QuotaSettings{
	FiveHour: config.FiveHourQuota{Threshold: 85},
	Weekly:   config.WeeklyQuota{Target: 85, Lead: 24 * time.Hour},
}

// weeklyReset is the reset of the weekly window in these tests, so its
// start is 7 days earlier.
var weeklyReset = time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)

func weekStart() time.Time { return weeklyReset.Add(-week) }

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// The pace limit rises through the week, starts at target x lead / 7 days,
// and never passes the target (the weekly pace).
func TestPaceLimitAtTheStartTheMiddleAndTheEndOfAWeek(t *testing.T) {
	tests := []struct {
		name string
		at   time.Time
		want float64
	}{
		{"before the start", weekStart().Add(-time.Hour), 85.0 / 7},
		{"the start", weekStart(), 85.0 / 7},
		{"the middle", weekStart().Add(week / 2), 85 * (3.5 + 1) / 7},
		{"one day before the end", weeklyReset.Add(-24 * time.Hour), 85},
		{"the end", weeklyReset.Add(-time.Minute), 85},
		{"after the reset", weeklyReset.Add(time.Hour), 85},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PaceLimit(defaults.Weekly, weeklyReset, tt.at); !near(got, tt.want) {
				t.Errorf("PaceLimit = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPaceLimitWithoutLead(t *testing.T) {
	settings := config.WeeklyQuota{Target: 70}
	if got := PaceLimit(settings, weeklyReset, weekStart()); got != 0 {
		t.Errorf("PaceLimit at the start = %v, want 0", got)
	}
	if got := PaceLimit(settings, weeklyReset, weekStart().Add(week*2/7)); !near(got, 20) {
		t.Errorf("PaceLimit after 2 days = %v, want 20", got)
	}
}

// The same weekly usage stops a start early in the week and lets it go
// later, by time alone.
func TestTheSameWeeklyUsageStopsEarlyAndPassesLater(t *testing.T) {
	usage := Usage{Weekly: Window{Utilization: 0.40, ResetsAt: weeklyReset}}
	early := Decide(usage, defaults, Allowance{}, weekStart().Add(24*time.Hour), time.UTC)
	if early.Allows() || len(early.Stopped) != 1 || early.Stopped[0] != Weekly {
		t.Errorf("early in the week: %+v, want stopped by the weekly window", early)
	}
	// 85 x (e + 1 day) / 7 days > 40 from e = 40/85 x 7 - 1 days, about 2.29 days.
	late := Decide(usage, defaults, Allowance{}, weekStart().Add(56*time.Hour), time.UTC)
	if !late.Allows() {
		t.Errorf("later in the week: %+v, want a start", late)
	}
}

// zones are fixed time zones of these tests. Two of them have an offset
// that is not a full hour.
var zones = map[string]*time.Location{
	"UTC":       time.UTC,
	"UTC+05:30": time.FixedZone("UTC+05:30", 5*3600+30*60),
	"UTC-03:30": time.FixedZone("UTC-03:30", -(3*3600 + 30*60)),
	"UTC+09:00": time.FixedZone("UTC+09:00", 9*3600),
}

// The time bands use the clock time in the location that the caller gives,
// whatever the zone of the time value and of the machine.
func TestFiveHourLimitByTimeBand(t *testing.T) {
	settings := config.FiveHourQuota{Threshold: 85, Bands: []config.TimeBand{
		{From: 23 * 60, To: 6 * 60, Threshold: 100},
		{From: 12 * 60, To: 13 * 60, Threshold: 95},
	}}
	tests := []struct {
		hour, minute int
		want         float64
	}{
		{23, 0, 100},
		{2, 30, 100},
		{6, 0, 85},
		{12, 59, 95},
		{13, 0, 85},
		{18, 0, 85},
	}
	for name, loc := range zones {
		t.Run(name, func(t *testing.T) {
			for _, tt := range tests {
				at := time.Date(2026, 10, 1, tt.hour, tt.minute, 0, 0, loc)
				// The same instant in another zone gives the same limit.
				for _, now := range []time.Time{at, at.UTC()} {
					if got := FiveHourLimit(settings, now, loc); got != tt.want {
						t.Errorf("FiveHourLimit at %02d:%02d = %v, want %v", tt.hour, tt.minute, got, tt.want)
					}
				}
			}
		})
	}
}

// The same instant is in a band in one location and outside it in another.
func TestFiveHourLimitFollowsTheLocation(t *testing.T) {
	settings := config.FiveHourQuota{Threshold: 85, Bands: []config.TimeBand{{From: 12 * 60, To: 13 * 60, Threshold: 95}}}
	now := time.Date(2026, 10, 1, 7, 0, 0, 0, time.UTC) // 12:30 at UTC+05:30
	if got := FiveHourLimit(settings, now, zones["UTC+05:30"]); got != 95 {
		t.Errorf("FiveHourLimit at UTC+05:30 = %v, want 95", got)
	}
	if got := FiveHourLimit(settings, now, time.UTC); got != 85 {
		t.Errorf("FiveHourLimit at UTC = %v, want 85", got)
	}
}

// "resume agent starts": the next try time is the start of the band at its clock time in the
// location, also when the offset is not a full hour.
func TestNextTryAtTheStartOfABandInTheLocation(t *testing.T) {
	settings := defaults
	settings.FiveHour.Bands = []config.TimeBand{{From: 23 * 60, To: 6 * 60, Threshold: 95}}
	for name, loc := range zones {
		t.Run(name, func(t *testing.T) {
			now := time.Date(2026, 10, 2, 22, 15, 0, 0, loc).UTC()
			usage := Usage{
				FiveHour: Window{Utilization: 0.90, ResetsAt: now.Add(3 * time.Hour)},
				Weekly:   Window{Utilization: 0.10, ResetsAt: now.Add(24 * time.Hour)},
			}
			want := time.Date(2026, 10, 2, 23, 0, 0, 0, loc)
			got, ok := NextTry(usage, settings, Allowance{}, now, loc)
			if !ok || !got.Equal(want) {
				t.Errorf("NextTry = %v, %v, want %v", got, ok, want)
			}
		})
	}
}

func TestDecideStopsAtOrAboveTheLimit(t *testing.T) {
	now := weeklyReset.Add(-time.Hour) // the weekly limit is the target
	fiveHourReset := now.Add(time.Hour)
	usage := func(fiveHour, weekly float64) Usage {
		return Usage{
			FiveHour: Window{Utilization: fiveHour, ResetsAt: fiveHourReset},
			Weekly:   Window{Utilization: weekly, ResetsAt: weeklyReset},
		}
	}
	tests := []struct {
		name  string
		usage Usage
		want  []Name
	}{
		{"both below", usage(0.84, 0.84), nil},
		{"5h at the limit", usage(0.85, 0.10), []Name{FiveHour}},
		{"weekly at the limit", usage(0.10, 0.85), []Name{Weekly}},
		{"both above", usage(0.99, 0.90), []Name{FiveHour, Weekly}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Decide(tt.usage, defaults, Allowance{}, now, time.UTC)
			if len(d.Stopped) != len(tt.want) {
				t.Fatalf("Stopped = %v, want %v", d.Stopped, tt.want)
			}
			for i := range tt.want {
				if d.Stopped[i] != tt.want[i] {
					t.Errorf("Stopped = %v, want %v", d.Stopped, tt.want)
				}
			}
			if d.Allows() != (len(tt.want) == 0) {
				t.Errorf("Allows = %v", d.Allows())
			}
		})
	}
}

// A utilization such as 0.29 is 28.999999999999996 in percent; it still
// reaches a limit of 29.
func TestDecideAbsorbsTheRoundingOfAPercent(t *testing.T) {
	settings := defaults
	settings.FiveHour.Threshold = 29
	now := weeklyReset.Add(-time.Hour)
	d := Decide(Usage{FiveHour: Window{Utilization: 0.29, ResetsAt: now.Add(time.Hour)}}, settings, Allowance{}, now, time.UTC)
	if d.Allows() {
		t.Errorf("0.29 against 29: %+v, want stopped", d)
	}
}

// A usage from before a reset stops nothing.
func TestDecideIgnoresAWindowWhoseResetHasPassed(t *testing.T) {
	now := weeklyReset.Add(time.Minute)
	usage := Usage{
		FiveHour: Window{Utilization: 1, ResetsAt: now.Add(-time.Minute)},
		Weekly:   Window{Utilization: 1, ResetsAt: weeklyReset},
	}
	if d := Decide(usage, defaults, Allowance{}, now, time.UTC); !d.Allows() {
		t.Errorf("Decide = %+v, want a start", d)
	}
}

// "resume agent starts": the next try time of each window, and of both.
func TestNextTryOfEachWindow(t *testing.T) {
	now := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC) // 1 day into the week of weeklyReset
	fiveHourReset := now.Add(3 * time.Hour)
	night := defaults
	night.FiveHour.Bands = []config.TimeBand{{From: 11 * 60, To: 12 * 60, Threshold: 95}}
	usage := func(fiveHour, weekly float64) Usage {
		return Usage{
			FiveHour: Window{Utilization: fiveHour, ResetsAt: fiveHourReset},
			Weekly:   Window{Utilization: weekly, ResetsAt: weeklyReset},
		}
	}
	// 85 x (e + 1 day) / 7 days = 40 at e = 40/85 x 7 days - 1 day.
	share := 40.0 / 85
	weeklyTime := weekStart().Add(time.Duration(share*float64(week)) - 24*time.Hour + retryMargin)
	tests := []struct {
		name     string
		usage    Usage
		settings config.QuotaSettings
		want     time.Time
	}{
		{"5h: its reset", usage(0.90, 0.10), defaults, fiveHourReset},
		{"5h: a band with a higher threshold starts first", usage(0.90, 0.10), night, now.Add(time.Hour)},
		{"5h: a band whose threshold is still below the usage", usage(0.96, 0.10), night, fiveHourReset},
		{"weekly: the pace passes the usage", usage(0.10, 0.40), defaults, weeklyTime},
		{"weekly: at the target, its reset", usage(0.10, 0.90), defaults, weeklyReset},
		{"both: the later time", usage(0.90, 0.40), defaults, weeklyTime},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := NextTry(tt.usage, tt.settings, Allowance{}, now, time.UTC)
			if diff := got.Sub(tt.want); !ok || diff < -time.Second || diff > time.Second {
				t.Errorf("NextTry = %v, %v, want %v", got, ok, tt.want)
			}
			// No try before that time can pass, and a try then does.
			if d := Decide(tt.usage, tt.settings, Allowance{}, got.Add(-time.Minute), time.UTC); d.Allows() && got.Sub(now) > time.Minute {
				t.Errorf("a try one minute earlier passes: %+v", d)
			}
			if d := Decide(tt.usage, tt.settings, Allowance{}, got, time.UTC); !d.Allows() && got.Before(weeklyReset) && !got.Equal(fiveHourReset) {
				t.Errorf("a try at the next try time stops: %+v", d)
			}
		})
	}
	if _, ok := NextTry(usage(0.10, 0.10), defaults, Allowance{}, now, time.UTC); ok {
		t.Error("NextTry without a stop reports a time")
	}
}

// Readings that come in any order keep the newest one of each window.
func TestNewerKeepsTheNewestReading(t *testing.T) {
	reset := weeklyReset
	older := Window{Utilization: 0.30, ResetsAt: reset}
	newer := Window{Utilization: 0.40, ResetsAt: reset}
	nextWindow := Window{Utilization: 0.05, ResetsAt: reset.Add(week)}
	tests := []struct {
		a, b, want Window
	}{
		{older, newer, newer},
		{newer, older, newer},
		{newer, nextWindow, nextWindow},
		{nextWindow, newer, nextWindow},
	}
	for _, tt := range tests {
		if got := Newer(tt.a, tt.b); got != tt.want {
			t.Errorf("Newer(%+v, %+v) = %+v, want %+v", tt.a, tt.b, got, tt.want)
		}
	}
}

// An allowance makes the 5h limit 100% until its end, and
// never passes the weekly pace limit.
func TestAnAllowanceLiftsOnlyTheFiveHourLimit(t *testing.T) {
	now := weeklyReset.Add(-time.Hour) // the weekly limit is the target
	fiveHourReset := now.Add(2 * time.Hour)
	allowance := Allowance{FiveHourUntil: fiveHourReset}
	usage := func(fiveHour, weekly float64) Usage {
		return Usage{
			FiveHour: Window{Utilization: fiveHour, ResetsAt: fiveHourReset},
			Weekly:   Window{Utilization: weekly, ResetsAt: weeklyReset},
		}
	}
	if d := Decide(usage(0.95, 0.10), defaults, allowance, now, time.UTC); !d.Allows() || d.FiveHourLimit != 100 {
		t.Errorf("5h over its threshold with an allowance: %+v, want a start at a limit of 100", d)
	}
	if d := Decide(usage(0.95, 0.90), defaults, allowance, now, time.UTC); d.Allows() || d.Stopped[0] != Weekly {
		t.Errorf("weekly over its pace with an allowance: %+v, want stopped by the weekly window", d)
	}
	if d := Decide(usage(0.95, 0.10), defaults, Allowance{FiveHourUntil: now}, now, time.UTC); d.Allows() {
		t.Errorf("an allowance that ended: %+v, want stopped", d)
	}
	if _, ok := NextTry(usage(0.95, 0.10), defaults, allowance, now, time.UTC); ok {
		t.Error("a new allowance does not end the wait")
	}
}

// A stored usage is new enough for 5 minutes after its read, the fifth
// minute included. A missing time of the read and a time after now are not
// new enough.
func TestFresh_AStoredUsageIsNewEnoughForFiveMinutes(t *testing.T) {
	now := weeklyReset.Add(-time.Hour)
	tests := []struct {
		name   string
		readAt time.Time
		want   bool
	}{
		{"read now", now, true},
		{"read one minute ago", now.Add(-time.Minute), true},
		{"read 5 minutes ago", now.Add(-5 * time.Minute), true},
		{"read 5 minutes and one second ago", now.Add(-5*time.Minute - time.Second), false},
		{"read one hour ago", now.Add(-time.Hour), false},
		{"never read", time.Time{}, false},
		{"read after now", now.Add(time.Second), false},
	}
	for _, tt := range tests {
		if got := Fresh(tt.readAt, now); got != tt.want {
			t.Errorf("%s: Fresh = %v, want %v", tt.name, got, tt.want)
		}
	}
}
