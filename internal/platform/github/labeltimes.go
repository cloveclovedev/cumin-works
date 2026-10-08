package github

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// labelTimesEvents is how many of the newest label events are read for one
// issue: the events that added a label and the events that removed one. An
// issue gets a handful of labels in its life; older events are not read.
const labelTimesEvents = 100

// The query of the label times of one requirement issue and its sub-issues.
// Measured on 2026-09-29 on cumin-works: `timelineItems` with
// `itemTypes: [LABELED_EVENT]` and `last` returns the newest events of a
// label with `createdAt` and `label { name }`, and the query costs 1 point.
// Measured on 2026-10-05 on cumin-works: with UNLABELED_EVENT beside
// LABELED_EVENT, the query still costs 1 point. The events that removed a
// label are read because of the rule of puttingLabelEvents.
// It runs only for a requirement issue where "mark the requirement as in
// work" can apply (docs/ja/designs/poll.md, the topic on the label times),
// so the poll query keeps its cost.
const labelTimesQuery = `query($owner: String!, $name: String!, $number: Int!, $subIssues: Int!, $events: Int!) {
  repository(owner: $owner, name: $name) {
    issue(number: $number) {
      number
      timelineItems(itemTypes: [LABELED_EVENT, UNLABELED_EVENT], last: $events) { nodes { __typename ...labeled ...unlabeled } }
      subIssues(first: $subIssues) {
        pageInfo { hasNextPage }
        nodes {
          number
          timelineItems(itemTypes: [LABELED_EVENT, UNLABELED_EVENT], last: $events) { nodes { __typename ...labeled ...unlabeled } }
        }
      }
    }
  }
  rateLimit { cost remaining }
}

fragment labeled on LabeledEvent { createdAt label { name } }
fragment unlabeled on UnlabeledEvent { createdAt label { name } }`

// LabelTimes holds, for each issue number, the time of the event that last
// put each label on the issue (puttingLabelEvents). A label that was never
// added has no entry.
type LabelTimes map[int]map[string]time.Time

// ReadLabelTimes reads when each label was last put on the requirement
// issue and on each of its sub-issues. GitHub records the adding of a label
// as an event of the issue.
func (c *AppClient) ReadLabelTimes(ctx context.Context, token, owner, repo string, number int) (LabelTimes, RateLimit, error) {
	variables := map[string]any{
		"owner": owner, "name": repo, "number": number,
		"subIssues": labelSubIssues, "events": labelTimesEvents,
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
		return nil, rate, fmt.Errorf("github: issue #%d has more than %d sub-issues", number, labelSubIssues)
	}
	times := LabelTimes{}
	times.add(issue.Number, issue.TimelineItems.Nodes)
	for _, sub := range issue.SubIssues.Nodes {
		times.add(sub.Number, sub.TimelineItems.Nodes)
	}
	return times, rate, nil
}

func (t LabelTimes) add(number int, events []labelEventNode) {
	times := map[string]time.Time{}
	for label, event := range puttingLabelEvents(events) {
		times[label] = event.CreatedAt
	}
	t[number] = times
}

// puttingLabelEvents returns, for each label, the event that last put the
// label on the issue: the first event that added the label after the last
// event that removed it before. The events are in the order of the
// timeline, oldest first. An event that adds a label again, with no event
// that removed the label in between, does not count: a label that is on an
// issue cannot be added again. Measured on 2026-10-05: GitHub can record
// such an event late, with the account that created the issue
// (docs/ja/evidence/measured-constraints.md). With no event that removed
// the label among the events, the first event that added it answers. Every
// read of the account and of the time of a label uses this rule.
func puttingLabelEvents(events []labelEventNode) map[string]labelEventNode {
	putting := map[string]labelEventNode{}
	on := map[string]bool{}
	for _, event := range events {
		if event.Label == nil {
			continue
		}
		name := event.Label.Name
		if event.Type == unlabeledEventType {
			on[name] = false
			continue
		}
		if !on[name] {
			putting[name], on[name] = event, true
		}
	}
	return putting
}

// The GraphQL response. It stops in this package.
type labelTimesResponse struct {
	Data struct {
		Repository *struct {
			Issue *struct {
				Number        int `json:"number"`
				TimelineItems struct {
					Nodes []labelEventNode `json:"nodes"`
				} `json:"timelineItems"`
				SubIssues struct {
					PageInfo pageInfo `json:"pageInfo"`
					Nodes    []struct {
						Number        int `json:"number"`
						TimelineItems struct {
							Nodes []labelEventNode `json:"nodes"`
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

// unlabeledEventType is the `__typename` of an event that removed a label.
const unlabeledEventType = "UnlabeledEvent"

// labelEventNode is one LabeledEvent or one UnlabeledEvent. A label that
// was deleted from the repository leaves the event with no label. The actor
// is read only by the query of the actor of a label, and only for a
// LabeledEvent; it is null when the account no longer exists.
type labelEventNode struct {
	Type      string    `json:"__typename"`
	CreatedAt time.Time `json:"createdAt"`
	Label     *struct {
		Name string `json:"name"`
	} `json:"label"`
	Actor *struct {
		Type  string `json:"__typename"`
		Login string `json:"login"`
	} `json:"actor"`
}
