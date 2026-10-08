package github

// This file is the GraphQL text of the snapshot and the shape of its
// answer: the query constants, and the response nodes with the methods
// that convert a node to an exported type. snapshot.go holds the exported
// types and the Read methods that send the queries.

import (
	"errors"
	"fmt"
	"time"
)

// snapshotQuery reads the open issues with the requirement label, their
// sub-issues with the title, the state of the blocked-by issues, and the
// files of .cumin/ on the default branch. It stops at the sub-issue: the
// open pull requests that close a sub-issue come from pullRequestsQuery. The field names come from the design note and
// measured-constraints.md row 55, and were checked against the schema by
// introspection on 2026-09-21 and on the sandbox on 2026-09-22
// (closedByPullRequestsReferences, Repository.object, and Blob).
// PullRequest.mergeable and Commit.committedDate were checked by
// introspection on 2026-10-03. Commit.pushedDate is no longer supported, and
// PullRequest.headRef was null for open pull requests on cumin-works, so the
// head commit is read as the last node of PullRequest.commits.
//
// "HEAD:" is the default branch of the repository, so the files never come
// from a pull request branch. The files are not a connection, so they do not
// change the cost of the query; $repositoryFiles asks for them on the first
// page only, because one poll reads them once.
const snapshotQuery = `query($owner: String!, $name: String!, $first: Int!, $after: String, $subIssues: Int!, $labels: Int!, $blockedBy: Int!, $repositoryFiles: Boolean!) {
  repository(owner: $owner, name: $name) {
    defaultBranchRef @include(if: $repositoryFiles) { name target { oid } }
    cuminConfig: object(expression: "HEAD:` + CuminConfigPath + `") @include(if: $repositoryFiles) { ...cuminFile }
    cuminRiskCriteria: object(expression: "HEAD:` + CuminRiskCriteriaPath + `") @include(if: $repositoryFiles) { ...cuminFile }
    issues(states: [OPEN], labels: ["` + requirementLabel + `"], first: $first, after: $after) {
      pageInfo { hasNextPage endCursor }
      nodes { ...requirementIssueFields }
    }
  }
  rateLimit { cost remaining }
}

fragment cuminFile on GitObject {
  ... on Blob { oid text byteSize isBinary isTruncated }
}
` + requirementIssueFields + subIssueFields

// pullRequestsQuery is the second query of a poll: the open pull requests
// that close each of the named sub-issues. The caller names the sub-issues
// by the id that the poll query read. Official: Query.nodes, and
// Issue.closedByPullRequestsReferences.
const pullRequestsQuery = `query($ids: [ID!]!, $labels: Int!, $pullRequests: Int!, $checks: Int!, $reviews: Int!) {
  nodes(ids: $ids) {
    __typename
    ... on Issue { number ...closingPullRequestFields }
  }
  rateLimit { cost remaining }
}
` + closingPullRequestFields

// requirementIssueFields, subIssueFields, and closingPullRequestFields are
// the fields of an issue that cumin reads. The two queries of the poll and
// the queries of one issue are built from these fragments, so that a rule
// gets the same facts from either read.
const requirementIssueFields = `
fragment requirementIssueFields on Issue {
  number
  title
  state
  labels(first: $labels) { pageInfo { hasNextPage } nodes { name } }
  blockedBy(first: $blockedBy) { pageInfo { hasNextPage } nodes { number state } }
  subIssues(first: $subIssues) {
    pageInfo { hasNextPage endCursor }
    nodes { ...subIssueFields }
  }
}
`

// requirementIssueWithPullRequestsFields is requirementIssueFields with the
// pull requests of each sub-issue, for the read of one requirement issue.
const requirementIssueWithPullRequestsFields = `
fragment requirementIssueFields on Issue {
  number
  title
  state
  labels(first: $labels) { pageInfo { hasNextPage } nodes { name } }
  blockedBy(first: $blockedBy) { pageInfo { hasNextPage } nodes { number state } }
  subIssues(first: $subIssues) {
    pageInfo { hasNextPage endCursor }
    nodes { ...subIssueFields ...closingPullRequestFields }
  }
}
`

