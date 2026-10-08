package githubtest

import (
	"fmt"
	"net/http"
	"net/url"
)

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
