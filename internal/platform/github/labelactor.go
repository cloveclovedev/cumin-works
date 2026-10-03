package github

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// The query of the actor of the newest event that added a label to one
// issue and to its sub-issues. It is the query of the label times with the
// field `actor` of LabeledEvent. Measured on 2026-10-03 on cumin-works: the
// schema of LabeledEvent has `actor` (an Actor, which can be null),
// `__typename` of the actor is "User" for a person and "Bot" for a GitHub
// App, and the query costs 1 point. It runs before each start of an agent
// (docs/ja/designs/poll.md, the topic on the login of the Owner).
const labelActorQuery = `query($owner: String!, $name: String!, $number: Int!, $subIssues: Int!, $events: Int!) {
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

fragment labeled on LabeledEvent { createdAt label { name } actor { __typename login } }`

// LabelActor is the account that added a label. Type is the type of the
// account as GraphQL names it: "User" for a person, "Bot" for a GitHub App.
// The zero value says that no event added the label, or that the account
// of the event no longer exists. At is the time of the event; it is zero
// when no event added the label.
type LabelActor struct {
	Login string
	Type  string
	At    time.Time
}

// ReadLabelActor reads the actor of the newest event that added the label
// to the issue. When the issue has no such event, the newest such event
// among its sub-issues answers; an implementation issue has no sub-issues.
// With no such event at all, the actor is the zero value.
func (c *AppClient) ReadLabelActor(ctx context.Context, token, owner, repo string, number int, label string) (LabelActor, RateLimit, error) {
	variables := map[string]any{
		"owner": owner, "name": repo, "number": number,
		"subIssues": snapshotSubIssues, "events": labelTimesEvents,
	}
	var resp labelActorResponse
	request := map[string]any{"query": labelActorQuery, "variables": variables}
	if err := c.do(ctx, token, http.MethodPost, "/graphql", "/graphql", request, http.StatusOK, &resp); err != nil {
		return LabelActor{}, RateLimit{}, fmt.Errorf("github: read the actor of the label %s of %s/%s#%d: %w", label, owner, repo, number, err)
	}
	if len(resp.Errors) > 0 {
		var messages []string
		for _, e := range resp.Errors {
			messages = append(messages, e.Message)
		}
		return LabelActor{}, RateLimit{}, fmt.Errorf("github: read the actor of the label %s of %s/%s#%d: %s", label, owner, repo, number, strings.Join(messages, "; "))
	}
	rate := RateLimit{Cost: resp.Data.RateLimit.Cost, Remaining: resp.Data.RateLimit.Remaining}
	if resp.Data.Repository == nil || resp.Data.Repository.Issue == nil {
		return LabelActor{}, rate, fmt.Errorf("github: read the actor of the label %s of %s/%s#%d: the response has no issue", label, owner, repo, number)
	}
	issue := resp.Data.Repository.Issue
	if event, ok := newestLabelEvent(label, issue.TimelineItems.Nodes); ok {
		return event.labelActor(), rate, nil
	}
	if issue.SubIssues.PageInfo.HasNextPage {
		return LabelActor{}, rate, fmt.Errorf("github: issue #%d has more than %d sub-issues", number, snapshotSubIssues)
	}
	var events []labelActorNode
	for _, sub := range issue.SubIssues.Nodes {
		events = append(events, sub.TimelineItems.Nodes...)
	}
	event, _ := newestLabelEvent(label, events)
	return event.labelActor(), rate, nil
}

// newestLabelEvent returns the newest of the events that added the label.
func newestLabelEvent(label string, events []labelActorNode) (labelActorNode, bool) {
	var newest labelActorNode
	found := false
	for _, event := range events {
		if event.Label == nil || event.Label.Name != label {
			continue
		}
		if !found || event.CreatedAt.After(newest.CreatedAt) {
			newest, found = event, true
		}
	}
	return newest, found
}

// The GraphQL response. It stops in this package.
type labelActorResponse struct {
	Data struct {
		Repository *struct {
			Issue *struct {
				Number        int `json:"number"`
				TimelineItems struct {
					Nodes []labelActorNode `json:"nodes"`
				} `json:"timelineItems"`
				SubIssues struct {
					PageInfo pageInfo `json:"pageInfo"`
					Nodes    []struct {
						Number        int `json:"number"`
						TimelineItems struct {
							Nodes []labelActorNode `json:"nodes"`
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

// labelActorNode is one LabeledEvent with its actor. The actor is null
// when the account no longer exists.
type labelActorNode struct {
	CreatedAt time.Time `json:"createdAt"`
	Label     *struct {
		Name string `json:"name"`
	} `json:"label"`
	Actor *struct {
		Type  string `json:"__typename"`
		Login string `json:"login"`
	} `json:"actor"`
}

func (n labelActorNode) labelActor() LabelActor {
	if n.Actor == nil {
		return LabelActor{At: n.CreatedAt}
	}
	return LabelActor{Login: n.Actor.Login, Type: n.Actor.Type, At: n.CreatedAt}
}
