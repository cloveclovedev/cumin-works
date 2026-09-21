package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Label is one label of a repository, as cumin creates it. Color is the
// hexadecimal code without the leading "#", as the REST API takes it.
type Label struct {
	Name        string
	Color       string
	Description string
}

// labelPage is the page size of "List labels for a repository" (1 to 100).
const labelPage = 100

// EnsureLabels creates each label whose name the repository does not have,
// and returns the names that it created. GitHub label names ignore case, so
// the comparison does too. A label that exists with another color or
// description is left as it is.
//
// Official: REST "List labels for a repository" (GET .../labels, 200) and
// "Create a label" (POST .../labels, 201).
func (c *AppClient) EnsureLabels(ctx context.Context, token, owner, repo string, labels []Label) ([]string, error) {
	base := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/labels"
	existing := map[string]bool{}
	for page := 1; ; page++ {
		var got []struct {
			Name string `json:"name"`
		}
		path := fmt.Sprintf("%s?per_page=%d&page=%d", base, labelPage, page)
		if err := c.do(ctx, token, http.MethodGet, path, base, nil, http.StatusOK, &got); err != nil {
			return nil, fmt.Errorf("github: list the labels of %s/%s: %w", owner, repo, err)
		}
		for _, label := range got {
			existing[strings.ToLower(label.Name)] = true
		}
		if len(got) < labelPage {
			break
		}
	}

	var created []string
	for _, label := range labels {
		if existing[strings.ToLower(label.Name)] {
			continue
		}
		request := struct {
			Name        string `json:"name"`
			Color       string `json:"color"`
			Description string `json:"description"`
		}{label.Name, label.Color, label.Description}
		var ignored struct{}
		if err := c.do(ctx, token, http.MethodPost, base, base, request, http.StatusCreated, &ignored); err != nil {
			return created, fmt.Errorf("github: create the label %q in %s/%s: %w", label.Name, owner, repo, err)
		}
		created = append(created, label.Name)
	}
	return created, nil
}

// SetIssueLabels replaces every label of the issue with names.
//
// Official: REST "Set labels for an issue" (PUT .../issues/{n}/labels, 200):
// "Removes any previous labels and sets the new labels for an issue."
func (c *AppClient) SetIssueLabels(ctx context.Context, token, owner, repo string, number int, names []string) error {
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/labels", url.PathEscape(owner), url.PathEscape(repo), number)
	request := struct {
		Labels []string `json:"labels"`
	}{names}
	if request.Labels == nil {
		request.Labels = []string{}
	}
	var ignored []struct{}
	if err := c.do(ctx, token, http.MethodPut, path, path, request, http.StatusOK, &ignored); err != nil {
		return fmt.Errorf("github: set the labels of %s/%s#%d: %w", owner, repo, number, err)
	}
	return nil
}
