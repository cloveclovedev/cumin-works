# Template: stop note

Written by: cumin, without an agent, as one comment on the implementation issue.
Read by: a Maintainer.

cumin writes this comment when it stops an issue and hands it back to a
Maintainer, and the reason is its own: a check of cumin failed, or an agent run
ended abnormally. When an agent returns `blocked`, cumin posts the
`blocked_reason` of the agent instead, which follows
[decision-request.md](decision-request.md).

## Rules

- Write one comment for each stop.
- Keep the heading `## Stopped for a Maintainer` exactly as written.
- Write the reason in one sentence: what cumin checked, and what it found.
- Name the action of cumin that stopped the issue, in the line `Step:`, as
  the tables of `issue-states.md` write it, so that a Maintainer can read
  the rule that applies.
- Say what a Maintainer does to start the work again.

## Template

```markdown
## Stopped for a Maintainer

Step: <the name of the action of issue-states.md, such as stop the implementation>
Reason: <one sentence: what cumin checked, and what it found>
Pull request: #<number>, or None
Retried: <once, or no>

To continue: <one sentence: what a Maintainer does>. Then a Maintainer adds the label `cumin/status/ready` to this issue.
```

## Example

```markdown
## Stopped for a Maintainer

Step: stop the implementation
Reason: The Implementer reported done, but no open pull request closes this issue.
Pull request: None
Retried: no

To continue: a Maintainer reads the reason, fixes what it names, and says in a comment how to go on. Then a Maintainer adds the label `cumin/status/ready` to this issue.
```
