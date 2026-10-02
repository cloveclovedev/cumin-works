package testenv_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cloveclovedev/cumin-works/internal/core/testenv"
)

// fakeTB records how SkipOrFail ends a test. The embedded testing.TB is
// nil: a call of any other method panics, and so fails the test.
type fakeTB struct {
	testing.TB
	skipped, failed string
}

func (f *fakeTB) Helper() {}

func (f *fakeTB) Skip(args ...any) { f.skipped = fmt.Sprint(args...) }

func (f *fakeTB) Fatalf(format string, args ...any) { f.failed = fmt.Sprintf(format, args...) }

func TestSkipOrFail_SkipsWithoutCI(t *testing.T) {
	for _, value := range []string{"", "false"} {
		t.Setenv("CI", value)
		fake := &fakeTB{}
		testenv.SkipOrFail(fake, "%s is not installed", "gh")
		if fake.skipped != "gh is not installed" {
			t.Errorf("CI=%q: skip message = %q, want %q", value, fake.skipped, "gh is not installed")
		}
		if fake.failed != "" {
			t.Errorf("CI=%q: the test failed: %s", value, fake.failed)
		}
	}
}

func TestSkipOrFail_FailsOnCIAndNamesWhatIsMissing(t *testing.T) {
	t.Setenv("CI", "true")
	fake := &fakeTB{}
	testenv.SkipOrFail(fake, "%s is not installed", "gh")
	if !strings.Contains(fake.failed, "gh is not installed") {
		t.Errorf("failure message = %q, want it to name what is missing", fake.failed)
	}
	if fake.skipped != "" {
		t.Errorf("the test skipped: %s", fake.skipped)
	}
}
