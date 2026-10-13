package scripts

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// tailscalePolicyRepoTools are the standard tools that
// scripts/tailscale-policy-repo.sh may call. Every run of the tests has only
// these and the fake gh on PATH, so a call of another tool fails the tests.
var tailscalePolicyRepoTools = []string{"dirname", "sed", "grep", "tr", "awk", "cp", "cat", "base64", "mktemp", "rm"}

// The fake gh holds one repository, acme/tailnet-policy. The file CREATED
// exists when the repository exists, and the file FILES holds one line for
// each file of the repository: the path, a tab, and the content as the
// script sent it. It writes every call to the file CALLS, without the
// content of a file. FAKE_READ_ERROR replaces the answer of GitHub to the
// read of the repository.
const tailscalePolicyRepoFakeGh = `#!/bin/sh
case "$*" in
  "api -X PUT repos/acme/tailnet-policy/contents/"*) echo "gh $1 $2 $3 $4" >>"$CALLS" ;;
  *) echo "gh $*" >>"$CALLS" ;;
esac
case "$*" in
  "auth status") ;;
  "api -i user") printf 'HTTP/2.0 200 OK\r\nX-Oauth-Scopes: repo, workflow\r\n\r\n{}\n' ;;
  "api repos/acme/tailnet-policy")
    if [ -n "${FAKE_READ_ERROR:-}" ]; then echo "$FAKE_READ_ERROR" >&2; exit 1; fi
    if [ ! -e "$CREATED" ]; then echo "gh: Not Found (HTTP 404)" >&2; exit 1; fi
    echo '{"private":true}' ;;
  "repo create acme/tailnet-policy --private")
    if [ -e "$CREATED" ]; then echo "fake gh: the repository exists" >&2; exit 1; fi
    echo >"$CREATED" ;;
  "api -X PUT repos/acme/tailnet-policy/contents/"*)
    if [ ! -e "$CREATED" ]; then echo "gh: Not Found (HTTP 404)" >&2; exit 1; fi
    printf '%s\t%s\n' "${4#repos/acme/tailnet-policy/contents/}" "${8#content=}" >>"$FILES" ;;
  *) echo "fake gh: unexpected call: $*" >&2; exit 1 ;;
esac
`

// tailscalePolicyRepoReads are the calls of the script before it creates
// the repository. None of them writes.
var tailscalePolicyRepoReads = []string{
	"gh auth status",
	"gh api -i user",
	"gh api repos/acme/tailnet-policy",
}

// tailscalePolicyRepo is a run of scripts/tailscale-policy-repo.sh against
// the fake gh.
type tailscalePolicyRepo struct {
	t       *testing.T
	bin     string
	calls   string
	created string
	files   string
	// readError is what gh prints when the script reads the repository.
	// Empty is the answer of GitHub.
	readError string
}

