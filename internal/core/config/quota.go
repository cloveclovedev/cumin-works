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
	defaultResetNear      = 30 * time.Minute
	fiveHourWindow        = 5 * time.Hour
)

// QuotaSettings holds the thresholds that stop new starts. This package only
// loads them. The quota rules (Q1 to Q3) use them.
type QuotaSettings struct {
	FiveHour QuotaWindow
	Weekly   QuotaWindow
	// ResetNear belongs to the 5h window: when less than this time is left
	// before the reset, the quota rules treat the reset as near.
	ResetNear time.Duration
}

// QuotaWindow holds the settings of one quota window.
type QuotaWindow struct {
	Threshold int // percent. It applies outside every time band.
	Bands     []TimeBand
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
	FiveHour fileFiveHourWindow `toml:"five_hour"`
	Weekly   fileQuotaWindow    `toml:"weekly"`
}

type fileQuotaWindow struct {
	Threshold int        `toml:"threshold"`
	Bands     []fileBand `toml:"bands"`
}

type fileFiveHourWindow struct {
	fileQuotaWindow
	ResetNear duration `toml:"reset_near"`
}

type fileBand struct {
	From      string `toml:"from"`
	To        string `toml:"to"`
	Threshold int    `toml:"threshold"`
}

func defaultQuota() fileQuota {
	window := fileQuotaWindow{Threshold: defaultQuotaThreshold}
	return fileQuota{
		FiveHour: fileFiveHourWindow{fileQuotaWindow: window, ResetNear: duration(defaultResetNear)},
		Weekly:   window,
	}
}

// settings checks the limits of the quota settings. fail records one problem
// of one key.
func (q fileQuota) settings(fail func(key, format string, args ...any)) QuotaSettings {
	resetNear := time.Duration(q.FiveHour.ResetNear)
	if resetNear < 0 || resetNear >= fiveHourWindow {
		fail("quota.five_hour.reset_near", "must be 0 or more, and less than %d hours", int(fiveHourWindow.Hours()))
	}
	return QuotaSettings{
		FiveHour:  q.FiveHour.settings("quota.five_hour", fail),
		Weekly:    q.Weekly.settings("quota.weekly", fail),
		ResetNear: resetNear,
	}
}

func (w fileQuotaWindow) settings(key string, fail func(key, format string, args ...any)) QuotaWindow {
	checkThreshold := func(key string, threshold int) {
		if threshold < 1 || threshold > 100 {
			fail(key, "must be from 1 to 100")
		}
	}
	checkThreshold(key+".threshold", w.Threshold)

	window := QuotaWindow{Threshold: w.Threshold}
	// positions[i] is the position in the file of window.Bands[i]. A band
	// with a wrong time is not in window.Bands.
	var positions []int
	for i, b := range w.Bands {
		bandKey := fmt.Sprintf("%s.bands[%d]", key, i)
		checkThreshold(bandKey+".threshold", b.Threshold)
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
