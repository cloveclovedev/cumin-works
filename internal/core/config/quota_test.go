package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadAppliesQuotaDefaults(t *testing.T) {
	s, err := Load(writeFile(t, required))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	q := s.Quota
	if q.FiveHour.Threshold != 85 || q.Weekly.Threshold != 85 {
		t.Errorf("thresholds = %d and %d, want 85 and 85", q.FiveHour.Threshold, q.Weekly.Threshold)
	}
	if q.ResetNear != 30*time.Minute {
		t.Errorf("ResetNear = %v, want 30m", q.ResetNear)
	}
	if len(q.FiveHour.Bands) != 0 || len(q.Weekly.Bands) != 0 {
		t.Errorf("bands = %v and %v, want none", q.FiveHour.Bands, q.Weekly.Bands)
	}
}

func TestLoadReadsQuotaSettings(t *testing.T) {
	s, err := Load(writeFile(t, required+`
[quota.five_hour]
threshold = 70
reset_near = "0s"

[[quota.five_hour.bands]]
from = "23:00"
to = "06:00"
threshold = 100

[[quota.five_hour.bands]]
from = "06:00"
to = "09:30"
threshold = 90

[quota.weekly]
threshold = 60

# The same band in the other window is not an overlap.
[[quota.weekly.bands]]
from = "23:00"
to = "06:00"
threshold = 80
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	q := s.Quota
	if q.FiveHour.Threshold != 70 || q.Weekly.Threshold != 60 || q.ResetNear != 0 {
		t.Errorf("quota = %+v", q)
	}
	want := []TimeBand{
		{From: 23 * 60, To: 6 * 60, Threshold: 100}, // passes midnight
		{From: 6 * 60, To: 9*60 + 30, Threshold: 90},
	}
	if len(q.FiveHour.Bands) != 2 || q.FiveHour.Bands[0] != want[0] || q.FiveHour.Bands[1] != want[1] {
		t.Errorf("FiveHour.Bands = %v, want %v", q.FiveHour.Bands, want)
	}
	if len(q.Weekly.Bands) != 1 || q.Weekly.Bands[0].Threshold != 80 {
		t.Errorf("Weekly.Bands = %v", q.Weekly.Bands)
	}
}

// Each case breaks one limit of the quota settings. The error must name the
// full key, and the position of a time band.
func TestLoadRejectsInvalidQuotaSettings(t *testing.T) {
	band := func(window, from, to, threshold string) string {
		return "[[quota." + window + ".bands]]\nfrom = \"" + from + "\"\nto = \"" + to + "\"\nthreshold = " + threshold + "\n"
	}
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"5h threshold zero", "[quota.five_hour]\nthreshold = 0", "quota.five_hour.threshold:"},
		{"5h threshold over 100", "[quota.five_hour]\nthreshold = 101", "quota.five_hour.threshold:"},
		{"weekly threshold zero", "[quota.weekly]\nthreshold = 0", "quota.weekly.threshold:"},
		{"weekly threshold over 100", "[quota.weekly]\nthreshold = 101", "quota.weekly.threshold:"},
		{"reset_near negative", "[quota.five_hour]\nreset_near = \"-1m\"", "quota.five_hour.reset_near:"},
		{"reset_near five hours", "[quota.five_hour]\nreset_near = \"5h\"", "quota.five_hour.reset_near:"},
		{"reset_near on the weekly window", "[quota.weekly]\nreset_near = \"30m\"", "quota.weekly.reset_near: unknown key"},
		{"band threshold over 100", band("five_hour", "01:00", "02:00", "101"), "quota.five_hour.bands[0].threshold:"},
		{"band without threshold", "[[quota.weekly.bands]]\nfrom = \"01:00\"\nto = \"02:00\"", "quota.weekly.bands[0].threshold:"},
		{"band from without leading zero", band("five_hour", "1:00", "02:00", "90"), "quota.five_hour.bands[0].from:"},
		{"band to is 24:00", band("five_hour", "22:00", "24:00", "90"), "quota.five_hour.bands[0].to:"},
		{"band without from", "[[quota.five_hour.bands]]\nto = \"02:00\"\nthreshold = 90", "quota.five_hour.bands[0].from:"},
		{"band from equals to", band("weekly", "03:00", "03:00", "90"), "quota.weekly.bands[0]:"},
		{
			"bands overlap",
			band("five_hour", "01:00", "05:00", "90") + band("five_hour", "04:59", "06:00", "95"),
			"quota.five_hour.bands[1]: overlaps with quota.five_hour.bands[0]",
		},
		{
			"band that passes midnight overlaps with an early band",
			band("weekly", "22:00", "02:00", "90") + band("weekly", "01:00", "03:00", "95"),
			"quota.weekly.bands[1]: overlaps with quota.weekly.bands[0]",
		},
		{
			"overlap names the position in the file",
			band("five_hour", "01:00", "02:00", "90") + band("five_hour", "9:00", "10:00", "90") + band("five_hour", "01:30", "03:00", "95"),
			"quota.five_hour.bands[2]: overlaps with quota.five_hour.bands[0]",
		},
		{"unknown key in a band", band("five_hour", "01:00", "02:00", "90") + "until = \"03:00\"", "until: unknown key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writeFile(t, required+tt.content))
			if err == nil {
				t.Fatal("Load succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error does not contain %q:\n%v", tt.want, err)
			}
		})
	}
}

func TestBandsThatTouchDoNotOverlap(t *testing.T) {
	_, err := Load(writeFile(t, required+`
[[quota.five_hour.bands]]
from = "22:00"
to = "00:00"
threshold = 90

[[quota.five_hour.bands]]
from = "00:00"
to = "06:00"
threshold = 95
`))
	if err != nil {
		t.Errorf("Load: %v", err)
	}
}

func TestTimeBandContains(t *testing.T) {
	night := TimeBand{From: 23 * 60, To: 6 * 60}
	day := TimeBand{From: 9 * 60, To: 17 * 60}
	tests := []struct {
		band TimeBand
		at   TimeOfDay
		want bool
	}{
		{night, 23 * 60, true},
		{night, 0, true},
		{night, 6*60 - 1, true},
		{night, 6 * 60, false},
		{night, 12 * 60, false},
		{day, 9 * 60, true},
		{day, 17 * 60, false},
		{day, 8*60 + 59, false},
	}
	for _, tt := range tests {
		if got := tt.band.contains(tt.at); got != tt.want {
			t.Errorf("band %s-%s contains %s = %v, want %v", tt.band.From, tt.band.To, tt.at, got, tt.want)
		}
	}
}
