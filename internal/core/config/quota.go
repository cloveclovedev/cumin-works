package config

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// Defaults and limits of the quota settings, from the settings table.
const (
	defaultQuotaThreshold = 85 // percent
	defaultWeeklyTarget   = 85 // percent
	defaultWeeklyLead     = 24 * time.Hour
	weeklyWindow          = 7 * 24 * time.Hour
)

// QuotaSettings holds the settings of the limits that stop new starts. This
// package only loads them. The quota rules (Q1 to Q3) use them.
type QuotaSettings struct {
	FiveHour FiveHourQuota
	Weekly   WeeklyQuota
}

// FiveHourQuota holds the thresholds of the 5h window.
type FiveHourQuota struct {
	Threshold int // percent. It applies outside every time band.
	Bands     []TimeBand
}

// WeeklyQuota holds the pace limit of the weekly window: target x min(1,
// (elapsed + lead) / 7 days). The weekly window has no time bands, and no
// setting holds its reset: the week starts at its reset time minus 7 days.
type WeeklyQuota struct {
	Target int // percent
	Lead   time.Duration
}

// TimeBand is a part of the day with its own threshold. The band starts at
// From and ends before To, in the local time of the Host. When To is not
// after From, the band passes midnight.
type TimeBand struct {
	From      TimeOfDay
	To        TimeOfDay
	Threshold int // percent
}

// TimeOfDay is the number of minutes after midnight.
type TimeOfDay int

func (t TimeOfDay) String() string { return fmt.Sprintf("%02d:%02d", int(t)/60, int(t)%60) }

type fileQuota struct {
	FiveHour fileFiveHourQuota `toml:"five_hour"`
	Weekly   fileWeeklyQuota   `toml:"weekly"`
}

type fileFiveHourQuota struct {
	Threshold int        `toml:"threshold"`
	Bands     []fileBand `toml:"bands"`
}

type fileWeeklyQuota struct {
	Target int      `toml:"target"`
	Lead   duration `toml:"lead"`
}

type fileBand struct {
	From      string `toml:"from"`
	To        string `toml:"to"`
	Threshold int    `toml:"threshold"`
}

func defaultQuota() fileQuota {
	return fileQuota{
		FiveHour: fileFiveHourQuota{Threshold: defaultQuotaThreshold},
		Weekly:   fileWeeklyQuota{Target: defaultWeeklyTarget, Lead: duration(defaultWeeklyLead)},
	}
}

// settings checks the limits of the quota settings. fail records one problem
// of one key.
func (q fileQuota) settings(fail func(key, format string, args ...any)) QuotaSettings {
	return QuotaSettings{
		FiveHour: q.FiveHour.settings("quota.five_hour", fail),
		Weekly:   q.Weekly.settings("quota.weekly", fail),
	}
}

func checkPercent(key string, percent int, fail func(key, format string, args ...any)) {
	if percent < 1 || percent > 100 {
		fail(key, "must be from 1 to 100")
	}
}

func (w fileWeeklyQuota) settings(key string, fail func(key, format string, args ...any)) WeeklyQuota {
	checkPercent(key+".target", w.Target, fail)
	lead := time.Duration(w.Lead)
	if lead < 0 || lead >= weeklyWindow {
		fail(key+".lead", "must be 0 or more, and less than 168h (7 days)")
	}
	return WeeklyQuota{Target: w.Target, Lead: lead}
}

func (w fileFiveHourQuota) settings(key string, fail func(key, format string, args ...any)) FiveHourQuota {
	checkPercent(key+".threshold", w.Threshold, fail)

	window := FiveHourQuota{Threshold: w.Threshold}
	// positions[i] is the position in the file of window.Bands[i]. A band
	// with a wrong time is not in window.Bands.
	var positions []int
	for i, b := range w.Bands {
		bandKey := fmt.Sprintf("%s.bands[%d]", key, i)
		checkPercent(bandKey+".threshold", b.Threshold, fail)
		from, fromOK := parseTimeOfDay(b.From)
		if !fromOK {
			fail(bandKey+".from", "%q must have the form HH:MM, from 00:00 to 23:59", b.From)
		}
		to, toOK := parseTimeOfDay(b.To)
		if !toOK {
			fail(bandKey+".to", "%q must have the form HH:MM, from 00:00 to 23:59", b.To)
		}
		if !fromOK || !toOK {
			continue
		}
		if from == to {
			fail(bandKey, "from and to must differ")
			continue
		}
		band := TimeBand{From: from, To: to, Threshold: b.Threshold}
		for j, other := range window.Bands {
			if band.overlaps(other) {
				fail(bandKey, "overlaps with %s.bands[%d]", key, positions[j])
			}
		}
		window.Bands = append(window.Bands, band)
		positions = append(positions, i)
	}
	return window
}

var timeOfDayPattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):([0-5][0-9])$`)

func parseTimeOfDay(s string) (TimeOfDay, bool) {
	m := timeOfDayPattern.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	hour, _ := strconv.Atoi(m[1])
	minute, _ := strconv.Atoi(m[2])
	return TimeOfDay(hour*60 + minute), true
}

// contains reports whether the band covers the time of day t.
func (b TimeBand) contains(t TimeOfDay) bool {
	if b.From < b.To {
		return b.From <= t && t < b.To
	}
	return t >= b.From || t < b.To // the band passes midnight
}

// overlaps reports whether two bands share a minute of the day.
func (b TimeBand) overlaps(other TimeBand) bool {
	// Two ranges on a circle overlap exactly when one of them contains the
	// start of the other.
	return b.contains(other.From) || other.contains(b.From)
}
