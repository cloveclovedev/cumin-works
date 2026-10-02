// Package testenv holds the rule for a test that needs something of the
// machine: a tool, a user that is not root, a directory mode.
package testenv

import (
	"fmt"
	"os"
	"testing"
)

// SkipOrFail ends a test whose machine lacks what the test needs. The
// message names what is missing. On the machine of a developer the test
// skips. On CI the test fails, because a skip there looks like a pass and
// hides a runner that lost a tool. GitHub Actions sets the environment
// variable CI to "true".
//
// A test that belongs to another system (a macOS test on Linux) does not
// call SkipOrFail. It skips behind a check of runtime.GOOS.
func SkipOrFail(t testing.TB, format string, args ...any) {
	t.Helper()
	missing := fmt.Sprintf(format, args...)
	if os.Getenv("CI") == "true" {
		t.Fatalf("%s: this test must run on CI (the environment variable CI is \"true\")", missing)
		return
	}
	t.Skip(missing)
}
