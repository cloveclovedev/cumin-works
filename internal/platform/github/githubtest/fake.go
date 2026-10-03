// Package githubtest is a fake GitHub for the acceptance tests of cumin. The
// real client of internal/platform/github talks to it over HTTP. It has only
// the endpoints that a test needs, and keeps its state in memory.
//
// docs/ja/designs/cumin-core.md, topic "Two layers of tests".
package githubtest

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Token is the installation token that the fake accepts. It is also the
// token that the fake creates for an App, so that a test can see it reach
// the place that uses it.
const Token = "ghs_fakeInstallationToken"

// App is the one GitHub App that the fake knows. The endpoints of an agent
// start (GET /app, GET /users/<slug>[bot]) answer from it.
type App struct {
	Slug string
	// Owner is the login of the account that owns the App.
	Owner string
	// BotID is the id of the bot user "<slug>[bot]".
	BotID int64
}

// RequirementLabel marks a requirement issue, as in issue-states.md.
const RequirementLabel = "cumin/type/requirement"

// Issue is one issue of the fake repository.
type Issue struct {
	Number int
	Title  string
	Closed bool
	// ClosedAt is when a closed issue closed. The zero time answers null.
	ClosedAt time.Time
	Labels   []string
	// Parent is the number of the parent issue, or 0.
	Parent int
	// BlockedBy holds the numbers of the issues that block this one.
	BlockedBy []int
	// StateReason is the reason of the last close through the REST API.
	StateReason string
	// LabelEvents are the times at which labels were added, oldest first,
	// as GitHub records them in the timeline. A test adds the events of the
	// past; the fake adds one for each label that "Set labels" adds.
	LabelEvents []LabelEvent
}

// LabelEvent is one LabeledEvent of the timeline of an issue. Actor is the
// login of the account that added the label and ActorType its type in
// GraphQL ("User", "Bot"); an empty Actor answers a null actor.
type LabelEvent struct {
	Label     string
	At        time.Time
	Actor     string
	ActorType string
}

// PullRequest is one pull request of the fake repository.
type PullRequest struct {
	Number int
	// Closed is true for a closed and for a merged pull request.
	Closed bool
	Merged bool
	// HeadCommit is the SHA of the head of the pull request.
	HeadCommit string
	// Author is the login. For a GitHub App it is the slug without "[bot]",
	// as GraphQL returns it, with AuthorIsBot true. Empty means no author.
	Author      string
	AuthorIsBot bool
	// Closes holds the numbers of the issues that the pull request closes.
	Closes []int
	// HeadBranch is the branch of the pull request.
	HeadBranch string
	// Labels are the labels of the pull request. I11 makes them equal to
	// the labels of the issue.
	Labels []string
	// Checks are the checks on the head commit, as statusCheckRollup
	// returns them.
	Checks []Check
	// Reviews are the reviews of the pull request, oldest first.
	Reviews []Review
	// Body is the description of the pull request.
	Body string
	// Threads are the review threads, as the follow-up note (I9) reads
	// them.
	Threads []ReviewThread
	// Conflict makes a merge answer 405, and mergeable read false, as
	// GitHub answers for a pull request that conflicts with its base.
	Conflict bool
	// Mergeable is the value of the GraphQL field mergeable. Empty answers
	// CONFLICTING for a pull request with Conflict, and MERGEABLE otherwise.
	Mergeable string
	// HeadCommittedAt is the commit time of the head commit.
	HeadCommittedAt time.Time
	// MergeMethod is the method of the merge that merged it.
	MergeMethod string
}

// ReviewThread is one thread of review comments on a line of a pull
// request, first comment first.
type ReviewThread struct {
	Path string
	// Line is the line now; 0 answers null, as GitHub does for a thread
	// on code that has moved. OriginalLine is the line it was written on.
	Line, OriginalLine int
	Comments           []ReviewComment
}

// ReviewComment is one comment of a review thread. Author is the login;
// for a GitHub App it is the slug without "[bot]", with AuthorIsBot true.
type ReviewComment struct {
	Author      string
	AuthorIsBot bool
	Body        string
	URL         string
}

// Review is one review of a pull request, as GraphQL returns it. Author is
// the login; for a GitHub App it is the slug without "[bot]", with
// AuthorIsBot true.
type Review struct {
	Author      string
	AuthorIsBot bool
	// State is APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED, or
	// PENDING.
	State string
	// Commit is the SHA that the review is on. Empty answers null, as
	// GitHub does for a commit that is gone.
	Commit string
	// SubmittedAt is zero for a pending review, which answers null.
	SubmittedAt time.Time
	URL         string
}

// CheckRun is one check run of a commit, as the REST endpoints of a failed
// check return it. A test that reads the content of a failed check adds it
// with AddCheckRun.
type CheckRun struct {
	ID   int64
	Name string
	// Conclusion is the word of REST (success, failure, skipped, ...).
	Conclusion string
	// JobID is the job of GitHub Actions. The fake builds the details
	// address from it, as GitHub does. 0 leaves the address without a job,
	// as a check run of another App has.
	JobID int64
	// AppID is the App that reported the check run. 0 leaves it out.
	AppID int64
	// DetailsURL replaces the address that the fake builds from JobID, for
	// a check run of an App that is not GitHub Actions.
	DetailsURL string
	// Annotations are the annotations of the check run.
	Annotations []Annotation
	// JobLog is the plain text log of the job.
	JobLog string
}

// Annotation is one annotation of a check run.
type Annotation struct {
	Path string
	// Level is the word of REST: failure, warning, or notice.
	Level   string
	Message string
}

// Check is one check on the head commit of a pull request. A check with
// CommitStatus is answered as a StatusContext and reads State; every other
// check is answered as a CheckRun and reads Status and Conclusion.
type Check struct {
	Name string
	// Status is the word of CheckStatusState. Empty means COMPLETED.
	Status string
	// Conclusion is the word of CheckConclusionState (SUCCESS, FAILURE,
	// SKIPPED, NEUTRAL, ...).
	Conclusion string
	// CommitStatus makes the fake answer a commit status. State is then
	// the word of StatusState (SUCCESS, FAILURE, PENDING, ...).
	CommitStatus bool
	State        string
	// Integration is the database id of the App of the check suite. It is
	// left out for a commit status.
	Integration int64
}

// RequiredCheck is one check that the rules of the default branch require in
// the fake. Integration is the App that must report it, or 0 for a rule that
// names no App.
type RequiredCheck struct {
	Name        string
	Integration int64
}

// Label is one label of a repository.
type Label struct {
	Name, Color, Description string
}

// Comment is one comment that the fake stored for an issue.
type Comment struct {
	ID   int64
	Body string
	// Author is the login without "[bot]", as GraphQL gives it, and
	// AuthorIsBot says that the author is a GitHub App. A comment that
	// cumin posts through the REST API has no author in the fake.
	Author      string
	AuthorIsBot bool
	// At is when the comment was written.
	At time.Time
}

// AddComment adds a comment of the past to an issue, for example one that
// an agent wrote.
func (f *Fake) AddComment(r *Repository, number int, comment Comment) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastCommentID++
	comment.ID = f.lastCommentID
	r.comments[number] = append(r.comments[number], comment)
}

// Repository is one repository of the fake.
type Repository struct {
	Owner, Name  string
	Issues       map[int]*Issue
	PullRequests map[int]*PullRequest
	Labels       []Label
	// DefaultBranch is the name that the snapshot reads. Empty means that
	// the repository has no commit, as a new repository on GitHub.
	DefaultBranch string
	// RequiredChecks are the checks that the rules of the default branch
	// require, as "Get rules for a branch" returns them.
	RequiredChecks []RequiredCheck
	// Files are the files of the default branch, by path. SetFile writes
	// them; the snapshot reads the files of .cumin/.
	Files map[string]File
	// CheckRuns are the check runs of a commit, by commit SHA.
	CheckRuns map[string][]CheckRun
	// comments holds the comments of each issue, by issue number, in the
	// order in which they were created.
	comments map[int][]Comment
	// installationID is the id of the installation of the App on the
	// repository, from the order of creation.
	installationID int64
}

// File is one file of the default branch of a fake repository. Binary and
// Truncated make the fake answer as GitHub answers for a file that cumin
// cannot read as a whole.
type File struct {
	Content   string
	Binary    bool
	Truncated bool
}

// Request is one request that the fake received.
type Request struct {
	Method, Path string
	Body         []byte
}

// Fake is the in-memory GitHub. Every method is safe for concurrent use.
type Fake struct {
	t *testing.T

	mu           sync.Mutex
	repositories map[string]*Repository
	app          *App
	users        map[string]int64
	requests     []Request
	// received is closed, and replaced, each time a request arrives, so
	// that WaitForRequests wakes.
	received chan struct{}
	failNext *failure
	// lastCommentID is the id of the comment that was created last.
	lastCommentID int64
	// commentAuthor is the author of the comments that the REST API
	// creates, as SetCommentAuthor set it.
	commentAuthor string
	// linkErrors and linksIgnored change the answer of the closing link
	// (addCloseIssueReferences), as SetLinkErrors and IgnoreLinks set them.
	linkErrors   []string
	linksIgnored bool
	// closeOnMerge makes a merge close the issues that the pull request
	// closes, as CloseIssuesOnMerge set it.
	closeOnMerge bool
	// permissions are the answers of the permission endpoint, by login, as
	// SetPermission set them.
	permissions map[string]Permission
	// now is the clock that stamps new comments, reviews, and label
	// events, as SetClock set it.
	now func() time.Time
}

