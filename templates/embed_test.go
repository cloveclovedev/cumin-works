package templates

import (
	"os"
	"strings"
	"testing"
)

// Every Markdown file of the directory is embedded, as it is.
func TestRead_ReturnsEveryTemplateFile(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		n++
		want, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		got, err := Read(entry.Name())
		if err != nil {
			t.Errorf("Read(%s): %v", entry.Name(), err)
			continue
		}
		if got != string(want) {
			t.Errorf("Read(%s) differs from the file", entry.Name())
		}
	}
	if n == 0 {
		t.Fatal("no template file found")
	}
}

func TestRead_RejectsAnUnknownName(t *testing.T) {
	for _, name := range []string{"missing.md", "../roles/implementer.md", ""} {
		if _, err := Read(name); err == nil {
			t.Errorf("Read(%q) succeeded, want an error", name)
		}
	}
}
