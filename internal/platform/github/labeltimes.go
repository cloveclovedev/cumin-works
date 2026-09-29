package github

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// labelTimesEvents is how many of the newest label events are read for one
// issue. The newest event of a label is the one that R3 needs, and an issue
// gets a handful of labels in its life; older events are not read.
const labelTimesEvents = 100

// The query of the label times of one requirement issue and its sub-issues.
// Measured on 2026-09-29 on cumin-works: `timelineItems` with
// `itemTypes: [LABELED_EVENT]` and `last` returns the newest events of a
// label with `createdAt` and `label { name }`, and the query costs 1 point.
// It runs only for a requirement issue where R3 can apply
// (docs/ja/designs/poll.md, the topic on the label times), so the poll
// query keeps its cost.
const labelTimesQuery = `query($owner: String!, $name: String!, $number: Int!, $subIssues: Int!, $events: Int!) {
  repository(owner: $owner, name: $name) {
    issue(number: $number) {
      number
      timelineItems(itemTypes: [LABELED_EVENT], last: $events) { nodes { ...labeled } }
      subIssues(first: $subIssues) {
        pageInfo { hasNextPage }
        nodes {
          number
          timelineItems(itemTypes: [LABELED_EVENT], last: $events) { nodes { ...labeled } }
        }
      }
    }
  }
  rateLimit { cost remaining }
}

fragment labeled on LabeledEvent { createdAt label { name } }`

// LabelTimes holds, for each issue number, the time of the newest event that
// added each label. A label that was never added has no entry.
type LabelTimes map[int]map[string]time.Time

// ReadLabelTimes reads when each label was last added to the requirement
// issue and to each of its sub-issues. GitHub records the adding of a label
// as an event of the issue.
func (c *AppClient) ReadLabelTimes(ctx context.Context, token, owner, repo string, number int) (LabelTimes, RateLimit, error) {
	variables := map[string]any{
		"owner": owner, "name": repo, "number": number,
		"subIssues": snapshotSubIssues, "events": labelTimesEvents,
	}
	var resp labelTimesResponse
	request := map[string]any{"query": labelTimesQuery, "variables": variables}
	if err := c.do(ctx, token, http.MethodPost, "/graphql", "/graphql", request, http.StatusOK, &resp); err != nil {
		return nil, RateLimit{}, fmt.Errorf("github: read the label times of %s/%s#%d: %w", owner, repo, number, err)
	}
	if len(resp.Errors) > 0 {
		var messages []string
		for _, e := range resp.Errors {
			messages = append(messages, e.Message)
		}
		return nil, RateLimit{}, fmt.Errorf("github: read the label times of %s/%s#%d: %s", owner, repo, number, strings.Join(messages, "; "))
	}
	rate := RateLimit{Cost: resp.Data.RateLimit.Cost, Remaining: resp.Data.RateLimit.Remaining}
	if resp.Data.Repository == nil || resp.Data.Repository.Issue == nil {
		return nil, rate, fmt.Errorf("github: read the label times of %s/%s#%d: the response has no issue", owner, repo, number)
	}
	issue := resp.Data.Repository.Issue
	if issue.SubIssues.PageInfo.HasNextPage {
		return nil, rate, fmt.Errorf("github: issue #%d has more than %d sub-issues", number, snapshotSubIssues)
	}
	times := LabelTimes{}
	times.add(issue.Number, issue.TimelineItems.Nodes)
	for _, sub := range issue.SubIssues.Nodes {
		times.add(sub.Number, sub.TimelineItems.Nodes)
	}
	return times, rate, nil
}

func (t LabelTimes) add(number int, events []labeledNode) {
	newest := map[string]time.Time{}
	for _, event := range events {
		if event.Label == nil {
			continue
		}
		if event.CreatedAt.After(newest[event.Label.Name]) {
			newest[event.Label.Name] = event.CreatedAt
		}
	}
	t[number] = newest
}

// The GraphQL response. It stops in this package.
type labelTimesResponse struct {
	Data struct {
		Repository *struct {
			Issue *struct {
				Number        int `json:"number"`
				TimelineItems struct {
					Nodes []labeledNode `json:"nodes"`
				} `json:"timelineItems"`
				SubIssues struct {
					PageInfo pageInfo `json:"pageInfo"`
					Nodes    []struct {
						Number        int `json:"number"`
						TimelineItems struct {
							Nodes []labeledNode `json:"nodes"`
						} `json:"timelineItems"`
					} `json:"nodes"`
				} `json:"subIssues"`
			} `json:"issue"`
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

// labeledNode is one LabeledEvent. A label that was deleted from the
// repository leaves the event with no label.
type labeledNode struct {
	CreatedAt time.Time `json:"createdAt"`
	Label     *struct {
		Name string `json:"name"`
	} `json:"label"`
}