// DefaultNow is the time of the clock of a fake whose test set no clock.
var DefaultNow = time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)

// SetClock sets the clock that stamps new comments, reviews, and label
// events. Without it, the clock always reads DefaultNow.
func (f *Fake) SetClock(now func() time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = now
}

// Permission is the answer of "Get repository permissions for a user".
type Permission struct {
	Permission string
	UserType   string
}

// SetPermission sets the permission of an account on every repository of
// the fake. An account without one reads "read", as any account reads on a
// public repository (measured in #286, M1).
func (f *Fake) SetPermission(login, permission, userType string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.permissions == nil {
		f.permissions = map[string]Permission{}
	}
	f.permissions[login] = Permission{Permission: permission, UserType: userType}
}

// CloseIssuesOnMerge makes a merge close the issues that the pull request
// closes, as GitHub does when it closes them through the link. Without it,
// a merge leaves them open, as GitHub did from 2026-09-30.
func (f *Fake) CloseIssuesOnMerge() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeOnMerge = true
}

// SetLinkErrors makes the closing link (addCloseIssueReferences) answer
// with these GraphQL errors and add nothing, as GitHub answers a call that
// it refuses. No messages make it work again.
func (f *Fake) SetLinkErrors(messages ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.linkErrors = messages
}

// IgnoreLinks makes the closing link answer as a success and add nothing,
// so that a read of the issue after it does not show the link.
func (f *Fake) IgnoreLinks() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.linksIgnored = true
}

// IssueNodeID and PullRequestNodeID are the GraphQL IDs of the fake. The
// closing link takes them.
func IssueNodeID(r *Repository, number int) string {
	return fmt.Sprintf("I_%s_%d", key(r.Owner, r.Name), number)
}

func PullRequestNodeID(r *Repository, number int) string {
	return fmt.Sprintf("PR_%s_%d", key(r.Owner, r.Name), number)
}

// SetCommentAuthor makes the comments that cumin creates through the REST
// API carry the App with the slug as their author, as GitHub does for an
// installation token. Without it they have no author.
func (f *Fake) SetCommentAuthor(slug string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commentAuthor = slug
}

// failure is one answer that the fake gives instead of the real one.
type failure struct {
	method, path string
	status       int
	// skip is how many matching requests are answered as usual first.
	skip int
	// hang leaves the request without an answer, as HangNext set it.
	hang bool
	// closed closes the connection without an answer, as CloseTimes set it.
	closed bool
	// limited answers a full primary rate limit with the reset time, as
	// LimitTimes set it.
	limited bool
	reset   time.Time
	// secondary answers a secondary rate limit, as SecondaryLimitTimes set
	// it. A retryAfter of 0 sends no retry-after header.
	secondary  bool
	retryAfter time.Duration
	// times is how many matching requests fail.
	times int
	// then is the failure that follows when this one is used up.
	then *failure
}

// New starts the fake. The server closes when the test ends.
func New(t *testing.T) (*Fake, *httptest.Server) {
	t.Helper()
	f := &Fake{t: t, repositories: map[string]*Repository{}, users: map[string]int64{}, received: make(chan struct{}),
		now: func() time.Time { return DefaultNow }}
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	return f, server
}

// AddRepository adds an empty repository.
func (f *Fake) AddRepository(owner, name string) *Repository {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := &Repository{Owner: owner, Name: name, Issues: map[int]*Issue{}, PullRequests: map[int]*PullRequest{},
		DefaultBranch: "main", Files: map[string]File{}, comments: map[int][]Comment{},
		installationID: int64(len(f.repositories) + 1)}
	f.repositories[key(owner, name)] = r
	return r
}

// AddApp registers the App of the fake. The fake then answers GET /app
// with it, and GET /users/<slug>[bot] with its bot user.
func (f *Fake) AddApp(app App) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.app = &app
}

// AddUser registers a user for GET /users/{login}.
func (f *Fake) AddUser(login string, id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users[login] = id
}

// TokenRequest is the body of one token request: the repositories and the
// permissions that the token is limited to.
type TokenRequest struct {
	Repositories []string          `json:"repositories"`
	Permissions  map[string]string `json:"permissions"`
}

// TokenRequests returns the bodies of the token requests that the fake
// received (POST /app/installations/{id}/access_tokens), in order.
func (f *Fake) TokenRequests() []TokenRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var requests []TokenRequest
	for _, r := range f.requests {
		if r.Method != http.MethodPost || !accessTokensPath.MatchString(r.Path) {
			continue
		}
		var body TokenRequest
		_ = json.Unmarshal(r.Body, &body)
		requests = append(requests, body)
	}
	return requests
}

// AddIssue adds an issue to the repository. The fake keeps the pointer, so a
// test can change the issue later.
func (f *Fake) AddIssue(r *Repository, issue *Issue) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r.Issues[issue.Number] = issue
}

// AddCheckRun adds one check run on a commit of the repository, for the
// REST endpoints that read what a failed check says.
func (f *Fake) AddCheckRun(r *Repository, sha string, run CheckRun) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.CheckRuns == nil {
		r.CheckRuns = map[string][]CheckRun{}
	}
	r.CheckRuns[sha] = append(r.CheckRuns[sha], run)
}

// AddPullRequest adds a pull request to the repository. The fake keeps the
// pointer, so a test can change the pull request later.
func (f *Fake) AddPullRequest(r *Repository, pr *PullRequest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r.PullRequests[pr.Number] = pr
}

// ClosePullRequest closes one pull request of the repository.
func (f *Fake) ClosePullRequest(r *Repository, number int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	pr, ok := r.PullRequests[number]
	if !ok {
		return fmt.Errorf("no pull request #%d", number)
	}
	pr.Closed = true
	return nil
}

// Reviews returns a copy of the reviews of one pull request.
func (f *Fake) Reviews(r *Repository, number int) []Review {
	f.mu.Lock()
	defer f.mu.Unlock()
	if pr, ok := r.PullRequests[number]; ok {
		return slices.Clone(pr.Reviews)
	}
	return nil
}

// Issue returns a copy of one issue, or nil.
func (f *Fake) Issue(r *Repository, number int) *Issue {
	f.mu.Lock()
	defer f.mu.Unlock()
	issue, ok := r.Issues[number]
	if !ok {
		return nil
	}
	copied := *issue
	copied.Labels = slices.Clone(issue.Labels)
	copied.BlockedBy = slices.Clone(issue.BlockedBy)
	copied.LabelEvents = slices.Clone(issue.LabelEvents)
	return &copied
}

// PullRequestLabels returns a copy of the labels of one pull request, or nil.
func (f *Fake) PullRequestLabels(r *Repository, number int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	pr, ok := r.PullRequests[number]
	if !ok {
		return nil
	}
	return slices.Clone(pr.Labels)
}

// SetPullRequestConflict makes the pull request conflict with its base: a
// merge answers 405, and mergeable reads false.
func (f *Fake) SetPullRequestConflict(r *Repository, number int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if pr, ok := r.PullRequests[number]; ok {
		pr.Conflict = true
	}
}

// SetPullRequestMergeable sets the value that the GraphQL field mergeable
// answers for the pull request, for example "UNKNOWN".
func (f *Fake) SetPullRequestMergeable(r *Repository, number int, mergeable string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if pr, ok := r.PullRequests[number]; ok {
		pr.Mergeable = mergeable
	}
}

// SetPullRequestHeadCommitTime sets the commit time of the head commit of
// the pull request.
func (f *Fake) SetPullRequestHeadCommitTime(r *Repository, number int, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if pr, ok := r.PullRequests[number]; ok {
		pr.HeadCommittedAt = at
	}
}

// PullRequestCloses returns the numbers of the issues that the pull request
// closes (its closing links).
func (f *Fake) PullRequestCloses(r *Repository, number int) []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	pr, ok := r.PullRequests[number]
	if !ok {
		return nil
	}
	return slices.Clone(pr.Closes)
}

// SetLabels replaces the labels of an issue or of a pull request, as the
// Owner does by hand on GitHub. Issues and pull requests share one sequence
// of numbers on GitHub, so the number names one of them.
func (f *Fake) SetLabels(r *Repository, number int, labels []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if issue, ok := r.Issues[number]; ok {
		issue.Labels = slices.Clone(labels)
		return nil
	}
	if pr, ok := r.PullRequests[number]; ok {
		pr.Labels = slices.Clone(labels)
		return nil
	}
	return fmt.Errorf("no issue or pull request #%d", number)
}

// SetFile puts a file on the default branch of the repository.
func (f *Fake) SetFile(r *Repository, path string, file File) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r.Files[path] = file
}

// RemoveFile removes a file from the default branch of the repository.
func (f *Fake) RemoveFile(r *Repository, path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(r.Files, path)
}

// AddLabel adds a label to the repository.
func (f *Fake) AddLabel(r *Repository, label Label) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r.Labels = append(r.Labels, label)
}

