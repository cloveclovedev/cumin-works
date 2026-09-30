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
	if q.FiveHour.Threshold != 85 || len(q.FiveHour.Bands) != 0 {
		t.Errorf("FiveHour = %+v, want threshold 85 and no bands", q.FiveHour)
	}
	if q.Weekly.Target != 85 || q.Weekly.Lead != 24*time.Hour {
		t.Errorf("Weekly = %+v, want target 85 and lead 24h", q.Weekly)
	}
}

func TestLoadReadsQuotaSettings(t *testing.T) {
	s, err := Load(writeFile(t, required+`
[quota.five_hour]
threshold = 70

[[quota.five_hour.bands]]
from = "23:00"
to = "06:00"
threshold = 100

[[quota.five_hour.bands]]
from = "06:00"
to = "09:30"
threshold = 90

[quota.weekly]
target = 60
lead = "0s"
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	q := s.Quota
	if q.FiveHour.Threshold != 70 || q.Weekly.Target != 60 || q.Weekly.Lead != 0 {
		t.Errorf("quota = %+v", q)
	}
	want := []TimeBand{
		{From: 23 * 60, To: 6 * 60, Threshold: 100}, // passes midnight
		{From: 6 * 60, To: 9*60 + 30, Threshold: 90},
	}
	if len(q.FiveHour.Bands) != 2 || q.FiveHour.Bands[0] != want[0] || q.FiveHour.Bands[1] != want[1] {
		t.Errorf("FiveHour.Bands = %v, want %v", q.FiveHour.Bands, want)
	}
}

// Each case breaks one limit of the quota settings. The error must name the
// full key, and the position of a time band.
func TestLoadRejectsInvalidQuotaSettings(t *testing.T) {
	band := func(from, to, threshold string) string {
		return "[[quota.five_hour.bands]]\nfrom = \"" + from + "\"\nto = \"" + to + "\"\nthreshold = " + threshold + "\n"
	}
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"5h threshold zero", "[quota.five_hour]\nthreshold = 0", "quota.five_hour.threshold:"},
		{"5h threshold over 100", "[quota.five_hour]\nthreshold = 101", "quota.five_hour.threshold:"},
		{"weekly target zero", "[quota.weekly]\ntarget = 0", "quota.weekly.target:"},
		{"weekly target over 100", "[quota.weekly]\ntarget = 101", "quota.weekly.target:"},
		{"weekly lead negative", "[quota.weekly]\nlead = \"-1m\"", "quota.weekly.lead:"},
		{"weekly lead of 7 days", "[quota.weekly]\nlead = \"168h\"", "quota.weekly.lead:"},
		{"weekly lead without a unit", "[quota.weekly]\nlead = 24", "quota.weekly.lead"},
		{"band threshold over 100", band("01:00", "02:00", "101"), "quota.five_hour.bands[0].threshold:"},
		{"band without threshold", "[[quota.five_hour.bands]]\nfrom = \"01:00\"\nto = \"02:00\"", "quota.five_hour.bands[0].threshold:"},
		{"band from without leading zero", band("1:00", "02:00", "90"), "quota.five_hour.bands[0].from:"},
		{"band to is 24:00", band("22:00", "24:00", "90"), "quota.five_hour.bands[0].to:"},
		{"band without from", "[[quota.five_hour.bands]]\nto = \"02:00\"\nthreshold = 90", "quota.five_hour.bands[0].from:"},
		{"band from equals to", band("03:00", "03:00", "90"), "quota.five_hour.bands[0]:"},
		{
			"bands overlap",
			band("01:00", "05:00", "90") + band("04:59", "06:00", "95"),
			"quota.five_hour.bands[1]: overlaps with quota.five_hour.bands[0]",
		},
		{
			"band that passes midnight overlaps with an early band",
			band("22:00", "02:00", "90") + band("01:00", "03:00", "95"),
			"quota.five_hour.bands[1]: overlaps with quota.five_hour.bands[0]",
		},
		{
			"overlap names the position in the file",
			band("01:00", "02:00", "90") + band("9:00", "10:00", "90") + band("01:30", "03:00", "95"),
			"quota.five_hour.bands[2]: overlaps with quota.five_hour.bands[0]",
		},
		{"unknown key in a band", band("01:00", "02:00", "90") + "until = \"03:00\"", "until: unknown key"},
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

// The keys that #241 removed from the requirement stop cumin as unknown
// keys, so that a settings file of an older cumin does not keep a rule that
// no longer exists.
func TestLoadRejectsRemovedQuotaKeys(t *testing.T) {
	tests := []struct{ content, key string }{
		{"[quota.five_hour]\nreset_near = \"30m\"", "quota.five_hour.reset_near"},
		{"[quota.weekly]\nthreshold = 85", "quota.weekly.threshold"},
		{"[[quota.weekly.bands]]\nfrom = \"23:00\"\nto = \"06:00\"\nthreshold = 100", "quota.weekly.bands"},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			_, err := Load(writeFile(t, required+tt.content))
			if err == nil {
				t.Fatal("Load succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tt.key) || !strings.Contains(err.Error(), "unknown key") {
				t.Errorf("error does not name %s as an unknown key:\n%v", tt.key, err)
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
		if got := tt.band.Contains(tt.at); got != tt.want {
			t.Errorf("band %s-%s contains %s = %v, want %v", tt.band.From, tt.band.To, tt.at, got, tt.want)
		}
	}
}
