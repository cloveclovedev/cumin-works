package agent

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// This file holds a slog.Handler for tests. The tests use it to assert what
// a log must not hold. A check on the text of a log line is wrong for a
// short value: the time attribute can hold the same digits (the CI of #80
// on 2026-09-21). The recorder keeps the message and the attributes of each
// record, and not the time.

// logRecord is one record without its time. Attribute keys carry the group
// as a prefix ("group.key"), and the values are their text. The attributes
// are a list, not a map: a record can hold one key more than once, and
// every value must be seen.
type logRecord struct {
	Level   slog.Level
	Message string
	Attrs   []logAttr
}

type logAttr struct {
	Key, Value string
}

// logRecorder collects the records of a test logger.
type logRecorder struct {
	mu      sync.Mutex
	records []logRecord
}

// newTestLogger returns a logger that records every record at info level
// and above, and the recorder.
func newTestLogger() (*slog.Logger, *logRecorder) {
	rec := &logRecorder{}
	return slog.New(&recorderHandler{rec: rec, level: slog.LevelInfo}), rec
}

// find returns where the text appears in the records: in a message, in an
// attribute key, or in an attribute value. The time is not part of a record.
func (r *logRecorder) find(text string) (where string, found bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, record := range r.records {
		if strings.Contains(record.Message, text) {
			return "the message " + record.Message, true
		}
		for _, a := range record.Attrs {
			if strings.Contains(a.Key, text) || strings.Contains(a.Value, text) {
				return "the attribute " + a.Key + " of the message " + record.Message, true
			}
		}
	}
	return "", false
}

// requireNoText fails the test when a message, an attribute key, or an
// attribute value holds one of the texts.
func (r *logRecorder) requireNoText(t *testing.T, forbidden ...string) {
	t.Helper()
	for _, text := range forbidden {
		if where, found := r.find(text); found {
			t.Errorf("the logs hold %q in %s", text, where)
		}
	}
}

// hasMessage reports if a record has this message.
func (r *logRecorder) hasMessage(message string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, record := range r.records {
		if record.Message == message {
			return true
		}
	}
	return false
}

// hasAttr reports if a record has an attribute with this key and this text.
func (r *logRecorder) hasAttr(key, value string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, record := range r.records {
		for _, a := range record.Attrs {
			if a.Key == key && a.Value == value {
				return true
			}
		}
	}
	return false
}

// recorderHandler is the slog.Handler behind newTestLogger. WithAttrs and
// WithGroup return a handler with the same recorder.
type recorderHandler struct {
	rec    *logRecorder
	level  slog.Level
	prefix string    // the open groups, as "a.b."
	attrs  []logAttr // the attributes of WithAttrs, with their prefix
}

func (h *recorderHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *recorderHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := append([]logAttr(nil), h.attrs...)
	r.Attrs(func(a slog.Attr) bool {
		attrs = flattenAttr(attrs, h.prefix, a)
		return true
	})
	h.rec.mu.Lock()
	defer h.rec.mu.Unlock()
	h.rec.records = append(h.rec.records, logRecord{Level: r.Level, Message: r.Message, Attrs: attrs})
	return nil
}

func (h *recorderHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := &recorderHandler{rec: h.rec, level: h.level, prefix: h.prefix, attrs: append([]logAttr(nil), h.attrs...)}
	for _, a := range attrs {
		next.attrs = flattenAttr(next.attrs, h.prefix, a)
	}
	return next
}

func (h *recorderHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return &recorderHandler{rec: h.rec, level: h.level, prefix: h.prefix + name + ".", attrs: h.attrs}
}

// flattenAttr appends the attribute to the list. A group becomes one entry
// for each of its members, with the group name in the key.
func flattenAttr(into []logAttr, prefix string, a slog.Attr) []logAttr {
	value := a.Value.Resolve()
	if value.Kind() != slog.KindGroup {
		return append(into, logAttr{Key: prefix + a.Key, Value: value.String()})
	}
	groupPrefix := prefix
	if a.Key != "" {
		groupPrefix += a.Key + "."
	}
	for _, member := range value.Group() {
		into = flattenAttr(into, groupPrefix, member)
	}
	return into
}

// The recorder finds a forbidden value in an attribute, in a group, and in
// a message, and it does not see the time of a record.
func TestLogRecorder_FindsValuesAndIgnoresTheTime(t *testing.T) {
	logger, rec := newTestLogger()
	logger.Info("usage", "five_hour", slog.GroupValue(slog.Float64("utilization", 0.31)))
	logger.With("session_id", "abc").WithGroup("run").Info("agent end", "result", "done")
	logger.Debug("not recorded", "secret", "0.61")
	// The same key two times: both values are kept.
	logger.Info("twice", "value", "0.77", "value", "redacted")

	for _, text := range []string{"0.31", "utilization", "five_hour.utilization", "agent end", "abc", "0.77"} {
		if _, found := rec.find(text); !found {
			t.Errorf("find(%q) = false, want true", text)
		}
	}
	if _, found := rec.find("0.61"); found {
		t.Error("a debug record was recorded")
	}
	if !rec.hasMessage("usage") || !rec.hasAttr("session_id", "abc") || !rec.hasAttr("run.result", "done") {
		t.Errorf("records = %+v", rec.records)
	}

	// A record whose time holds the digits of a forbidden value, and nothing else.
	_, timed := newTestLogger()
	handler := &recorderHandler{rec: timed, level: slog.LevelInfo}
	at := time.Date(2026, 9, 21, 10, 31, 0, 310000000, time.UTC)
	if err := handler.Handle(context.Background(), slog.NewRecord(at, slog.LevelInfo, "quota usage read", 0)); err != nil {
		t.Fatal(err)
	}
	if !timed.hasMessage("quota usage read") {
		t.Fatal("the record was not recorded")
	}
	for _, text := range []string{"10:31", "0.31", ".31"} {
		if where, found := timed.find(text); found {
			t.Errorf("find(%q) = true in %s, want the time to be ignored", text, where)
		}
	}
}
