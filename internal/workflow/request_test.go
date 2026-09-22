package workflow

import (
	"strings"
	"testing"
)

// I1 (issue-states.md): the branch of a request is cumin/<number>-<slug>,
// with the slug rule of the design note.
func TestBranchName_I1(t *testing.T) {
	tests := []struct {
		name, title, want string
	}{
		{"a plain title", "Add the login screen", "cumin/10-add-the-login-screen"},
		{"a Conventional Commits prefix", "feat(github): read the title", "cumin/10-feat-github-read-the-title"},
		{"upper case and punctuation", "Fix I2: verify the PR (again)!", "cumin/10-fix-i2-verify-the-pr-again"},
		{"characters outside a-z and 0-9 become one dash", "日本語 の 題 with émojis 🎉 and_underscores", "cumin/10-with-mojis-and-underscores"},
		{"a long title is cut at a word", "read the title and the closing pull requests of each sub-issue in the poll snapshot", "cumin/10-read-the-title-and-the-closing-pull"},
		{"a cut exactly at the limit keeps the word", "aaaa bbbb cccc dddd eeee ffff gggg hhhh iiii jjjj", "cumin/10-aaaa-bbbb-cccc-dddd-eeee-ffff-gggg-hhhh"},
		{"a single long word is cut to the limit", strings.Repeat("a", 50), "cumin/10-" + strings.Repeat("a", 40)},
		{"an empty title", "", "cumin/10-issue"},
		{"a title without letters or digits", "???", "cumin/10-issue"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BranchName(10, tt.title); got != tt.want {
				t.Errorf("BranchName(10, %q) = %q, want %q", tt.title, got, tt.want)
			}
		})
	}
}

// I1 (issue-states.md): the request text of the kind "implement" names
// the repository, the issue, the branch, and the work directory, and
// orders one pull request with Closes #N.
func TestImplementRequestText_I1(t *testing.T) {
	text := ImplementRequestText("example-org/example-repo", 10, "cumin/10-add-the-login-screen", "/work/example-org/example-repo/10-implementer")
	for _, want := range []string{
		"Request: implement\n",
		"Repository: example-org/example-repo\n",
		"Implementation issue: #10\n",
		"Branch: cumin/10-add-the-login-screen\n",
		"Work directory: /work/example-org/example-repo/10-implementer\n",
		"one pull request",
		"skill cumin-pull-request",
		"\"Closes #10\"",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the request text does not hold %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "**") {
		t.Error("the request text uses bold text")
	}
}
