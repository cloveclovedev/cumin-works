package github

import (
	"context"
	"fmt"
	"time"
)

// The query of the actor of the event that put a label on one issue and on
// its sub-issues (puttingLabelEvents). It is the query of the label times
// with the field `actor` of LabeledEvent. Measured on 2026-10-03: the
// schema of LabeledEvent has `actor` (an Actor, which can be null),
// `__typename` of the actor is "User" for a person and "Bot" for a GitHub
// App, and the query costs 1 point. Measured on 2026-10-05 on cumin-works:
// with UNLABELED_EVENT beside LABELED_EVENT, it still costs 1 point. It
// runs before each start of an agent (docs/ja/designs/poll.md, the topic
// on the login of the Issue Owner). The sub-issues are read in the pages of
// the query of the label times, and only when the issue itself has no event
// of the label.
const labelActorQuery = `query($owner: String!, $name: String!, $number: Int!, $subIssues: Int!, $after: String, $events: Int!) {
  repository(owner: $owner, name: $name) {
    issue(number: $number) {
      number
      timelineItems(itemTypes: [LABELED_EVENT, UNLABELED_EVENT], last: $events) { nodes { __typename ...labeled ...unlabeled } }
      subIssues(first: $subIssues, after: $after) {
        pageInfo { hasNextPage endCursor }
        nodes {
          number
          timelineItems(itemTypes: [LABELED_EVENT, UNLABELED_EVENT], last: $events) { nodes { __typename ...labeled ...unlabeled } }
        }
      }
    }
  }
  rateLimit { cost remaining }
}

fragment labeled on LabeledEvent { createdAt label { name } actor { __typename login } }
fragment unlabeled on UnlabeledEvent { createdAt label { name } }`

// LabelActor is the account that put a label on an issue. Type is the type
// of the account as GraphQL names it: "User" for a person, "Bot" for a
// GitHub App.
// The zero value says that no event added the label, or that the account
// of the event no longer exists. At is the time of the event; it is zero
// when no event added the label.
type LabelActor struct {
	Login string
	Type  string
	At    time.Time
}

// ReadLabelActor reads the actor of the event that last put the label on
// the issue (puttingLabelEvents). When the issue has no such event, the
// newest such event among its sub-issues answers; an implementation issue
// has no sub-issues. With no such event at all, the actor is the zero
// value.
func (c *AppClient) ReadLabelActor(ctx context.Context, token, owner, repo string, number int, label string) (LabelActor, RateLimit, error) {
	return c.readLabelActor(ctx, token, owner, repo, number, label, true)
}

// ReadOwnLabelActor reads the actor of the event that last put the label
// on the issue itself (puttingLabelEvents), and never an event of a
// sub-issue. With no such event among the newest events that are read,
// the actor is the zero value. The check of who may start work uses it:
// an event of another issue must not answer for this one.
func (c *AppClient) ReadOwnLabelActor(ctx context.Context, token, owner, repo string, number int, label string) (LabelActor, RateLimit, error) {
	return c.readLabelActor(ctx, token, owner, repo, number, label, false)
}

func (c *AppClient) readLabelActor(ctx context.Context, token, owner, repo string, number int, label string, subIssues bool) (LabelActor, RateLimit, error) {
	issue, rate, err := c.readLabelEvents(ctx, token, owner, repo, number, labelActorQuery, nil)
	if err != nil {
		return LabelActor{}, rate, fmt.Errorf("github: read the actor of the label %s of %s/%s#%d: %w", label, owner, repo, number, err)
	}
	if event, ok := puttingLabelEvents(issue.TimelineItems.Nodes)[label]; ok {
		return event.labelActor(), rate, nil
	}
	if !subIssues {
		return LabelActor{}, rate, nil
	}
	if err := c.readNextLabelEvents(ctx, token, owner, repo, issue, labelActorQuery, &rate); err != nil {
		return LabelActor{}, rate, fmt.Errorf("github: read the actor of the label %s of %s/%s#%d: %w", label, owner, repo, number, err)
	}
	var newest labelEventNode
	found := false
	for _, sub := range issue.SubIssues.Nodes {
		event, ok := puttingLabelEvents(sub.TimelineItems.Nodes)[label]
		if ok && (!found || event.CreatedAt.After(newest.CreatedAt)) {
			newest, found = event, true
		}
	}
	return newest.labelActor(), rate, nil
}

func (n labelEventNode) labelActor() LabelActor {
	if n.Actor == nil {
		return LabelActor{At: n.CreatedAt}
	}
	return LabelActor{Login: n.Actor.Login, Type: n.Actor.Type, At: n.CreatedAt}
}
