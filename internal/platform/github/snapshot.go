package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Page sizes of the snapshot query. GitHub scores a query by the `first`
// arguments along each path (official: Rate limits and node limits for the
// GraphQL API). Measured on the sandbox on 2026-09-25, the cost of one page
// is (requirement issues x sub-issues x k) / 100, where k counts the
// connections: 2 under a sub-issue (labels, blocked by), 1 for the pull
// requests, and one more for each connection under a pull request,
// multiplied by the number of pull requests. The page sizes inside those
// connections (checks, labels) change nothing.
//
// Only the sub-issues and the pull requests change the cost, so only those
// two are small; they stay above the limits of the requirement, which allows
// 12 sub-issues for a requirement issue (requirement-sizing.md) and leaves an
// implementation issue with one open closing pull request in normal use.
// Every other size is at the maximum of GraphQL (100), because a connection
// over its size stops the poll of that repository and costs nothing to widen:
// a matrix workflow can put more than twenty checks on a commit, and another
// tool can add labels of its own.
//
// With these sizes one page costs 11 points, against 5,000 points per hour
// for one installation (measured on the sandbox on 2026-09-26). The reviews
// of a pull request are one more connection under it, and raise the cost of
// one page to 14 points (measured on the sandbox on 2026-09-30). The head
// commit with its time is one more connection again (commits), and raises
// the cost of one page to 17 points (measured on cumin-works on 2026-10-03).
//
// The pull requests cost 14 of those 17 points, so the poll query stops at
// the sub-issue, and one page costs 3 points. A second query of the same
// poll reads the pull requests of the few sub-issues that a rule reads them
// for (pullRequestsQuery; measured on cumin-works on 2026-10-03).
//
// MaxOpenClosingPullRequests is the most open closing pull requests that
// the snapshot reads for one issue. cumin adds no closing link that would go
// over it, because every later poll would then fail on that issue.
const MaxOpenClosingPullRequests = 2

const (
	// Requirement issues are read in pages of this size, with a cursor.
	snapshotIssuePage = 10
	// Sub-issues, labels, blocked-by issues, and open closing pull requests
	// are read once, up to this many for one issue. More is an error. An
	// issue has one open closing pull request in normal use; a second one
	// is read so that the newest of two is found (VerifyDone).
	snapshotSubIssues    = 15
	snapshotLabels       = 100
	snapshotBlockedBy    = 100
	snapshotPullRequests = MaxOpenClosingPullRequests
	// Checks of the head commit of one pull request. A repository requires
	// a handful of checks, and a commit carries every other check as well.
	snapshotChecks = 100
	// Reviews of one pull request. The Reviewer gives one review for each
	// round, and max_review_rounds is a few; people may add their own.
	snapshotReviews = 100
	// Sub-issues whose pull requests one call of the second query reads. A
	// poll with more of them makes one call for each page of this size.
	// GraphQL returns at most 100 nodes for nodes(ids:).
	snapshotPullRequestIssues = 100
)

// requirementLabel marks a requirement issue (issue-states.md). The poll
// reads the open issues with this label, and the read of one issue refuses
// an issue that the poll does not read.
const requirementLabel = "cumin/type/requirement"

// Paths of the files that a target repository keeps on its default branch.
// cumin reads them with the poll query, never from a pull request branch
// (cumin-core.md, the topic on settings).
const (
	CuminConfigPath       = ".cumin/config.toml"
	CuminRiskCriteriaPath = ".cumin/risk-criteria.md"
)

// RepositorySnapshot is what one poll reads of one repository: the open
// requirement issues with their sub-issues, and the rate limit of the call.
// The sub-issues carry no pull request: ReadPullRequests reads those.
// docs/ja/designs/poll.md, topic "What one poll reads".
type RepositorySnapshot struct {
	// DefaultBranch is the name of the default branch, and DefaultBranchOID
	// is the commit at its head. A repository without a commit has neither.
	DefaultBranch    string
	DefaultBranchOID string
	// CuminConfig and CuminRiskCriteria are the files of .cumin/ on the
	// default branch. A file that does not exist is nil.
	CuminConfig       *RepositoryFile
	CuminRiskCriteria *RepositoryFile
	RequirementIssues []Issue
	RateLimit         RateLimit
}

