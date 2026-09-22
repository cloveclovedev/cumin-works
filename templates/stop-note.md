# Template: stop note

Written by: cumin, without an agent, as one comment on the implementation issue.
Read by: the Owner.

cumin writes this comment when it stops an issue and hands it back to the
Owner, and the reason is its own: a check of cumin failed, or an agent run
ended abnormally. When an agent returns `blocked`, cumin posts the
`blocked_reason` of the agent instead, which follows
[decision-request.md](decision-request.md).

## Rules

- Write one comment for each stop.
- Keep the heading `## Stopped for the Owner` exactly as written.
- Write the reason in one sentence: what cumin checked, and what it found.
- Name the row of the table in `issue-states.md` that stopped the issue, so
  that the Owner can read the rule that applies.
- Say what the Owner does to start the work again.

## Template

```markdown
## Stopped for the Owner

Row: <the row of issue-states.md, such as I2>
Reason: <one sentence: what cumin checked, and what it found>
Pull request: #<number>, or None
Retried: <once, or no>

To continue: <one sentence: what the Owner does>. Then add the label `cumin/status/ready` to this issue.
```

## Example

```markdown
## Stopped for the Owner

Row: I2
Reason: The Implementer reported done, but no open pull request closes this issue.
Pull request: None
Retried: no

To continue: check the work directory and the branch, then say in a comment how to go on. Then add the label `cumin/status/ready` to this issue.
```
