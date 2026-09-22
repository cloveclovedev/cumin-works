// Package notify tells the Owner that cumin needs attention.
//
// The requirement (docs/ja/requirements/cumin-core.md, the section on
// notifications) says that every notification goes through this one place,
// and that the way of sending must be replaceable. So this package holds
// what a notification says, and a sender takes the finished text to one
// channel. Nothing here knows Discord, HTTP, or the Keychain.
//
// This file is the pure part: the notification and the message that it
// becomes. It imports only the standard library.
package notify

import (
	"strings"
	"unicode"
)

// maxReason is how much of a reason one message carries. The reason is a
// summary: the whole text stays in the comment on the issue, which the link
// opens. A channel has a limit of its own, and a reason that an agent wrote
// can be long, so the reason is cut here, where the link is still the last
// line that a reader must see.
const maxReason = 500

// reasonCut marks a reason that was cut.
const reasonCut = "..."

// Notification is one thing that cumin tells the Owner. The caller fills
// the fields that it has; an empty field is left out of the message.
type Notification struct {
	// Row is the row number of the table in
	// docs/ja/requirements/workflow/issue-states.md, such as "I2". It is
	// empty when no row covers the notification, as for a poll of one
	// repository that keeps failing.
	Row string
	// Reason is why the Owner must look, in one line. A reason with more
	// than one line is joined into one.
	Reason string
	// Repository is the target repository, as "<owner>/<repo>".
	Repository string
	// Subject names what the Owner should open, such as "issue #12". It is
	// empty when the notification is about the repository itself.
	Subject string
	// Link is the address that the Owner opens. The caller builds it,
	// because this package knows no forge.
	Link string
}

// Message returns the text of one notification. The first line is the
// summary, so that a channel that shows one line shows the reason. The
// other lines name the repository, the subject, and the link.
//
// The text is the only value that crosses to a sender, so that a provider
// package never has to import this one (docs/ja/designs/cumin-core.md, the
// topic on notifications to the Owner).
func Message(n Notification) string {
	var lines []string
	summary := cutReason(oneLine(n.Reason))
	if n.Row != "" {
		summary = oneLine(n.Row) + ": " + summary
	}
	lines = append(lines, "cumin: "+summary)

	where := strings.TrimSpace(strings.Join([]string{oneLine(n.Repository), oneLine(n.Subject)}, " "))
	if where != "" {
		lines = append(lines, where)
	}
	if link := oneLine(n.Link); link != "" {
		lines = append(lines, link)
	}
	return strings.Join(lines, "\n")
}

// cutReason keeps at most maxReason characters of a reason.
func cutReason(reason string) string {
	runes := []rune(reason)
	if len(runes) <= maxReason {
		return reason
	}
	return string(runes[:maxReason-len(reasonCut)]) + reasonCut
}

// oneLine joins a value into one line: every run of spaces, tabs, and line
// breaks becomes one space. A message of one line for each field keeps the
// shape of the notification the same in every channel.
func oneLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
}
