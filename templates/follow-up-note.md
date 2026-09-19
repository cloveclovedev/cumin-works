# Template: follow-up note

Written by: cumin, without an agent, as one comment on the requirement issue.
Read by: the Owner, when the Owner accepts the requirement.

cumin writes this comment after a pull request of an implementation issue is merged. The comment collects the work that is left, so that it is not lost in the pull request.

## Rules

- Write the comment only if there is something to list.
- Write at most one comment for each pull request.
- Copy the text of "Follow-up" from the pull request description as it is. Do not change the text.
- List each open non-blocking review comment with a link. A non-blocking comment is open if no reply starts with `Fixed` or `Answer`. Do not list comments with the label `praise` or `note`.

## Template

```markdown
## Follow-up from #<pull request> (<implementation issue title>)

From the pull request description:
<the text of the "Follow-up" section, or "None">

Open non-blocking review comments:
- `<path>:<line>` — <the first line of the comment> (<link>)
- ...

To do any of this work: write a new requirement issue that names the items. This list is only a record.
```