const subIssueFields = `
fragment subIssueFields on Issue {
  number
  id
  title
  state
  closedAt
  labels(first: $labels) { pageInfo { hasNextPage } nodes { name } }
  blockedBy(first: $blockedBy) { pageInfo { hasNextPage } nodes { number state } }
}
`

const closingPullRequestFields = `
fragment closingPullRequestFields on Issue {
  closedByPullRequestsReferences(first: $pullRequests) {
    pageInfo { hasNextPage }
    nodes {
      number
      headRefOid
      headRefName
      mergeable
      commits(last: 1) { nodes { commit { oid committedDate } } }
      author { __typename login }
      labels(first: $labels) { pageInfo { hasNextPage } nodes { name } }
      statusCheckRollup {
        contexts(first: $checks) {
          pageInfo { hasNextPage }
          nodes {
            __typename
            ... on CheckRun { name status conclusion checkSuite { app { databaseId } } }
            ... on StatusContext { context state }
          }
        }
      }
      reviews(first: $reviews) {
        pageInfo { hasNextPage }
        nodes { author { __typename login } state submittedAt url commit { oid } }
      }
    }
  }
}
`

// requirementIssueQuery and subIssueQuery read one issue by its number, with
// the fields and the limits of the two queries of the poll. A rule that the end of an
// agent run triggers reads the facts of one issue only, so it does not read
// every page of the repository again. The default branch is a scalar path:
// the merge reads the required checks of that branch.
//
// The read returns an issue only when a poll reads it too: a requirement
// issue is open and has the requirement label, and a sub-issue has such a
// parent (issue-states.md, principle 6: closed requirement issues and their
// sub-issues are not read). subIssueQuery reads the state and the labels of
// the parent for that. Any other issue is an error, so that a rule of the
// run end acts only when the poll would act.
//
// Measured on cumin-works on 2026-10-03 with rateLimit { cost }: 2 points for
// a requirement issue with its sub-issues, and 1 point for a sub-issue.
const requirementIssueQuery = `query($owner: String!, $name: String!, $number: Int!, $subIssues: Int!, $labels: Int!, $blockedBy: Int!, $pullRequests: Int!, $checks: Int!, $reviews: Int!) {
  repository(owner: $owner, name: $name) {
    defaultBranchRef { name }
    issue(number: $number) { ...requirementIssueFields }
  }
  rateLimit { cost remaining }
}
` + requirementIssueWithPullRequestsFields + subIssueFields + closingPullRequestFields

const subIssueQuery = `query($owner: String!, $name: String!, $number: Int!, $labels: Int!, $blockedBy: Int!, $pullRequests: Int!, $checks: Int!, $reviews: Int!) {
  repository(owner: $owner, name: $name) {
    defaultBranchRef { name }
    issue(number: $number) {
      ...subIssueFields
      ...closingPullRequestFields
      parent { number state labels(first: $labels) { pageInfo { hasNextPage } nodes { name } } }
    }
  }
  rateLimit { cost remaining }
}
` + subIssueFields + closingPullRequestFields

// subIssuePageQuery and subIssuePageWithPullRequestsQuery read a next page
// of the sub-issues of one requirement issue, with the fields of the poll
// query and of the read of one requirement issue. Only an issue whose page
// says hasNextPage is asked. Official: Issue.subIssues takes after and first,
// and returns an IssueConnection with pageInfo
// (https://docs.github.com/en/graphql/reference/issues).
//
// Measured on cumin-works on 2026-10-08 with rateLimit { cost }: 1 point for
// a next page, with or without the pull requests.
const subIssuePageQuery = `query($owner: String!, $name: String!, $number: Int!, $subIssues: Int!, $after: String!, $labels: Int!, $blockedBy: Int!) {
  repository(owner: $owner, name: $name) {
    issue(number: $number) {
      number
      subIssues(first: $subIssues, after: $after) {
        pageInfo { hasNextPage endCursor }
        nodes { ...subIssueFields }
      }
    }
  }
  rateLimit { cost remaining }
}
` + subIssueFields

