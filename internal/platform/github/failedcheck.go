package github

// This file reads what a failed check says, for the request of I4 (a
// required check failed, fix it in the same session). Row 54 of
// measured-constraints.md holds what an installation token can read of a
// failed GitHub Actions check in a public repository: the annotations of
// the check run, and the log of its job, whose id is the last part of the
// details address of the check run.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

// Limits of the text that FailedCheckContent returns. They are constants of
// the code, not settings: the settings table of the requirement does not
// list them. The request of I4 carries the text, so it stays small enough
// to read.
const (
	// checkContentLimit is the longest text for one failed check.
	checkContentLimit = 4000
	// logTailBytes is how much of the end of a job log the text holds. The
	// failure of a job is at its end.
	logTailBytes = 2000
	// logReadLimit is how much of a job log cumin reads to find that end. A
	// log of a long job is larger than anything worth keeping.
	logReadLimit = 8 << 20
	// annotationPage is the page size of the annotations of a check run.
	annotationPage = 100
	// checkRunPage is the page size of the check runs of a commit.
	checkRunPage = 100
)

// FailedCheckContent returns, for each name, what the failed check says on
// the commit: its failure annotations, and then the end of the log of its
// job. One text is at most checkContentLimit characters, and a text that
// was cut says so.
//
// Nothing here is an error for the caller. A check that cumin cannot read
// (a commit status, a missing permission, a job that is gone) gives a text
// that names the check and says that its content could not be read, and one
// line goes to the log. The request of I4 is worth sending with the names
// alone.
//
// Official: REST "List check runs for a Git reference", "List check run
// annotations", and "Download job logs for a workflow run" (a redirect to a
// plain text file).
func (c *AppClient) FailedCheckContent(ctx context.Context, token, owner, repo, sha string, names []string, logger *slog.Logger) map[string]string {
	if logger == nil {
		logger = slog.Default()
	}
	content := make(map[string]string, len(names))
	if len(names) == 0 {
		return content
	}
	runs, err := c.checkRuns(ctx, token, owner, repo, sha)
	if err != nil {
		logger.Warn("the check runs of the commit were not read", "error", err.Error())
		for _, name := range names {
			content[name] = contentNotRead(name)
		}
		return content
	}
	for _, name := range names {
		run, ok := runs[name]
		if !ok {
			// A commit status has no check run, so it has neither
			// annotations nor a job log.
			content[name] = contentNotRead(name)
			continue
		}
		content[name] = c.contentOf(ctx, token, owner, repo, name, run, logger)
	}
	return content
}

// contentNotRead is the text of a check whose content cumin could not read.
func contentNotRead(name string) string {
	return fmt.Sprintf("Check %q failed. cumin could not read its content; open the check on GitHub.", name)
}

// checkRun is one check run of a commit, as much as the content needs.
type checkRun struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
	// DetailsURL ends with the id of the job of GitHub Actions (row 54).
	DetailsURL string `json:"details_url"`
}

// checkRuns returns the check runs of a commit, by name. When a name has
// more than one run, the one with the highest id is kept: it is the newest.
func (c *AppClient) checkRuns(ctx context.Context, token, owner, repo, sha string) (map[string]checkRun, error) {
	base := fmt.Sprintf("/repos/%s/%s/commits/%s/check-runs",
		url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(sha))
	runs := map[string]checkRun{}
	for page := 1; ; page++ {
		var answer struct {
			CheckRuns []checkRun `json:"check_runs"`
		}
		path := fmt.Sprintf("%s?per_page=%d&page=%d", base, checkRunPage, page)
		if err := c.do(ctx, token, http.MethodGet, path, base, nil, http.StatusOK, &answer); err != nil {
			return nil, fmt.Errorf("github: read the check runs of %s/%s@%s: %w", owner, repo, short(sha), err)
		}
		for _, run := range answer.CheckRuns {
			if kept, ok := runs[run.Name]; !ok || run.ID > kept.ID {
				runs[run.Name] = run
			}
		}
		if len(answer.CheckRuns) < checkRunPage {
			return runs, nil
		}
	}
}

