package github_test

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/platform/github"
)

// privateRepoVariable names the private repository of the measurement. It is
// not CUMIN_LIVE_REPO, so that the other live checks never run on it.
const privateRepoVariable = "CUMIN_LIVE_PRIVATE_REPO"

// privateJobVariable names the job of GitHub Actions that the test reads when
// the cumin-core token cannot list the check runs.
const privateJobVariable = "CUMIN_LIVE_PRIVATE_JOB"

// TestLivePrivateRepositoryFacts measures four facts of a private repository
// for the design of #790: the rulesets, the reads of the checks with the
// cumin-core token, git with and without a token (also with the token of a
// clone and a fetch of cumin), and the image address of a
// diagram. It only reads. A read that GitHub refuses is a fact with its
// status, not a failure. docs/ja/development/live-tests.md says what the
// repository needs.
func TestLivePrivateRepositoryFacts(t *testing.T) {
	l := newLivePrivate(t)
	defer func() { t.Log("\n" + l.tableWithoutTokens(t)) }()

	core := l.token(t, "cumin-core")
	l.recordRulesetsFact(t, core)
	l.recordCheckReadsFact(t, core)
	l.recordGitFact(t)
	l.recordImageFact(t, core)
}

// newLivePrivate skips the test unless the Maintainer enabled the live checks
// and named a private repository. It stops when the repository is public.
func newLivePrivate(t *testing.T) *live {
	t.Helper()
	if os.Getenv("CUMIN_LIVE") != "1" {
		t.Skip("live checks run only with CUMIN_LIVE=1")
	}
	value := os.Getenv(privateRepoVariable)
	if value == "" {
		t.Skip("the measurement of a private repository runs only with " + privateRepoVariable + "=<owner>/<repo>")
	}
	owner, repo, ok := strings.Cut(value, "/")
	if !ok || owner == "" || repo == "" {
		t.Fatal("set " + privateRepoVariable + " to <owner>/<repo> of the private repository")
	}
	l := &live{owner: owner, repo: repo, runID: time.Now().UTC().Format("20060102-150405"), tokens: map[string]string{}}

	// A call without authentication sees only a public repository.
	if resp := l.api(t, "", http.MethodGet, "/repos/{repo}", nil); resp.status == http.StatusOK {
		t.Fatalf("%s must be a private repository: a call without authentication reads %s/%s, so it is public", privateRepoVariable, owner, repo)
	}
	l.clientIDs = liveClientIDs(t, owner)

	var repository struct {
		DefaultBranch string `json:"default_branch"`
		Private       bool   `json:"private"`
	}
	l.api(t, l.token(t, "cumin-core"), http.MethodGet, "/repos/{repo}", nil).mustJSON(t, http.StatusOK, &repository)
	if !repository.Private {
		t.Fatalf("%s must be a private repository: GitHub answers \"private\": false for %s/%s", privateRepoVariable, owner, repo)
	}
	if repository.DefaultBranch == "" {
		t.Fatal("the repository has no default branch")
	}
	l.branch = repository.DefaultBranch
	return l
}

// tableWithoutTokens returns the result table. A token in the table is a
// failure of the test, and the table shows a marker in its place.
func (l *live) tableWithoutTokens(t *testing.T) string {
	t.Helper()
	table := l.table()
	l.mu.Lock()
	defer l.mu.Unlock()
	for app, token := range l.tokens {
		for _, secret := range []string{token, basicHeader(token)} {
			if strings.Contains(table, secret) {
				t.Errorf("the token of the app %s appeared in the result table", app)
				table = strings.ReplaceAll(table, secret, "[redacted]")
			}
		}
	}
	return table
}

// basicHeader is the form of a token that git sends to GitHub over HTTPS.
func basicHeader(token string) string {
	return base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
}

// answer describes one answer of GitHub: the status, and the message of a
// refusal.
func answer(r response) string {
	if message := r.message(); r.status >= 400 && message != "" {
		return fmt.Sprintf("status %d (%s)", r.status, message)
	}
	return fmt.Sprintf("status %d", r.status)
}

