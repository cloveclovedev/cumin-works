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
// two are small. The sub-issues are read in pages of 12, the limit of the
// requirement for one split (requirement-sizing.md), up to 36. The pull
// requests stay above normal use, which leaves an implementation issue with
// one open closing pull request.
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
	// Sub-issues are read in pages of this size: the 12 sub-issues that the
	// requirement allows for one split (requirement-sizing.md). A requirement
	// issue that is split again keeps the closed sub-issues of the earlier
	// split, so an issue with more is read in next pages, up to
	// snapshotSubIssuePages pages. More is an error. A page of 10 requirement
	// issues costs 3 points, and a next page of one issue costs 1 point
	// (measured on cumin-works on 2026-10-08). The label times query and the
	// label actor query read the sub-issues in the same pages.
	snapshotSubIssues     = 12
	snapshotSubIssuePages = 3
	// Labels, blocked-by issues, and open closing pull requests are read
	// once, up to this many for one issue. More is an error. An issue has
	// one open closing pull request in normal use; a second one is read so
	// that the newest of two is found (VerifyDone).
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
	// Unread are the issues over a limit of the query. RequirementIssues
	// holds no requirement issue of them: no rule decides on a partial issue.
	Unread []UnreadIssue
	// UnreadRequirementIssues are the requirement issues of Unread, as much
	// as the query returned of them. They are partial: the caller reads
	// them only to count the issues in progress.
	UnreadRequirementIssues []Issue
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

// UnreadIssue is an issue that cumin cannot read in full: it has more of one
// thing than the query reads (cumin-core.md, "Issues that cumin cannot read
// in full").
type UnreadIssue struct {
	// Requirement is the requirement issue that the poll leaves out for
	// the issue. ReadPullRequests does not know it and leaves it 0.
	Requirement int
	// Issue is the issue over the limit: the requirement issue itself, or
	// one of its sub-issues.
	Issue int
	// Limit is the limit as text, for example "more than 36 sub-issues".
	Limit string
}

// overLimitError is the error of an issue over a limit of the query. A poll
// leaves the issue out and goes on; the read of one issue returns the error.
type overLimitError struct {
	issue int
	limit string
}

func (e *overLimitError) Error() string {
	return fmt.Sprintf("issue #%d has %s", e.issue, e.limit)
}

