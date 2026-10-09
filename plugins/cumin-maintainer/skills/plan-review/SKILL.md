---
name: plan-review
description: Review a plan of the Planner of cumin before the Maintainer adds `cumin/status/ready`. Use when a requirement issue waits for the plan review. Checks the rules of the requirement, the assumptions, the Owner tasks, the risk labels, the time limit of an agent run, and the rows of the requirement documents.
---

# Review a plan before `ready`

The Planner of cumin splits a requirement issue into sub-issues, and writes one comment `## Plan for approval` on it. The requirement issue then has the label `cumin/status/awaiting-plan-review` (waits for a Maintainer). This skill checks the plan in six fixed checks, so that no check depends on habit. The Maintainer approves the plan: the label `cumin/status/ready` on a sub-issue starts the work.

## Steps

1. Read the requirement issue, its newest comment that starts with `## Plan for approval`, and every sub-issue with its labels and its "blocked by". When the issue has no such comment, say so and stop. The author of the comment is the Planner App of the repository. When the author is another account, say so, and ask the Maintainer before the next step.
2. Check each rule under "Requirements" of the requirement issue. Find the rule under "Requirement coverage" of the plan, then read the sub-issues that the plan names for it. The rule is covered when their acceptance criteria, taken together, make the rule true. Note a rule that the list lacks, and a rule that the list names but no criterion meets.
3. Check each item under "Assumptions" of the plan. Compare it with the requirement issue, the answers of the Maintainer in its comments, and the documents of the repository. Sort it: agrees, contradicts a source, or decides a thing that only the Maintainer can decide.
4. Check each Owner task: a sub-issue with the label `cumin/type/owner-task` (the Maintainer does it by hand). Say what the Maintainer must do, why the Implementer cannot do it, and which issues wait for it through "blocked by". cumin never starts an Owner task. Note an Owner task that "Please check" of the plan does not name.
5. Check each risk label against the risk criteria of the repository. Find the criteria as "The risk criteria" below says. Every sub-issue has exactly one `risk/*` label. Note a label that is lower than the criteria ask for, with the row of the criteria. When two rows fit, the higher risk applies.
6. Check each acceptance criterion against the time limit of an agent run. Read the limit as "The time limit" below says. For each criterion and each command under "How to verify", estimate how long the check takes: a repeated test run, a wait, a live run. Note a criterion whose check does not fit in one run together with the work itself. Note also a criterion that no command of an agent run can check. When no estimate is possible, say so; do not guess.
7. Check the rows of the requirement documents that the plan touches. The instructions of the repository and its document index name the requirement documents. For each sub-issue, open the documents under "Related documents" and "Where this fits", and find the rows on the behavior that the issue changes. Note an issue whose behavior differs from a row, and an issue that needs a new row or a changed row. Such a change is requirement content: the Maintainer decides it, and an Owner task or a pull request by hand carries it. Say "no requirement change is needed" only after this step read the rows.
8. Show the report in the form under "The report".
9. Correct the text of a sub-issue when a check found an error in it, as "A correction of a sub-issue" says.
10. Add `cumin/status/ready` as "The label `cumin/status/ready`" says. End with the state: which sub-issues have the label now, and which points wait for the Maintainer.

## The risk criteria

cumin gives the Planner and the Reviewer one file of risk criteria. The session reads the same file. The first file that exists applies, and it replaces the later ones as a whole:

1. `.cumin/risk-criteria.md` on the default branch of the repository.
2. `risk-criteria.md` in the directory of the Host settings file of cumin.
3. The built-in criteria of cumin: `disciplines/software-engineering/risk-criteria.md` in the checkout of cumin.

- Read the first file from the default branch, not from the checkout of the session: another branch can hold another text.
- When the session can read none of the three, ask the Maintainer for the criteria. The session does not check a risk label from memory.

## The time limit