// RepositoryFile is one text file of the default branch. OID is the blob of
// git: it changes when the content changes, so the caller can parse the file
// again only after a change.
type RepositoryFile struct {
	Path string
	OID  string
	Text string
}

// Issue is one issue as the snapshot sees it. A requirement issue has
// SubIssues and BlockedBy ("request the split"). A sub-issue has Title,
// BlockedBy, and PullRequests.
type Issue struct {
	Number    int
	Closed    bool
	Labels    []string
	SubIssues []Issue
	// Title is read for sub-issues only; the branch name of a request is
	// made from it.
	Title string
	// NodeID is the GraphQL ID, read for sub-issues only: cumin adds the
	// closing link with it. The field is a scalar, so it does not change
	// the cost of the query.
	NodeID    string
	BlockedBy []IssueRef
	// ClosedAt is when a closed sub-issue closed. "request the acceptance
	// check" and "ask for the acceptance" compare it with the time of the
	// acceptance check comment. The field is a scalar, so it does not
	// change the cost of the query.
	ClosedAt time.Time
	// PullRequests are the open pull requests that close the sub-issue (the
	// link that "Closes #N" makes). Closed and merged pull requests are not
	// read: no rule of the poll needs them, and old pull requests of a
	// waiting or closed issue must not reach the page limit. The follow-up
	// note reads the merged pull request of a closed issue separately.
	// The poll query does not read them: the caller takes them from
	// ReadPullRequests. The read of one issue fills them.
	PullRequests []PullRequest
}

// PullRequest is an open pull request that closes an issue, as much of it
// as the rules need.
type PullRequest struct {
	Number int
	// HeadCommit is the full SHA of the head of the pull request.
	HeadCommit string
	// HeadBranch is the branch of the pull request. A request that
	// continues the work of an open pull request runs on it
	// ("request the implementation").
	HeadBranch string
	// Labels are the labels of the pull request now. "copy the labels to
	// the pull request" compares them with the labels of the issue; no
	// decision reads them (principle 5).
	Labels []string
	// Checks are the checks on the head commit, from statusCheckRollup.
	// The decision on the checks reads them together with the required
	// checks of the branch (RequiredChecks).
	Checks []CheckResult
	// Author is the login of the author as the REST API shows it: a GitHub
	// App is "<slug>[bot]", the form of the identity that an agent commits
	// with. GraphQL gives the login of a Bot without "[bot]" (measured on
	// the sandbox on 2026-09-22), so it is added here. Empty when the
	// author is gone (a deleted account).
	Author string
	// Reviews are the reviews of the pull request, oldest first. The round
	// of the review and the checks after a Reviewer run read them
	// ("request the review", "request a review fix", "request the cause").
	Reviews []Review
	// Mergeable is what GitHub says about a merge into the base branch.
	Mergeable MergeableState
	// HeadCommittedAt is the commit time of the head commit (committedDate).
	// Zero when the last commit that GitHub lists is not HeadCommit: a push
	// came between the two reads of GitHub.
	HeadCommittedAt time.Time
}

// MergeableState is the mergeability of a pull request (the GraphQL schema,
// MergeableState).
type MergeableState string

const (
	// Mergeable: the pull request can be merged.
	Mergeable MergeableState = "MERGEABLE"
	// Conflicting: the pull request has merge conflicts.
	Conflicting MergeableState = "CONFLICTING"
	// MergeableUnknown: GitHub is still calculating the mergeability.
	MergeableUnknown MergeableState = "UNKNOWN"
)

