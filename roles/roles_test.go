package roles

import (
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
)

// Every role that cumin drives has a file, and the file is a document of
// plain Markdown: it starts with a heading and uses no bold text.
func TestFile_ExistsForEveryRole(t *testing.T) {
	for _, role := range config.AllRoles() {
		text, err := File(role)
		if err != nil {
			t.Errorf("File(%s): %v", role, err)
			continue
		}
		if !strings.HasPrefix(text, "# ") || strings.Contains(text, "**") {
			t.Errorf("File(%s) must start with a heading and must not use bold text:\n%s", role, text)
		}
	}
}

// A name that is not a role never comes back as a role file, so that a
// path or another file of the directory cannot reach an agent as the
// contract of a role.
func TestFile_RejectsANameThatIsNotARole(t *testing.T) {
	for _, name := range []config.Role{"", "tester", "../roles/implementer", "roles/implementer"} {
		if _, err := File(name); err == nil {
			t.Errorf("File(%q) succeeded, want an error", name)
		}
	}
}
