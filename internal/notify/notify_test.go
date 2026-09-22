package notify

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestMessage_HoldsTheRowTheReasonAndTheLink(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		notification Notification
		want         string
	}{
		{
			name: "every field",
			notification: Notification{
				Row:        "I2",
				Reason:     "the Implementer returned blocked",
				Repository: "example-org/example-repo",
				Subject:    "issue #12",
				Link:       "https://github.com/example-org/example-repo/issues/12",
			},
			want: "cumin: I2: the Implementer returned blocked\n" +
				"example-org/example-repo issue #12\n" +
				"https://github.com/example-org/example-repo/issues/12",
		},
		{
			name: "no row, no subject",
			notification: Notification{
				Reason:     "the poll failed three times with the same reason",
				Repository: "example-org/example-repo",
				Link:       "https://github.com/example-org/example-repo",
			},
			want: "cumin: the poll failed three times with the same reason\n" +
				"example-org/example-repo\n" +
				"https://github.com/example-org/example-repo",
		},
		{
			name:         "a reason of more than one line becomes one line",
			notification: Notification{Row: "I2", Reason: "the run ended abnormally\n\nkind: time limit\n"},
			want:         "cumin: I2: the run ended abnormally kind: time limit",
		},
		{
			name:         "nothing but a reason",
			notification: Notification{Reason: "cumin stopped"},
			want:         "cumin: cumin stopped",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := Message(test.notification); got != test.want {
				t.Errorf("Message() =\n%q\nwant\n%q", got, test.want)
			}
		})
	}
}

// A reason that an agent wrote can be long. The message keeps the link,
// which is the line that the Owner must be able to open.
func TestMessage_CutsALongReasonAndKeepsTheLink(t *testing.T) {
	t.Parallel()
	link := "https://github.com/example-org/example-repo/issues/12"
	got := Message(Notification{
		Row:        "I2",
		Reason:     strings.Repeat("a", maxReason+1000),
		Repository: "example-org/example-repo",
		Subject:    "issue #12",
		Link:       link,
	})
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("the message has %d lines, want 3:\n%s", len(lines), got)
	}
	if lines[2] != link {
		t.Errorf("the last line is %q, want the link", lines[2])
	}
	if n := len([]rune(lines[0])); n != len("cumin: I2: ")+maxReason {
		t.Errorf("the summary holds %d characters, want the prefix and %d", n, maxReason)
	}
	if !strings.HasSuffix(lines[0], reasonCut) {
		t.Errorf("the summary does not end with %q: %q", reasonCut, lines[0])
	}
}

// fakeSender records the texts that it was given, and may fail.
type fakeSender struct {
	texts []string
	err   error
}

func (f *fakeSender) Send(_ context.Context, text string) error {
	f.texts = append(f.texts, text)
	return f.err
}

func TestNotify_SendsTheMessageThroughTheSender(t *testing.T) {
	t.Parallel()
	sender := &fakeSender{}
	notification := Notification{Row: "I2", Reason: "the verification failed", Repository: "example-org/example-repo"}

	if err := New(sender).Notify(context.Background(), notification); err != nil {
		t.Fatalf("Notify() = %v, want nil", err)
	}
	if len(sender.texts) != 1 {
		t.Fatalf("the sender got %d messages, want 1", len(sender.texts))
	}
	if want := Message(notification); sender.texts[0] != want {
		t.Errorf("the sender got %q, want %q", sender.texts[0], want)
	}
}

func TestNotify_ReturnsTheErrorOfTheSender(t *testing.T) {
	t.Parallel()
	failure := errors.New("the channel answered 500")
	err := New(&fakeSender{err: failure}).Notify(context.Background(), Notification{Reason: "stopped"})
	if !errors.Is(err, failure) {
		t.Errorf("Notify() = %v, want the error of the sender", err)
	}
}

func TestNotify_WithoutASenderReportsIt(t *testing.T) {
	t.Parallel()
	var missing *Notifier
	if err := missing.Notify(context.Background(), Notification{Reason: "stopped"}); !errors.Is(err, ErrNoSender) {
		t.Errorf("a nil notifier gave %v, want ErrNoSender", err)
	}
	if err := New(nil).Notify(context.Background(), Notification{Reason: "stopped"}); !errors.Is(err, ErrNoSender) {
		t.Errorf("a notifier without a sender gave %v, want ErrNoSender", err)
	}
}

// TestPackage_ImportsOnlyTheStandardLibrary keeps the package free of any
// channel: nothing here may know Discord, HTTP, or the Keychain. A sender
// takes plain text, so a provider package never has to be imported.
func TestPackage_ImportsOnlyTheStandardLibrary(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read the package directory: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(".", name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imported := range file.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatalf("read the import %s of %s: %v", imported.Path.Value, name, err)
			}
			if strings.Contains(path, ".") {
				t.Errorf("%s imports %q; this package uses the standard library only", name, path)
			}
		}
	}
}