// recordRulesetsFact records fact P1: the rulesets of the repository and the
// rules of the default branch.
func (l *live) recordRulesetsFact(t *testing.T, token string) {
	t.Helper()
	// "Get all repository rulesets" (docs.github.com, REST, "Rules"): each
	// item holds name, target, source_type, and enforcement.
	rulesets := l.api(t, token, http.MethodGet, "/repos/{repo}/rulesets?per_page=100", nil)
	var sets []struct {
		Name        string `json:"name"`
		Target      string `json:"target"`
		SourceType  string `json:"source_type"`
		Enforcement string `json:"enforcement"`
	}
	if rulesets.status == http.StatusOK {
		rulesets.json(t, &sets)
	}
	var names []string
	for _, set := range sets {
		names = append(names, fmt.Sprintf("`%s` (%s, %s, %s)", set.Name, set.Target, set.SourceType, set.Enforcement))
	}

	rules := l.api(t, token, http.MethodGet, "/repos/{repo}/rules/branches/"+url.PathEscape(l.branch), nil)
	var items []struct {
		Type       string `json:"type"`
		Parameters struct {
			RequiredStatusChecks []struct {
				Context string `json:"context"`
			} `json:"required_status_checks"`
		} `json:"parameters"`
	}
	if rules.status == http.StatusOK {
		rules.json(t, &items)
	}
	var types, required []string
	for _, item := range items {
		types = append(types, item.Type)
		for _, check := range item.Parameters.RequiredStatusChecks {
			required = append(required, check.Context)
		}
	}
	l.record("P1", "The rulesets of a private repository, and the rules of its default branch, read with the cumin-core token (measured-constraints.md rows 13 and 53)", "Not known",
		fmt.Sprintf("`GET .../rulesets`: %s, %d rulesets (target, source, enforcement): %s. `GET .../rules/branches/{branch}`: %s, rules `%s`, required checks `%s`",
			answer(rulesets), len(sets), strings.Join(names, ", "), answer(rules), strings.Join(types, "`, `"), strings.Join(required, "`, `")))
}

// recordCheckReadsFact records fact P2: what the cumin-core token reads of
// the checks of a commit.
func (l *live) recordCheckReadsFact(t *testing.T, token string) {
	t.Helper()
	const what = "The cumin-core token reads the check runs, the commit statuses, the annotations, and the job log of a private repository (measured-constraints.md row 54)"
	// The token holds at most the permissions that cumin asks for.
	permissions, _ := github.AppPermissions("cumin-core")
	var granted []string
	for name, level := range permissions {
		granted = append(granted, name+": "+level)
	}
	sort.Strings(granted)
	asked := "Permissions that cumin asks for the token: `" + strings.Join(granted, "`, `") + "`"

	sha, source := l.measuredCommit(t, token)
	if sha == "" {
		l.record("P2", what, "Not known", asked+". Not measured: the token reads no commit ("+source+")")
		return
	}

	checkRuns := l.api(t, token, http.MethodGet, "/repos/{repo}/commits/"+sha+"/check-runs", nil)
	var runs struct {
		CheckRuns []struct {
			ID         int64  `json:"id"`
			DetailsURL string `json:"details_url"`
			App        struct {
				Slug string `json:"slug"`
			} `json:"app"`
		} `json:"check_runs"`
	}
	if checkRuns.status == http.StatusOK {
		checkRuns.json(t, &runs)
	}
	statuses := l.api(t, token, http.MethodGet, "/repos/{repo}/commits/"+sha+"/status", nil)
	var combined struct {
		Statuses []struct{} `json:"statuses"`
	}
	if statuses.status == http.StatusOK {
		statuses.json(t, &combined)
	}

	// The job of a check run of GitHub Actions: its ID is the last part of
	// details_url (measured-constraints.md row 54). When the token cannot
	// list the check runs, the Maintainer gives the ID of a job. The check
	// run of a job of GitHub Actions has the ID of the job.
	checkRunID, jobID := "", ""
	for _, run := range runs.CheckRuns {
		if run.App.Slug == "github-actions" {
			checkRunID = strconv.FormatInt(run.ID, 10)
			jobID = run.DetailsURL[strings.LastIndex(run.DetailsURL, "/")+1:]
			break
		}
	}
	idSource := "from the check runs"
	if jobID == "" {
		jobID = os.Getenv(privateJobVariable)
		if _, err := strconv.ParseInt(jobID, 10, 64); jobID != "" && err != nil {
			t.Fatalf("set %s to the number of a job of GitHub Actions", privateJobVariable)
		}
		checkRunID, idSource = jobID, "from "+privateJobVariable
	}
	details := fmt.Sprintf("Not measured: the commit has no check run of GitHub Actions that the token reads. Set `%s`", privateJobVariable)
	if jobID != "" {
		annotations := l.api(t, token, http.MethodGet, "/repos/{repo}/check-runs/"+checkRunID+"/annotations", nil)
		logs := l.api(t, token, http.MethodGet, "/repos/{repo}/actions/jobs/"+jobID+"/logs", nil)
		details = fmt.Sprintf("`GET /check-runs/{id}/annotations`: %s. `GET /actions/jobs/{id}/logs`: %s. The IDs are %s", answer(annotations), answer(logs), idSource)
	}
	l.record("P2", what, "Not known",
		fmt.Sprintf("%s. Commit: %s. `GET .../check-runs`: %s, %d check runs. `GET .../status`: %s, %d commit statuses. %s",
			asked, source, answer(checkRuns), len(runs.CheckRuns), answer(statuses), len(combined.Statuses), details))
}

