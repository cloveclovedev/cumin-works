# Template: pull request description

Written by: the Implementer.
Read by: the Reviewer and the Owner.

## Rules

- Title: use Conventional Commits, `<type>(<scope>): <description>`. Write the description as an order: "add ...", not "added ...".
- Write `Closes #<implementation issue>` so that the merge closes the issue.
- Keep the description within 40 lines, folded blocks excluded. The Owner reads it on a phone and decides from "What", the diagram, "Design", and the table under "How it was checked".
- Under "What", give the same information that the diff of a design note gives: how it worked before, what this pull request changes, and the approach. Write it as "Before" and "After", each one to three lines, or as a diagram (a text diagram of at most 40 characters wide, or an image of the repository at the commit of this branch, readable on a phone). An identifier never stands alone; add its meaning.
- Under "How it was checked", write one row for each thing that changed: what it is, how you checked it, and the result. Put the command output in a `<details>` block. Do not only say that the tests pass.
- Update the description when you push more commits, so that it still describes the whole change.
- Under "Follow-up", write only work that you noticed yourself and that is outside the scope of the issue. cumin copies this section to the requirement issue after the merge. Do not create issues.
- If someone must do something after the merge, write it under "Follow-up", and nowhere else. cumin reads only "Follow-up". Text under "Notes for the reviewer" is lost after the merge.

## Template

```markdown
## What
<!-- One or two sentences, written as an order: "Add ...". Then "Before" and "After" (one to three lines each) or a diagram: how it worked, what changes, and the approach. -->
Before: ...
After: ...

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
<!-- Files that you updated. Or "No documentation change is needed." -->

## Follow-up
<!-- Work outside the scope of the issue that you noticed, with the reason. Or "None". Do not copy review comments here. -->
```

## Example

````markdown
## What
Hand the issue back to the Owner when the I2 verification (open pull request, author, head pushed) fails after `done`.

Before: a failed verification was logged; the issue kept `cumin/status/implementing` with nothing on GitHub to say why.
After: cumin posts one comment in a fixed shape (`templates/stop-note.md`), sets `cumin/status/awaiting-owner-decision`, and sends one notification that names the failed check. The comment is a template of cumin, not of an agent, so that its shape stays a contract.

```
Implementer done
   |  I2 verify
   +-- pass --> awaiting-checks
   +-- fail --> comment
                + awaiting-owner-decision
                + one notification
```

## Why
A stopped issue was invisible to the Owner. The same sentence goes into the comment and the notification, so the Owner reads the same words in both places.
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
- `designs/poll.md` (the way back to the Owner), `designs/poll-verify.puml` and its SVG, `designs/code-layout.md`, `templates/stop-note.md` (new).

## Follow-up
- The retry after an abnormal end is the next issue; it calls the same step with `Retried: once`.
````
