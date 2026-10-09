package githubtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

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
	// Labels are the labels of the pull request. "copy the labels to the
	// pull request" makes them equal to the labels of the issue.
	Labels []string
	// Checks are the checks on the head commit, as statusCheckRollup
	// returns them.
	Checks []Check
	// Reviews are the reviews of the pull request, oldest first.
	Reviews []Review
	// Body is the description of the pull request.
	Body string
	// Threads are the review threads, as the follow-up note reads
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
	// RequestedReviewers are the logins whose review is requested, each
	// once, in the order of the first request.
	RequestedReviewers []string
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

// CloseIssuesOnMerge makes a merge close the issues that the pull request
// closes, as GitHub does when it closes them through the link. Without it,
// a merge leaves them open, as GitHub did from 2026-09-30.
func (f *Fake) CloseIssuesOnMerge() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeOnMerge = true
}

// RefuseMergesForBaseBranch makes the next merges answer 405 "Base branch
// was modified. Review and try the merge again.", as GitHub does right
// after another merge moved the base branch. Nothing is merged.
func (f *Fake) RefuseMergesForBaseBranch(times int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.baseModified = times
}

// MergeTimes returns the time at which the fake received each merge
// request, oldest first, on the clock of the machine. The clock of SetClock
// does not move during a poll, so it cannot show a wait between two merges.
func (f *Fake) MergeTimes() []time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.mergeTimes)
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

// RequestedReviewers returns a copy of the logins whose review is requested
// on one pull request.
func (f *Fake) RequestedReviewers(r *Repository, number int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if pr, ok := r.PullRequests[number]; ok {
		return slices.Clone(pr.RequestedReviewers)
	}
	return nil
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

// serveRequestReviewers answers POST
// /repos/{owner}/{repo}/pulls/{number}/requested_reviewers (official:
// "Request reviewers for a pull request") with 201. A login that is
// requested already stays listed once (measured in #510, V2). A login that
// is not a collaborator answers 422 and requests nobody (V3): in the fake,
// a collaborator is SeedActor or a login that SetPermission gave a
// permission other than none.
func (f *Fake) serveRequestReviewers(w http.ResponseWriter, body []byte, owner, name string, number int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	repo, ok := f.repository(w, owner, name)
	if !ok {
		return
	}
	pr, ok := repo.PullRequests[number]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return
	}
	var request struct {
		Reviewers []string `json:"reviewers"`
	}
	if err := json.Unmarshal(body, &request); err != nil || len(request.Reviewers) == 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"message": "Validation Failed"})
		return
	}
	for _, login := range request.Reviewers {
		if p, set := f.permissions[login]; login != SeedActor && (!set || p.Permission == "none") {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"message": "Reviews may only be requested from collaborators. " +
				"One or more of the users or teams you specified is not a collaborator of the " + owner + "/" + name + " repository."})
			return
		}
	}
	for _, login := range request.Reviewers {
		if !slices.Contains(pr.RequestedReviewers, login) {
			pr.RequestedReviewers = append(pr.RequestedReviewers, login)
		}
	}
	requested := make([]map[string]any, 0, len(pr.RequestedReviewers))
	for _, login := range pr.RequestedReviewers {
		requested = append(requested, map[string]any{"login": login})
	}
	writeJSON(w, http.StatusCreated, map[string]any{"number": number, "requested_reviewers": requested})
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
	f.mergeTimes = append(f.mergeTimes, time.Now())
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
	case f.baseModified > 0:
		f.baseModified--
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"message": "Base branch was modified. Review and try the merge again."})
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
