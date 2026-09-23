# Template: implementation issue

Written by: the Planner.
Read by: the Implementer and the Reviewer.
One implementation issue becomes one pull request.

## Rules

- Create the issue as a sub-issue of the requirement issue.
- Make the issue body complete. The Implementer must be able to do the work from the issue body, the linked documents, and the repository. Do not rely on later comments.
- Write each acceptance criterion so that it can be checked as true or false.
- Write exact commands under "How to verify". The Implementer runs these commands.
- Add exactly one `risk/*` label. Record dependencies with the GitHub "blocked by" relationship, not in the body. Record a dependency only on a sub-issue of the same requirement issue.
- If the requirement issue has a milestone, set the same milestone on the implementation issue.

## Template

```markdown
## Context
<!-- 2 to 4 sentences: the problem, and why this issue exists. -->
Part of #<requirement issue>

## Scope
In scope:
- ...

Out of scope:
- ...

## Pointers
<!-- Files or packages to change. An existing file to use as the pattern. -->
- Change: `path/...`
- Follow the pattern in: `path/...`

## Acceptance criteria
- [ ] ...
- [ ] Tests for the new behavior are included.
- [ ] Documentation is updated, or no documentation change is needed.

## How to verify
<!-- Exact commands, and the expected result. -->

## Related documents
<!-- Links to requirement documents, design documents, and interfaces. Or "None". -->
```