const subIssuePageWithPullRequestsQuery = `query($owner: String!, $name: String!, $number: Int!, $subIssues: Int!, $after: String!, $labels: Int!, $blockedBy: Int!, $pullRequests: Int!, $checks: Int!, $reviews: Int!) {
  repository(owner: $owner, name: $name) {
    issue(number: $number) {
      number
      subIssues(first: $subIssues, after: $after) {
        pageInfo { hasNextPage endCursor }
        nodes { ...subIssueFields ...closingPullRequestFields }
      }
    }
  }
  rateLimit { cost remaining }
}
` + subIssueFields + closingPullRequestFields

type issueResponse struct {
	Data struct {
		Repository *struct {
			DefaultBranchRef *struct {
				Name string `json:"name"`
			} `json:"defaultBranchRef"`
			Issue *issueNode `json:"issue"`
		} `json:"repository"`
		RateLimit struct {
			Cost      int `json:"cost"`
			Remaining int `json:"remaining"`
		} `json:"rateLimit"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// The GraphQL response. It stops in this package.
type snapshotResponse struct {
	Data struct {
		Repository *struct {
			DefaultBranchRef *struct {
				Name   string `json:"name"`
				Target *struct {
					OID string `json:"oid"`
				} `json:"target"`
			} `json:"defaultBranchRef"`
			CuminConfig       *blobNode `json:"cuminConfig"`
			CuminRiskCriteria *blobNode `json:"cuminRiskCriteria"`
			Issues            struct {
				PageInfo pageInfo    `json:"pageInfo"`
				Nodes    []issueNode `json:"nodes"`
			} `json:"issues"`
		} `json:"repository"`
		RateLimit struct {
			Cost      int `json:"cost"`
			Remaining int `json:"remaining"`
		} `json:"rateLimit"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// blobNode is one file of the query. An object that is not a blob (a
// directory, for example) answers the fragment with no field.
type blobNode struct {
	OID         string  `json:"oid"`
	Text        *string `json:"text"`
	ByteSize    int     `json:"byteSize"`
	IsBinary    bool    `json:"isBinary"`
	IsTruncated bool    `json:"isTruncated"`
}

// file converts the node. A file that cumin cannot read as a whole is an
// error: a rule must never run on a part of a file.
func (n *blobNode) file(path string) (*RepositoryFile, error) {
	if n == nil {
		return nil, nil
	}
	switch {
	case n.OID == "":
		return nil, fmt.Errorf("%s is not a file", path)
	case n.IsBinary || n.Text == nil:
		return nil, fmt.Errorf("%s is not text", path)
	case n.IsTruncated:
		return nil, fmt.Errorf("%s is too large to read in one call (%d bytes)", path, n.ByteSize)
	}
	return &RepositoryFile{Path: path, OID: n.OID, Text: *n.Text}, nil
}

type pageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type issueNode struct {
	Number   int        `json:"number"`
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	State    string     `json:"state"`
	ClosedAt *time.Time `json:"closedAt"`
	Labels   struct {
		PageInfo pageInfo `json:"pageInfo"`
		Nodes    []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	SubIssues struct {
		PageInfo pageInfo    `json:"pageInfo"`
		Nodes    []issueNode `json:"nodes"`
	} `json:"subIssues"`
	BlockedBy struct {
		PageInfo pageInfo `json:"pageInfo"`
		Nodes    []struct {
			Number int    `json:"number"`
			State  string `json:"state"`
		} `json:"nodes"`
	} `json:"blockedBy"`
	PullRequests struct {
		PageInfo pageInfo          `json:"pageInfo"`
		Nodes    []pullRequestNode `json:"nodes"`
	} `json:"closedByPullRequestsReferences"`
	// Parent is read by the query of one sub-issue only.
	Parent *issueNode `json:"parent"`
}

// polledAsRequirement says why a poll does not read the node as a
// requirement issue, or nil when it does: the poll reads the open issues
// with the requirement label.
func (n issueNode) polledAsRequirement() error {
	if n.State != "OPEN" {
		return fmt.Errorf("issue #%d is not open", n.Number)
	}
	if n.Labels.PageInfo.HasNextPage {
		return fmt.Errorf("issue #%d has more than %d labels", n.Number, snapshotLabels)
	}
	for _, label := range n.Labels.Nodes {
		if label.Name == requirementLabel {
			return nil
		}
	}
	return fmt.Errorf("issue #%d has no label %s", n.Number, requirementLabel)
}

type pullRequestNode struct {
	Number      int    `json:"number"`
	HeadRefOid  string `json:"headRefOid"`
	HeadRefName string `json:"headRefName"`
	Mergeable   string `json:"mergeable"`
	Commits     struct {
		Nodes []struct {
			Commit struct {
				OID           string    `json:"oid"`
				CommittedDate time.Time `json:"committedDate"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
	Author *struct {
		TypeName string `json:"__typename"`
		Login    string `json:"login"`
	} `json:"author"`
	Labels struct {
		PageInfo pageInfo `json:"pageInfo"`
		Nodes    []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	// StatusCheckRollup is null when the head commit has no check at all.
	StatusCheckRollup *struct {
		Contexts struct {
			PageInfo pageInfo    `json:"pageInfo"`
			Nodes    []checkNode `json:"nodes"`
		} `json:"contexts"`
	} `json:"statusCheckRollup"`
	Reviews struct {
		PageInfo pageInfo     `json:"pageInfo"`
		Nodes    []reviewNode `json:"nodes"`
	} `json:"reviews"`
}

// reviewNode is one review. The names of the fields come from the schema
// (introspection on 2026-09-30): the author is an Actor, the commit is null
// when it is gone, and submittedAt is null for a pending review.
type reviewNode struct {
	Author *struct {
		TypeName string `json:"__typename"`
		Login    string `json:"login"`
	} `json:"author"`
	State       string     `json:"state"`
	SubmittedAt *time.Time `json:"submittedAt"`
	URL         string     `json:"url"`
	Commit      *struct {
		OID string `json:"oid"`
	} `json:"commit"`
}

func (n reviewNode) review() Review {
	review := Review{State: n.State, URL: n.URL}
	if n.Author != nil {
		review.Author = restLogin(n.Author.TypeName, n.Author.Login)
	}
	if n.SubmittedAt != nil {
		review.SubmittedAt = *n.SubmittedAt
	}
	if n.Commit != nil {
		review.Commit = n.Commit.OID
	}
	return review
}

// checkNode is one context of the rollup: a CheckRun (a GitHub Actions job
// and the like) or a StatusContext (a commit status). The names of the
// fields come from the schema (introspection on 2026-09-24).
type checkNode struct {
	TypeName string `json:"__typename"`
	// A CheckRun.
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	// CheckSuite carries the App that reported the check run.
	CheckSuite *struct {
		App *struct {
			DatabaseID int64 `json:"databaseId"`
		} `json:"app"`
	} `json:"checkSuite"`
	// A StatusContext.
	Context string `json:"context"`
	State   string `json:"state"`
}

// result converts one context. An unknown type is an error: cumin must not
// decide "request the review" or "request a check fix" on a check whose
// shape it does not know.
func (n checkNode) result() (CheckResult, error) {
	switch n.TypeName {
	case "CheckRun":
		result := CheckResult{Name: n.Name, Conclusion: checkRunConclusion(n.Status, n.Conclusion)}
		if n.CheckSuite != nil && n.CheckSuite.App != nil {
			result.Integration = n.CheckSuite.App.DatabaseID
		}
		return result, nil
	case "StatusContext":
		return CheckResult{Name: n.Context, Conclusion: statusConclusion(n.State)}, nil
	}
	return CheckResult{}, fmt.Errorf("the check %q has the unknown type %q", n.Name+n.Context, n.TypeName)
}

// checkRunConclusion reads a check run. A run that has not completed has no
// conclusion yet, whatever it will be.
func checkRunConclusion(status, conclusion string) CheckConclusion {
	if status != "COMPLETED" {
		return CheckPending
	}
	switch conclusion {
	case "SUCCESS", "SKIPPED", "NEUTRAL":
		return CheckPassed
	case "":
		return CheckPending
	}
	return CheckFailed
}

// statusConclusion reads a commit status. EXPECTED and PENDING mean that
// the answer is still to come.
func statusConclusion(state string) CheckConclusion {
	switch state {
	case "SUCCESS":
		return CheckPassed
	case "EXPECTED", "PENDING", "":
		return CheckPending
	}
	return CheckFailed
}

// restLogin returns a login as the REST API shows it. GraphQL gives the
// login of a Bot without "[bot]" (measured on the sandbox on 2026-09-22).
func restLogin(typeName, login string) string {
	if typeName == "Bot" {
		return login + "[bot]"
	}
	return login
}

// pullRequest converts one node. Without includeClosedPrs, the connection
// holds open pull requests only (the schema: closedByPullRequestsReferences).
// A connection over its page size is an error, as it is for an issue: the
// caller puts the number of the issue in it. An unknown mergeable value is an error too: cumin must not decide on a value
// whose meaning it does not know.
func (n pullRequestNode) pullRequest() (PullRequest, error) {
	pr := PullRequest{Number: n.Number, HeadCommit: n.HeadRefOid, HeadBranch: n.HeadRefName}
	switch state := MergeableState(n.Mergeable); state {
	case Mergeable, Conflicting, MergeableUnknown:
		pr.Mergeable = state
	default:
		return PullRequest{}, fmt.Errorf("pull request #%d has the unknown mergeable value %q", n.Number, n.Mergeable)
	}
	for _, node := range n.Commits.Nodes {
		if node.Commit.OID == n.HeadRefOid {
			pr.HeadCommittedAt = node.Commit.CommittedDate
		}
	}
	if n.Author != nil {
		pr.Author = restLogin(n.Author.TypeName, n.Author.Login)
	}
	if n.Labels.PageInfo.HasNextPage {
		return PullRequest{}, &overLimitError{limit: fmt.Sprintf("more than %d labels on pull request #%d", snapshotLabels, n.Number)}
	}
	for _, label := range n.Labels.Nodes {
		pr.Labels = append(pr.Labels, label.Name)
	}
	if n.Reviews.PageInfo.HasNextPage {
		return PullRequest{}, &overLimitError{limit: fmt.Sprintf("more than %d reviews on pull request #%d", snapshotReviews, n.Number)}
	}
	for _, node := range n.Reviews.Nodes {
		pr.Reviews = append(pr.Reviews, node.review())
	}
	if n.StatusCheckRollup == nil {
		return pr, nil
	}
	if n.StatusCheckRollup.Contexts.PageInfo.HasNextPage {
		return PullRequest{}, &overLimitError{limit: fmt.Sprintf("more than %d checks on pull request #%d", snapshotChecks, n.Number)}
	}
	for _, node := range n.StatusCheckRollup.Contexts.Nodes {
		check, err := node.result()
		if err != nil {
			return PullRequest{}, fmt.Errorf("pull request #%d: %w", n.Number, err)
		}
		pr.Checks = append(pr.Checks, check)
	}
	return pr, nil
}

type pullRequestsResponse struct {
	Data struct {
		Nodes []*struct {
			TypeName string `json:"__typename"`
			issueNode
		} `json:"nodes"`
		RateLimit struct {
			Cost      int `json:"cost"`
			Remaining int `json:"remaining"`
		} `json:"rateLimit"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// issue converts one node. A connection with more nodes than the page size
// is an error (overLimitError), so that a rule never decides on a partial
// issue. The
// sub-issues are over their size when the last of their pages has a next
// page (readNextSubIssues).
func (n issueNode) issue() (Issue, error) {
	if n.Labels.PageInfo.HasNextPage {
		return Issue{}, &overLimitError{issue: n.Number, limit: fmt.Sprintf("more than %d labels", snapshotLabels)}
	}
	if n.SubIssues.PageInfo.HasNextPage {
		return Issue{}, &overLimitError{issue: n.Number, limit: fmt.Sprintf("more than %d sub-issues", snapshotSubIssues*snapshotSubIssuePages)}
	}
	if n.BlockedBy.PageInfo.HasNextPage {
		return Issue{}, &overLimitError{issue: n.Number, limit: fmt.Sprintf("more than %d blocked-by issues", snapshotBlockedBy)}
	}
	issue := Issue{Number: n.Number, Title: n.Title, NodeID: n.ID, Closed: n.State == "CLOSED"}
	if n.ClosedAt != nil {
		issue.ClosedAt = *n.ClosedAt
	}
	if n.State != "OPEN" && n.State != "CLOSED" {
		return Issue{}, fmt.Errorf("issue #%d has the unknown state %q", n.Number, n.State)
	}
	for _, label := range n.Labels.Nodes {
		issue.Labels = append(issue.Labels, label.Name)
	}
	var errs []error
	for _, sub := range n.SubIssues.Nodes {
		subIssue, err := sub.issue()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		issue.SubIssues = append(issue.SubIssues, subIssue)
	}
	for _, blocker := range n.BlockedBy.Nodes {
		issue.BlockedBy = append(issue.BlockedBy, IssueRef{Number: blocker.Number, Closed: blocker.State == "CLOSED"})
	}
	pullRequests, err := n.pullRequests()
	if err != nil {
		errs = append(errs, err)
	}
	issue.PullRequests = pullRequests
	return issue, errors.Join(errs...)
}

// partialIssue converts as much of one node as the query returned, for an
// issue over a limit: the number, the state, the labels, and the sub-issues
// that were read. No rule decides on it; the caller counts the issues in
// progress from it.
func (n issueNode) partialIssue() Issue {
	issue := Issue{Number: n.Number, Title: n.Title, NodeID: n.ID, Closed: n.State == "CLOSED", LabelsOverLimit: n.Labels.PageInfo.HasNextPage}
	for _, label := range n.Labels.Nodes {
		issue.Labels = append(issue.Labels, label.Name)
	}
	for _, sub := range n.SubIssues.Nodes {
		issue.SubIssues = append(issue.SubIssues, sub.partialIssue())
	}
	return issue
}

// pullRequests converts the open closing pull requests of the node. More
// pull requests than the page size is an error, as it is for the other
// connections of an issue.
func (n issueNode) pullRequests() ([]PullRequest, error) {
	if n.PullRequests.PageInfo.HasNextPage {
		return nil, &overLimitError{issue: n.Number, limit: fmt.Sprintf("more than %d open closing pull requests", snapshotPullRequests)}
	}
	var pullRequests []PullRequest
	var errs []error
	for _, node := range n.PullRequests.Nodes {
		pr, err := node.pullRequest()
		if limit, ok := err.(*overLimitError); ok {
			limit.issue = n.Number
			errs = append(errs, limit)
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("issue #%d: %w", n.Number, err))
			continue
		}
		pullRequests = append(pullRequests, pr)
	}
	return pullRequests, errors.Join(errs...)
}