// measuredCommit returns the commit whose checks the test reads: the head of
// the pull request that changed last, or the head of the default branch when
// the token reads no pull request. source says which one, or why none.
func (l *live) measuredCommit(t *testing.T, token string) (sha, source string) {
	t.Helper()
	pulls := l.api(t, token, http.MethodGet, "/repos/{repo}/pulls?state=all&sort=updated&direction=desc&per_page=1", nil)
	var list []struct {
		Head struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if pulls.status == http.StatusOK {
		pulls.json(t, &list)
	}
	if len(list) > 0 && list[0].Head.SHA != "" {
		return list[0].Head.SHA, "the head of the pull request that changed last"
	}
	ref := l.api(t, token, http.MethodGet, "/repos/{repo}/git/ref/heads/"+l.branch, nil)
	var head struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if ref.status == http.StatusOK {
		ref.json(t, &head)
	}
	if head.Object.SHA != "" {
		return head.Object.SHA, "the head of the default branch (the token reads no pull request)"
	}
	return "", fmt.Sprintf("the pull requests: %s, the default branch: %s", answer(pulls), answer(ref))
}

// recordGitFact records fact P3: git over HTTPS without credentials, and with
// the token of each App.
func (l *live) recordGitFact(t *testing.T) {
	t.Helper()
	parts := []string{"No credentials: " + l.gitReads(t, "")}
	for _, app := range []string{"planner", "implementer", "reviewer", "cumin-core"} {
		parts = append(parts, "Token of the "+app+" App: "+l.gitReads(t, l.token(t, app)))
	}
	// The token of a clone and a fetch of cumin: of the App of a role,
	// limited to the one repository and to the read of its contents
	// (docs/ja/designs/agent-run.md, the topic on the work directory).
	for _, app := range []string{"planner", "implementer", "reviewer"} {
		parts = append(parts, "Token of the "+app+" App limited to `contents: read`: "+l.gitReads(t, l.readToken(t, app)))
	}
	l.record("P3", "`git ls-remote`, `git clone`, `git fetch`, and `git remote set-head origin --auto` of a private repository over HTTPS, without credentials, with the token of each App, and with the token of each role App limited to `contents: read`, in the environment of git", "Not known",
		strings.Join(parts, ". "))
}

// readToken returns a token of one role App that is limited to the one
// repository and to the read of its contents, as cumin creates it for a
// clone and a fetch.
func (l *live) readToken(t *testing.T, app string) string {
	t.Helper()
	client := github.NewAppClient(github.DefaultBaseURL, nil)
	created, err := client.CreateReadToken(context.Background(), l.credentials(t, app), app, l.owner, l.repo)
	if err != nil {
		t.Fatalf("app %s: %v", app, err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	// Kept so that tableWithoutTokens looks for it too.
	l.tokens[app+" (contents: read)"] = created.Token
	return created.Token
}

// gitReads runs the four reads of git and returns their results. git gets
// the token as cumin gives it to an agent (internal/agent/env.go): through
// GIT_CONFIG_*, never as an argument and never in a file. git reads no
// settings of the user, so no credential helper answers. An empty token runs
// git without credentials.
func (l *live) gitReads(t *testing.T, token string) string {
	t.Helper()
	env := append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
		"GIT_SSH_COMMAND=false", "GIT_ASKPASS=", "SSH_ASKPASS=",
		// Also drops a header that the environment of the test holds.
		"GIT_CONFIG_COUNT=0",
	)
	if token != "" {
		env = append(env,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http.https://github.com/.extraheader",
			"GIT_CONFIG_VALUE_0=Authorization: Basic "+basicHeader(token),
		)
	}
	g := &gitRepo{dir: t.TempDir(), env: env}
	remote := "https://github.com/" + l.owner + "/" + l.repo + ".git"

	lsRemote := gitResult(t, g, "ls-remote", remote, "HEAD")
	clone := gitResult(t, g, "clone", "--quiet", "--depth", "1", remote, "clone")
	g.mustRun(t, "init", "--quiet")
	g.mustRun(t, "remote", "add", "origin", remote)
	fetch := gitResult(t, g, "fetch", "--quiet", "--depth", "1", "origin", l.branch)
	// After the fetch, as Workspace.Prepare runs it (internal/agent/worktree.go).
	setHead := gitResult(t, g, "remote", "set-head", "origin", "--auto")
	// An error of git names the remote. The table does not name the private
	// repository.
	return strings.ReplaceAll(fmt.Sprintf("`ls-remote` %s, `clone` %s, `fetch` %s, `remote set-head origin --auto` %s", lsRemote, clone, fetch, setHead), l.owner+"/"+l.repo, "<owner>/<repo>")
}

// gitResult runs git and returns the exit code, with the last line of the
// output when git fails. The output of a success holds nothing to record.
func gitResult(t *testing.T, g *gitRepo, args ...string) string {
	t.Helper()
	out, err := g.run(args...)
	if err == nil {
		return "exit 0"
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("git %s: %v", args[0], err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return fmt.Sprintf("exit %d (%s)", exit.ExitCode(), strings.TrimSpace(lines[len(lines)-1]))
}

// recordImageFact records fact P4: the image address of one SVG on
// cumin/diagrams, as an issue shows it.
func (l *live) recordImageFact(t *testing.T, token string) {
	t.Helper()
	const what = "The image address `https://raw.githubusercontent.com/<owner>/<repo>/<commit>/<path>.svg` of a diagram on `cumin/diagrams` of a private repository (measured-constraints.md row 115)"
	ref := l.api(t, token, http.MethodGet, "/repos/{repo}/git/ref/heads/"+diagramsBranch, nil)
	var head struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if ref.status == http.StatusOK {
		ref.json(t, &head)
	}
	if head.Object.SHA == "" {
		l.record("P4", what, "Not known", "Not measured: the cumin-core token reads no branch `"+diagramsBranch+"`: "+answer(ref))
		return
	}
	tree := l.api(t, token, http.MethodGet, "/repos/{repo}/git/trees/"+head.Object.SHA+"?recursive=1", nil)
	var files struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
	}
	if tree.status == http.StatusOK {
		tree.json(t, &files)
	}
	path := ""
	for _, file := range files.Tree {
		if file.Type == "blob" && strings.HasSuffix(file.Path, ".svg") {
			path = file.Path
			break
		}
	}
	if path == "" {
		l.record("P4", what, "Not known", "Not measured: the cumin-core token finds no SVG on `"+diagramsBranch+"`: "+answer(tree))
		return
	}
	// The address is not in the table: it names the private repository.
	address := "https://raw.githubusercontent.com/" + l.owner + "/" + l.repo + "/" + head.Object.SHA + "/" + path
	l.record("P4", what, "Not known",
		fmt.Sprintf("Without authentication: %s. With the cumin-core token as `Authorization: Bearer`: %s", imageAnswer(t, address, ""), imageAnswer(t, address, token)))
}

// imageAnswer reads an image address and returns the status and the content
// type. An empty token makes a call without authentication.
func imageAnswer(t *testing.T, address, token string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, address, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// The error of the client names the address of the private repository.
		var urlError *url.Error
		if errors.As(err, &urlError) {
			err = urlError.Err
		}
		t.Fatalf("read the image address: %v", err)
	}
	defer resp.Body.Close()
	return fmt.Sprintf("status %d, content type `%s`", resp.StatusCode, resp.Header.Get("Content-Type"))
}