// Review is one review of a pull request, as the rules need it.
type Review struct {
	// Author is the login in the REST form: "<slug>[bot]" for an App.
	// Empty when the author is gone.
	Author string
	// State is the state of GraphQL: APPROVED, CHANGES_REQUESTED,
	// COMMENTED, DISMISSED, or PENDING (the schema, PullRequestReviewState).
	State string
	// Commit is the SHA of the commit that the review is on. Empty when
	// that commit is no longer in the repository.
	Commit string
	// SubmittedAt is when the review was submitted; zero for a pending
	// review.
	SubmittedAt time.Time
	// URL is the address of the review. A request that names the review
	// links it.
	URL string
}

// CheckConclusion is what one check says, as the decision on the checks
// reads it. GitHub has more conclusions; cumin needs only these three.
type CheckConclusion int

const (
	// CheckPending: the check has not finished, or has not reported yet.
	CheckPending CheckConclusion = iota
	// CheckPassed: success, skipped, or neutral. GitHub treats these three
	// as not blocking a merge (measured-constraints.md row 51).
	CheckPassed
	// CheckFailed: the check finished with any other conclusion.
	CheckFailed
)

func (c CheckConclusion) String() string {
	switch c {
	case CheckPending:
		return "pending"
	case CheckPassed:
		return "passed"
	case CheckFailed:
		return "failed"
	}
	return fmt.Sprintf("CheckConclusion(%d)", int(c))
}

// CheckResult is one check on the head commit of a pull request. A check
// run and a commit status give the same values, because the required checks
// of a branch name both by the same name. Integration is the database id of
// the App of the check suite, or 0 for a commit status and for a check run
// whose App cannot be read. A required check that names an App is met only
// by the check of that App.
type CheckResult struct {
	Name        string
	Conclusion  CheckConclusion
	Integration int64
}

// IssueRef is an issue that another issue points to: only its number and
// whether it is closed.
type IssueRef struct {
	Number int
	Closed bool
}

