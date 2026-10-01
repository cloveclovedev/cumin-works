package main

// The live scenario E2E-1: one requirement issue goes from the Owner's ready
// to the acceptance check on the sandbox, with cumin running under launchd.
// It runs only with CUMIN_LIVE=1. docs/ja/development/live-tests.md says how
// to prepare the Host and how to run it.
//
// The test plays the Owner: it writes the requirement issue, settles the risk
// labels, adds cumin/status/ready, and approves the risk/medium pull request.
// It does this with the `gh` login of the Host, which stays in the `gh`
// processes of this test and never reaches cumin or an agent. Everything
// else is the work of cumin: the test never changes another status label,
// never merges, and never comments.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cloveclovedev/cumin-works/internal/core/config"
	"github.com/cloveclovedev/cumin-works/internal/setup"
)

const (
	e2eStatusPrefix = "cumin/status/"
	e2eRiskPrefix   = "risk/"
	e2ePollEvery    = 20 * time.Second
	e2eSplitLimit   = 45 * time.Minute
	e2eWorkLimit    = 150 * time.Minute
	e2eAcceptLimit  = 45 * time.Minute
)

// e2eRequirement is the requirement issue of the scenario. Each pull request
// leaves one line under "Follow-up", so that the follow-up note is written
// for both.
const e2eRequirement = `## Goal
The sandbox gains two small pages about the live scenario E2E-1 of cumin-works, so that one run of cumin crosses every step from the split to the acceptance check.

## Why
No earlier live scenario crossed every hand-over in one run. This requirement is small on purpose.

## Requirements
- [ ] The file ` + "`live/e2e-%[1]s/short.md`" + ` exists. It holds a heading and this one sentence: "The live scenario E2E-1 follows one requirement issue from ready to acceptance."
- [ ] The file ` + "`live/e2e-%[1]s/labels.md`" + ` exists. It holds a heading and a Markdown table with one row for each of these labels, and the meaning of each label in one English sentence: ` + "`cumin/status/ready`, `cumin/status/planning`, `cumin/status/implementing`, `cumin/status/awaiting-checks`, `cumin/status/reviewing`, `cumin/status/awaiting-owner-review`, `cumin/status/awaiting-owner-decision`" + `.
- [ ] The description of each pull request holds one line under "Follow-up": "Link <the file of this pull request> from README.md."

## Out of scope
- Every other file. ` + "`README.md`" + ` does not change.

## Constraints
- Exactly two implementation issues, one for each file. Neither waits for the other.
- No file under a protected path changes.

## Open questions
None
`

type e2e struct {
	repo    string
	owner   string // the gh login that plays the Owner
	runID   string
	logPath string
	logFrom int64
	rows    [][2]string // step, evidence
}

func (e *e2e) record(step, evidence string) {
	e.rows = append(e.rows, [2]string{step, strings.ReplaceAll(evidence, "|", "\\|")})
}

func (e *e2e) table() string {
	var b strings.Builder
	b.WriteString("| # | Step | Evidence |\n|---|---|---|\n")
	for i, row := range e.rows {
		fmt.Fprintf(&b, "| %d | %s | %s |\n", i+1, row[0], row[1])
	}
	return b.String()
}

