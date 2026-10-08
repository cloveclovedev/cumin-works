package state

// This file is the monitor file of the Host: `cumin run` writes it at the
// end of every poll and at the end of every agent run, for a tool that shows cumin from outside (the menu bar
// app). No code of cumin reads it, and cumin decides nothing from it; a
// lost file only makes the display old until the next write
// (designs/status-menu-bar.md, the topic on the monitor file).

import (
	"encoding/json"
	"fmt"
	"time"
)

// MonitorFileName is the name of the monitor file in the state directory.
const MonitorFileName = "monitor.json"

// MonitorFileVersion is the version of the format of the monitor file. It
// rises only for a change that breaks a reader; a new field does not raise
// it.
const MonitorFileVersion = 1

// The values of MonitorQuota.State.
const (
	// MonitorQuotaOpen: agent starts go on.
	MonitorQuotaOpen = "open"
	// MonitorQuotaStopped: a window is at its limit ("stop agent starts").
	MonitorQuotaStopped = "stopped"
	// MonitorQuotaUnread: the usage was not read ("stop agent starts").
	MonitorQuotaUnread = "unread"
)

// MonitorFile is the content of the monitor file. The names of the fields
// are the contract with the readers (designs/status-menu-bar.md, the table
// of the fields). It holds no usage number.
type MonitorFile struct {
	Version       int             `json:"version"`
	LastPoll      MonitorLastPoll `json:"last_poll"`
	StopRequested bool            `json:"stop_requested"`
	Quota         MonitorQuota    `json:"quota"`
	// Agents are the agent runs that `cumin run` holds as running. It is
	// written as [] when there is none.
	Agents []MonitorAgent `json:"agents"`
	// Waiting are the open issues that wait for a Maintainer. It is written
	// as [] when there is none.
	Waiting []MonitorWaiting `json:"waiting"`
}

// MonitorAgent is one agent run that `cumin run` holds as running.
type MonitorAgent struct {
	// Repository is "<owner>/<repo>".
	Repository string `json:"repository"`
	Issue      int    `json:"issue"`
	// Role is "planner", "implementer", or "reviewer".
	Role string `json:"role"`
	// Request is the request kind, as the role file names it.
	Request string `json:"request"`
	// Title is the title of the issue.
	Title string `json:"title"`
	// URL is the URL of the issue.
	URL string `json:"url"`
}

// The values of MonitorWaiting.Kind: one for each status label that waits
// for a person, named after the label without "awaiting-".
const (
	MonitorWaitingPlanReview    = "plan-review"
	MonitorWaitingMergeDecision = "merge-decision"
	MonitorWaitingAcceptance    = "acceptance"
	MonitorWaitingDecision      = "decision"
)

// MonitorWaiting is one open issue that waits for a Maintainer.
type MonitorWaiting struct {
	// Repository is "<owner>/<repo>".
	Repository string `json:"repository"`
	Issue      int    `json:"issue"`
	// Kind is what the issue waits for, one of the MonitorWaiting* values.
	Kind string `json:"kind"`
	// Title is the title of the issue.
	Title string `json:"title"`
	// URL is the URL of the open pull request for "merge-decision", and
	// the URL of the issue for the other kinds.
	URL string `json:"url"`
}

// MonitorLastPoll is the last poll of all target repositories.
type MonitorLastPoll struct {
	// At is when the poll ended. A reader takes the file for old from it.
	At time.Time `json:"at"`
	// Errors are the repositories whose last poll failed. It is written as
	// [] when there is none.
	Errors []MonitorPollError `json:"errors"`
}

// MonitorPollError is one repository whose last poll failed.
type MonitorPollError struct {
	// Repository is "<owner>/<repo>".
	Repository string `json:"repository"`
	// Message is the reason of the failure on one line.
	Message string `json:"message"`
}

// MonitorQuota is the quota state: the name of the state, the names of
// the windows, and the next try time. It never holds a utilization, a
// limit, or a reset time.
type MonitorQuota struct {
	State string `json:"state"`
	// StoppedWindows are the windows at their limit, "5h" and "weekly". It
	// is written as [] when there is none.
	StoppedWindows []string `json:"stopped_windows"`
	// NextTryAt is when cumin tries the start of an agent again ("resume
	// agent starts"). Nil writes no key.
	NextTryAt *time.Time `json:"next_try_at,omitempty"`
}

// WriteMonitorFile writes the monitor file through a temporary file and a
// rename, so that a reader never reads half a file. The times are written
// in UTC, and an array with no element as [].
func WriteMonitorFile(path string, m MonitorFile) error {
	m.LastPoll.At = m.LastPoll.At.UTC()
	if m.LastPoll.Errors == nil {
		m.LastPoll.Errors = []MonitorPollError{}
	}
	if m.Quota.StoppedWindows == nil {
		m.Quota.StoppedWindows = []string{}
	}
	if m.Agents == nil {
		m.Agents = []MonitorAgent{}
	}
	if m.Waiting == nil {
		m.Waiting = []MonitorWaiting{}
	}
	if m.Quota.NextTryAt != nil {
		next := m.Quota.NextTryAt.UTC()
		m.Quota.NextTryAt = &next
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("monitor file: write %s: %w", path, err)
	}
	return writeFile(path, append(raw, '\n'))
}