// contentOf is the text of one failed check run.
func (c *AppClient) contentOf(ctx context.Context, token, owner, repo, name string, run checkRun, logger *slog.Logger) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Check %q failed.\n", name)

	annotations, err := c.annotations(ctx, token, owner, repo, run.ID)
	if err != nil {
		logger.Warn("the annotations of a failed check were not read", "check", name, "error", err.Error())
	}
	if len(annotations) > 0 {
		b.WriteString("\nAnnotations:\n")
		for _, note := range annotations {
			fmt.Fprintf(&b, "- %s: %s\n", note.Path, strings.TrimSpace(note.Message))
		}
	}

	tail, err := c.jobLogTail(ctx, token, owner, repo, run.DetailsURL)
	if err != nil {
		logger.Warn("the job log of a failed check was not read", "check", name, "error", err.Error())
	}
	if tail != "" {
		b.WriteString("\nThe end of the job log:\n")
		b.WriteString(tail)
		if !strings.HasSuffix(tail, "\n") {
			b.WriteString("\n")
		}
	}
	if len(annotations) == 0 && tail == "" {
		return contentNotRead(name)
	}
	return cut(b.String(), checkContentLimit)
}

// annotation is one failure annotation of a check run.
type annotation struct {
	Path    string `json:"path"`
	Level   string `json:"annotation_level"`
	Message string `json:"message"`
}

// annotations returns the failure annotations of a check run. Annotations
// of another level (notice, warning) are left out: the request of I4 is
// about what failed.
func (c *AppClient) annotations(ctx context.Context, token, owner, repo string, id int64) ([]annotation, error) {
	path := fmt.Sprintf("/repos/%s/%s/check-runs/%d/annotations?per_page=%d",
		url.PathEscape(owner), url.PathEscape(repo), id, annotationPage)
	label := fmt.Sprintf("/repos/%s/%s/check-runs/{id}/annotations", owner, repo)
	var read []annotation
	if err := c.do(ctx, token, http.MethodGet, path, label, nil, http.StatusOK, &read); err != nil {
		return nil, err
	}
	var failures []annotation
	for _, note := range read {
		if note.Level == "failure" {
			failures = append(failures, note)
		}
	}
	return failures, nil
}

// jobLogTail returns the end of the log of the job of a check run. The job
// id is the last part of the details address of the check run (row 54). A
// check run of another kind of App has no job, and then the tail is empty.
func (c *AppClient) jobLogTail(ctx context.Context, token, owner, repo, detailsURL string) (string, error) {
	id := jobID(detailsURL)
	if id == "" {
		return "", nil
	}
	path := fmt.Sprintf("/repos/%s/%s/actions/jobs/%s/logs",
		url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(id))
	label := fmt.Sprintf("/repos/%s/%s/actions/jobs/{id}/logs", owner, repo)
	raw, err := c.text(ctx, token, path, label)
	if err != nil {
		return "", err
	}
	return tailLines(raw), nil
}

// jobID is the last part of the details address of a check run of GitHub
// Actions (".../runs/<run id>/job/<job id>"), or an empty string when the
// address does not end in a number.
func jobID(detailsURL string) string {
	last := detailsURL[strings.LastIndex(detailsURL, "/")+1:]
	if last == "" {
		return ""
	}
	for _, r := range last {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return last
}

// text reads the body of a response as text, up to logReadLimit, and keeps
// the last logTailBytes of it. The log of a job is plain text behind a
// redirect; Go follows it and drops the Authorization header on the way to
// another host.
func (c *AppClient) text(ctx context.Context, token, path, label string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return "", fmt.Errorf("GET %s: cannot build the request", label)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "cumin-works")
	resp, err := c.http.Do(req)
	if err != nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return "", fmt.Errorf("GET %s: %w", label, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: status %d", label, resp.StatusCode)
	}
	// The end of the log is what says why the job failed, so the read
	// keeps the last bytes and throws the rest away as it goes.
	var tail []byte
	buf := make([]byte, 32<<10)
	total := 0
	for total < logReadLimit {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			total += n
			tail = append(tail, buf[:n]...)
			if len(tail) > logTailBytes {
				tail = tail[len(tail)-logTailBytes:]
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", fmt.Errorf("GET %s: %w", label, err)
		}
	}
	return string(tail), nil
}

// tailLines drops a first line that the tail cut in the middle, so that the
// text starts at a line.
func tailLines(raw string) string {
	raw = strings.TrimRight(raw, "\n")
	if raw == "" {
		return ""
	}
	if i := strings.Index(raw, "\n"); i >= 0 && len(raw) >= logTailBytes {
		raw = raw[i+1:]
	}
	return raw
}

// cut shortens a text to limit characters and says that it was cut.
func cut(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	const note = "\n[cut by cumin]\n"
	keep := limit - len(note)
	return text[:keep] + note
}

// short is the first seven characters of a commit, for an error.
func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
