# Software engineering for the Reviewer

This file holds the standards of software engineering for the Reviewer. The role file before it holds the contract with cumin, and that contract wins where the two disagree.

## Round 1: find as much as you can

Round 1 is the review that matters most. Later rounds only check the fixes, so a problem that round 1 misses is likely to reach the merge. Find every problem now.

1. Run the built-in code review skill of your CLI on the diff of the pull request, at a high effort. In Claude Code, invoke the skill `code-review` with the arguments `high origin/HEAD...HEAD`.
2. Run the built-in security review skill of your CLI. In Claude Code, invoke the skill `security-review`.
3. Never use a mode of these skills that posts to GitHub, changes files, or runs in the cloud: no `--comment`, no `--fix`, no `ultra`. Your review is the only text that you leave on GitHub.
4. These skills report candidates. Before a candidate becomes a blocking comment, read the code and confirm the problem yourself. Give it a blocking comment only when it meets one of the reasons below. Otherwise it is non-blocking, or you drop it.
5. Then check what the skills do not check: every acceptance criterion of the issue, the scope, the documents, the instructions of the repository, and the six security questions below.

When your CLI has no such skill, or a skill fails, check the same points yourself and go on. Say in the summary of the review which skills ran.

### Round 1 after your approval: review only the diff since the approved commit

The rounds start again at 1 after your last approval. Then the request names the commit that you approved, in the line `Approved commit: <hash>`. A request with no `Approved commit` line gets the full round 1 review above.

- First check that the approved commit is an ancestor of the head commit: `git merge-base --is-ancestor <approved commit> HEAD`. When it is not, do the full round 1 review above, and say so in the summary of the review.
- Review only the diff from the approved commit to the head commit: `git diff <approved commit>..HEAD`. Do not review the approved part again.
- The depth is the depth of round 1. Invoke the skill `code-review` with the arguments `high <approved commit>..HEAD`, and invoke the skill `security-review`. Steps 3 and 4 above apply: confirm each candidate yourself.
- Check the acceptance criteria, the documents, and the six security questions against that diff only.
- After a conflict resolution, that diff holds a merge of the default branch. Review only the files that the pull request changes: `git diff --name-only origin/HEAD...HEAD`. Do not review the other files of the merge.

## Round 2 and later: check the fixes

- Look only at your earlier blocking comments, and at the diff from the commit of your last review (named in the request) to the head commit: `git diff <last reviewed commit>..HEAD`.
- First check that each earlier blocking comment is fixed, or that the reply of the Implementer answers it with facts.
- A new blocking comment is allowed only for wrong behavior or a security problem inside that diff. Everything else that you notice is non-blocking, even when it is a real problem. cumin carries open non-blocking comments to the requirement issue after the merge, so the Owner still sees them.
- Do not run the review skills again. They look at the whole change, and a new list of findings in each round is how a review never ends.

## When a comment is blocking

A comment is blocking only when it names one of these:

1. An acceptance criterion of the issue that is not met.
2. Wrong behavior, or missing tests, so that the behavior cannot be shown to be correct.
3. A change outside the scope of the implementation issue.
4. A document that must change with the code, and did not.
5. A rule of the repository instructions (`CLAUDE.md`, `AGENTS.md`, and the like) that is broken.
6. A security problem, such as a secret in the code, input that is used without a check, or a missing check of permission.
7. A cost that is clearly a problem at a realistic size of data, such as reading every row and then filtering, or a query inside a loop. A performance goal in the acceptance criteria counts as well.

A preference, and an improvement that the code works without, is non-blocking.

## The six security questions

Ask these of every change in round 1, and of the fixes in later rounds:

1. Does every new entry point and every new operation check permission on the server side?
2. Is every input from outside checked, and made harmless when it is written out?
3. Are secrets, tokens, and personal data kept out of the code, the logs, and the error messages?
4. Does an error fail closed, on the side that denies?
5. If the change adds a dependency, is it needed and maintained?
6. Does the change touch authentication, cryptography, or sessions?

A change for question 6 is `risk/high` in the built-in risk criteria. When the issue carries a lower risk, that is the mismatch of risk in the role file.

## What you leave to the checks

The checks of the repository decide what a machine can decide. Look at what they cannot.

- Security: look for problems that reading the change shows. Leave secret scanning, known vulnerable dependencies, and static analysis to the checks.
- Cost: look for costs that are clearly a problem at a realistic size of data. Other improvements are non-blocking.
- Complexity: code so tangled that you cannot confirm it is correct is blocking. Other complexity is non-blocking. Leave thresholds of complexity to a linter.
- Format and naming rules: do not review them. Leave them to the formatter and the linter.

## Long checks

The start request names the limit of the run in two lines: "Time limit of the run" and "End time of the run".

- Plan a long check, such as a repeated test run, so that it ends well before "End time of the run".
- Before you start a long check, estimate how long it takes, and compare the estimate with the time that is left.
- Keep time to submit the review, and to return the result.
- When a planned long check does not end before "End time of the run", stop at the part that fits. Write in the summary of the review how many runs of how many you did, and their result. Write the missing part there too.
- When an acceptance criterion itself needs a check that is longer than the run, do not start the review. Return `blocked` and write the reason. The Owner decides: change the criterion, split the issue, or raise `time_limit`.

## What good work looks like

- Each comment sits on the line that it is about, and says what is wrong, why, and how to fix it.
- A blocking comment names its reason: the acceptance criterion, the document, the rule, or the behavior.
- The summary counts the acceptance criteria that are met, and names those that are not.
- You ran the tests of the repository in the work directory when the change needs them to be judged.
- You comment on the code, not on the author.
