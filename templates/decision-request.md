# Template: decision request

Written by: any agent, when it cannot continue without a decision from the Owner.
Read by: the Owner.

Use this template in two cases:

- Blocked: write the decision request as the value of `blocked_reason`. cumin posts it as a comment on the issue.
- Unresolved after 3 review rounds: the Reviewer writes the decision request as a comment on the pull request.

## Rules

- Put the decision in the first line. The Owner must understand the question without reading the rest.
- Say what is not decided. A review that does not end usually means that something in the requirement is not decided.
- Give 2 or 3 options, with the good and bad points of each. Recommend one option.
- Keep it short. Give only the facts that the Owner needs to decide.

## Template

```markdown
## Decision needed: <the question in one sentence>

Type: Blocked | Unresolved after 3 review rounds
Work stopped: #<issue or pull request> <title>

Situation: <1 or 2 sentences: what happened.>
Not decided: <what is not decided, and where it should be written: the requirement issue, the implementation issue, or a document.>
Background: <only the facts that are needed to decide. Add links.>

| | Option | Good | Bad |
|---|---|---|---|
| A | ... | ... | ... |
| B | ... | ... | ... |

Recommendation: <A or B>, because <one sentence>.

To continue: write your decision as a comment, or edit the issue. Then add the label `cumin/status/ready` to the implementation issue.
Until then: this issue stays stopped. Other issues continue.
```

For "Unresolved after 3 review rounds", list each open blocking comment under "Background":

```markdown
Background:
1. `<path>:<line>` — Reviewer: <position in one sentence>. Implementer: <position in one sentence>.
```

## Example

```markdown
## Decision needed: Which Firebase project does the staging server use?

Type: Blocked
Work stopped: #42 Verify Firebase ID tokens in the API

Situation: The token check needs a Firebase project ID for staging. No document names a staging project.
Not decided: The Firebase project for staging. It should be written in `docs/architecture/overview.md`.
Background: `docs/architecture/overview.md` lists only the production project. #43 and #44 do not depend on this decision.

| | Option | Good | Bad |
|---|---|---|---|
| A | Create a new Firebase project for staging | Staging users are separate from production users | You must create the project and add one secret |
| B | Use the production project in staging | No setup | Test accounts are mixed with real accounts |

Recommendation: A, because test data stays out of production.

To continue: write your decision as a comment, or edit the issue. Then add the label `cumin/status/ready` to the implementation issue.
Until then: this issue stays stopped. Other issues continue.
```
