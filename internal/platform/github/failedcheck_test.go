package github_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/cloveclovedev/cumin-works/internal/platform/github"
	"github.com/cloveclovedev/cumin-works/internal/platform/github/githubtest"
)

const headSHA = "2222222222222222222222222222222222222222"

// TestFailedCheckContent_HoldsTheAnnotationsAndTheEndOfTheJobLog is what the
// request of I4 (a required check failed, fix it) carries.
func TestFailedCheckContent_HoldsTheAnnotationsAndTheEndOfTheJobLog(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddCheckRun(repo, headSHA, githubtest.CheckRun{
		ID: 7, Name: "ci", Conclusion: "failure", JobID: 42,
		Annotations: []githubtest.Annotation{
			{Path: "internal/a/a.go", Level: "failure", Message: "a_test.go:12: want 2, got 1\n"},
			{Path: "internal/b/b.go", Level: "warning", Message: "this line is long"},
		},
		JobLog: "step 1\nstep 2\nFAIL\tinternal/a\n",
	})
	client := github.NewAppClient(server.URL, server.Client())
	logs := &bytes.Buffer{}

	content := client.FailedCheckContent(context.Background(), githubtest.Token,
		"example-org", "example-repo", headSHA, []github.RequiredCheck{{Name: "ci"}}, logger(logs))

	text := contentOf(t, content, "ci")
	for _, want := range []string{`Check "ci" failed.`, "internal/a/a.go: a_test.go:12: want 2, got 1", "FAIL\tinternal/a"} {
		if !strings.Contains(text, want) {
			t.Errorf("the text does not hold %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "this line is long") {
		t.Errorf("the text holds an annotation that is not a failure:\n%s", text)
	}
	if logs.Len() != 0 {
		t.Errorf("a check that could be read warned: %s", logs)
	}
}

// TestFailedCheckContent_IsCutAtTheLimit keeps the request of I4 small.
func TestFailedCheckContent_IsCutAtTheLimit(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	var log strings.Builder
	for i := range 2000 {
		log.WriteString("a line of the log that repeats, number ")
		log.WriteString(strings.Repeat("x", 20))
		log.WriteString("\n")
		_ = i
	}
	fake.AddCheckRun(repo, headSHA, githubtest.CheckRun{
		ID: 7, Name: "ci", Conclusion: "failure", JobID: 42, JobLog: log.String(),
	})
	client := github.NewAppClient(server.URL, server.Client())

	content := client.FailedCheckContent(context.Background(), githubtest.Token,
		"example-org", "example-repo", headSHA, []github.RequiredCheck{{Name: "ci"}}, nil)

	text := contentOf(t, content, "ci")
	if len(text) > 4000 {
		t.Errorf("the text is %d characters, want 4000 at most", len(text))
	}
	// The end of the log is what says why a job failed, so the text keeps
	// the end and not the beginning.
	if !strings.HasSuffix(strings.TrimSpace(text), strings.Repeat("x", 20)) {
		t.Errorf("the text does not end with the end of the log:\n%s", text[max(0, len(text)-200):])
	}
}

// TestFailedCheckContent_AChecksWhoseContentIsOutOfReachNamesItself: a
// commit status, a missing job, and a call that fails are not errors. The
// request of I4 goes out with the name of the check.
func TestFailedCheckContent_AChecksWhoseContentIsOutOfReachNamesItself(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setUp func(fake *githubtest.Fake, repo *githubtest.Repository)
	}{
		{
			name:  "a commit status has no check run",
			setUp: func(*githubtest.Fake, *githubtest.Repository) {},
		},
		{
			name: "a check run without a job of GitHub Actions",
			setUp: func(fake *githubtest.Fake, repo *githubtest.Repository) {
				fake.AddCheckRun(repo, headSHA, githubtest.CheckRun{ID: 7, Name: "ci", Conclusion: "failure"})
			},
		},
		{
			name: "the check runs of the commit cannot be read",
			setUp: func(fake *githubtest.Fake, repo *githubtest.Repository) {
				fake.AddCheckRun(repo, headSHA, githubtest.CheckRun{ID: 7, Name: "ci", Conclusion: "failure", JobID: 42, JobLog: "x\n"})
				fake.FailNext(http.MethodGet, "/repos/example-org/example-repo/commits/"+headSHA+"/check-runs", http.StatusForbidden)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake, server := githubtest.New(t)
			repo := fake.AddRepository("example-org", "example-repo")
			tc.setUp(fake, repo)
			client := github.NewAppClient(server.URL, server.Client())
			logs := &bytes.Buffer{}

			content := client.FailedCheckContent(context.Background(), githubtest.Token,
				"example-org", "example-repo", headSHA, []github.RequiredCheck{{Name: "ci"}}, logger(logs))

			text := contentOf(t, content, "ci")
			if !strings.Contains(text, `Check "ci" failed.`) || !strings.Contains(text, "could not read its content") {
				t.Errorf("the text does not name the check and the missing content:\n%s", text)
			}
		})
	}
}

// TestFailedCheckContent_ReadsOnlyTheChecksThatFailed: a passed check costs
// no call.
func TestFailedCheckContent_ReadsOnlyTheChecksThatFailed(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddCheckRun(repo, headSHA, githubtest.CheckRun{ID: 7, Name: "ci", Conclusion: "failure", JobID: 42, JobLog: "FAIL\n"})
	fake.AddCheckRun(repo, headSHA, githubtest.CheckRun{ID: 8, Name: "lint", Conclusion: "success", JobID: 43, JobLog: "ok\n"})
	client := github.NewAppClient(server.URL, server.Client())

	content := client.FailedCheckContent(context.Background(), githubtest.Token,
		"example-org", "example-repo", headSHA, []github.RequiredCheck{{Name: "ci"}}, nil)

	if len(content) != 1 || content[0].Check.Name != "ci" {
		t.Errorf("content = %+v, want the failed check only", content)
	}
	if n := fake.CountRequests(http.MethodGet, "/repos/example-org/example-repo/actions/jobs/43/logs"); n != 0 {
		t.Errorf("%d reads of the log of the passed check, want none", n)
	}
	if n := fake.CountRequests(http.MethodGet, "/repos/example-org/example-repo/actions/jobs/42/logs"); n != 1 {
		t.Errorf("%d reads of the log of the failed check, want 1", n)
	}
}

// TestFailedCheckContent_TwoRulesOfTheSameNameKeepTheirOwnText: two
// rulesets can require the same name from two Apps. Each one keeps its own
// annotations and log, so the request of I4 holds both failures.
func TestFailedCheckContent_TwoRulesOfTheSameNameKeepTheirOwnText(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddCheckRun(repo, headSHA, githubtest.CheckRun{
		ID: 7, Name: "build", Conclusion: "failure", AppID: 15368, JobID: 42, JobLog: "the job of the first App\n",
	})
	fake.AddCheckRun(repo, headSHA, githubtest.CheckRun{
		ID: 8, Name: "build", Conclusion: "failure", AppID: 999, JobID: 43, JobLog: "the job of the second App\n",
	})
	client := github.NewAppClient(server.URL, server.Client())

	content := client.FailedCheckContent(context.Background(), githubtest.Token,
		"example-org", "example-repo", headSHA, []github.RequiredCheck{
			{Name: "build", Integration: 15368},
			{Name: "build", Integration: 999},
		}, nil)

	if len(content) != 2 {
		t.Fatalf("%d texts, want one for each rule", len(content))
	}
	if !strings.Contains(content[0].Content, "the first App") || !strings.Contains(content[1].Content, "the second App") {
		t.Errorf("the texts do not keep their own App:\n%+v", content)
	}
}

// TestFailedCheckContent_TheAppOfTheRuleDecidesWhichRunIsRead: a rule that
// names an App is met only by that App, in the decision (I3, I4) and here.
func TestFailedCheckContent_TheAppOfTheRuleDecidesWhichRunIsRead(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	// Two Apps report a check of the same name. The newer run is the one
	// of the other App, and it passed.
	fake.AddCheckRun(repo, headSHA, githubtest.CheckRun{
		ID: 7, Name: "protected-paths", Conclusion: "failure", AppID: 15368, JobID: 42,
		JobLog: "the job of GitHub Actions\n",
	})
	fake.AddCheckRun(repo, headSHA, githubtest.CheckRun{
		ID: 9, Name: "protected-paths", Conclusion: "success", AppID: 999, JobID: 43,
		JobLog: "the job of another App\n",
	})
	client := github.NewAppClient(server.URL, server.Client())

	content := client.FailedCheckContent(context.Background(), githubtest.Token,
		"example-org", "example-repo", headSHA,
		[]github.RequiredCheck{{Name: "protected-paths", Integration: 15368}}, nil)

	text := contentOf(t, content, "protected-paths")
	if !strings.Contains(text, "the job of GitHub Actions") {
		t.Errorf("the text does not come from the App of the rule:\n%s", text)
	}
	if strings.Contains(text, "another App") {
		t.Errorf("the text comes from the run of another App:\n%s", text)
	}
}

// TestFailedCheckContent_ReadsEveryPageOfTheAnnotations: a failure can
// stand behind a page of warnings.
func TestFailedCheckContent_ReadsEveryPageOfTheAnnotations(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	run := githubtest.CheckRun{ID: 7, Name: "ci", Conclusion: "failure", JobID: 42, JobLog: "x\n"}
	for range 100 {
		run.Annotations = append(run.Annotations, githubtest.Annotation{Path: "a.go", Level: "warning", Message: "a warning"})
	}
	run.Annotations = append(run.Annotations, githubtest.Annotation{Path: "b.go", Level: "failure", Message: "the failure of the second page"})
	fake.AddCheckRun(repo, headSHA, run)
	client := github.NewAppClient(server.URL, server.Client())

	content := client.FailedCheckContent(context.Background(), githubtest.Token,
		"example-org", "example-repo", headSHA, []github.RequiredCheck{{Name: "ci"}}, nil)

	if text := contentOf(t, content, "ci"); !strings.Contains(text, "the failure of the second page") {
		t.Errorf("the failure of the second page is missing:\n%s", text)
	}
}

// TestFailedCheckContent_CutsAtTheEndOfARune keeps the text valid UTF-8,
// because an agent receives it as the text of a request.
func TestFailedCheckContent_CutsAtTheEndOfARune(t *testing.T) {
	fake, server := githubtest.New(t)
	repo := fake.AddRepository("example-org", "example-repo")
	fake.AddCheckRun(repo, headSHA, githubtest.CheckRun{
		ID: 7, Name: "ci", Conclusion: "failure", JobID: 42,
		JobLog: strings.Repeat("あ", 3000) + "\n",
	})
	client := github.NewAppClient(server.URL, server.Client())

	content := client.FailedCheckContent(context.Background(), githubtest.Token,
		"example-org", "example-repo", headSHA, []github.RequiredCheck{{Name: "ci"}}, nil)

	if text := contentOf(t, content, "ci"); !utf8.ValidString(text) {
		t.Errorf("the text is not valid UTF-8: %q", text)
	}
}

// TestFailedCheckContent_NoNameCostsNoCall: the poll calls this only after
// a failure, so an empty list must not reach GitHub.
func TestFailedCheckContent_NoNameCostsNoCall(t *testing.T) {
	fake, server := githubtest.New(t)
	fake.AddRepository("example-org", "example-repo")
	client := github.NewAppClient(server.URL, server.Client())

	content := client.FailedCheckContent(context.Background(), githubtest.Token,
		"example-org", "example-repo", headSHA, nil, nil)

	if len(content) != 0 || len(fake.Requests()) != 0 {
		t.Errorf("content = %v, %d requests, want none of either", content, len(fake.Requests()))
	}
}

// contentOf is the text of the check with the name, for a test.
func contentOf(t *testing.T, content []github.FailedCheck, name string) string {
	t.Helper()
	for _, check := range content {
		if check.Check.Name == name {
			return check.Content
		}
	}
	t.Fatalf("no text for the check %q in %+v", name, content)
	return ""
}

func logger(out *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
