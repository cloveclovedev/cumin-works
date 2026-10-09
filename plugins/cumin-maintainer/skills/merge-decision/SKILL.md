---
name: merge-decision
description: Check one pull request that waits for the merge decision of a Maintainer, and report in a fixed form. Use when an implementation issue has the label cumin/status/awaiting-merge-decision, or when the Maintainer asks whether a pull request of cumin can merge. A script checks the approval, the required checks, and the protected paths.
---

# Prepare the merge decision of one pull request

cumin asks a Maintainer for the merge decision of a pull request with `risk/medium` or `risk/high`. The label `cumin/status/awaiting-merge-decision` on the implementation issue shows that state. cumin merges after the Maintainer approves the head commit in a review. This skill checks one pull request and reports. The Maintainer decides.

## Steps

1. Take one pull request. When several pull requests wait, show the list, and start with the one that the Maintainer names.
2. Run `${CLAUDE_SKILL_DIR}/check-pull-request.sh <owner>/<repository> <number>`. Run it once. Do not replace it with a loop or with single `gh` calls typed in the chat.
3. Read the exit code with the table under "What the script says". Only the exit code 0 means that the approval and the checks are in order. Empty output never means "passed".
4. Read the implementation issue that the pull request closes, and the rules of its requirement issue.
5. Read the whole diff outside the tests with `gh pr diff <number>`. Read every changed file that is not a test, to its end. Read the tests for what they prove.
6. Compare the diff with the acceptance criteria of the issue and with the rules of the requirement issue.
7. Report under the four headings of "The report". Copy the output of the script into the second heading.
8. Wait for the answer of the Maintainer. Approve only as "The approval" says.

## What the script says

| Exit code | Meaning | What the session does |
|---|---|---|
| 0 | An approval is on the head commit, and every required check passed | Go on with step 4 |
| 1 | Not ready: the last line names each reason | Report the reasons. Recommend no approval |
| 2 | A failed read, an empty read, or a wrong argument | Say so. Run the script again once. Decide nothing from this run |
| 124 | The time limit of 60 seconds ended | Say so. Decide nothing from this run |

- An approval on an older commit does not count. The Reviewer of cumin reviews the new head commit first.
- A cancelled check is not a failed check. The Implementer cannot fix it. Propose to run it again with `gh run rerun <run id> --failed`, and ask first.
- A queued, a skipped, or a missing check is not a pass. Say which check it is, and that the pull request is not ready.
- The script lists each changed file under a protected path. An agent of cumin must not change such a file. Name each one under the third heading.
- The script reads the protected paths from `.cumin/config.toml` of the default branch. It folds the case of ASCII letters only.

## The report

Write the report in the language of the Maintainer. Keep these four headings, in this order:

```text
### What changes
### What was checked and how
### What the Maintainer should look at
### Recommendation
```

| Heading | Content |
|---|---|
| What changes | The pull request with its meaning, the issue that it closes, and the behavior that changes, in a few lines. Name the files outside the tests |
| What was checked and how | The output of the script with its exit code. Then what the session read: the diff outside the tests, and each acceptance criterion with "met" or "not met" |
| What the Maintainer should look at | Each changed file under a protected path. Each place where the diff and the issue differ. Each choice that the issue did not decide. Write "Nothing" when the list is empty |
| Recommendation | One of: approve, request changes, wait. One line with the reason |

- Recommend "approve" only after the exit code 0 and a full read of the diff outside the tests.
- Say what the session did not read or could not check. Do not write that a test passes when the session only read it.

## The approval

- The fixed question is: `Approve pull request <number> (<meaning>) at commit <first 7 characters of the head commit>?`
- Ask this question for each approval, with the number in it. Only a clear yes to this question is an approval. A word of agreement on the report is not an approval.
- Without the question, approve only inside the allowance that the Maintainer gave this session at its start. The allowance names its conditions, for example the risk label. A pull request outside these conditions gets the fixed question.
- An allowance of an earlier session does not hold. Without an allowance, ask the fixed question every time.
- Approve one pull request at a time. Run the script again directly before the approval, and approve only after the exit code 0 with the same head commit as in the report.
- Approve with `gh pr review <number> --approve`. Do not merge. cumin merges after the approval.
- After the approval, report the link of the review. Start the next pull request only after that.
- When the Maintainer asks for changes, draft the review text, and ask before `gh pr review <number> --request-changes --body-file <file>`.

## Rules of the session

These rules hold in every skill of this plugin. The session helps a Maintainer or the Operator who is there. cumin and its agents do the work of cumin.

### The session does not act in cumin's place

- Implement no issue that cumin could implement. Write the issue, and let cumin start its agent.
- Change no requirement content without the Maintainer. Propose the change, and wait for the answer.
- Approve nothing outside what the Maintainer allowed in this conversation.
- Stop when a permission check of Claude Code refuses an action. Say what was refused. Do not look for another way.

### How the session talks

- Write in the language of the Maintainer. Text that GitHub records stays in English.
- Give an issue or a pull request a short meaning on its first mention: the number, then what it is for. Never write the number alone.
- When a message involves several issues or pull requests, show the tree first: the requirement issue, its sub-issues, their pull requests, each with its meaning and its state.
- Ask for a decision in a short request: the question, the options, and a recommendation.
- Say a mistake of the session plainly, in the first lines of the next message, with what was done about it.

### Ask before an action that GitHub records or that changes the Host

- Read without asking: issues, pull requests, files, and the output of `cumin status`.
- Ask before an action that GitHub records: a comment, a label, an issue, a pull request, a review, a merge, a push.
- Ask before an action that changes the Host: the settings of cumin, its binary, its process.
- The question names the action and its target, with the number of the issue or of the pull request.
- An answer covers one action. Skip the question only for an action that the Maintainer allowed in this conversation.

## Commands

Read, without asking:

- `${CLAUDE_SKILL_DIR}/check-pull-request.sh <owner>/<repository> <number>`
- `gh pr view <number>`
- `gh pr diff <number>`
- `gh pr checks <number>`
- `gh issue view <number> --comments`
- `gh pr list --search <text>`

Recorded by GitHub, so ask first:

- `gh pr review <number> --approve`
- `gh pr review <number> --request-changes --body-file <file>`
- `gh run rerun <run id> --failed`

The script runs only `gh api` with the method GET. It reads the pull request, its reviews, its changed files, the rules of the base branch, the check runs and the commit statuses of the head commit, and `.cumin/config.toml`. It writes nothing on GitHub.
