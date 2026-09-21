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
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Token is the installation token that the fake accepts.
const Token = "ghs_fakeInstallationToken"

// RequirementLabel marks a requirement issue, as in issue-states.md.
const RequirementLabel = "cumin/type/requirement"

// Issue is one issue of the fake repository.
type Issue struct {
	Number int
	Closed bool
	Labels []string
	// Parent is the number of the parent issue, or 0.
	Parent int
	// BlockedBy holds the numbers of the issues that block this one.
	BlockedBy []int
}

// Repository is one repository of the fake.
type Repository struct {
	Owner, Name string
	Issues      map[int]*Issue
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
	requests     []Request
}

// New starts the fake. The server closes when the test ends.
func New(t *testing.T) (*Fake, *httptest.Server) {
	t.Helper()
	f := &Fake{t: t, repositories: map[string]*Repository{}}
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	return f, server
}

// AddRepository adds an empty repository.
func (f *Fake) AddRepository(owner, name string) *Repository {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := &Repository{Owner: owner, Name: name, Issues: map[int]*Issue{}}
	f.repositories[key(owner, name)] = r
	return r
}

// AddIssue adds an issue to the repository. The fake keeps the pointer, so a
// test can change the issue later.
func (f *Fake) AddIssue(r *Repository, issue *Issue) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r.Issues[issue.Number] = issue
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
	return &copied
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

func key(owner, name string) string { return strings.ToLower(owner + "/" + name) }

func (f *Fake) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := readBody(r)
	f.mu.Lock()
	f.requests = append(f.requests, Request{Method: r.Method, Path: r.URL.Path, Body: body})
	f.mu.Unlock()

	if r.Header.Get("Authorization") != "Bearer "+Token {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"message": "Bad credentials"})
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/graphql":
		f.serveGraphQL(w, body)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
	}
}

// serveGraphQL answers the snapshot query of the client. It does not parse
// the query text: it answers from the variables, in the shape that the
// client expects. The cursor is the number of the last issue of the page.
func (f *Fake) serveGraphQL(w http.ResponseWriter, body []byte) {
	var request struct {
		Query     string `json:"query"`
		Variables struct {
			Owner     string  `json:"owner"`
			Name      string  `json:"name"`
			First     int     `json:"first"`
			After     *string `json:"after"`
			SubIssues int     `json:"subIssues"`
			Labels    int     `json:"labels"`
			BlockedBy int     `json:"blockedBy"`
		} `json:"variables"`
	}
	if err := json.Unmarshal(body, &request); err != nil || !strings.Contains(request.Query, "rateLimit") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"message": "Problems parsing JSON"})
		return
	}
	v := request.Variables

	f.mu.Lock()
	defer f.mu.Unlock()
	repo, ok := f.repositories[key(v.Owner, v.Name)]
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"data":   map[string]any{"repository": nil, "rateLimit": rateLimit(1)},
			"errors": []map[string]any{{"message": "Could not resolve to a Repository with the name '" + v.Owner + "/" + v.Name + "'."}},
		})
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
		page = append(page, f.issueNode(repo, issue, v.Labels, v.SubIssues, v.BlockedBy))
	}
	endCursor := any(nil)
	if len(page) > 0 {
		endCursor = strconv.Itoa(page[len(page)-1]["number"].(int))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"repository": map[string]any{
				"issues": map[string]any{
					"pageInfo": map[string]any{"hasNextPage": hasNextPage, "endCursor": endCursor},
					"nodes":    page,
				},
			},
			"rateLimit": rateLimit(len(page)),
		},
	})
}

func (f *Fake) issueNode(repo *Repository, issue *Issue, labels, subIssues, blockedBy int) map[string]any {
	node := map[string]any{
		"number": issue.Number,
		"state":  state(issue.Closed),
		"labels": connection(issue.Labels, labels, func(name string) any { return map[string]any{"name": name} }),
	}
	var subs []*Issue
	for _, candidate := range sortedIssues(repo) {
		if candidate.Parent == issue.Number {
			subs = append(subs, candidate)
		}
	}
	node["subIssues"] = connection(subs, subIssues, func(sub *Issue) any {
		return f.issueNode(repo, sub, labels, subIssues, blockedBy)
	})
	node["blockedBy"] = connection(issue.BlockedBy, blockedBy, func(number int) any {
		closed := false
		if blocker, ok := repo.Issues[number]; ok {
			closed = blocker.Closed
		}
		return map[string]any{"number": number, "state": state(closed)}
	})
	return node
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