// LabelNames returns the names of the labels of the repository, in the
// order of creation.
func (f *Fake) LabelNames(r *Repository) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var names []string
	for _, label := range r.Labels {
		names = append(names, label.Name)
	}
	return names
}

// Comments returns the comments of one issue, in the order in which they
// were created.
func (f *Fake) Comments(r *Repository, number int) []Comment {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(r.comments[number])
}

// FailNext makes the fake answer the next request with the method and the
// path with the status and an error body, once. The request is recorded and
// changes nothing.
func (f *Fake) FailNext(method, path string, status int) {
	f.FailAfter(method, path, 0, status)
}

// FailAfter is FailNext after skip matching requests are answered as
// usual. A test uses it to fail one of several GraphQL queries of a poll.
func (f *Fake) FailAfter(method, path string, skip, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failNext = &failure{method: method, path: path, status: status, skip: skip, times: 1}
}

// FailTimes is FailAfter for the next times matching requests after the
// skipped ones. A test uses it to fail every try of a read that the client
// sends again. A failure of FailTimes or CloseTimes that still waits comes
// first.
func (f *Fake) FailTimes(method, path string, skip, times, status int) {
	f.failThen(&failure{method: method, path: path, status: status, skip: skip, times: times})
}

// CloseTimes makes the fake close the connection of the next times
// requests with the method and the path, without an answer, as a network
// error. The requests are recorded and change nothing. A failure of
// FailTimes or CloseTimes that still waits comes first.
func (f *Fake) CloseTimes(method, path string, times int) {
	f.failThen(&failure{method: method, path: path, closed: true, times: times})
}

// LimitTimes makes the fake answer the next times requests with the method
// and the path as GitHub answers a full primary rate limit: the header
// x-ratelimit-remaining with 0, and the reset time in x-ratelimit-reset. A
// REST request gets the status (403 or 429). A GraphQL request gets the
// status 200 and an error, whatever the status is. The requests are
// recorded and change nothing. A failure of FailTimes or CloseTimes that
// still waits comes first.
func (f *Fake) LimitTimes(method, path string, times, status int, reset time.Time) {
	f.failThen(&failure{method: method, path: path, status: status, limited: true, reset: reset, times: times})
}

// SecondaryLimitTimes makes the fake answer the next times requests with
// the method and the path as GitHub answers a secondary rate limit: the
// status (403 or 429) and an error message that names the secondary rate
// limit. A GraphQL request with the status 200 gets the message as a
// GraphQL error. A retryAfter above 0 adds the header retry-after in
// seconds. The answer has no header x-ratelimit-remaining with 0. The
// requests are recorded and change nothing. A failure of FailTimes or
// CloseTimes that still waits comes first.
func (f *Fake) SecondaryLimitTimes(method, path string, times, status int, retryAfter time.Duration) {
	f.failThen(&failure{method: method, path: path, status: status, secondary: true, retryAfter: retryAfter, times: times})
}

// failThen adds a failure after the failures that still wait.
func (f *Fake) failThen(next *failure) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failNext == nil {
		f.failNext = next
		return
	}
	last := f.failNext
	for last.then != nil {
		last = last.then
	}
	last.then = next
}

// HangNext makes the fake leave the next request with the method and the
// path without an answer, once, as a stalled connection. The request is
// recorded and changes nothing. The fake holds the request until the client
// gives up, so the client needs a timeout or a context with a deadline.
// HangNext replaces a failure that FailNext or FailAfter set.
func (f *Fake) HangNext(method, path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failNext = &failure{method: method, path: path, hang: true, times: 1}
}

// HangTimes is HangNext for the next times matching requests: every try
// of a read that the client sends again stalls.
func (f *Fake) HangTimes(method, path string, times int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failNext = &failure{method: method, path: path, hang: true, times: times}
}

// Requests returns the requests that the fake received, in order.
func (f *Fake) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

// CountRequests returns how many requests had the method and the path.
func (f *Fake) CountRequests(method, path string) int {
	n := 0
	for _, r := range f.Requests() {
		if r.Method == method && r.Path == path {
			n++
		}
	}
	return n
}

// WaitForRequests waits until the fake has received n requests with the
// method and the path. It returns false when the guard passes first; the
// guard is only there against a hang.
func (f *Fake) WaitForRequests(method, path string, n int, guard time.Duration) bool {
	timeout := time.After(guard)
	for {
		f.mu.Lock()
		received := f.received
		f.mu.Unlock()
		if f.CountRequests(method, path) >= n {
			return true
		}
		select {
		case <-received:
		case <-timeout:
			return false
		}
	}
}

func key(owner, name string) string { return strings.ToLower(owner + "/" + name) }

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := readBody(r)
	f.mu.Lock()
	f.requests = append(f.requests, Request{Method: r.Method, Path: r.URL.Path, Body: body})
	close(f.received)
	f.received = make(chan struct{})
	fail := f.failNext
	if fail != nil && fail.method == r.Method && fail.path == r.URL.Path && fail.skip > 0 {
		fail.skip--
		fail = nil
	} else if fail != nil && fail.method == r.Method && fail.path == r.URL.Path {
		if fail.times--; fail.times <= 0 {
			f.failNext = fail.then
		}
	} else {
		fail = nil
	}
	f.mu.Unlock()
	if fail != nil && fail.hang {
		// The context ends when the client closes the connection. Without
		// this return, the Close of the server waits forever.
		<-r.Context().Done()
		return
	}
	if fail != nil && fail.closed {
		// net/http closes the connection without an answer.
		panic(http.ErrAbortHandler)
	}
	if fail != nil && fail.limited {
		const message = "API rate limit exceeded for installation ID 1."
		w.Header().Set("x-ratelimit-limit", "5000")
		w.Header().Set("x-ratelimit-remaining", "0")
		w.Header().Set("x-ratelimit-used", "5000")
		w.Header().Set("x-ratelimit-reset", strconv.FormatInt(fail.reset.Unix(), 10))
		if r.URL.Path == "/graphql" {
			w.Header().Set("x-ratelimit-resource", "graphql")
			writeJSON(w, http.StatusOK, map[string]any{"errors": []any{map[string]any{"type": "RATE_LIMITED", "message": message}}})
			return
		}
		w.Header().Set("x-ratelimit-resource", "core")
		writeJSON(w, fail.status, map[string]any{"message": message})
		return
	}
	if fail != nil && fail.secondary {
		const message = "You have exceeded a secondary rate limit. Please wait a few minutes before you try again."
		w.Header().Set("x-ratelimit-limit", "5000")
		w.Header().Set("x-ratelimit-remaining", "4990")
		if fail.retryAfter > 0 {
			w.Header().Set("retry-after", strconv.Itoa(int(fail.retryAfter/time.Second)))
		}
		if r.URL.Path == "/graphql" && fail.status == http.StatusOK {
			writeJSON(w, http.StatusOK, map[string]any{"errors": []any{map[string]any{"message": message}}})
			return
		}
		writeJSON(w, fail.status, map[string]any{"message": message})
		return
	}
	if fail != nil {
		writeJSON(w, fail.status, map[string]any{"message": "Failure requested by the test"})
		return
	}

	installation := installationPath.FindStringSubmatch(r.URL.Path)
	accessTokens := accessTokensPath.FindStringSubmatch(r.URL.Path)
	user := userPath.FindStringSubmatch(r.URL.Path)
	// The endpoints of an App take a JWT that the client signs with its
	// private key. The fake accepts any bearer there and does not verify
	// the signature. Every other endpoint needs the installation token.
	appEndpoint := r.URL.Path == "/app" || installation != nil || accessTokens != nil
	bearer, isBearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !isBearer || bearer == "" || (!appEndpoint && bearer != Token) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"message": "Bad credentials"})
		return
	}
	labels := repoLabelsPath.FindStringSubmatch(r.URL.Path)
	issueLabels := issueLabelsPath.FindStringSubmatch(r.URL.Path)
	issueComments := issueCommentsPath.FindStringSubmatch(r.URL.Path)
	branchRules := branchRulesPath.FindStringSubmatch(r.URL.Path)
	commitChecks := commitChecksPath.FindStringSubmatch(r.URL.Path)
	annotations := annotationsPath.FindStringSubmatch(r.URL.Path)
	jobLog := jobLogPath.FindStringSubmatch(r.URL.Path)
	reviews := reviewsPath.FindStringSubmatch(r.URL.Path)
	moveHead := moveHeadPath.FindStringSubmatch(r.URL.Path)
	pulls := pullsPath.FindStringSubmatch(r.URL.Path)
	pull := pullPath.FindStringSubmatch(r.URL.Path)
	merge := mergePath.FindStringSubmatch(r.URL.Path)
	issue := issuePath.FindStringSubmatch(r.URL.Path)
	permission := permissionPath.FindStringSubmatch(r.URL.Path)
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/app":
		f.serveApp(w)
	case r.Method == http.MethodGet && installation != nil:
		f.serveInstallation(w, installation[1], installation[2])
	case r.Method == http.MethodPost && accessTokens != nil:
		id, _ := strconv.ParseInt(accessTokens[1], 10, 64)
		f.serveAccessToken(w, id)
	case r.Method == http.MethodGet && user != nil:
		f.serveUser(w, user[1])
	case r.Method == http.MethodPost && r.URL.Path == "/graphql":
		f.serveGraphQL(w, body)
	case r.Method == http.MethodGet && labels != nil:
		f.serveListLabels(w, r, labels[1], labels[2])
	case r.Method == http.MethodPost && labels != nil:
		f.serveCreateLabel(w, body, labels[1], labels[2])
	case r.Method == http.MethodPut && issueLabels != nil:
		number, _ := strconv.Atoi(issueLabels[3])
		f.serveSetIssueLabels(w, body, issueLabels[1], issueLabels[2], number)
	case r.Method == http.MethodPost && issueComments != nil:
		number, _ := strconv.Atoi(issueComments[3])
		f.serveCreateIssueComment(w, body, issueComments[1], issueComments[2], number)
	case r.Method == http.MethodGet && branchRules != nil:
		f.serveBranchRules(w, r, branchRules[1], branchRules[2], branchRules[3])
	case r.Method == http.MethodGet && commitChecks != nil:
		f.serveCommitCheckRuns(w, r, commitChecks[1], commitChecks[2], commitChecks[3])
	case r.Method == http.MethodGet && annotations != nil:
		id, _ := strconv.ParseInt(annotations[3], 10, 64)
		f.serveAnnotations(w, r, annotations[1], annotations[2], id)
	case r.Method == http.MethodGet && jobLog != nil:
		id, _ := strconv.ParseInt(jobLog[3], 10, 64)
		f.serveJobLog(w, jobLog[1], jobLog[2], id)
	case r.Method == http.MethodPost && moveHead != nil:
		number, _ := strconv.Atoi(moveHead[3])
		f.serveMoveHead(w, body, moveHead[1], moveHead[2], number)
	case r.Method == http.MethodGet && permission != nil:
		f.servePermission(w, permission[3])
	case r.Method == http.MethodPut && merge != nil:
		number, _ := strconv.Atoi(merge[3])
		f.serveMerge(w, body, merge[1], merge[2], number)
	case r.Method == http.MethodGet && pull != nil:
		number, _ := strconv.Atoi(pull[3])
		f.servePull(w, pull[1], pull[2], number)
	case (r.Method == http.MethodGet || r.Method == http.MethodPatch) && issue != nil:
		number, _ := strconv.Atoi(issue[3])
		f.serveIssue(w, r.Method, body, issue[1], issue[2], number)
	case r.Method == http.MethodGet && pulls != nil:
		f.serveListPulls(w, r, pulls[1], pulls[2])
	case r.Method == http.MethodPost && reviews != nil:
		number, _ := strconv.Atoi(reviews[3])
		f.serveCreateReview(w, body, reviews[1], reviews[2], number)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
	}
}

