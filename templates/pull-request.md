# Template: pull request description

Written by: the Implementer.
Read by: the Reviewer and the Owner.

## Rules

- Title: use Conventional Commits, `<type>(<scope>): <description>`. Write the description as an order: "add ...", not "added ...".
- Write `Closes #<implementation issue>` so that the merge closes the issue.
- Keep the description within 40 lines, folded blocks excluded. The Owner reads it on a phone and decides from "What", the diagram, "Design", and the table under "How it was checked".
- Show a flow or a state change as a diagram under "What": a text diagram (at most 40 characters wide) or an image of the repository at the commit of this branch, readable on a phone. A row number never stands alone; add its meaning.
- Under "How it was checked", write one row for each thing that changed: what it is, how you checked it, and the result. Put the command output in a `<details>` block. Do not only say that the tests pass.
- Under "Documentation", name first the design note topics that changed, one line each, so that the Owner can judge the direction from those lines alone.
- Update the description when you push more commits, so that it still describes the whole change.
- Under "Follow-up", write only work that you noticed yourself and that is outside the scope of the issue. cumin copies this section to the requirement issue after the merge. Do not create issues.
- If someone must do something after the merge, write it under "Follow-up", and nowhere else. cumin reads only "Follow-up". Text under "Notes for the reviewer" is lost after the merge.

## Template

```markdown
## What
<!-- One or two sentences, written as an order: "Add ...". Then the diagram of the flow or the state change, when there is one. -->

## Why
<!-- The problem, and why you chose this approach. Known limits of this approach. -->
Closes #<implementation issue>

## Acceptance criteria
<!-- Copy from the issue. Mark each criterion, and say where it is covered. -->
- [x] ... — `path/to/test`

## How it was checked
<!-- One row for each thing that changed. The command output goes into a folded block below the table. -->
| What changed | How it was checked | Result |
|---|---|---|
| ... | ... | ... |

<details><summary>Commands and output</summary>

```
$ go test -race ./...
```
</details>

## Notes for the reviewer
<!-- Where to start reading. Decisions that you are not sure about. Risk, and how to revert. Or "None". -->

## Documentation
<!-- First the design note topics that changed, one line each: which topic, what is new. Then the other documents. Or "No documentation change is needed." -->

## Follow-up
<!-- Work outside the scope of the issue that you noticed, with the reason. Or "None". Do not copy review comments here. -->
```

## Example

````markdown
## What
Hand the issue back to the Owner when the I2 verification fails after `done`: one comment from `templates/stop-note.md`, the label `cumin/status/awaiting-owner-decision`, one notification that names the failed check.

```
Implementer done
   |  I2 verify: open PR? author? head pushed?
   +-- pass --> awaiting-checks
   +-- fail --> comment + awaiting-owner-decision
                + one notification
```

## Why
`VerifyDone` already said which check failed; the poll only logged it, so the issue stayed `cumin/status/implementing` with nothing on GitHub to say why.
Closes #141

## Acceptance criteria
- [x] Each check gives its own reason, the same in the comment and in the notification — `VerificationReason`, `TestVerificationReason_OneSentenceForEachCheck`
- [x] One comment, the label, exactly one notification — `TestI2_DoneWithout...`, `...OfAnotherAuthor...`, `...NotPushed...`
- [x] The template reaches no agent — `TestTemplatesOfCumin_ReachNoAgent`

## How it was checked
| What changed | How it was checked | Result |
|---|---|---|
| The stop after a failed verification | 3 tests with the fake GitHub, a fake CLI, a fake webhook | pass, one comment, one notification each |
| The comment follows `stop-note.md` | `TestStopNote_FollowsTheTemplate` | pass |
| The diagram `poll-verify.puml` and its SVG | `scripts/render-diagrams.sh`, then `git status` | no diff |

<details><summary>Commands and output</summary>

```
$ go build ./... && go vet ./... && gofmt -l .
$ go test -race -count=1 ./...
ok  (every package)
```
</details>

## Notes for the reviewer
- Start at `stopForOwner` in `internal/workflow/stop.go`. Revert: the path only logged before.

## Documentation
- `designs/poll.md`, the way back to the Owner: a failed check gets a comment in a fixed shape.
- `designs/poll-verify.puml`: the failure branches.
- `templates/stop-note.md`: new. `designs/code-layout.md`: which templates cumin writes itself.

## Follow-up
- The retry after an abnormal end is the next issue; it calls the same step with `Retried: once`.
````