// RateLimit is the cost of one call and what is left of the hourly points of
// the installation. The caller logs it.
type RateLimit struct {
	Cost      int
	Remaining int
}

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
  state
  labels(first: $labels) { pageInfo { hasNextPage } nodes { name } }
  blockedBy(first: $blockedBy) { pageInfo { hasNextPage } nodes { number state } }
  subIssues(first: $subIssues) {
    pageInfo { hasNextPage }
    nodes { ...subIssueFields }
  }
}
`

// requirementIssueWithPullRequestsFields is requirementIssueFields with the
// pull requests of each sub-issue, for the read of one requirement issue.
const requirementIssueWithPullRequestsFields = `
fragment requirementIssueFields on Issue {
  number
  state
  labels(first: $labels) { pageInfo { hasNextPage } nodes { name } }
  blockedBy(first: $blockedBy) { pageInfo { hasNextPage } nodes { number state } }
  subIssues(first: $subIssues) {
    pageInfo { hasNextPage }
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

// IssueRead is what the read of one issue returns: the issue, the name of
// the default branch, and the rate limit of the call.
type IssueRead struct {
	DefaultBranch string
	Issue         Issue
	RateLimit     RateLimit
}

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
// A connection over its page size is an error, as it is for an issue. An
// unknown mergeable value is an error too: cumin must not decide on a value
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
		return PullRequest{}, fmt.Errorf("pull request #%d has more than %d labels", n.Number, snapshotLabels)
	}
	for _, label := range n.Labels.Nodes {
		pr.Labels = append(pr.Labels, label.Name)
	}
	if n.Reviews.PageInfo.HasNextPage {
		return PullRequest{}, fmt.Errorf("pull request #%d has more than %d reviews", n.Number, snapshotReviews)
	}
	for _, node := range n.Reviews.Nodes {
		pr.Reviews = append(pr.Reviews, node.review())
	}
	if n.StatusCheckRollup == nil {
		return pr, nil
	}
	if n.StatusCheckRollup.Contexts.PageInfo.HasNextPage {
		return PullRequest{}, fmt.Errorf("pull request #%d has more than %d checks", n.Number, snapshotChecks)
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

// ReadSnapshot reads the snapshot of one repository with the installation
// token: one GraphQL query for each page of requirement issues. Closed
// requirement issues are not read (issue-states.md, principle 6). The
// sub-issues come without their pull requests (ReadPullRequests).
func (c *AppClient) ReadSnapshot(ctx context.Context, token, owner, repo string) (RepositorySnapshot, error) {
	var snapshot RepositorySnapshot
	var after *string
	for {
		firstPage := after == nil
		variables := map[string]any{
			"owner": owner, "name": repo, "first": snapshotIssuePage, "after": after,
			"subIssues": snapshotSubIssues, "labels": snapshotLabels, "blockedBy": snapshotBlockedBy,
			"repositoryFiles": firstPage,
		}
		var resp snapshotResponse
		request := map[string]any{"query": snapshotQuery, "variables": variables}
		if err := c.do(ctx, token, http.MethodPost, "/graphql", "/graphql", request, http.StatusOK, &resp); err != nil {
			return RepositorySnapshot{}, fmt.Errorf("github: read the snapshot of %s/%s: %w", owner, repo, err)
		}
		if len(resp.Errors) > 0 {
			var messages []string
			for _, e := range resp.Errors {
				messages = append(messages, e.Message)
			}
			return RepositorySnapshot{}, fmt.Errorf("github: read the snapshot of %s/%s: %s", owner, repo, strings.Join(messages, "; "))
		}
		if resp.Data.Repository == nil {
			return RepositorySnapshot{}, fmt.Errorf("github: read the snapshot of %s/%s: the response has no repository", owner, repo)
		}
		// The last page reports the cost of the whole read only for itself;
		// sum the cost, and keep the remaining points of the last call.
		snapshot.RateLimit.Cost += resp.Data.RateLimit.Cost
		snapshot.RateLimit.Remaining = resp.Data.RateLimit.Remaining
		if firstPage {
			if err := snapshot.readRepositoryFiles(resp); err != nil {
				return RepositorySnapshot{}, fmt.Errorf("github: read the snapshot of %s/%s: %w", owner, repo, err)
			}
		}
		for _, node := range resp.Data.Repository.Issues.Nodes {
			issue, err := node.issue()
			if err != nil {
				return RepositorySnapshot{}, fmt.Errorf("github: read the snapshot of %s/%s: %w", owner, repo, err)
			}
			snapshot.RequirementIssues = append(snapshot.RequirementIssues, issue)
		}
		page := resp.Data.Repository.Issues.PageInfo
		if !page.HasNextPage {
			return snapshot, nil
		}
		cursor := page.EndCursor
		after = &cursor
	}
}

// PullRequestsRead is what the second query of a poll returns: the open
// closing pull requests of each sub-issue that the caller named, by the
// number of the sub-issue, and the rate limit of the calls.
type PullRequestsRead struct {
	PullRequests map[int][]PullRequest
	RateLimit    RateLimit
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

// ReadPullRequests reads the open pull requests that close each of the
// sub-issues, with the installation token. issueIDs are the node ids that
// ReadSnapshot read. No id means no query. An id that is no longer an issue
// (deleted or moved between the two reads) is an error, so that a rule never
// decides on a sub-issue whose pull requests were not read.
func (c *AppClient) ReadPullRequests(ctx context.Context, token, owner, repo string, issueIDs []string) (PullRequestsRead, error) {
	read := PullRequestsRead{PullRequests: map[int][]PullRequest{}}
	for page := range slices.Chunk(issueIDs, snapshotPullRequestIssues) {
		variables := map[string]any{
			"ids": page, "labels": snapshotLabels,
			"pullRequests": snapshotPullRequests,
			"checks":       snapshotChecks,
			"reviews":      snapshotReviews,
		}
		var resp pullRequestsResponse
		request := map[string]any{"query": pullRequestsQuery, "variables": variables}
		if err := c.do(ctx, token, http.MethodPost, "/graphql", "/graphql", request, http.StatusOK, &resp); err != nil {
			return PullRequestsRead{}, fmt.Errorf("github: read the pull requests of %s/%s: %w", owner, repo, err)
		}
		if len(resp.Errors) > 0 {
			var messages []string
			for _, e := range resp.Errors {
				messages = append(messages, e.Message)
			}
			return PullRequestsRead{}, fmt.Errorf("github: read the pull requests of %s/%s: %s", owner, repo, strings.Join(messages, "; "))
		}
		if len(resp.Data.Nodes) != len(page) {
			return PullRequestsRead{}, fmt.Errorf("github: read the pull requests of %s/%s: the response has %d issues, want %d", owner, repo, len(resp.Data.Nodes), len(page))
		}
		read.RateLimit.Cost += resp.Data.RateLimit.Cost
		read.RateLimit.Remaining = resp.Data.RateLimit.Remaining
		var errs []error
		for i, node := range resp.Data.Nodes {
			if node == nil || node.TypeName != "Issue" {
				return PullRequestsRead{}, fmt.Errorf("github: read the pull requests of %s/%s: the id %s is not an issue", owner, repo, page[i])
			}
			pullRequests, err := node.pullRequests()
			if err != nil {
				errs = append(errs, err)
				continue
			}
			read.PullRequests[node.Number] = pullRequests
		}
		if err := errors.Join(errs...); err != nil {
			return PullRequestsRead{}, fmt.Errorf("github: read the pull requests of %s/%s: %w", owner, repo, err)
		}
	}
	return read, nil
}

// readRepositoryFiles takes the default branch and the files of .cumin/
// from the answer of the first page.
func (s *RepositorySnapshot) readRepositoryFiles(resp snapshotResponse) error {
	repository := resp.Data.Repository
	if ref := repository.DefaultBranchRef; ref != nil {
		s.DefaultBranch = ref.Name
		if ref.Target != nil {
			s.DefaultBranchOID = ref.Target.OID
		}
	}
	config, configErr := repository.CuminConfig.file(CuminConfigPath)
	criteria, criteriaErr := repository.CuminRiskCriteria.file(CuminRiskCriteriaPath)
	if err := errors.Join(configErr, criteriaErr); err != nil {
		return err
	}
	s.CuminConfig, s.CuminRiskCriteria = config, criteria
	return nil
}

// issue converts one node. A connection with more nodes than the page size
// is an error, so that a rule never decides on a partial issue.
func (n issueNode) issue() (Issue, error) {
	if n.Labels.PageInfo.HasNextPage {
		return Issue{}, fmt.Errorf("issue #%d has more than %d labels", n.Number, snapshotLabels)
	}
	if n.SubIssues.PageInfo.HasNextPage {
		return Issue{}, fmt.Errorf("issue #%d has more than %d sub-issues", n.Number, snapshotSubIssues)
	}
	if n.BlockedBy.PageInfo.HasNextPage {
		return Issue{}, fmt.Errorf("issue #%d has more than %d blocked-by issues", n.Number, snapshotBlockedBy)
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

// pullRequests converts the open closing pull requests of the node. More
// pull requests than the page size is an error, as it is for the other
// connections of an issue.
func (n issueNode) pullRequests() ([]PullRequest, error) {
	if n.PullRequests.PageInfo.HasNextPage {
		return nil, fmt.Errorf("issue #%d has more than %d open closing pull requests", n.Number, snapshotPullRequests)
	}
	var pullRequests []PullRequest
	var errs []error
	for _, node := range n.PullRequests.Nodes {
		pr, err := node.pullRequest()
		if err != nil {
			errs = append(errs, fmt.Errorf("issue #%d: %w", n.Number, err))
			continue
		}
		pullRequests = append(pullRequests, pr)
	}
	return pullRequests, errors.Join(errs...)
}

// ReadRequirementIssue reads one requirement issue with its sub-issues, as
// one poll reads it, in one GraphQL query. An issue over a limit of the
// query is an error that names the issue. An issue that a poll does not read
// (closed, or without the requirement label) is an error too.
func (c *AppClient) ReadRequirementIssue(ctx context.Context, token, owner, repo string, number int) (IssueRead, error) {
	return c.readIssue(ctx, token, owner, repo, number, requirementIssueQuery, map[string]any{"subIssues": snapshotSubIssues},
		func(n issueNode) error { return n.polledAsRequirement() })
}

// ReadSubIssue reads one sub-issue with its open closing pull requests, as
// one poll reads it, in one GraphQL query. An issue over a limit of the
// query is an error that names the issue. An issue that a poll does not read
// (no parent, or a parent that is closed or has no requirement label) is an
// error too.
func (c *AppClient) ReadSubIssue(ctx context.Context, token, owner, repo string, number int) (IssueRead, error) {
	return c.readIssue(ctx, token, owner, repo, number, subIssueQuery, map[string]any{}, func(n issueNode) error {
		if n.Parent == nil {
			return fmt.Errorf("issue #%d has no parent issue", n.Number)
		}
		if err := n.Parent.polledAsRequirement(); err != nil {
			return fmt.Errorf("the parent of issue #%d is not a requirement issue of the poll: %w", n.Number, err)
		}
		return nil
	})
}

// readIssue runs one of the two queries. polled says why a poll does not
// read the issue, or nil when it does.
func (c *AppClient) readIssue(ctx context.Context, token, owner, repo string, number int, query string, variables map[string]any, polled func(issueNode) error) (IssueRead, error) {
	for name, value := range map[string]any{
		"owner": owner, "name": repo, "number": number,
		"labels": snapshotLabels, "blockedBy": snapshotBlockedBy,
		"pullRequests": snapshotPullRequests,
		"checks":       snapshotChecks,
		"reviews":      snapshotReviews,
	} {
		variables[name] = value
	}
	var resp issueResponse
	request := map[string]any{"query": query, "variables": variables}
	if err := c.do(ctx, token, http.MethodPost, "/graphql", "/graphql", request, http.StatusOK, &resp); err != nil {
		return IssueRead{}, fmt.Errorf("github: read issue #%d of %s/%s: %w", number, owner, repo, err)
	}
	if len(resp.Errors) > 0 {
		var messages []string
		for _, e := range resp.Errors {
			messages = append(messages, e.Message)
		}
		return IssueRead{}, fmt.Errorf("github: read issue #%d of %s/%s: %s", number, owner, repo, strings.Join(messages, "; "))
	}
	if resp.Data.Repository == nil || resp.Data.Repository.Issue == nil {
		return IssueRead{}, fmt.Errorf("github: read issue #%d of %s/%s: the response has no issue", number, owner, repo)
	}
	if err := polled(*resp.Data.Repository.Issue); err != nil {
		return IssueRead{}, fmt.Errorf("github: read issue #%d of %s/%s: %w", number, owner, repo, err)
	}
	issue, err := resp.Data.Repository.Issue.issue()
	if err != nil {
		return IssueRead{}, fmt.Errorf("github: read issue #%d of %s/%s: %w", number, owner, repo, err)
	}
	read := IssueRead{Issue: issue, RateLimit: RateLimit{Cost: resp.Data.RateLimit.Cost, Remaining: resp.Data.RateLimit.Remaining}}
	if ref := resp.Data.Repository.DefaultBranchRef; ref != nil {
		read.DefaultBranch = ref.Name
	}
	return read, nil
}