var (
	repoLabelsPath    = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/labels$`)
	issueLabelsPath   = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/issues/(\d+)/labels$`)
	issueCommentsPath = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/issues/(\d+)/comments$`)
	installationPath  = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/installation$`)
	accessTokensPath  = regexp.MustCompile(`^/app/installations/(\d+)/access_tokens$`)
	userPath          = regexp.MustCompile(`^/users/([^/]+)$`)
	branchRulesPath   = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/rules/branches/(.+)$`)
	commitChecksPath  = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/commits/([^/]+)/check-runs$`)
	annotationsPath   = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/check-runs/(\d+)/annotations$`)
	jobLogPath        = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/actions/jobs/(\d+)/logs$`)
	reviewsPath       = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/pulls/(\d+)/reviews$`)
	pullsPath         = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/pulls$`)
	pullPath          = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/pulls/(\d+)$`)
	mergePath         = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/pulls/(\d+)/merge$`)
	issuePath         = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/issues/(\d+)$`)
	permissionPath    = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/collaborators/([^/]+)/permission$`)
	// moveHeadPath is not an endpoint of GitHub. A fake agent run calls it
	// to move the head of a pull request while it runs, as a push would.
	moveHeadPath = regexp.MustCompile(`^/_fake/repos/([^/]+)/([^/]+)/pulls/(\d+)/head$`)
)

// serveCommitCheckRuns answers GET .../commits/{sha}/check-runs. Official:
// "List check runs for a Git reference". The answer is paginated.
func (f *Fake) serveCommitCheckRuns(w http.ResponseWriter, r *http.Request, owner, name, sha string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	repo, ok := f.repository(w, owner, name)
	if !ok {
		return
	}
	runs := []map[string]any{}
	for _, run := range repo.CheckRuns[sha] {
		details := run.DetailsURL
		if details == "" && run.JobID != 0 {
			details = fmt.Sprintf("https://github.com/%s/%s/actions/runs/1/job/%d", owner, name, run.JobID)
		}
		node := map[string]any{
			"id": run.ID, "name": run.Name, "status": "completed",
			"conclusion": run.Conclusion, "details_url": details,
		}
		if run.AppID != 0 {
			node["app"] = map[string]any{"id": run.AppID}
		}
		runs = append(runs, node)
	}
	page := page(runs, r)
	writeJSON(w, http.StatusOK, map[string]any{"total_count": len(runs), "check_runs": page})
}

// serveAnnotations answers GET .../check-runs/{id}/annotations. Official:
// "List check run annotations".
func (f *Fake) serveAnnotations(w http.ResponseWriter, r *http.Request, owner, name string, id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	repo, ok := f.repository(w, owner, name)
	if !ok {
		return
	}
	run, ok := f.checkRun(repo, id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return
	}
	notes := []map[string]any{}
	for _, note := range run.Annotations {
		notes = append(notes, map[string]any{
			"path": note.Path, "annotation_level": note.Level, "message": note.Message,
		})
	}
	writeJSON(w, http.StatusOK, page(notes, r))
}

// serveJobLog answers GET .../actions/jobs/{id}/logs with the plain text
// log. GitHub answers with a redirect to a file; the fake answers the file
// itself, which the client reads the same way.
func (f *Fake) serveJobLog(w http.ResponseWriter, owner, name string, id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	repo, ok := f.repository(w, owner, name)
	if !ok {
		return
	}
	for _, runs := range repo.CheckRuns {
		for _, run := range runs {
			if run.JobID == id {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(run.JobLog))
				return
			}
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
}

// checkRun finds one check run of the repository by its id. The caller
// holds the lock.
func (f *Fake) checkRun(repo *Repository, id int64) (CheckRun, bool) {
	for _, runs := range repo.CheckRuns {
		for _, run := range runs {
			if run.ID == id {
				return run, true
			}
		}
	}
	return CheckRun{}, false
}

// serveBranchRules answers GET /repos/{owner}/{repo}/rules/branches/{branch}
// with the rules that apply to the branch. Official: "Get rules for a
// branch". The fake answers the required checks of the repository for its
// default branch, and no rule for any other branch.
//
// The answer is paginated, as GitHub's is. Each required check becomes its
// own rule, as a repository with several rulesets has, so that a client that
// reads one page only misses a required check.
func (f *Fake) serveBranchRules(w http.ResponseWriter, r *http.Request, owner, name, branch string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	repo, ok := f.repository(w, owner, name)
	if !ok {
		return
	}
	decoded, err := url.PathUnescape(branch)
	if err != nil {
		decoded = branch
	}
	all := []map[string]any{}
	if decoded == repo.DefaultBranch && len(repo.RequiredChecks) > 0 {
		all = append(all, map[string]any{"type": "update", "parameters": map[string]any{}})
		for _, check := range repo.RequiredChecks {
			entry := map[string]any{"context": check.Name}
			if check.Integration != 0 {
				entry["integration_id"] = check.Integration
			}
			all = append(all, map[string]any{"type": "required_status_checks", "parameters": map[string]any{
				"required_status_checks":               []map[string]any{entry},
				"strict_required_status_checks_policy": false,
			}})
		}
	}
	writeJSON(w, http.StatusOK, page(all, r))
}

// page returns the page of items that the query asks for, as a paginated
// REST answer does. A missing per_page is the default of GitHub, 30.
func page[T any](items []T, r *http.Request) []T {
	perPage, err := strconv.Atoi(r.URL.Query().Get("per_page"))
	if err != nil || perPage < 1 || perPage > 100 {
		perPage = 30
	}
	number, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || number < 1 {
		number = 1
	}
	from := (number - 1) * perPage
	if from >= len(items) {
		return []T{}
	}
	to := min(from+perPage, len(items))
	return items[from:to]
}

// serveApp answers GET /app with the registered App. Official: "Get the
// authenticated app".
func (f *Fake) serveApp(w http.ResponseWriter) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.app == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"slug":        f.app.Slug,
		"html_url":    "https://github.com/apps/" + f.app.Slug,
		"owner":       map[string]any{"login": f.app.Owner},
		"permissions": map[string]any{"contents": "write", "metadata": "read"},
	})
}

// serveInstallation answers GET /repos/{owner}/{repo}/installation with
// the installation of the App on the repository. Official: "Get a
// repository installation for the authenticated app".
func (f *Fake) serveInstallation(w http.ResponseWriter, owner, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	repo, ok := f.repository(w, owner, name)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": repo.installationID})
}

