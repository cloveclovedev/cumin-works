package workflow

import (
	"strings"
	"testing"
)

// The list of actions holds no empty name and no name twice, and every
// name is written as issue-states.md writes it: lower case, single spaces.
func TestActionNamesAreSetAndUnique(t *testing.T) {
	seen := map[ActionName]bool{}
	for i, name := range ActionNames {
		text := string(name)
		if text == "" {
			t.Errorf("action %d of the list has an empty name", i)
			continue
		}
		if text != strings.ToLower(text) || text != strings.Join(strings.Fields(text), " ") {
			t.Errorf("action name %q is not lower case with single spaces", text)
		}
		if seen[name] {
			t.Errorf("action name %q is in the list twice", text)
		}
		seen[name] = true
	}
}
