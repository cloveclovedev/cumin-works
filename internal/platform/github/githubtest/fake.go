// Package githubtest is a fake GitHub for the acceptance tests of cumin. The
// real client of internal/platform/github talks to it over HTTP. It has only
// the endpoints that a test needs, and keeps its state in memory.
//
// docs/ja/designs/cumin-core.md, topic "Two layers of tests".
package githubtest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

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
	// labelWriter is the actor of the label events of a label change, as
	// SetLabelWriter set it.
	labelWriter string
	users       map[string]int64
	requests    []Request
	// received is closed, and replaced, each time a request arrives, so
	// that WaitForRequests wakes.
	received chan struct{}
	failNext *failure
	// beforeAnswer is what BeforeNextAnswer set.
	beforeAnswer *answerHook
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
	// baseModified is how many merges the fake still refuses with 405
	// "Base branch was modified", as RefuseMergesForBaseBranch set it.
	baseModified int
	// mergeTimes holds the time of each merge request, as MergeTimes
	// returns them.
	mergeTimes []time.Time
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
	// dropped answers the request as usual and then closes the connection
	// without the answer, as DropAnswers set it.
	dropped bool
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
	server := httptest.NewServer(http.HandlerFunc(f.serveWithHook))
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

// DropAnswers makes the fake handle the next times requests with the
// method and the path as usual, and then close the connection without the
// answer: the write reached GitHub, and its answer got lost. The requests
// are recorded, and they change the state. A failure of FailTimes or
// CloseTimes that still waits comes first.
func (f *Fake) DropAnswers(method, path string, times int) {
	f.failThen(&failure{method: method, path: path, dropped: true, times: times})
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

// answerHook is a function that runs between the answer of one request and
// its way to the client.
type answerHook struct {
	method, path string
	run          func()
}

// BeforeNextAnswer makes the fake call run once: after it built the answer
// of the next request with the method and the path, and before it sends
// that answer. The client then gets facts that are already old, as when
// something changes on GitHub while an answer is on its way.
func (f *Fake) BeforeNextAnswer(method, path string, run func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.beforeAnswer = &answerHook{method: method, path: path, run: run}
}

// serveWithHook answers a request, and runs the hook of BeforeNextAnswer
// between the answer and its way to the client.
func (f *Fake) serveWithHook(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	hook := f.beforeAnswer
	if hook != nil && hook.method == r.Method && hook.path == r.URL.Path {
		f.beforeAnswer = nil
	} else {
		hook = nil
	}
	f.mu.Unlock()
	if hook == nil {
		f.serve(w, r)
		return
	}
	answer := httptest.NewRecorder()
	f.serve(answer, r)
	hook.run()
	for name, values := range answer.Header() {
		w.Header()[name] = values
	}
	w.WriteHeader(answer.Code)
	_, _ = w.Write(answer.Body.Bytes())
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

// droppedAnswer takes the answer of a request whose answer gets lost.
type droppedAnswer struct{}

func (droppedAnswer) Header() http.Header         { return http.Header{} }
func (droppedAnswer) Write(b []byte) (int, error) { return len(b), nil }
func (droppedAnswer) WriteHeader(int)             {}

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
	if fail != nil && fail.dropped {
		// The handlers below change the state. Their answer goes nowhere,
		// and net/http closes the connection without an answer.
		w = droppedAnswer{}
		fail = nil
		defer panic(http.ErrAbortHandler)
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
	reviewRequest := reviewRequestPath.FindStringSubmatch(r.URL.Path)
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
	case r.Method == http.MethodPost && reviewRequest != nil:
		number, _ := strconv.Atoi(reviewRequest[3])
		f.serveRequestReviewers(w, body, reviewRequest[1], reviewRequest[2], number)
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
	reviewRequestPath = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/pulls/(\d+)/requested_reviewers$`)
	pullsPath         = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/pulls$`)
	pullPath          = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/pulls/(\d+)$`)
	mergePath         = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/pulls/(\d+)/merge$`)
	issuePath         = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/issues/(\d+)$`)
	permissionPath    = regexp.MustCompile(`^/repos/([^/]+)/([^/]+)/collaborators/([^/]+)/permission$`)
	// moveHeadPath is not an endpoint of GitHub. A fake agent run calls it
	// to move the head of a pull request while it runs, as a push would.
	moveHeadPath = regexp.MustCompile(`^/_fake/repos/([^/]+)/([^/]+)/pulls/(\d+)/head$`)
)

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

// repository returns the repository, or answers 404.
func (f *Fake) repository(w http.ResponseWriter, owner, name string) (*Repository, bool) {
	repo, ok := f.repositories[key(owner, name)]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
	}
	return repo, ok
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