// serveAccessToken answers POST /app/installations/{id}/access_tokens with
// Token, which expires in one hour. The body is recorded (TokenRequests).
// Official: "Create an installation access token for an app".
func (f *Fake) serveAccessToken(w http.ResponseWriter, id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	found := false
	for _, repo := range f.repositories {
		if repo.installationID == id {
			found = true
		}
	}
	if !found {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return
	}
	// The client compares the expiry with the real clock and takes no clock
	// from a test, so the expiry is one hour after the real time.
	writeJSON(w, http.StatusCreated, map[string]any{
		"token":      Token,
		"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
}

// serveUser answers GET /users/{login} for the bot of the App and for the
// registered users. Official: "Get a user".
func (f *Fake) serveUser(w http.ResponseWriter, login string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	login, _ = url.PathUnescape(login)
	if f.app != nil && login == f.app.Slug+"[bot]" {
		writeJSON(w, http.StatusOK, map[string]any{"id": f.app.BotID, "login": login, "type": "Bot"})
		return
	}
	if id, ok := f.users[login]; ok {
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "login": login, "type": "User"})
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
}

// repository returns the repository, or answers 404.
func (f *Fake) repository(w http.ResponseWriter, owner, name string) (*Repository, bool) {
	repo, ok := f.repositories[key(owner, name)]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
	}
	return repo, ok
}

// serveListLabels answers GET /repos/{owner}/{repo}/labels with the page
// that per_page and page select. Official: "List labels for a repository".
func (f *Fake) serveListLabels(w http.ResponseWriter, r *http.Request, owner, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	repo, ok := f.repository(w, owner, name)
	if !ok {
		return
	}
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if perPage < 1 || perPage > 100 {
		perPage = 30
	}
	if page < 1 {
		page = 1
	}
	labels := []map[string]any{}
	for i := (page - 1) * perPage; i < page*perPage && i < len(repo.Labels); i++ {
		labels = append(labels, labelJSON(repo.Labels[i]))
	}
	writeJSON(w, http.StatusOK, labels)
}

// serveCreateLabel answers POST /repos/{owner}/{repo}/labels. A name that
// exists (without case) answers 422, as GitHub does. Official: "Create a
// label".
func (f *Fake) serveCreateLabel(w http.ResponseWriter, body []byte, owner, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	repo, ok := f.repository(w, owner, name)
	if !ok {
		return
	}
	var label Label
	if err := json.Unmarshal(body, &label); err != nil || label.Name == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed"})
		return
	}
	for _, existing := range repo.Labels {
		if strings.EqualFold(existing.Name, label.Name) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed: already_exists"})
			return
		}
	}
	repo.Labels = append(repo.Labels, label)
	writeJSON(w, http.StatusCreated, labelJSON(label))
}

// serveSetIssueLabels answers PUT /repos/{owner}/{repo}/issues/{n}/labels:
// it replaces every label of the issue. A pull request is an issue on this
// endpoint, so the number may name a pull request too (I11). Official: "Set
// labels for an issue", "Every pull request is an issue".
func (f *Fake) serveSetIssueLabels(w http.ResponseWriter, body []byte, owner, name string, number int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	repo, ok := f.repository(w, owner, name)
	if !ok {
		return
	}
	var target *[]string
	issue, isIssue := repo.Issues[number]
	if isIssue {
		target = &issue.Labels
	} else if pr, ok := repo.PullRequests[number]; ok {
		target = &pr.Labels
	} else {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return
	}
	var request struct {
		Labels *[]string `json:"labels"`
	}
	if err := json.Unmarshal(body, &request); err != nil || request.Labels == nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed"})
		return
	}
	if isIssue {
		now := f.now()
		for _, label := range *request.Labels {
			if !slices.Contains(issue.Labels, label) {
				issue.LabelEvents = append(issue.LabelEvents, LabelEvent{Label: label, At: now})
			}
		}
	}
	*target = slices.Clone(*request.Labels)
	labels := []map[string]any{}
	for _, labelName := range *target {
		labels = append(labels, map[string]any{"name": labelName})
	}
	writeJSON(w, http.StatusOK, labels)
}

// serveCreateIssueComment answers
// POST /repos/{owner}/{repo}/issues/{n}/comments: it stores the comment and
// answers with it. Official: "Create an issue comment".
func (f *Fake) serveCreateIssueComment(w http.ResponseWriter, body []byte, owner, name string, number int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	repo, ok := f.repository(w, owner, name)
	if !ok {
		return
	}
	_, isIssue := repo.Issues[number]
	_, isPullRequest := repo.PullRequests[number]
	if !isIssue && !isPullRequest {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return
	}
	var request struct {
		Body *string `json:"body"`
	}
	if err := json.Unmarshal(body, &request); err != nil || request.Body == nil || *request.Body == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed"})
		return
	}
	// The ids grow over the whole fake, as they do on GitHub.
	f.lastCommentID++
	comment := Comment{ID: f.lastCommentID, Body: *request.Body, At: f.now(), Author: f.commentAuthor, AuthorIsBot: f.commentAuthor != ""}
	// The token of the fake is the same for cumin and for the agents. In the
	// tests only an agent comments on a pull request (the Reviewer of I8),
	// and cumin comments on issues; so a comment on a pull request is by
	// the App of the fake.
	if isPullRequest && f.app != nil {
		comment.Author, comment.AuthorIsBot = f.app.Slug, true
	}
	repo.comments[number] = append(repo.comments[number], comment)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":   comment.ID,
		"body": comment.Body,
		"html_url": fmt.Sprintf("https://github.com/%s/%s/issues/%d#issuecomment-%d",
			repo.Owner, repo.Name, number, comment.ID),
	})
}

// serveMoveHead sets the head commit of a pull request to the "sha" of the
// body. It stands for a push to the branch of the pull request.
func (f *Fake) serveMoveHead(w http.ResponseWriter, body []byte, owner, name string, number int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	repo, ok := f.repository(w, owner, name)
	if !ok {
		return
	}
	pr, ok := repo.PullRequests[number]
	var request struct {
		SHA string `json:"sha"`
	}
	if !ok || json.Unmarshal(body, &request) != nil || request.SHA == "" {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed"})
		return
	}
	pr.HeadCommit = request.SHA
	writeJSON(w, http.StatusOK, map[string]any{"sha": request.SHA})
}

// serveCreateReview answers POST /repos/{owner}/{repo}/pulls/{number}/reviews
// (official: "Create a review for a pull request"). The author is the App
// of the fake, as the token of an agent run belongs to it. An event of
// APPROVE, REQUEST_CHANGES, or COMMENT submits the review; no event leaves
// it pending. REQUEST_CHANGES and COMMENT need a body.
func (f *Fake) serveCreateReview(w http.ResponseWriter, body []byte, owner, name string, number int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	repo, ok := f.repository(w, owner, name)
	if !ok {
		return
	}
	pr, ok := repo.PullRequests[number]
	if !ok || f.app == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return
	}
	var request struct {
		CommitID string `json:"commit_id"`
		Event    string `json:"event"`
		Body     string `json:"body"`
	}
	states := map[string]string{"APPROVE": "APPROVED", "REQUEST_CHANGES": "CHANGES_REQUESTED", "COMMENT": "COMMENTED", "": "PENDING"}
	err := json.Unmarshal(body, &request)
	state, known := states[request.Event]
	if err != nil || !known || (request.Body == "" && (state == "CHANGES_REQUESTED" || state == "COMMENTED")) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed"})
		return
	}
	commit := request.CommitID
	if commit == "" {
		commit = pr.HeadCommit
	}
	f.lastCommentID++
	review := Review{Author: f.app.Slug, AuthorIsBot: true, State: state, Commit: commit,
		URL: fmt.Sprintf("https://github.com/%s/%s/pull/%d#pullrequestreview-%d", repo.Owner, repo.Name, number, f.lastCommentID)}
	if state != "PENDING" {
		// Each review is later than the one before, even within the same
		// clock tick, as on GitHub.
		review.SubmittedAt = f.now().UTC()
		for _, before := range pr.Reviews {
			if !review.SubmittedAt.After(before.SubmittedAt) {
				review.SubmittedAt = before.SubmittedAt.Add(time.Millisecond)
			}
		}
	}
	pr.Reviews = append(pr.Reviews, review)
	writeJSON(w, http.StatusOK, map[string]any{"id": f.lastCommentID, "state": state, "commit_id": commit, "html_url": review.URL})
}

// serveListPulls answers GET .../pulls. Official: "List pull requests",
// with state and head as "owner:branch". The fake reads state=open and a
// head of the repository owner only, as cumin asks.
func (f *Fake) serveListPulls(w http.ResponseWriter, r *http.Request, owner, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	repo, ok := f.repositories[key(owner, name)]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return
	}
	headOwner, branch, _ := strings.Cut(r.URL.Query().Get("head"), ":")
	list := []map[string]any{}
	for _, pr := range sortedPullRequests(repo) {
		if r.URL.Query().Get("state") == "open" && pr.Closed {
			continue
		}
		if branch != "" && (!strings.EqualFold(headOwner, owner) || pr.HeadBranch != branch) {
			continue
		}
		var user any
		if pr.Author != "" {
			login, kind := pr.Author, "User"
			if pr.AuthorIsBot {
				login, kind = pr.Author+"[bot]", "Bot"
			}
			user = map[string]any{"login": login, "type": kind}
		}
		list = append(list, map[string]any{
			"number":  pr.Number,
			"node_id": PullRequestNodeID(repo, pr.Number),
			"user":    user,
			"head":    map[string]any{"sha": pr.HeadCommit, "ref": pr.HeadBranch},
		})
	}
	// GitHub lists the newest first.
	slices.Reverse(list)
	writeJSON(w, http.StatusOK, list)
}