// overLimits returns the limits that err consists of. ok is false when err
// is nil or holds any other error: that error stays an error of the read.
func overLimits(err error) (limits []*overLimitError, ok bool) {
	switch e := err.(type) {
	case nil:
		return nil, false
	case *overLimitError:
		return []*overLimitError{e}, true
	case interface{ Unwrap() []error }:
		for _, inner := range e.Unwrap() {
			found, ok := overLimits(inner)
			if !ok {
				return nil, false
			}
			limits = append(limits, found...)
		}
		return limits, len(limits) > 0
	}
	return nil, false
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
// SubIssues and BlockedBy ("request the split"). A sub-issue has
// BlockedBy and PullRequests.
type Issue struct {
	Number int
	Closed bool
	Labels []string
	// LabelsOverLimit is true when the issue has more labels than Labels
	// holds. Only an issue of UnreadRequirementIssues can have it.
	LabelsOverLimit bool
	SubIssues       []Issue
	// Title is the title of the issue. The branch name of a request is made
	// from the title of a sub-issue, and the monitor file shows the title
	// of both. The field is a scalar, so it does not change the cost.
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

// IssueRead is what the read of one issue returns: the issue, the name of
// the default branch, and the rate limit of the call.
type IssueRead struct {
	DefaultBranch string
	Issue         Issue
	RateLimit     RateLimit
}

// ReadSnapshot reads the snapshot of one repository with the installation
// token: one GraphQL query for each page of requirement issues, and one more
// for each next page of the sub-issues of one issue. Closed requirement
// issues are not read (issue-states.md, principle 6). The sub-issues come
// without their pull requests (ReadPullRequests). A requirement issue that is
// over a limit of the query, or that has such a sub-issue, is not in
// RequirementIssues: Unread names the issue and the limit. Every other
// failure is an error of the whole read.
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
			if err := c.readNextSubIssues(ctx, token, owner, repo, &node, subIssuePageQuery, map[string]any{}, &snapshot.RateLimit); err != nil {
				return RepositorySnapshot{}, fmt.Errorf("github: read the snapshot of %s/%s: %w", owner, repo, err)
			}
			issue, err := node.issue()
			if limits, ok := overLimits(err); ok {
				for _, limit := range limits {
					snapshot.Unread = append(snapshot.Unread, UnreadIssue{Requirement: node.Number, Issue: limit.issue, Limit: limit.limit})
				}
				snapshot.UnreadRequirementIssues = append(snapshot.UnreadRequirementIssues, node.partialIssue())
				continue
			}
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
// number of the sub-issue, and the rate limit of the calls. Unread are the
// sub-issues over a limit of the query; PullRequests has no entry for them.
type PullRequestsRead struct {
	PullRequests map[int][]PullRequest
	Unread       []UnreadIssue
	RateLimit    RateLimit
}

// ReadPullRequests reads the open pull requests that close each of the
// sub-issues, with the installation token. issueIDs are the node ids that
// ReadSnapshot read. No id means no query. An id that is no longer an issue
// (deleted or moved between the two reads) is an error, so that a rule never
// decides on a sub-issue whose pull requests were not read. A sub-issue over
// a limit of the query is not an error: Unread names it and the limit.
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
			if limits, ok := overLimits(err); ok {
				for _, limit := range limits {
					read.Unread = append(read.Unread, UnreadIssue{Issue: limit.issue, Limit: limit.limit})
				}
				continue
			}
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

// readNextSubIssues reads the next pages of the sub-issues of one requirement
// issue into the node, one GraphQL query for each page, until the node holds
// snapshotSubIssuePages pages. An issue whose page has no next page costs no
// call. The cost of each call is added to rate. variables holds what the
// query needs beside the issue, the page, and the sizes of the sub-issue.
func (c *AppClient) readNextSubIssues(ctx context.Context, token, owner, repo string, node *issueNode, query string, variables map[string]any, rate *RateLimit) error {
	for pages := 1; pages < snapshotSubIssuePages && node.SubIssues.PageInfo.HasNextPage; pages++ {
		for name, value := range map[string]any{
			"owner": owner, "name": repo, "number": node.Number,
			"subIssues": snapshotSubIssues, "after": node.SubIssues.PageInfo.EndCursor,
			"labels": snapshotLabels, "blockedBy": snapshotBlockedBy,
		} {
			variables[name] = value
		}
		var resp issueResponse
		request := map[string]any{"query": query, "variables": variables}
		if err := c.do(ctx, token, http.MethodPost, "/graphql", "/graphql", request, http.StatusOK, &resp); err != nil {
			return fmt.Errorf("read the next sub-issues of issue #%d: %w", node.Number, err)
		}
		if len(resp.Errors) > 0 {
			var messages []string
			for _, e := range resp.Errors {
				messages = append(messages, e.Message)
			}
			return fmt.Errorf("read the next sub-issues of issue #%d: %s", node.Number, strings.Join(messages, "; "))
		}
		if resp.Data.Repository == nil || resp.Data.Repository.Issue == nil {
			return fmt.Errorf("read the next sub-issues of issue #%d: the response has no issue", node.Number)
		}
		rate.Cost += resp.Data.RateLimit.Cost
		rate.Remaining = resp.Data.RateLimit.Remaining
		page := resp.Data.Repository.Issue.SubIssues
		node.SubIssues.Nodes = append(node.SubIssues.Nodes, page.Nodes...)
		node.SubIssues.PageInfo = page.PageInfo
	}
	return nil
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

// ReadRequirementIssue reads one requirement issue with its sub-issues, as
// one poll reads it, in one GraphQL query, and one more for each next page
// of its sub-issues. An issue over a limit of the query is an error that
// names the issue. An issue that a poll does not read (closed, or without
// the requirement label) is an error too.
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
	rate := RateLimit{Cost: resp.Data.RateLimit.Cost, Remaining: resp.Data.RateLimit.Remaining}
	// A sub-issue is read without sub-issues, so only a requirement issue
	// has a next page.
	nextPage := map[string]any{"pullRequests": snapshotPullRequests, "checks": snapshotChecks, "reviews": snapshotReviews}
	if err := c.readNextSubIssues(ctx, token, owner, repo, resp.Data.Repository.Issue, subIssuePageWithPullRequestsQuery, nextPage, &rate); err != nil {
		return IssueRead{}, fmt.Errorf("github: read issue #%d of %s/%s: %w", number, owner, repo, err)
	}
	issue, err := resp.Data.Repository.Issue.issue()
	if err != nil {
		return IssueRead{}, fmt.Errorf("github: read issue #%d of %s/%s: %w", number, owner, repo, err)
	}
	read := IssueRead{Issue: issue, RateLimit: rate}
	if ref := resp.Data.Repository.DefaultBranchRef; ref != nil {
		read.DefaultBranch = ref.Name
	}
	return read, nil
}