// gh runs the GitHub CLI with the login of the Host.
func (e *e2e) gh(t *testing.T, args ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("gh", args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("gh %s: %v: %s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

// api reads one REST path of the sandbox into out. A paginated list comes
// back as one array (--slurp joins the pages).
func (e *e2e) api(t *testing.T, path string, out any) {
	t.Helper()
	data := e.gh(t, "api", "--paginate", "--slurp", "repos/"+e.repo+"/"+path)
	var pages []json.RawMessage
	if err := json.Unmarshal([]byte(data), &pages); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if len(pages) == 1 {
		if err := json.Unmarshal(pages[0], out); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		return
	}
	// More than one page of a list: join the arrays.
	var all []json.RawMessage
	for _, page := range pages {
		var items []json.RawMessage
		if err := json.Unmarshal(page, &items); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		all = append(all, items...)
	}
	joined, _ := json.Marshal(all)
	if err := json.Unmarshal(joined, out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

type e2eUser struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

// isApp reports whether the account is the bot of the named cumin App. The
// name of an App ends with "cumin-<app>" (setup-guide.md), so the test holds
// no App name of one Organization.
func (u e2eUser) isApp(app string) bool {
	return u.Type == "Bot" && strings.HasSuffix(u.Login, "cumin-"+app+"[bot]")
}

type e2eIssue struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	State       string `json:"state"`
	StateReason string `json:"state_reason"`
	ClosedAt    string `json:"closed_at"`
	User        e2eUser
	Labels      []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

func (i e2eIssue) labels(prefix string) []string {
	var names []string
	for _, l := range i.Labels {
		if strings.HasPrefix(l.Name, prefix) {
			names = append(names, l.Name)
		}
	}
	return names
}

func (i e2eIssue) status() string { return strings.Join(i.labels(e2eStatusPrefix), ",") }

func (e *e2e) issue(t *testing.T, number int) e2eIssue {
	t.Helper()
	var issue e2eIssue
	e.api(t, fmt.Sprintf("issues/%d", number), &issue)
	return issue
}

type e2eLabelEvent struct {
	Event     string  `json:"event"`
	CreatedAt string  `json:"created_at"`
	Actor     e2eUser `json:"actor"`
	Label     struct {
		Name string `json:"name"`
	} `json:"label"`
}

// statusEvents returns the "labeled" events of the status labels of an
// issue, in order: the states that the issue went through, and who set each.
func (e *e2e) statusEvents(t *testing.T, number int) []e2eLabelEvent {
	t.Helper()
	var events, status []e2eLabelEvent
	e.api(t, fmt.Sprintf("issues/%d/events?per_page=100", number), &events)
	for _, ev := range events {
		if ev.Event == "labeled" && strings.HasPrefix(ev.Label.Name, e2eStatusPrefix) {
			status = append(status, ev)
		}
	}
	return status
}

// checkStatusPath checks that the states start with the wanted ones, that
// the Owner set every ready, and that cumin-core set every other state.
func (e *e2e) checkStatusPath(t *testing.T, number int, want []string, last string) string {
	t.Helper()
	var path []string
	for _, ev := range e.statusEvents(t, number) {
		name := strings.TrimPrefix(ev.Label.Name, e2eStatusPrefix)
		path = append(path, name)
		if name == "ready" {
			if ev.Actor.Login != e.owner {
				t.Errorf("issue #%d: %s set ready, want the Owner", number, ev.Actor.Login)
			}
		} else if !ev.Actor.isApp("core") {
			t.Errorf("issue #%d: %s set %s, want the cumin-core App", number, ev.Actor.Login, name)
		}
	}
	joined := strings.Join(path, " > ")
	if len(path) < len(want) || !slices.Equal(path[:len(want)], want) {
		t.Errorf("issue #%d: states %s, want them to start with %s", number, joined, strings.Join(want, " > "))
	}
	if len(path) > 0 && path[len(path)-1] != last {
		t.Errorf("issue #%d: last state %s, want %s", number, path[len(path)-1], last)
	}
	return joined
}

type e2eComment struct {
	Body      string  `json:"body"`
	CreatedAt string  `json:"created_at"`
	HTMLURL   string  `json:"html_url"`
	User      e2eUser `json:"user"`
}

func (e *e2e) comments(t *testing.T, number int) []e2eComment {
	t.Helper()
	var comments []e2eComment
	e.api(t, fmt.Sprintf("issues/%d/comments?per_page=100", number), &comments)
	return comments
}

// commentsOf returns the comments of one App that start with the heading.
func commentsOf(comments []e2eComment, app, heading string) []e2eComment {
	var found []e2eComment
	for _, c := range comments {
		if c.User.isApp(app) && strings.HasPrefix(c.Body, heading) {
			found = append(found, c)
		}
	}
	return found
}

type e2ePull struct {
	Number         int     `json:"number"`
	Body           string  `json:"body"`
	Merged         bool    `json:"merged"`
	MergedAt       string  `json:"merged_at"`
	MergedBy       e2eUser `json:"merged_by"`
	MergeCommitSHA string  `json:"merge_commit_sha"`
	User           e2eUser `json:"user"`
	Head           struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
}

type e2eReview struct {
	State       string  `json:"state"`
	CommitID    string  `json:"commit_id"`
	SubmittedAt string  `json:"submitted_at"`
	User        e2eUser `json:"user"`
}

// linkedPulls returns the numbers of the pull requests that close an issue,
// open or not (GraphQL closedByPullRequestsReferences).
func (e *e2e) linkedPulls(t *testing.T, issue int) []int {
	t.Helper()
	owner, name, _ := strings.Cut(e.repo, "/")
	query := `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    issue(number: $number) { closedByPullRequestsReferences(first: 10, includeClosedPrs: true) { nodes { number } } }
  }
}`
	out := e.gh(t, "api", "graphql", "-f", "query="+query, "-f", "owner="+owner, "-f", "name="+name, "-F", "number="+strconv.Itoa(issue),
		"--jq", ".data.repository.issue.closedByPullRequestsReferences.nodes[].number")
	var numbers []int
	for _, field := range strings.Fields(out) {
		n, err := strconv.Atoi(field)
		if err != nil {
			t.Fatalf("read the pull requests of issue #%d: %q", issue, out)
		}
		numbers = append(numbers, n)
	}
	return numbers
}

// logLine is one line of the log of cumin, as far as the test reads it.
type logLine struct {
	Level            string `json:"level"`
	Msg              string `json:"msg"`
	Repository       string `json:"repository"`
	Issue            int    `json:"issue"`
	RequirementIssue int    `json:"requirement_issue"`
	Row              string `json:"row"`
	raw              string
}

// logLines returns the lines that cumin wrote for the sandbox since the
// start of the test.
func (e *e2e) logLines(t *testing.T) []logLine {
	t.Helper()
	f, err := os.Open(e.logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Seek(e.logFrom, 0); err != nil {
		t.Fatal(err)
	}
	var lines []logLine
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<24)
	for scanner.Scan() {
		line := logLine{raw: scanner.Text()}
		if json.Unmarshal(scanner.Bytes(), &line) != nil {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

// checkLogOrder checks that cumin logged the messages for the issue in this
// order, and returns them for the record.
func checkLogOrder(t *testing.T, lines []logLine, repo string, issue int, messages ...string) string {
	t.Helper()
	next := 0
	for _, line := range lines {
		if next < len(messages) && line.Repository == repo && (line.Issue == issue || line.RequirementIssue == issue) && line.Msg == messages[next] {
			next++
		}
	}
	if next < len(messages) {
		t.Errorf("issue #%d: the log has no line %q after the %d lines before it", issue, messages[next], next)
	}
	return "log: `" + strings.Join(messages, "`, `") + "`"
}

// launchdPID returns the process of the LaunchAgent of cumin, or 0 when the
// job is not running.
func launchdPID(t *testing.T) int {
	t.Helper()
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), setup.LaunchAgentLabel)
	out, err := exec.Command("launchctl", "print", target).Output()
	if err != nil || !regexp.MustCompile(`(?m)^\s*state = running$`).Match(out) {
		return 0
	}
	match := regexp.MustCompile(`(?m)^\s*pid = (\d+)$`).FindSubmatch(out)
	if match == nil {
		return 0
	}
	pid, _ := strconv.Atoi(string(match[1]))
	return pid
}

// newE2E checks everything that must hold before the test creates an issue.
func newE2E(t *testing.T) *e2e {
	t.Helper()
	if os.Getenv("CUMIN_LIVE") != "1" {
		t.Skip("live checks run only with CUMIN_LIVE=1")
	}
	e := &e2e{repo: os.Getenv("CUMIN_LIVE_REPO"), runID: time.Now().UTC().Format("20060102-150405")}
	if owner, name, ok := strings.Cut(e.repo, "/"); !ok || owner == "" || name == "" {
		t.Fatal("set CUMIN_LIVE_REPO to <owner>/<repo> of the sandbox repository")
	}

	// cumin runs under launchd, and its settings name the sandbox.
	if launchdPID(t) == 0 {
		t.Fatalf("the LaunchAgent %s is not running. Start it by docs/ja/development/live-tests.md (E2E-1)", setup.LaunchAgentLabel)
	}
	path, err := config.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	settings, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(settings.Repositories, func(r config.Repository) bool { return strings.EqualFold(r.String(), e.repo) }) {
		t.Fatalf("the Host settings do not list %s under repositories", e.repo)
	}
	state, err := config.DefaultStateDir()
	if err != nil {
		t.Fatal(err)
	}
	e.logPath = filepath.Join(state, "cumin.log")
	info, err := os.Stat(e.logPath)
	if err != nil {
		t.Fatal(err)
	}
	e.logFrom = info.Size()

	// The gh login is the Owner: a person with write permission or more.
	var user e2eUser
	if err := json.Unmarshal([]byte(e.gh(t, "api", "user")), &user); err != nil {
		t.Fatal(err)
	}
	e.owner = user.Login
	permission := e.gh(t, "api", "repos/"+e.repo+"/collaborators/"+user.Login+"/permission", "--jq", ".permission")
	if user.Type != "User" || (permission != "admin" && permission != "write") {
		t.Fatalf("the gh login must be a person with write permission on %s: type %s, permission %s", e.repo, user.Type, permission)
	}

	// No open issue holds a status that cumin acts on or counts.
	var open []e2eIssue
	e.api(t, "issues?state=open&per_page=100", &open)
	for _, issue := range open {
		switch status := issue.status(); status {
		case "", e2eStatusPrefix + "awaiting-owner-review", e2eStatusPrefix + "awaiting-owner-decision":
		default:
			t.Fatalf("the sandbox holds issue #%d with %s. Close it or remove the label first", issue.Number, status)
		}
	}
	return e
}

// waitFor polls until done reports true. An issue that cumin hands back to
// the Owner with awaiting-owner-decision ends the test: cumin could not go
// on, which is a defect to record.
func (e *e2e) waitFor(t *testing.T, what string, limit time.Duration, issues []int, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for {
		if done() {
			return
		}
		for _, number := range issues {
			if issue := e.issue(t, number); strings.Contains(issue.status(), "awaiting-owner-decision") {
				t.Fatalf("%s: cumin stopped issue #%d for the Owner (awaiting-owner-decision)", what, number)
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: not reached within %s", what, limit)
		}
		time.Sleep(e2ePollEvery)
	}
}

func TestLiveE2E(t *testing.T) {
	e := newE2E(t)
	pid := launchdPID(t)
	defer func() { t.Logf("E2E-1 evidence (run %s):\n\n%s", e.runID, e.table()) }()

	// The Owner writes the requirement issue and says that it is ready.
	url := e.gh(t, "issue", "create", "--repo", e.repo, "--title", "Describe the live scenario E2E-1 ("+e.runID+")",
		"--body", fmt.Sprintf(e2eRequirement, e.runID), "--label", "cumin/type/requirement", "--label", "cumin/status/ready")
	requirement, err := strconv.Atoi(url[strings.LastIndex(url, "/")+1:])
	if err != nil {
		t.Fatalf("gh issue create answered %q", url)
	}
	t.Logf("requirement issue #%d", requirement)
	e.record("The Owner's ready on the requirement issue", fmt.Sprintf("issue #%d, created with `cumin/status/ready`", requirement))

	// The split: start (R1) and verification (R2).
	e.waitFor(t, "the split", e2eSplitLimit, []int{requirement}, func() bool {
		return e.issue(t, requirement).status() == e2eStatusPrefix+"awaiting-owner-review"
	})
	path := e.checkStatusPath(t, requirement, []string{"ready", "planning", "awaiting-owner-review"}, "awaiting-owner-review")
	var subs []e2eIssue
	e.api(t, fmt.Sprintf("issues/%d/sub_issues?per_page=100", requirement), &subs)
	if len(subs) != 2 {
		t.Fatalf("the Planner made %d sub-issues, want 2", len(subs))
	}
	slices.SortFunc(subs, func(a, b e2eIssue) int { return a.Number - b.Number })
	var planned []string
	for _, sub := range subs {
		if !sub.User.isApp("planner") {
			t.Errorf("sub-issue #%d: author %s, want the Planner App", sub.Number, sub.User.Login)
		}
		risk := sub.labels(e2eRiskPrefix)
		if len(risk) != 1 {
			t.Fatalf("sub-issue #%d has the risk labels %v, want exactly one", sub.Number, risk)
		}
		planned = append(planned, fmt.Sprintf("#%d %s", sub.Number, risk[0]))
	}
	plans := commentsOf(e.comments(t, requirement), "planner", "## Plan for approval")
	if len(plans) != 1 {
		t.Fatalf("the requirement issue has %d plan summaries of the Planner, want 1", len(plans))
	}
	e.record("Start the split, verify the split (R1, R2)", fmt.Sprintf("states of #%d: %s; sub-issues of the Planner: %s; plan summary %s",
		requirement, path, strings.Join(planned, ", "), plans[0].HTMLURL))

	// The Owner settles the risk: one risk/low and one risk/medium. The
	// choice of the Planner stays when it already is that.
	low, medium := subs[1].Number, subs[0].Number
	if subs[0].labels(e2eRiskPrefix)[0] == "risk/low" && subs[1].labels(e2eRiskPrefix)[0] != "risk/low" {
		low, medium = subs[0].Number, subs[1].Number
	}
	for _, sub := range subs {
		want := "risk/medium"
		if sub.Number == low {
			want = "risk/low"
		}
		if have := sub.labels(e2eRiskPrefix)[0]; have != want {
			e.gh(t, "issue", "edit", strconv.Itoa(sub.Number), "--repo", e.repo, "--remove-label", have, "--add-label", want)
		}
	}
	for _, sub := range subs {
		e.gh(t, "issue", "edit", strconv.Itoa(sub.Number), "--repo", e.repo, "--add-label", "cumin/status/ready")
	}
	e.record("The Owner's risk and ready on the sub-issues", fmt.Sprintf("#%d `risk/low`, #%d `risk/medium`, both `cumin/status/ready`", low, medium))

	// The work on both sub-issues. The only step of the Owner is the
	// approval of the risk/medium pull request when cumin asks for it (I7).
	var approvedAt time.Time
	all := []int{requirement, low, medium}
	e.waitFor(t, "the merge of both pull requests", e2eWorkLimit, all, func() bool {
		if approvedAt.IsZero() && e.issue(t, medium).status() == e2eStatusPrefix+"awaiting-owner-review" {
			pulls := e.linkedPulls(t, medium)
			if len(pulls) != 1 {
				t.Fatalf("issue #%d has the pull requests %v, want exactly one", medium, pulls)
			}
			var pull e2ePull
			e.api(t, fmt.Sprintf("pulls/%d", pulls[0]), &pull)
			if pull.Merged {
				t.Fatalf("pull request #%d (risk/medium) was merged before the Owner approved it", pull.Number)
			}
			e.gh(t, "pr", "review", strconv.Itoa(pull.Number), "--repo", e.repo, "--approve")
			approvedAt = time.Now()
			e.record("The Owner's approval of the risk/medium pull request", fmt.Sprintf("pull request #%d, not merged while #%d waited in `awaiting-owner-review`", pull.Number, medium))
		}
		return e.issue(t, low).State == "closed" && e.issue(t, medium).State == "closed"
	})

	pulls := map[int]e2ePull{}
	var lastClose time.Time
	for _, number := range []int{low, medium} {
		last := "reviewing"
		if number == medium {
			last = "awaiting-owner-review"
		}
		path := e.checkStatusPath(t, number, []string{"ready", "implementing", "awaiting-checks", "reviewing"}, last)
		linked := e.linkedPulls(t, number)
		if len(linked) != 1 {
			t.Fatalf("issue #%d has the pull requests %v, want exactly one", number, linked)
		}
		var pull e2ePull
		e.api(t, fmt.Sprintf("pulls/%d", linked[0]), &pull)
		pulls[number] = pull
		if !pull.User.isApp("implementer") {
			t.Errorf("pull request #%d: author %s, want the Implementer App", pull.Number, pull.User.Login)
		}
		if !strings.HasPrefix(pull.Head.Ref, fmt.Sprintf("cumin/%d-", number)) {
			t.Errorf("pull request #%d: branch %s, want cumin/%d-...", pull.Number, pull.Head.Ref, number)
		}
		if !strings.Contains(pull.Body, fmt.Sprintf("Closes #%d", number)) {
			t.Errorf("pull request #%d: the description has no \"Closes #%d\"", pull.Number, number)
		}
		var reviews []e2eReview
		e.api(t, fmt.Sprintf("pulls/%d/reviews?per_page=100", pull.Number), &reviews)
		var reviewer, owner *e2eReview
		for i, r := range reviews {
			if r.User.isApp("reviewer") {
				reviewer = &reviews[i]
			}
			if r.User.Login == e.owner {
				owner = &reviews[i]
			}
		}
		if reviewer == nil || reviewer.State != "APPROVED" || reviewer.CommitID != pull.Head.SHA {
			t.Errorf("pull request #%d: the last review of the Reviewer App is not APPROVED on the head commit: %+v", pull.Number, reviewer)
		}
		if !pull.Merged || !pull.MergedBy.isApp("core") {
			t.Fatalf("pull request #%d: merged %v by %s, want a merge by the cumin-core App", pull.Number, pull.Merged, pull.MergedBy.Login)
		}
		if parents := e.gh(t, "api", "repos/"+e.repo+"/commits/"+pull.MergeCommitSHA, "--jq", ".parents | length"); parents != "1" {
			t.Errorf("pull request #%d: the merge commit has %s parents, want 1 (squash)", pull.Number, parents)
		}
		issue := e.issue(t, number)
		if issue.StateReason != "completed" {
			t.Errorf("issue #%d: closed as %s, want completed", number, issue.StateReason)
		}
		if closed, _ := time.Parse(time.RFC3339, issue.ClosedAt); closed.After(lastClose) {
			lastClose = closed
		}
		evidence := fmt.Sprintf("states of #%d: %s; pull request #%d by the Implementer App on `%s`; Reviewer App APPROVED the head commit; merged by cumin-core at %s (squash); issue closed as completed at %s",
			number, path, pull.Number, pull.Head.Ref, pull.MergedAt, issue.ClosedAt)
		if number == medium {
			if owner == nil || owner.State != "APPROVED" || owner.CommitID != pull.Head.SHA || owner.SubmittedAt > pull.MergedAt {
				t.Errorf("pull request #%d: the merge at %s does not follow an approval of the Owner on the head commit: %+v", pull.Number, pull.MergedAt, owner)
			} else {
				evidence += "; the Owner approved at " + owner.SubmittedAt
			}
			e.record("risk/medium: claim, verify done, review, ask the Owner, merge after the approval, close (I1, I2, I3, I7, I12)", evidence)
		} else {
			e.record("risk/low: claim, verify done, review, merge, close (I1, I2, I3, I6)", evidence)
		}
	}

	// The acceptance check (R4) and the wait for the Owner (R7).
	e.waitFor(t, "the acceptance check", e2eAcceptLimit, []int{requirement}, func() bool {
		return e.issue(t, requirement).status() == e2eStatusPrefix+"awaiting-owner-review" &&
			len(commentsOf(e.comments(t, requirement), "planner", "## Acceptance check")) > 0
	})
	comments := e.comments(t, requirement)
	checks := commentsOf(comments, "planner", "## Acceptance check")
	if len(checks) != 1 {
		t.Fatalf("the requirement issue has %d acceptance checks, want 1", len(checks))
	}
	if checked, _ := time.Parse(time.RFC3339, checks[0].CreatedAt); !checked.After(lastClose) {
		t.Errorf("the acceptance check at %s is not after the last close at %s", checks[0].CreatedAt, lastClose.Format(time.RFC3339))
	}

	// The follow-up notes (I9): one for each pull request, before the
	// acceptance check.
	var notes []string
	for _, number := range []int{low, medium} {
		marker := fmt.Sprintf("<!-- cumin:follow-up-note issue=%d pull-request=%d ", number, pulls[number].Number)
		var found []e2eComment
		for _, c := range commentsOf(comments, "core", fmt.Sprintf("## Follow-up from #%d ", pulls[number].Number)) {
			if strings.Contains(c.Body, marker) {
				found = append(found, c)
			}
		}
		if len(found) != 1 {
			t.Errorf("pull request #%d has %d follow-up notes on the requirement issue, want 1", pulls[number].Number, len(found))
			continue
		}
		if found[0].CreatedAt > checks[0].CreatedAt {
			t.Errorf("the follow-up note of #%d at %s is after the acceptance check at %s", pulls[number].Number, found[0].CreatedAt, checks[0].CreatedAt)
		}
		notes = append(notes, found[0].HTMLURL)
	}
	e.record("Follow-up notes (I9)", "one note of cumin-core for each pull request, before the acceptance check: "+strings.Join(notes, ", "))

	path = e.checkStatusPath(t, requirement, []string{"ready", "planning", "awaiting-owner-review", "implementing", "awaiting-owner-review"}, "awaiting-owner-review")
	events := e.statusEvents(t, requirement)
	if last := events[len(events)-1]; last.CreatedAt < checks[0].CreatedAt {
		t.Errorf("the requirement issue went to awaiting-owner-review at %s, before the acceptance check at %s", last.CreatedAt, checks[0].CreatedAt)
	}
	e.record("Requirement to implementing, acceptance check, wait for the acceptance (R3, R4, R7)",
		fmt.Sprintf("states of #%d: %s; acceptance check of the Planner App %s", requirement, path, checks[0].HTMLURL))

	// The log of cumin: the steps in order, the notifications, and nothing
	// secret. The notification of R7 follows the label, so wait for it.
	notified := func(lines []logLine, row string, issue int) int {
		count := 0
		for _, line := range lines {
			if line.Msg == "the Owner was notified" && line.Repository == e.repo && line.Row == row && line.Issue == issue {
				count++
			}
		}
		return count
	}
	e.waitFor(t, "the notification of the acceptance in the log", 3*time.Minute, nil, func() bool {
		return notified(e.logLines(t), "R7", requirement) > 0
	})
	lines := e.logLines(t)
	e.record("Log of the requirement issue", checkLogOrder(t, lines, e.repo, requirement,
		"R1: moved the requirement issue to planning", "R1: requested the Planner", "R2: the split waits for the Owner",
		"R3: the sub-issues of the requirement issue are in progress", "I9: wrote the follow-up note", "I9: wrote the follow-up note",
		"R4: requested the Planner", "R7: the requirement issue waits for the acceptance of the Owner"))
	e.record("Log of the risk/low sub-issue", checkLogOrder(t, lines, e.repo, low,
		"I1: claimed the issue", "I2: verified the pull request", "I3: the pull request is ready for review",
		"I3: the Reviewer approved the head commit", "I6: merged the pull request"))
	e.record("Log of the risk/medium sub-issue", checkLogOrder(t, lines, e.repo, medium,
		"I1: claimed the issue", "I2: verified the pull request", "I3: the pull request is ready for review",
		"I3: the Reviewer approved the head commit", "I7: the merge waits for the Owner",
		"I12: the Owner approved the head commit", "I12: merged the pull request"))
	for _, n := range []struct {
		row   string
		issue int
	}{{"R2", requirement}, {"I7", medium}, {"R7", requirement}} {
		if count := notified(lines, n.row, n.issue); count != 1 {
			t.Errorf("the log has %d notifications of %s for issue #%d, want 1", count, n.row, n.issue)
		}
	}
	e.record("Notifications", "log: `the Owner was notified` once each for the split (R2), the merge decision (I7), and the acceptance (R7)")

	secret := regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}|github_pat_|/api/webhooks/|PRIVATE KEY|utilization|agent quota usage`)
	for _, line := range lines {
		if found := secret.FindString(line.raw); found != "" {
			t.Errorf("the log holds %q in a line with the message %q", found, line.Msg)
		}
	}
	e.record("Nothing secret in the log", fmt.Sprintf("%d lines since the start: no token, no key, no webhook address, no usage number", len(lines)))

	if now := launchdPID(t); now != pid {
		t.Errorf("the process of the LaunchAgent changed during the run: %d, then %d", pid, now)
	}
	e.record("cumin under launchd for the whole run", fmt.Sprintf("the LaunchAgent `%s` kept one process from the start to the end", setup.LaunchAgentLabel))
}