// serveMerge answers PUT .../pulls/{n}/merge. Official: "Merge a pull
// request": 409 when sha is not the head, 405 when the merge cannot be
// performed (a conflict, measured in #286). A merge closes the issues of
// the pull request only after CloseIssuesOnMerge.
func (f *Fake) serveMerge(w http.ResponseWriter, body []byte, owner, name string, number int) {
	var request struct {
		MergeMethod string `json:"merge_method"`
		SHA         string `json:"sha"`
	}
	_ = json.Unmarshal(body, &request)
	f.mu.Lock()
	defer f.mu.Unlock()
	repo, ok := f.repositories[key(owner, name)]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return
	}
	pr, ok := repo.PullRequests[number]
	switch {
	case !ok:
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
	case pr.Closed:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"message": "Pull Request is not mergeable"})
	case request.SHA != "" && request.SHA != pr.HeadCommit:
		writeJSON(w, http.StatusConflict, map[string]any{"message": "Head branch was modified. Review and try the merge again."})
	case pr.Conflict:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"message": "Pull Request has merge conflicts"})
	default:
		pr.Closed, pr.Merged, pr.MergeMethod = true, true, request.MergeMethod
		if f.closeOnMerge {
			for _, n := range pr.Closes {
				if issue, ok := repo.Issues[n]; ok {
					issue.Closed = true
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"sha": "merge-" + pr.HeadCommit, "merged": true, "message": "Pull Request successfully merged"})
	}
}

// servePermission answers GET .../collaborators/{username}/permission.
func (f *Fake) servePermission(w http.ResponseWriter, login string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.permissions[login]
	if !ok {
		p = Permission{Permission: "read", UserType: "User"}
		if strings.HasSuffix(login, "[bot]") {
			p = Permission{Permission: "none", UserType: "Bot"}
		}
		if login == SeedActor {
			p = Permission{Permission: "admin", UserType: "User"}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"permission": p.Permission, "role_name": p.Permission,
		"user": map[string]any{"login": login, "type": p.UserType}})
}

// servePull answers GET .../pulls/{n} with the fields that cumin reads.
func (f *Fake) servePull(w http.ResponseWriter, owner, name string, number int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	repo, ok := f.repositories[key(owner, name)]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return
	}
	pr, ok := repo.PullRequests[number]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return
	}
	mergeableState := "blocked"
	if pr.Conflict {
		mergeableState = "dirty"
	}
	writeJSON(w, http.StatusOK, map[string]any{"number": pr.Number, "merged": pr.Merged, "mergeable": !pr.Conflict, "mergeable_state": mergeableState})
}

// serveIssue answers GET and PATCH .../issues/{n}: the state, and a close.
func (f *Fake) serveIssue(w http.ResponseWriter, method string, body []byte, owner, name string, number int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	repo, ok := f.repositories[key(owner, name)]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return
	}
	issue, ok := repo.Issues[number]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return
	}
	if method == http.MethodPatch {
		var request struct {
			State       string `json:"state"`
			StateReason string `json:"state_reason"`
		}
		_ = json.Unmarshal(body, &request)
		if request.State == "closed" {
			issue.Closed, issue.StateReason = true, request.StateReason
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"number": issue.Number, "state": strings.ToLower(state(issue.Closed)), "state_reason": issue.StateReason})
}

// serveAddClosingLink answers the mutation addCloseIssueReferences: each
// pull request then closes the issue. Official, GraphQL reference, Issues.
func (f *Fake) serveAddClosingLink(w http.ResponseWriter, issueID string, pullRequestIDs []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.linkErrors) > 0 {
		var errs []map[string]any
		for _, message := range f.linkErrors {
			errs = append(errs, map[string]any{"message": message})
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"addCloseIssueReferences": nil}, "errors": errs})
		return
	}
	for _, repo := range f.repositories {
		for number, issue := range repo.Issues {
			if IssueNodeID(repo, number) != issueID {
				continue
			}
			for _, id := range pullRequestIDs {
				for prNumber, pr := range repo.PullRequests {
					if PullRequestNodeID(repo, prNumber) == id && !f.linksIgnored && !slices.Contains(pr.Closes, number) {
						pr.Closes = append(pr.Closes, number)
					}
				}
			}
			writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{
				"addCloseIssueReferences": map[string]any{"issue": map[string]any{"number": issue.Number}},
			}})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data":   map[string]any{"addCloseIssueReferences": nil},
		"errors": []map[string]any{{"message": "Could not resolve to a node with the global id of '" + issueID + "'"}},
	})
}

func labelJSON(label Label) map[string]any {
	return map[string]any{"name": label.Name, "color": label.Color, "description": label.Description}
}

// serveGraphQL answers the GraphQL queries of the client. It does not parse
// the query text: it answers from the variables, in the shape that the
// client expects. The cursor is the number of the last issue of the page.
func (f *Fake) serveGraphQL(w http.ResponseWriter, body []byte) {
	var request struct {
		Query     string `json:"query"`
		Variables struct {
			Owner        string  `json:"owner"`
			Name         string  `json:"name"`
			First        int     `json:"first"`
			After        *string `json:"after"`
			SubIssues    int     `json:"subIssues"`
			Labels       int     `json:"labels"`
			BlockedBy    int     `json:"blockedBy"`
			PullRequests int     `json:"pullRequests"`
			Checks       int     `json:"checks"`
			Reviews      int     `json:"reviews"`
			// RepositoryFiles asks for the default branch and the files of
			// .cumin/. The client asks for them on the first page only.
			RepositoryFiles bool `json:"repositoryFiles"`
			// Number and Events belong to the query of the label times of
			// one requirement issue.
			Number int `json:"number"`
			Events int `json:"events"`
			// Last and Before belong to the query of the comments of one
			// issue. Before is the index of the first comment of the page
			// that the client read last.
			Last   int     `json:"last"`
			Before *string `json:"before"`
			// Linked belongs to the query of the linked pull requests of
			// one issue, and Threads to the query of one pull request for
			// the follow-up note (I9).
			Linked  int `json:"linked"`
			Threads int `json:"threads"`
			// IssueID and PullRequestIDs belong to the closing link.
			IssueID        string   `json:"issueId"`
			PullRequestIDs []string `json:"pullRequestIds"`
			// IDs belongs to the second query of the poll: the node ids
			// of the sub-issues whose pull requests the client asks for.
			IDs []string `json:"ids"`
		} `json:"variables"`
	}
	if err := json.Unmarshal(body, &request); err == nil && strings.HasPrefix(strings.TrimSpace(request.Query), "mutation") && strings.Contains(request.Query, "addCloseIssueReferences") {
		f.serveAddClosingLink(w, request.Variables.IssueID, request.Variables.PullRequestIDs)
		return
	}
	if err := json.Unmarshal(body, &request); err != nil || !strings.Contains(request.Query, "rateLimit") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"message": "Problems parsing JSON"})
		return
	}
	v := request.Variables

	f.mu.Lock()
	defer f.mu.Unlock()
	if v.IDs != nil {
		f.servePullRequests(w, v.IDs, v.Labels, v.PullRequests, v.Checks, v.Reviews)
		return
	}
	repo, ok := f.repositories[key(v.Owner, v.Name)]
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"data":   map[string]any{"repository": nil, "rateLimit": rateLimit(1)},
			"errors": []map[string]any{{"message": "Could not resolve to a Repository with the name '" + v.Owner + "/" + v.Name + "'."}},
		})
		return
	}

	if v.Number != 0 && v.Linked != 0 {
		f.serveLinked(w, repo, v.Number, v.Linked)
		return
	}
	if v.Number != 0 && v.Threads != 0 {
		f.servePullRequestNote(w, repo, v.Number, v.Threads)
		return
	}
	if v.Number != 0 && v.Last != 0 {
		f.serveIssueComments(w, repo, v.Number, v.Last, v.Before)
		return
	}
	if v.Number != 0 && v.PullRequests != 0 {
		f.serveOneIssue(w, repo, v.Number, v.Labels, v.SubIssues, v.BlockedBy, v.PullRequests, v.Checks, v.Reviews)
		return
	}
	if v.Number != 0 {
		f.serveLabelTimes(w, repo, v.Number, v.SubIssues, v.Events)
		return
	}

	after := 0
	if v.After != nil {
		after, _ = strconv.Atoi(*v.After)
	}
	var page []map[string]any
	hasNextPage := false
	for _, issue := range sortedIssues(repo) {
		if issue.Closed || !slices.Contains(issue.Labels, RequirementLabel) || issue.Number <= after {
			continue
		}
		if len(page) == v.First {
			hasNextPage = true
			break
		}
		page = append(page, f.issueNode(repo, issue, v.Labels, v.SubIssues, v.BlockedBy, v.PullRequests, v.Checks, v.Reviews))
	}
	endCursor := any(nil)
	if len(page) > 0 {
		endCursor = strconv.Itoa(page[len(page)-1]["number"].(int))
	}
	repository := map[string]any{
		"issues": map[string]any{
			"pageInfo": map[string]any{"hasNextPage": hasNextPage, "endCursor": endCursor},
			"nodes":    page,
		},
	}
	if v.RepositoryFiles {
		repository["defaultBranchRef"] = defaultBranchRefJSON(repo)
		repository["cuminConfig"] = blobJSON(repo, ".cumin/config.toml")
		repository["cuminRiskCriteria"] = blobJSON(repo, ".cumin/risk-criteria.md")
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{"repository": repository, "rateLimit": rateLimit(len(page))},
	})
}

