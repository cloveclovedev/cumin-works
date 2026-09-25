# Template: acceptance check

Written by: the Planner, as one comment on the requirement issue, after all sub-issues are closed.
Read by: the Owner, to accept the requirement or to send work back.

## Rules

- Check on the latest `main`, in a fresh work directory. Do not rely on the pull requests. Each pull request was checked alone; this check covers all of them together.
- Check every rule under "Requirements" in the requirement issue, one row for each rule. Also check the rules under "Constraints".
- Show evidence. Write the command that you ran and the result, or the file and line that you read. The Owner uses the evidence to tell a real failure from a mistake in the check.
- Write `Fail` when a rule is not met. Do not fix anything. Do not create or change issues. For each `Fail`, propose a fix under "Proposed fixes": a title for a new sub-issue, and one or two sentences about the work. The Owner decides what to do.
- Under "Left after this requirement", list the work that is still open: the follow-up notes on the requirement issue, without duplicates and without items that a later pull request already did. Also list work outside the scope that a pull request describes in a section other than "Follow-up".
- Keep each cell of the table to one or two lines: the name of the test or the file and line, and the result. Put command output in a `<details>` block after the table. Keep the text outside the table within 15 lines, and "Left after this requirement" to the items that need a decision or a place to go.
- Keep the heading `## Acceptance check` exactly as written. cumin looks for this heading.

## Template

```markdown
## Acceptance check

All <N> sub-issues are closed. I checked each requirement on `main` at <commit SHA>.

| Requirement | Result | Evidence | Pull requests |
|---|---|---|---|
| <the rule, shortened> | Pass | <command and result, or file and line> | #101, #102 |
| <the rule, shortened> | Fail | <what is missing, with evidence> | #103 |

Constraints:
- <each constraint, with the result and the evidence. Or "None".>

Not included, as the plan says:
<!-- Items that the plan summary listed under "Not included". Or "None". -->

Proposed fixes:
<!-- One item for each Fail: a title for a new sub-issue, and what to do. Or "None". -->

Left after this requirement:
<!-- Open follow-up work, with a link to each source. Or "None". -->

To accept: close this issue. To send work back: add a sub-issue and add the label `cumin/status/ready` to it. You can copy a proposed fix into the new sub-issue.
```
