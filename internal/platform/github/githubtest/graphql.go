package githubtest

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

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
			// the follow-up note.
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
	if v.Number != 0 && v.Events != 0 {
		after := ""
		if v.After != nil {
			after = *v.After
		}
		f.serveLabelTimes(w, repo, v.Number, after, v.SubIssues, v.Events)
		return
	}
	if v.Number != 0 && v.After != nil {
		f.serveSubIssuePage(w, repo, v.Number, *v.After, v.Labels, v.SubIssues, v.BlockedBy, v.PullRequests, v.Checks, v.Reviews)
		return
	}
	if v.Number != 0 && v.PullRequests != 0 {
		f.serveOneIssue(w, repo, v.Number, v.Labels, v.SubIssues, v.BlockedBy, v.PullRequests, v.Checks, v.Reviews)
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

// serveSubIssuePage answers the query of a next page of the sub-issues of
// one issue: the sub-issues after the cursor, with their pull requests when
// the query asks for them. Official: Issue.subIssues with first and after.
func (f *Fake) serveSubIssuePage(w http.ResponseWriter, repo *Repository, number int, after string, labels, subIssues, blockedBy, pullRequests, checks, reviews int) {
	issue, ok := repo.Issues[number]
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"data":   map[string]any{"repository": map[string]any{"issue": nil}, "rateLimit": rateLimit(1)},
			"errors": []map[string]any{{"message": fmt.Sprintf("Could not resolve to an Issue with the number of %d.", number)}},
		})
		return
	}
	node := map[string]any{
		"number":    issue.Number,
		"subIssues": f.subIssues(repo, issue, after, labels, subIssues, blockedBy, pullRequests, checks, reviews),
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{"repository": map[string]any{"issue": node}, "rateLimit": rateLimit(1)},
	})
}

// subIssues is one page of the sub-issues of the issue: at most first of
// them after the cursor, which is the number of the last sub-issue of the
// page before, or empty for the first page.
func (f *Fake) subIssues(repo *Repository, issue *Issue, after string, labels, first, blockedBy, pullRequests, checks, reviews int) map[string]any {
	last, _ := strconv.Atoi(after)
	var subs []*Issue
	for _, candidate := range sortedIssues(repo) {
		if candidate.Parent == issue.Number && candidate.Number > last {
			subs = append(subs, candidate)
		}
	}
	page := connection(subs, first, func(sub *Issue) any {
		return f.issueNode(repo, sub, labels, first, blockedBy, pullRequests, checks, reviews)
	})
	endCursor := any(nil)
	if n := min(len(subs), first); n > 0 {
		endCursor = strconv.Itoa(subs[n-1].Number)
	}
	page["pageInfo"].(map[string]any)["endCursor"] = endCursor
	return page
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
// permission is admin until a test sets another one, so it is a Maintainer.
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
// its sub-issues, each with its actor. The sub-issues are one page: at most
// subIssues of them after the cursor, which is the number of the last
// sub-issue of the page before, or empty for the first page. Official: the
// LabeledEvent and the UnlabeledEvent of the timeline of an Issue.
func (f *Fake) serveLabelTimes(w http.ResponseWriter, repo *Repository, number int, after string, subIssues, events int) {
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
			node := map[string]any{"__typename": "LabeledEvent", "createdAt": event.At.UTC().Format(time.RFC3339Nano), "label": map[string]any{"name": event.Label}, "actor": actor}
			if event.Removed {
				node = map[string]any{"__typename": "UnlabeledEvent", "createdAt": node["createdAt"], "label": node["label"]}
			}
			nodes = append(nodes, node)
		}
		return map[string]any{"nodes": nodes}
	}
	last, _ := strconv.Atoi(after)
	var subs []*Issue
	for _, candidate := range sortedIssues(repo) {
		if candidate.Parent == number && candidate.Number > last {
			subs = append(subs, candidate)
		}
	}
	page := connection(subs, subIssues, func(sub *Issue) any {
		return map[string]any{"number": sub.Number, "timelineItems": timeline(sub)}
	})
	endCursor := any(nil)
	if n := min(len(subs), subIssues); n > 0 {
		endCursor = strconv.Itoa(subs[n-1].Number)
	}
	page["pageInfo"].(map[string]any)["endCursor"] = endCursor
	node := map[string]any{
		"number":        number,
		"timelineItems": timeline(issue),
		"subIssues":     page,
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
	node["subIssues"] = f.subIssues(repo, issue, "", labels, subIssues, blockedBy, pullRequests, checks, reviews)
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