// servePullRequests answers the second query of the poll: the open closing
// pull requests of each issue that the client names by its node id, in the
// order of the ids. An id that is not an issue is null, with an error, and
// more than 100 ids are an error with no data (measured on cumin-works on
// 2026-10-03). Official: Query.nodes.
func (f *Fake) servePullRequests(w http.ResponseWriter, ids []string, labels, pullRequests, checks, reviews int) {
	if len(ids) > 100 {
		writeJSON(w, http.StatusOK, map[string]any{
			"errors": []map[string]any{{"message": fmt.Sprintf("You may not provide more than 100 node ids; you provided %d.", len(ids))}},
		})
		return
	}
	nodes := []any{}
	var errs []map[string]any
	for _, id := range ids {
		var node any
		for _, repo := range f.repositories {
			for _, issue := range repo.Issues {
				if IssueNodeID(repo, issue.Number) == id {
					node = map[string]any{
						"__typename":                     "Issue",
						"number":                         issue.Number,
						"closedByPullRequestsReferences": closingPullRequests(repo, issue, labels, pullRequests, checks, reviews),
					}
				}
			}
		}
		if node == nil {
			errs = append(errs, map[string]any{"message": "Could not resolve to a node with the global id of '" + id + "'."})
		}
		nodes = append(nodes, node)
	}
	answer := map[string]any{"data": map[string]any{"nodes": nodes, "rateLimit": rateLimit(1)}}
	if errs != nil {
		answer["errors"] = errs
	}
	writeJSON(w, http.StatusOK, answer)
}

// serveOneIssue answers the query of one issue with the fields of the poll
// query. The query of a requirement issue carries the page size of the
// sub-issues, and asks for no pull request of the issue itself; the query of
// a sub-issue carries no such size, asks for no sub-issue, and asks for the
// state and the labels of the parent. Official: Repository.issue and
// Issue.parent.
func (f *Fake) serveOneIssue(w http.ResponseWriter, repo *Repository, number, labels, subIssues, blockedBy, pullRequests, checks, reviews int) {
	issue, ok := repo.Issues[number]
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"data":   map[string]any{"repository": map[string]any{"issue": nil}, "rateLimit": rateLimit(1)},
			"errors": []map[string]any{{"message": fmt.Sprintf("Could not resolve to an Issue with the number of %d.", number)}},
		})
		return
	}
	node := f.issueNode(repo, issue, labels, subIssues, blockedBy, pullRequests, checks, reviews)
	if subIssues == 0 {
		delete(node, "subIssues")
		node["parent"] = nil
		if parent, ok := repo.Issues[issue.Parent]; ok {
			node["parent"] = map[string]any{
				"number": parent.Number,
				"state":  state(parent.Closed),
				"labels": connection(parent.Labels, labels, func(name string) any { return map[string]any{"name": name} }),
			}
		}
	} else {
		delete(node, "closedByPullRequestsReferences")
	}
	var defaultBranch any
	if ref, ok := defaultBranchRefJSON(repo).(map[string]any); ok {
		defaultBranch = map[string]any{"name": ref["name"]}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{"repository": map[string]any{"defaultBranchRef": defaultBranch, "issue": node}, "rateLimit": rateLimit(1)},
	})
}

// serveLinked answers the query of the pull requests that are linked to
// close an issue, open, closed, and merged: every pull request whose Closes
// holds the issue. Official: Issue.closedByPullRequestsReferences with
// includeClosedPrs.
func (f *Fake) serveLinked(w http.ResponseWriter, repo *Repository, number, first int) {
	if _, ok := repo.Issues[number]; !ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"data":   map[string]any{"repository": map[string]any{"issue": nil}, "rateLimit": rateLimit(1)},
			"errors": []map[string]any{{"message": fmt.Sprintf("Could not resolve to an Issue with the number of %d.", number)}},
		})
		return
	}
	var linked []*PullRequest
	for _, pr := range sortedPullRequests(repo) {
		if slices.Contains(pr.Closes, number) {
			linked = append(linked, pr)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{"repository": map[string]any{"issue": map[string]any{
			"closedByPullRequestsReferences": connection(linked, first, func(pr *PullRequest) any {
				return map[string]any{"number": pr.Number, "merged": pr.Merged, "closed": pr.Closed}
			}),
		}}, "rateLimit": rateLimit(1)},
	})
}

// servePullRequestNote answers the query of one pull request for the
// follow-up note: the description and the review threads. Official:
// PullRequest.reviewThreads.
func (f *Fake) servePullRequestNote(w http.ResponseWriter, repo *Repository, number, threads int) {
	pr, ok := repo.PullRequests[number]
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"data":   map[string]any{"repository": map[string]any{"pullRequest": nil}, "rateLimit": rateLimit(1)},
			"errors": []map[string]any{{"message": fmt.Sprintf("Could not resolve to a PullRequest with the number of %d.", number)}},
		})
		return
	}
	node := map[string]any{
		"number": pr.Number,
		"merged": pr.Merged,
		"body":   pr.Body,
		"reviewThreads": connection(pr.Threads, threads, func(t ReviewThread) any {
			thread := map[string]any{"path": t.Path, "line": nil, "originalLine": nil}
			if t.Line != 0 {
				thread["line"] = t.Line
			}
			if t.OriginalLine != 0 {
				thread["originalLine"] = t.OriginalLine
			}
			thread["comments"] = connection(t.Comments, threads, func(c ReviewComment) any {
				var author any
				if c.Author != "" {
					typeName := "User"
					if c.AuthorIsBot {
						typeName = "Bot"
					}
					author = map[string]any{"__typename": typeName, "login": c.Author}
				}
				return map[string]any{"body": c.Body, "url": c.URL, "author": author}
			})
			return thread
		}),
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{"repository": map[string]any{"pullRequest": node}, "rateLimit": rateLimit(1)},
	})
}

// serveIssueComments answers the query of the newest comments of an issue,
// oldest first, with the author as GraphQL gives it.
func (f *Fake) serveIssueComments(w http.ResponseWriter, repo *Repository, number, last int, before *string) {
	_, isIssue := repo.Issues[number]
	_, isPullRequest := repo.PullRequests[number]
	if !isIssue && !isPullRequest {
		writeJSON(w, http.StatusOK, map[string]any{
			"data":   map[string]any{"repository": map[string]any{"issueOrPullRequest": nil}, "rateLimit": rateLimit(1)},
			"errors": []map[string]any{{"message": fmt.Sprintf("Could not resolve to an issue or pull request with the number of %d.", number)}},
		})
		return
	}
	list := repo.comments[number]
	end := len(list)
	if before != nil {
		end, _ = strconv.Atoi(*before)
	}
	start := max(0, end-last)
	list = list[start:end]
	nodes := []any{}
	for _, comment := range list {
		var author any
		if comment.Author != "" {
			typeName := "User"
			if comment.AuthorIsBot {
				typeName = "Bot"
			}
			author = map[string]any{"__typename": typeName, "login": comment.Author}
		}
		nodes = append(nodes, map[string]any{"createdAt": comment.At.UTC().Format(time.RFC3339Nano), "body": comment.Body, "author": author,
			"url": fmt.Sprintf("https://github.com/%s/%s/issues/%d#issuecomment-%d", repo.Owner, repo.Name, number, comment.ID)})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{"repository": map[string]any{"issueOrPullRequest": map[string]any{"comments": map[string]any{
			"pageInfo": map[string]any{"hasPreviousPage": start > 0, "startCursor": strconv.Itoa(start)},
			"nodes":    nodes,
		}}}, "rateLimit": rateLimit(1)},
	})
}

// SeedActor is the account of the label events that the fake answers for
// the labels that a test gave an issue without an event. On GitHub every
// label of an issue has an event, so the fake answers one for each such
// label: at the zero time, before every other event, by this account. Its
// permission is admin until a test sets another one, so it is an Owner.
const SeedActor = "seed-owner"

// withSeededLabelEvents returns the label events of the issue, with one
// event of SeedActor first for each label that the issue carries and that
// no event added.
func withSeededLabelEvents(issue *Issue) []LabelEvent {
	var list []LabelEvent
	for _, label := range issue.Labels {
		if !slices.ContainsFunc(issue.LabelEvents, func(e LabelEvent) bool { return e.Label == label }) {
			list = append(list, LabelEvent{Label: label, Actor: SeedActor, ActorType: "User"})
		}
	}
	return append(list, issue.LabelEvents...)
}

