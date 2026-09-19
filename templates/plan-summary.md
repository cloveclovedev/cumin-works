# Template: plan summary

Written by: the Chief Engineer, as one comment on the requirement issue.
Read by: the Owner, to approve the plan.

## Rules

- Write the comment after you create all implementation issues.
- Show that every requirement is covered by at least one issue.
- Write every assumption that you made. The Owner corrects wrong assumptions before work starts.

## Template

```markdown
## Plan for approval

Summary: I split this requirement into <N> issues. Each issue is one pull request.

| Order | Issue | What it delivers | Blocked by | Risk | Reason for the risk |
|---|---|---|---|---|---|
| 1 | #101 <title> | ... | None | risk/low | ... |
| 2 | #102 <title> | ... | #101 | risk/high | Database migration |

Requirement coverage:
- "<requirement 1>" -> #101, #102
- "<requirement 2>" -> #103

Not included:
<!-- Items from "Out of scope", and anything that you left out, with the reason. Or "None". -->

Assumptions:
<!-- Things that you decided because the requirement did not say. Or "None". -->

Please check:
<!-- The points where you most want the Owner's attention. Or "None". -->

To approve: check each issue and its risk label. Correct them if needed. Then add the label `cumin/status/ready` to each issue that can start.
```
