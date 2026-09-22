package github

// cumin writes the reason of a stop as a comment on the implementation
// issue: the blocked_reason of an agent (I2, I10), and its own note when a
// check fails (I2, I4, I8). This file holds that one write.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// IssueComment is one comment that cumin created.
type IssueComment struct {
	// ID is the id of the comment in the REST API.
	ID int64
	// URL is the address of the comment on GitHub, for a log or a
	// notification.
	URL string
}

// CreateIssueComment writes one comment on an issue and returns it. The
// body is Markdown, as the Owner reads it on GitHub.
//
// Official: REST "Create an issue comment"
// (POST /repos/{owner}/{repo}/issues/{issue_number}/comments, 201). The
// App of cumin-core has the write permission for issues.
func (c *AppClient) CreateIssueComment(ctx context.Context, token, owner, repo string, number int, body string) (IssueComment, error) {
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments", url.PathEscape(owner), url.PathEscape(repo), number)
	request := struct {
		Body string `json:"body"`
	}{body}
	var created struct {
		ID      int64  `json:"id"`
		HTMLURL string `json:"html_url"`
	}
	if err := c.do(ctx, token, http.MethodPost, path, path, request, http.StatusCreated, &created); err != nil {
		return IssueComment{}, fmt.Errorf("github: comment on %s/%s#%d: %w", owner, repo, number, err)
	}
	return IssueComment{ID: created.ID, URL: created.HTMLURL}, nil
}

// webHost is the address of GitHub for a person, as against the API. A
// link in a notification or in a log opens there.
const webHost = "https://github.com"

// IssueURL is the address of one issue on GitHub. cumin uses it for the
// link of a notification when it has no address of its own to point at.
func IssueURL(owner, repo string, number int) string {
	return fmt.Sprintf("%s/%s/%s/issues/%d", webHost, url.PathEscape(owner), url.PathEscape(repo), number)
}

// RepositoryURL is the address of one repository on GitHub, for a
// notification that is about the repository and not about one issue.
func RepositoryURL(owner, repo string) string {
	return fmt.Sprintf("%s/%s/%s", webHost, url.PathEscape(owner), url.PathEscape(repo))
}