// serveLabelTimes answers the query of the label times and the query of
// the actor of a label: the newest label events of the issue and of each of
// its sub-issues, each with its actor. Official: the LabeledEvent of the
// timeline of an Issue.
func (f *Fake) serveLabelTimes(w http.ResponseWriter, repo *Repository, number, subIssues, events int) {
	issue, ok := repo.Issues[number]
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"data":   map[string]any{"repository": map[string]any{"issue": nil}, "rateLimit": rateLimit(1)},
			"errors": []map[string]any{{"message": fmt.Sprintf("Could not resolve to an Issue with the number of %d.", number)}},
		})
		return
	}
	timeline := func(issue *Issue) map[string]any {
		list := withSeededLabelEvents(issue)
		if len(list) > events {
			list = list[len(list)-events:]
		}
		nodes := []any{}
		for _, event := range list {
			actor := any(nil)
			if event.Actor != "" {
				actor = map[string]any{"__typename": event.ActorType, "login": event.Actor}
			}
			nodes = append(nodes, map[string]any{"createdAt": event.At.UTC().Format(time.RFC3339Nano), "label": map[string]any{"name": event.Label}, "actor": actor})
		}
		return map[string]any{"nodes": nodes}
	}
	var subs []*Issue
	for _, candidate := range sortedIssues(repo) {
		if candidate.Parent == number {
			subs = append(subs, candidate)
		}
	}
	node := map[string]any{
		"number":        number,
		"timelineItems": timeline(issue),
		"subIssues": connection(subs, subIssues, func(sub *Issue) any {
			return map[string]any{"number": sub.Number, "timelineItems": timeline(sub)}
		}),
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{"repository": map[string]any{"issue": node}, "rateLimit": rateLimit(1)},
	})
}

func (f *Fake) issueNode(repo *Repository, issue *Issue, labels, subIssues, blockedBy, pullRequests, checks, reviews int) map[string]any {
	node := map[string]any{
		"number": issue.Number,
		"id":     IssueNodeID(repo, issue.Number),
		"title":  issue.Title,
		"state":  state(issue.Closed),
		"closedAt": func() any {
			if !issue.Closed || issue.ClosedAt.IsZero() {
				return nil
			}
			return issue.ClosedAt.UTC().Format(time.RFC3339Nano)
		}(),
		"labels": connection(issue.Labels, labels, func(name string) any { return map[string]any{"name": name} }),
	}
	var subs []*Issue
	for _, candidate := range sortedIssues(repo) {
		if candidate.Parent == issue.Number {
			subs = append(subs, candidate)
		}
	}
	node["subIssues"] = connection(subs, subIssues, func(sub *Issue) any {
		return f.issueNode(repo, sub, labels, subIssues, blockedBy, pullRequests, checks, reviews)
	})
	node["blockedBy"] = connection(issue.BlockedBy, blockedBy, func(number int) any {
		closed := false
		if blocker, ok := repo.Issues[number]; ok {
			closed = blocker.Closed
		}
		return map[string]any{"number": number, "state": state(closed)}
	})
	// The poll query asks for no pull request; the queries of one issue do.
	if pullRequests != 0 {
		node["closedByPullRequestsReferences"] = closingPullRequests(repo, issue, labels, pullRequests, checks, reviews)
	}
	return node
}

// closingPullRequests is the connection of the open pull requests that
// close the issue. Without includeClosedPrs, GitHub lists the open pull
// requests only.
func closingPullRequests(repo *Repository, issue *Issue, labels, pullRequests, checks, reviews int) map[string]any {
	var closing []*PullRequest
	for _, pr := range sortedPullRequests(repo) {
		if !pr.Closed && slices.Contains(pr.Closes, issue.Number) {
			closing = append(closing, pr)
		}
	}
	return connection(closing, pullRequests, func(pr *PullRequest) any {
		return pullRequestNode(pr, labels, checks, reviews)
	})
}

// pullRequestNode is one pull request as GraphQL returns it: the author
// of an App is a Bot whose login has no "[bot]", and the rollup is null
// when the head commit has no check. The last commit is the head commit.
func pullRequestNode(pr *PullRequest, labels, checks, reviews int) map[string]any {
	mergeable := pr.Mergeable
	switch {
	case mergeable != "":
	case pr.Conflict:
		mergeable = "CONFLICTING"
	default:
		mergeable = "MERGEABLE"
	}
	node := map[string]any{
		"number":      pr.Number,
		"headRefOid":  pr.HeadCommit,
		"headRefName": pr.HeadBranch,
		"mergeable":   mergeable,
		"commits": map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{
			"oid": pr.HeadCommit, "committedDate": pr.HeadCommittedAt.UTC().Format(time.RFC3339Nano),
		}}}},
		"author":  nil,
		"labels":  connection(pr.Labels, labels, func(name string) any { return map[string]any{"name": name} }),
		"reviews": connection(pr.Reviews, reviews, reviewNode),
	}
	if pr.Author != "" {
		typeName := "User"
		if pr.AuthorIsBot {
			typeName = "Bot"
		}
		node["author"] = map[string]any{"__typename": typeName, "login": pr.Author}
	}
	node["statusCheckRollup"] = nil
	if len(pr.Checks) > 0 {
		node["statusCheckRollup"] = map[string]any{
			"contexts": connection(pr.Checks, checks, checkNode),
		}
	}
	return node
}

// reviewNode is one review as GraphQL returns it.
func reviewNode(review Review) any {
	node := map[string]any{"author": nil, "state": review.State, "submittedAt": nil, "url": review.URL, "commit": nil}
	if review.Author != "" {
		typeName := "User"
		if review.AuthorIsBot {
			typeName = "Bot"
		}
		node["author"] = map[string]any{"__typename": typeName, "login": review.Author}
	}
	if !review.SubmittedAt.IsZero() {
		node["submittedAt"] = review.SubmittedAt.UTC().Format(time.RFC3339Nano)
	}
	if review.Commit != "" {
		node["commit"] = map[string]any{"oid": review.Commit}
	}
	return node
}

// checkNode is one context of the rollup: a commit status or a check run.
func checkNode(check Check) any {
	if check.CommitStatus {
		return map[string]any{"__typename": "StatusContext", "context": check.Name, "state": check.State}
	}
	status := check.Status
	if status == "" {
		status = "COMPLETED"
	}
	node := map[string]any{
		"__typename": "CheckRun",
		"name":       check.Name,
		"status":     status,
		"conclusion": check.Conclusion,
		"checkSuite": nil,
	}
	if check.Integration != 0 {
		node["checkSuite"] = map[string]any{"app": map[string]any{"databaseId": check.Integration}}
	}
	return node
}

func sortedPullRequests(repo *Repository) []*PullRequest {
	pulls := make([]*PullRequest, 0, len(repo.PullRequests))
	for _, pr := range repo.PullRequests {
		pulls = append(pulls, pr)
	}
	slices.SortFunc(pulls, func(a, b *PullRequest) int { return a.Number - b.Number })
	return pulls
}

// defaultBranchRefJSON answers the default branch and the commit at its
// head. The head changes when a file changes, as a commit does.
func defaultBranchRefJSON(repo *Repository) any {
	if repo.DefaultBranch == "" {
		return nil
	}
	paths := slices.Sorted(maps.Keys(repo.Files))
	head := sha1.New()
	for _, path := range paths {
		file := repo.Files[path]
		fmt.Fprintf(head, "%s\x00%s\x00", path, file.Content)
	}
	return map[string]any{
		"name":   repo.DefaultBranch,
		"target": map[string]any{"oid": hex.EncodeToString(head.Sum(nil))},
	}
}

// blobJSON answers one file, or nil when the repository does not have it.
// The oid is the blob hash of git, so that it changes with the content.
func blobJSON(repo *Repository, path string) any {
	file, ok := repo.Files[path]
	if !ok {
		return nil
	}
	sum := sha1.Sum([]byte(fmt.Sprintf("blob %d\x00%s", len(file.Content), file.Content)))
	blob := map[string]any{
		"oid":         hex.EncodeToString(sum[:]),
		"byteSize":    len(file.Content),
		"isBinary":    file.Binary,
		"isTruncated": file.Truncated,
		"text":        any(file.Content),
	}
	if file.Binary {
		blob["text"] = nil
	}
	return blob
}

// connection builds one GraphQL connection with at most first nodes.
func connection[T any](items []T, first int, node func(T) any) map[string]any {
	nodes := []any{}
	for i, item := range items {
		if i == first {
			break
		}
		nodes = append(nodes, node(item))
	}
	return map[string]any{
		"pageInfo": map[string]any{"hasNextPage": len(items) > first},
		"nodes":    nodes,
	}
}

func sortedIssues(repo *Repository) []*Issue {
	issues := make([]*Issue, 0, len(repo.Issues))
	for _, issue := range repo.Issues {
		issues = append(issues, issue)
	}
	slices.SortFunc(issues, func(a, b *Issue) int { return a.Number - b.Number })
	return issues
}

func state(closed bool) string {
	if closed {
		return "CLOSED"
	}
	return "OPEN"
}

// rateLimit is a made-up rate limit: one point for each page.
func rateLimit(cost int) map[string]any {
	if cost < 1 {
		cost = 1
	}
	return map[string]any{"cost": cost, "remaining": 4000 - cost}
}

func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	defer r.Body.Close()
	return io.ReadAll(r.Body)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
