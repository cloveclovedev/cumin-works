# Template: review

Written by: the Reviewer, with the GitHub pull request review feature.
Read by: the Implementer and the Owner.

## Rules

- Every comment has exactly one label and exactly one decoration: `(blocking)` or `(non-blocking)`.
- Allowed labels: `issue`, `todo`, `question`, `suggestion`, `nitpick`, `note`, `praise`. The labels `nitpick`, `note`, and `praise` are always non-blocking.
- A comment can be `(blocking)` only if it names one of these:
  - an acceptance criterion that is not met
  - wrong behavior, or missing tests for the new behavior
  - a security problem
  - a change outside the scope of the issue
  - documentation that must change but did not
  - a rule in the repository instructions that is broken
  - a clear performance problem at a realistic data size
- Everything else is `(non-blocking)`. Preferences are non-blocking.
- Result: if there is one or more `(blocking)` comment, submit `REQUEST_CHANGES`. If there is none, submit `APPROVE`. Never submit a review with only `COMMENT`.
- Comment on the code, not on the author.
- In round 2 and later, first check that each earlier blocking comment is fixed. Add a new blocking comment on unchanged code only if it is about wrong behavior or security.

## Comment format

```markdown
<label> (<blocking|non-blocking>): <the problem in one sentence>

Why: <what goes wrong, or which acceptance criterion, document, or rule is broken>
Fix: <the change that you suggest>
```

Use a GitHub suggestion block under "Fix" when the fix is a few lines.

Example of a blocking comment:

```markdown
issue (blocking): The handler returns the tasks of another user when `user_id` is set in the query.

Why: The user ID must come from the verified token. This breaks acceptance criterion 2 ("A user can read only their own tasks").
Fix: Read the user ID from the request context, and ignore the query parameter.
```

Example of a non-blocking comment:

```markdown
suggestion (non-blocking): This loop can use `slices.Contains`.

Why: The code is shorter and has the same behavior.
Fix: Replace the loop with `if slices.Contains(allowed, status) {`.
```

## Review summary format

Write this as the body of the review.

```markdown
Result: Changes requested (round <n> of 3)
Blocking: <count>. Non-blocking: <count>.

Blocking comments:
1. `<path>:<line>` — <the problem in one sentence>
2. ...

Acceptance criteria: <x> of <y> are met. Not met: <list, or "None">.
```

When the result is approval, write `Result: Approved (round <n> of 3)` and "Blocking comments: None".
