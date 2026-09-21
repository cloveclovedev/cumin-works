package workflow

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The names come from the table "Labels" in issue-states.md. Change the
// document and RepositoryLabels together.
func TestRepositoryLabels_MatchTheDocument(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "ja", "requirements", "workflow", "issue-states.md"))
	if err != nil {
		t.Fatal(err)
	}
	// The table is between the first and the second level-2 heading.
	parts := strings.Split(string(doc), "\n## ")
	if len(parts) < 3 {
		t.Fatal("issue-states.md has no label section")
	}
	section := parts[1]
	var want []string
	for _, match := range regexp.MustCompile("`((?:cumin|risk)/[a-z/-]+)`").FindAllStringSubmatch(section, -1) {
		if !slices.Contains(want, match[1]) {
			want = append(want, match[1])
		}
	}
	// The table names the kinds cumin/type/* and cumin/status/* too.
	want = slices.DeleteFunc(want, func(name string) bool { return strings.HasSuffix(name, "/") })

	var got []string
	for _, label := range RepositoryLabels() {
		got = append(got, label.Name)
		if len(label.Color) != 6 || label.Description == "" {
			t.Errorf("label %s has color %q and description %q", label.Name, label.Color, label.Description)
		}
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("RepositoryLabels = %v\nthe document = %v", got, want)
	}
	// One type label, seven status labels, three risk labels.
	if len(got) != 11 {
		t.Errorf("%d labels, want 11", len(got))
	}
}
