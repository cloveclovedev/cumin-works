# Template: implementation issue

Written by: the Planner.
Read by: the Implementer and the Reviewer.
One implementation issue becomes one pull request.

## Rules

- Keep the issue within 40 lines. The Implementer reads it once; the Owner reads it on a phone.
- Start with "Where this fits": the part of the product that this issue changes. Take the smallest diagram of the design documents that shows that part, as an image, and name in one sentence the box or the arrow that this issue builds. Then the rules of the documents that it implements, each with its meaning. The granularity is right when a reader can point at one place in the diagram. When no diagram shows the part, say so and add "the design document gains a diagram of this part" to the acceptance criteria.

- Create the issue as a sub-issue of the requirement issue.
- Make the issue body complete. The Implementer must be able to do the work from the issue body, the linked documents, and the repository. Do not rely on later comments.
- Write each acceptance criterion so that it can be checked as true or false.
- Write exact commands under "How to verify". The Implementer runs these commands.
- Add exactly one `risk/*` label. Record dependencies with the GitHub "blocked by" relationship, not in the body. Record a dependency only on a sub-issue of the same requirement issue.
- If the requirement issue has a milestone, set the same milestone on the implementation issue.

## Template

```markdown
## Where this fits
<!-- The smallest diagram of the design documents that shows the part this issue changes, as an image (SVG of the repository at a commit), and one sentence that names the box or the arrow. Then the rules of the documents that it implements, each with its meaning in five words or fewer. -->

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

## Example

````markdown
## Where this fits
![implementation issue states](https://raw.githubusercontent.com/<owner>/<repo>/<commit>/docs/ja/requirements/workflow/implementation-issue-states.svg)

The arrow from `awaiting-checks` back to `implementing`: I4 (a required check failed, fix in the same session), and its exit to `awaiting-owner-decision` at the limit.

## Context
When a required check fails, cumin asks the Implementer to fix it in the session of the last run, with the failed checks and their content. The count of such requests lives on the Host; at the limit, the issue goes to the Owner (stop step of #81).
Part of #156

## Scope
In scope:
- The pure decision of I4 and its application: label to `cumin/status/implementing`, the worktree on the branch of the pull request, the request "check fix" with `--resume`.
- The request text: repository, issue, branch, work directory, each failed check with its content.
- The count in the state file, raised before the request; at `max_check_fix_requests`, the stop step with the row I4 instead of a request.
- The end of the run as for I1: `done` verifies I2 again; `blocked` and a second abnormal end stop the issue.

Out of scope:
- I5 and the Reviewer (#157).

## Pointers
- Change: `internal/workflow/{domain,request,service}.go`, `docs/ja/designs/poll.md`, `docs/ja/getting-started.md`
- Follow the pattern in: `ImplementRequestText`, `startImplementer`, `stopForOwner` with `RowI2`

## Acceptance criteria
- [ ] A failed required check sends exactly one request across polls (the label changes first, principle 3).
- [ ] The request resumes the last session of the issue and names every failed check with its content.
- [ ] No new branch and no new pull request; after `done`, I2 runs again.
- [ ] The count grows by one per request, survives a restart, and starts at zero after `cumin/status/ready`.
- [ ] At the limit: one comment, `cumin/status/awaiting-owner-decision`, one notification, through the stop step with the row I4.
- [ ] Log lines start with `I4: `. `poll.md` and `getting-started.md` are updated. Tests for the new behavior are included.

## How to verify
`go test -race -run TestI4 ./internal/workflow/` passes; `gofmt -l .` prints nothing.

## Related documents
- `issue-states.md` (I4, sessions), `agents/implementer.md` (request kind "check fix"), `designs/poll.md`, `designs/agent-run.md` (`--resume`), `development/configuration.md` (`max_check_fix_requests`)
````
