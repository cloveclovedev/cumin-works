package githubtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

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
	// LabelEvents are the times at which labels were added and removed,
	// oldest first, as GitHub records them in the timeline. A test adds the
	// events of the past; the fake adds one for each label that "Set
	// labels" adds or removes.
	LabelEvents []LabelEvent
}

// LabelEvent is one LabeledEvent of the timeline of an issue, or one
// UnlabeledEvent when Removed is true. Actor is the login of the account
// that added the label and ActorType its type in GraphQL ("User", "Bot");
// an empty Actor answers a null actor. GitHub can record a LabeledEvent
// for a label that is already on the issue (measured on 2026-10-05), so a
// test may repeat a label with no Removed event in between.
type LabelEvent struct {
	Label     string
	At        time.Time
	Actor     string
	ActorType string
	Removed   bool
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

// SetCommentAuthor makes the comments that cumin creates through the REST
// API carry the App with the slug as their author, as GitHub does for an
// installation token. Without it they have no author.
func (f *Fake) SetCommentAuthor(slug string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commentAuthor = slug
}

// SetLabelWriter names the GitHub App that the label events of a label
// change carry as their actor: the slug, without "[bot]", as GraphQL gives
// it. Without it, such an event has no actor.
func (f *Fake) SetLabelWriter(slug string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.labelWriter = slug
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
	copied.LabelEvents = slices.Clone(issue.LabelEvents)
	return &copied
}

// SetLabels replaces the labels of an issue or of a pull request, as a
// Maintainer does by hand on GitHub. Issues and pull requests share one
// sequence of numbers on GitHub, so the number names one of them.
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
// endpoint, so the number may name a pull request too
// ("copy the labels to the pull request"). Official: "Set labels for an
// issue", "Every pull request is an issue".
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
		for _, label := range issue.Labels {
			if !slices.Contains(*request.Labels, label) {
				issue.LabelEvents = append(issue.LabelEvents, LabelEvent{Label: label, At: now, Removed: true})
			}
		}
		for _, label := range *request.Labels {
			if !slices.Contains(issue.Labels, label) {
				event := LabelEvent{Label: label, At: now}
				if f.labelWriter != "" {
					event.Actor, event.ActorType = f.labelWriter, "Bot"
				}
				issue.LabelEvents = append(issue.LabelEvents, event)
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
	// tests only an agent comments on a pull request (the Reviewer of
	// "request the cause"), and cumin comments on issues; so a comment on a
	// pull request is by the App of the fake.
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

func labelJSON(label Label) map[string]any {
	return map[string]any{"name": label.Name, "color": label.Color, "description": label.Description}
}
