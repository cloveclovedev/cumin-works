# Template: pull request description

Written by: the Implementer.
Read by: the Reviewer and the Owner.

## Rules

- Title: use Conventional Commits, `<type>(<scope>): <description>`. Write the description as an order: "add ...", not "added ...".
- Write `Closes #<implementation issue>` so that the merge closes the issue.
- Show evidence. Paste the result of the commands that you ran. Do not only say that the tests pass.
- Update the description when you push more commits, so that it still describes the whole change.
- Under "Follow-up", write only work that you noticed yourself and that is outside the scope of the issue. cumin copies this section to the requirement issue after the merge. Do not create issues.
- If someone must do something after the merge, write it under "Follow-up", and nowhere else. cumin reads only "Follow-up". Text under "Notes for the reviewer" is lost after the merge.

## Template

```markdown
## What
<!-- One or two sentences, written as an order: "Add ...". -->

## Why
<!-- The problem, and why you chose this approach. Known limits of this approach. -->
Closes #<implementation issue>

## Acceptance criteria
<!-- Copy from the issue. Mark each criterion, and say where it is covered. -->
- [x] ... — `path/to/test`

## How it was tested
<!-- The commands that you ran, and the summary line of each result. -->

## Notes for the reviewer
<!-- Where to start reading. Decisions that you are not sure about. Risk, and how to revert. Or "None". -->

## Documentation
<!-- Files that you updated. Or "No documentation change is needed." -->

## Follow-up
<!-- Work outside the scope of the issue that you noticed, with the reason. Or "None". Do not copy review comments here. -->
```