func newTailscalePolicyRepo(t *testing.T) *tailscalePolicyRepo {
	t.Helper()
	dir := t.TempDir()
	r := &tailscalePolicyRepo{
		t:       t,
		bin:     filepath.Join(dir, "bin"),
		calls:   filepath.Join(dir, "calls"),
		created: filepath.Join(dir, "created"),
		files:   filepath.Join(dir, "files"),
	}
	if err := os.MkdirAll(r.bin, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tool := range tailscalePolicyRepoTools {
		path, err := exec.LookPath(tool)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(path, filepath.Join(r.bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(r.bin, "gh"), []byte(tailscalePolicyRepoFakeGh), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{r.calls, r.files} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

// exists makes the repository exist before the script runs.
func (r *tailscalePolicyRepo) exists() {
	r.t.Helper()
	if err := os.WriteFile(r.created, nil, 0o600); err != nil {
		r.t.Fatal(err)
	}
}

// run runs the script to its end, and returns its output and its exit code.
func (r *tailscalePolicyRepo) run(args ...string) (string, int) {
	r.t.Helper()
	command := exec.Command("./tailscale-policy-repo.sh", args...)
	command.Env = []string{
		"PATH=" + r.bin,
		"TMPDIR=" + r.t.TempDir(),
		"CALLS=" + r.calls,
		"CREATED=" + r.created,
		"FILES=" + r.files,
		"FAKE_READ_ERROR=" + r.readError,
	}
	out, err := command.CombinedOutput()
	return string(out), exitCode(r.t, err)
}

// wantCalls fails when the calls of the fake gh are not exactly these.
func (r *tailscalePolicyRepo) wantCalls(want ...string) {
	r.t.Helper()
	calls, err := os.ReadFile(r.calls)
	if err != nil {
		r.t.Fatal(err)
	}
	var wanted strings.Builder
	for _, call := range want {
		wanted.WriteString(call + "\n")
	}
	if got := string(calls); got != wanted.String() {
		r.t.Errorf("the calls are:\n%swant:\n%s", got, wanted.String())
	}
}

// repoFiles returns the files of the repository, by path.
func (r *tailscalePolicyRepo) repoFiles() map[string]string {
	r.t.Helper()
	data, err := os.ReadFile(r.files)
	if err != nil {
		r.t.Fatal(err)
	}
	files := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		path, encoded, _ := strings.Cut(line, "\t")
		content, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			r.t.Fatalf("the content of %s is not base64: %v", path, err)
		}
		files[path] = string(content)
	}
	return files
}

func readTailscalePolicyWorkflow(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("tailscale/policy-workflow.yml")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestTailscalePolicyRepo_CreatesOnePrivateRepositoryWithTheTwoFiles(t *testing.T) {
	const member = "octocat@github"
	r := newTailscalePolicyRepo(t)
	out, code := r.run("acme/tailnet-policy", "--member", member)
	if code != 0 {
		t.Fatalf("exit code %d, want 0:\n%s", code, out)
	}
	r.wantCalls(append(append([]string{}, tailscalePolicyRepoReads...),
		"gh repo create acme/tailnet-policy --private",
		"gh api -X PUT repos/acme/tailnet-policy/contents/policy.hujson",
		"gh api -X PUT repos/acme/tailnet-policy/contents/.github/workflows/tailscale-policy.yml",
	)...)

	files := r.repoFiles()
	if len(files) != 2 {
		t.Fatalf("the repository holds %d files, want 2: %v", len(files), files)
	}
	policy := files["policy.hujson"]
	if strings.Contains(policy, tailscalePolicyPlaceholder) {
		t.Errorf("policy.hujson still holds the placeholder:\n%s", policy)
	}
	// The login in place of the placeholder is the only change of the template.
	if want := strings.ReplaceAll(readTailscalePolicy(t), tailscalePolicyPlaceholder, member); policy != want {
		t.Errorf("policy.hujson is:\n%swant:\n%s", policy, want)
	}
	if !strings.Contains(policy, `"src": "`+member+`"`) {
		t.Errorf("policy.hujson does not hold the login %s:\n%s", member, policy)
	}
	workflow := readTailscalePolicyWorkflow(t)
	if got := files[".github/workflows/tailscale-policy.yml"]; got != workflow {
		t.Errorf("the workflow of the repository is:\n%swant the template:\n%s", got, workflow)
	}

	// The person who runs the script learns every secret that the workflow reads.
	secrets := tailscalePolicyWorkflowSecret.FindAllStringSubmatch(workflow, -1)
	if len(secrets) == 0 {
		t.Fatal("the workflow reads no secret")
	}
	for _, secret := range secrets {
		if !strings.Contains(out, "  "+secret[1]+" ") {
			t.Errorf("the output does not name the secret %s:\n%s", secret[1], out)
		}
	}
}

func TestTailscalePolicyRepo_ALoginWithCharactersOfSedIsKeptAsItIs(t *testing.T) {
	const member = "a&b|c/d@github"
	r := newTailscalePolicyRepo(t)
	if out, code := r.run("acme/tailnet-policy", "--member", member); code != 0 {
		t.Fatalf("exit code %d, want 0:\n%s", code, out)
	}
	want := strings.ReplaceAll(readTailscalePolicy(t), tailscalePolicyPlaceholder, member)
	if got := r.repoFiles()["policy.hujson"]; got != want {
		t.Errorf("policy.hujson is:\n%swant:\n%s", got, want)
	}
}

func TestTailscalePolicyRepo_ARepositoryThatExistsIsNotChanged(t *testing.T) {
	r := newTailscalePolicyRepo(t)
	r.exists()
	out, code := r.run("acme/tailnet-policy", "--member", "octocat@github")
	if code != 1 {
		t.Fatalf("exit code %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "error: the repository acme/tailnet-policy exists") {
		t.Errorf("the output does not say that the repository exists:\n%s", out)
	}
	r.wantCalls(tailscalePolicyRepoReads...)
	if files := r.repoFiles(); len(files) != 0 {
		t.Errorf("the script wrote files: %v", files)
	}
}

// Only a 404 says that the repository does not exist. After another failure
// the script must not create a repository.
func TestTailscalePolicyRepo_AFailedReadCreatesNothing(t *testing.T) {
	r := newTailscalePolicyRepo(t)
	r.readError = "gh: Server Error (HTTP 502)"
	out, code := r.run("acme/tailnet-policy", "--member", "octocat@github")
	if code != 1 {
		t.Fatalf("exit code %d, want 1:\n%s", code, out)
	}
	if !strings.Contains(out, "error: cannot read whether acme/tailnet-policy exists") {
		t.Errorf("the output does not say that the read failed:\n%s", out)
	}
	r.wantCalls(tailscalePolicyRepoReads...)
}

func TestTailscalePolicyRepo_AWrongCallMakesNoCall(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no --member", []string{"acme/tailnet-policy"}, "Usage:"},
		{"--member with no login", []string{"acme/tailnet-policy", "--member"}, "Usage:"},
		{"an empty login", []string{"acme/tailnet-policy", "--member", ""}, "Usage:"},
		{"no repository", []string{"--member", "octocat@github"}, "Usage:"},
		{"no argument", nil, "Usage:"},
		{"an unknown option", []string{"acme/tailnet-policy", "--member", "octocat@github", "--public"}, "unknown option: --public"},
		{"a repository with no owner", []string{"tailnet-policy", "--member", "octocat@github"}, "must be <owner>/<repo>"},
		{"a login that ends the JSON string", []string{"acme/tailnet-policy", "--member", `a"], "accept": ["*:*`}, "must not contain a double quote"},
		{"the placeholder as the login", []string{"acme/tailnet-policy", "--member", tailscalePolicyPlaceholder}, "must not be the placeholder"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newTailscalePolicyRepo(t)
			out, code := r.run(c.args...)
			if code != 2 {
				t.Fatalf("exit code %d, want 2:\n%s", code, out)
			}
			if !strings.Contains(out, c.want) {
				t.Errorf("the output does not hold %q:\n%s", c.want, out)
			}
			r.wantCalls()
		})
	}
}

// tailscalePolicyAction is the GitOps action of Tailscale.
const tailscalePolicyAction = "tailscale/gitops-acl-action"

// tailscalePolicyCredentials are the inputs of the action that hold a
// credential or the name of the tailnet. The repository of cumin-works is
// public, so the template gives each of them only as a secret.
var tailscalePolicyCredentials = []string{"tailnet", "api-key", "oauth-client-id", "oauth-secret", "audience"}

var (
	tailscalePolicyWorkflowSecret = regexp.MustCompile(`\$\{\{ secrets\.([A-Z][A-Z0-9_]*) \}\}`)
	tailscalePolicyWorkflowPin    = regexp.MustCompile(`^[^@\s]+@([0-9a-f]{40}|v[0-9]+\.[0-9]+\.[0-9]+)$`)
	tailscalePolicyWorkflowKey    = regexp.MustCompile(`^(?:- )?([A-Za-z_-]+):\s*(.*)$`)
)

// checkTailscalePolicyWorkflow returns what is wrong with the workflow of
// the policy repository: it must run on pull_request and on push, test the
// policy on a pull request, apply it on a push to the default branch, read
// every credential from secrets.*, and pin every action. It reads the file
// line by line, in the layout of the template.
func checkTailscalePolicyWorkflow(data string) error {
	type step struct{ condition, uses, action string }
	var steps []step
	events := map[string]bool{}
	section := ""
	for _, raw := range strings.Split(data, "\n") {
		line, _, _ := strings.Cut(raw, " #")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		text := strings.TrimSpace(line)
		if indent == 0 {
			section = strings.TrimSuffix(text, ":")
			continue
		}
		if section == "on" && indent == 2 {
			events[strings.TrimSuffix(text, ":")] = true
			continue
		}
		if section != "jobs" {
			continue
		}
		if strings.HasPrefix(text, "- ") {
			steps = append(steps, step{})
		}
		match := tailscalePolicyWorkflowKey.FindStringSubmatch(text)
		if match == nil || len(steps) == 0 {
			continue
		}
		current := &steps[len(steps)-1]
		key, value := match[1], match[2]
		switch key {
		case "if":
			current.condition = value
		case "uses":
			if !tailscalePolicyWorkflowPin.MatchString(value) {
				return fmt.Errorf("the action %q has no pin to a commit or to a released version", value)
			}
			current.uses = value
		case "action":
			current.action = value
		}
		for _, credential := range tailscalePolicyCredentials {
			if key == credential && tailscalePolicyWorkflowSecret.FindString(value) != value {
				return fmt.Errorf("the input %s is %q, want only a value of secrets.*", key, value)
			}
		}
	}
	for _, event := range []string{"pull_request", "push"} {
		if !events[event] {
			return fmt.Errorf("the workflow does not run on %s", event)
		}
	}
	var tests, applies bool
	for _, s := range steps {
		if !strings.HasPrefix(s.uses, tailscalePolicyAction+"@") {
			continue
		}
		switch {
		case s.action == "test" && s.condition == "github.event_name == 'pull_request'":
			tests = true
		case s.action == "apply" && s.condition == "github.event_name == 'push' && github.ref_name == github.event.repository.default_branch":
			applies = true
		default:
			return fmt.Errorf("a step runs the action %q on the condition %q, want only the test on pull_request and the apply on a push to the default branch", s.action, s.condition)
		}
	}
	if !tests {
		return fmt.Errorf("the workflow lacks the test of the policy on pull_request")
	}
	if !applies {
		return fmt.Errorf("the workflow lacks the apply of the policy on push")
	}
	return nil
}

func TestTailscalePolicyRepoWorkflowTestsOnPullRequestAndAppliesOnPush(t *testing.T) {
	if err := checkTailscalePolicyWorkflow(readTailscalePolicyWorkflow(t)); err != nil {
		t.Fatal(err)
	}
}

// Each case changes one place of the template into a workflow that the
// check must refuse.
func TestTailscalePolicyRepoWorkflowCheckRefusesAWeakerWorkflow(t *testing.T) {
	const (
		onPullRequest = "  pull_request:\n"
		onPush        = "  push:\n"
		testStep      = "if: github.event_name == 'pull_request'"
		applyStep     = "if: github.event_name == 'push' && github.ref_name == github.event.repository.default_branch"
		actionPin     = tailscalePolicyAction + "@5a4a17f5708e9bf96f4ee915a95e9f83c2eebe1a"
	)
	cases := []struct {
		name     string
		old, new string
		want     string
	}{
		{"no run on pull_request", onPullRequest, "", "does not run on pull_request"},
		{"no run on push", onPush, "", "does not run on push"},
		{"no test step", "action: test", "action: apply", "want only the test on pull_request"},
		{"no apply step", "action: apply", "action: test", "want only the test on pull_request"},
		{"the test runs on push", testStep, "if: github.event_name == 'push'", "want only the test on pull_request"},
		{"the apply runs on pull_request", applyStep, testStep, "want only the test on pull_request"},
		{"the apply runs on a push to any branch", applyStep, "if: github.event_name == 'push'", "want only the test on pull_request"},
		{"the apply has no condition", "        " + applyStep + "\n", "", "want only the test on pull_request"},
		{"an OAuth secret in the file", "oauth-secret: ${{ secrets.TS_OAUTH_SECRET }}", "oauth-secret: tskey-client-example", "the input oauth-secret"},
		{"an OAuth client ID in the file", "oauth-client-id: ${{ secrets.TS_OAUTH_ID }}", "oauth-client-id: example-id", "the input oauth-client-id"},
		{"an API key in the file", "oauth-secret: ${{ secrets.TS_OAUTH_SECRET }}", "api-key: tskey-api-example", "the input api-key"},
		{"an API key from a variable", "oauth-secret: ${{ secrets.TS_OAUTH_SECRET }}", "api-key: ${{ vars.TS_API_KEY }}", "the input api-key"},
		{"a secret with other text", "oauth-secret: ${{ secrets.TS_OAUTH_SECRET }}", "oauth-secret: example-${{ secrets.TS_OAUTH_SECRET }}", "the input oauth-secret"},
		{"the name of the tailnet in the file", "tailnet: ${{ secrets.TS_TAILNET }}", "tailnet: example.ts.net", "the input tailnet"},
		{"the action on a major version", actionPin, tailscalePolicyAction + "@v1", "has no pin"},
		{"the action on a branch", actionPin, tailscalePolicyAction + "@main", "has no pin"},
	}
	template := readTailscalePolicyWorkflow(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(template, c.old) {
				t.Fatalf("the template does not hold %s", c.old)
			}
			err := checkTailscalePolicyWorkflow(strings.Replace(template, c.old, c.new, 1))
			if err == nil {
				t.Fatal("the check accepts the workflow")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("the check refuses the workflow for another reason: %v", err)
			}
		})
	}
}