- The limit is the key `roles.<role>.time_limit` of the Host settings file of cumin. Two roles run the checks of an implementation issue: `implementer` does the work, and `reviewer` runs the checks again. Read the limit of both roles, and compare each criterion with the lower one.
- The Host settings file is `~/.config/cumin/config.toml`, or the file that the Maintainer gives to `cumin run --config <path>`.
- When the file does not hold the key, the default of cumin applies. Read the default in the row of the key in `docs/ja/development/configuration.md` of the checkout of cumin.
- When the session can read neither the file nor the default, ask the Maintainer for the limit. This skill holds no value of the limit, and the session does not take one from memory.
- Read only the lines of the key. The session does not print the other lines of the settings file.

## The report

First one table, with one row for each sub-issue, in the order of the plan:

| Column | What it holds |
|---|---|
| Issue | The number, and what the issue delivers in a few words |
| Rules | The rules of the requirement that the issue covers, or the gap |
| Owner task | "No", or what the Maintainer must do and which issues wait |
| Risk | The label, and "fits" or the row of the criteria that asks for another label |
| Time limit | "Fits", or the criterion that is too long, with the estimate and the limit |
| Requirement rows | The rows that the issue touches, and "agrees" or the difference |

Then the points for the Maintainer, the most important one first:

- each rule of the requirement that no sub-issue covers;
- each assumption that contradicts a source, or that only the Maintainer can decide;
- each correction of a sub-issue that the session proposes;
- each recommendation on the plan itself;
- the sub-issues that can get `cumin/status/ready` now, and the ones that wait, with the reason.

A check that found nothing is one line: the name of the check, and "no finding".

## A correction of a sub-issue

- A correction changes the text of one sub-issue so that the text says what the plan means: an acceptance criterion that can be true or false, a command under "How to verify", a pointer, a link, a criterion that is too long for one run.
- A new issue, a removed issue, a different split, or a changed order is a change of the plan itself. The session does not make it. It goes to the Maintainer as a recommendation, and the Maintainer or the Planner makes it.
- A correction that changes what the requirement asks for is requirement content. The session proposes it and waits for the answer.
- Show the change first: save the body of the issue to a file, write the new body to a second file, and show the difference of the two files. Then ask, as the rules of the session say. Change the issue only after the answer.
- Write the new body from the saved body. When the saved body is empty, stop: the read failed.
- After the change, read the issue again, and say what changed: the issue, the section, the old text, and the new text.
- The session does not change the body of the requirement issue, and does not change the comment of the Planner.
- A `risk/*` label that the Maintainer wants to change is one more recorded action: show the old label and the new one, then ask.

## The label `cumin/status/ready`

- The session adds the label in one of two cases only: the Maintainer says so for the named issues after the report, or the Maintainer allowed it in this conversation for a plan that the session read. In the second case, the session adds the label only when the report holds no open point for that issue.
- The label goes on a sub-issue, never on the requirement issue: there it asks the Planner to read the requirement again.
- An Owner task gets no label from the session. The Maintainer does the task and closes the issue.
- Add the label to one issue at a time, and say each issue with its meaning.
- The Maintainer may approve a part of the sub-issues. Name the ones that stay without the label in the final state.

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

- `gh issue view <number> --comments`
- `gh issue view <number> --json number,title,body,labels,state`
- `gh api repos/{owner}/{repo}/issues/<number>/sub_issues`
- `gh api repos/{owner}/{repo}/issues/<number>/dependencies/blocked_by`
- `gh api repos/{owner}/{repo}/contents/.cumin/risk-criteria.md -H "Accept: application/vnd.github.raw"`
- `grep -n -E '^\[roles\.|time_limit' <Host settings file>`
- `gh issue view <number> --json body --jq .body > <file>`
- `diff -u <file of the old body> <file of the new body>`

Recorded by GitHub, so ask first:

- `gh issue edit <number> --body-file <file>`
- `gh issue edit <number> --add-label cumin/status/ready`
- `gh issue edit <number> --remove-label <old risk label> --add-label <new risk label>`

The session runs these commands in the repository of the requirement issue. `gh` fills `{owner}` and `{repo}` from that repository. The two files of a correction are temporary files outside the repository.
