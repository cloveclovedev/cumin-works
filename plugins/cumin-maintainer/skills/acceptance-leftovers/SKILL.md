---
name: acceptance-leftovers
description: Sort the leftovers of an acceptance check of cumin. Use when a requirement issue waits for the acceptance and its acceptance check lists open work. Each leftover goes to a sub-issue, the backlog, the list of measured constraints, or a new requirement issue.
---

# Sort the leftovers of an acceptance check

The Planner of cumin writes one comment `## Acceptance check` on a requirement issue when every sub-issue is closed. The comment lists the rules that fail, the proposed fixes, and the work that is still open. A leftover is one such item. This skill gives every leftover one place, so that nothing stays only in a comment.

## Steps

1. Read the requirement issue and its newest comment that starts with `## Acceptance check`. When the issue has no such comment, say so and stop.
2. List the leftovers: each row with `Fail`, each item under "Proposed fixes", and each item under "Left after this requirement". Open the source that an item links to.
3. Remove an item that a merged pull request or an open issue already covers. Say which one covers it.
4. Sort each remaining leftover with the table under "Where a leftover goes". When two rows fit, take the first one.
5. Find the documents of the repository: the backlog and the list of measured constraints. The instructions of the repository and its document index name them. When the repository has no such document, ask the Maintainer where the leftover goes.
6. Show the result to the Maintainer as one table: the leftover, its source, the place, and the reason in one line. Ask for the decision once, for the whole table.
7. After the answer, do one action at a time, and ask before each one, as the rules of the session say. Report each result with its link.
8. End with the state: which leftovers have a place now, and which ones wait for the Maintainer.

## Where a leftover goes

| The leftover is | It goes to | How |
|---|---|---|
| Work that a rule of this requirement needs: a `Fail`, or a defect of the delivered behavior | A sub-issue of the requirement issue | Write the issue in the form of the implementation issue template of cumin. Link it as a sub-issue. The Maintainer adds `cumin/status/ready`, which sends the work back. |
| A fact of an external tool, measured or read in its official documentation | The list of measured constraints | Write one row with the evidence, the confidence, the date, and the version. A value that the product itself produces does not go there. |
| Work that is useful later, and that the Maintainer does not start now | The backlog | Write one item: what it is, the reason to wait, and the event that starts it again. Open no issue for it. |
| A new need outside the goal of this requirement, to start now | A new requirement issue | Write the issue in the form of the requirement issue template of cumin: the goal, the reason, and rules that are true or false. The Maintainer adds the labels. |
| Nothing: a duplicate, or an item that the Maintainer drops | No place | Say the reason in the report. |

- The session writes no code for a leftover. A sub-issue is the way to the Implementer of cumin.
- The backlog is requirement content in most repositories. The session drafts the text, and the Maintainer decides it.
- A change of a document goes through a pull request, one topic for one pull request. An agent of cumin cannot change a protected path, so the session and the Maintainer make that pull request by hand.
- The session does not close the requirement issue. To accept is the decision of the Maintainer.

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

- `cumin status`
- `gh issue view <number> --comments`
- `gh issue list --search <text> --state all`
- `gh pr view <number>`
- `gh pr list --search <text> --state all`
- `gh api repos/{owner}/{repo}/issues/<number>/sub_issues`

Recorded by GitHub, so ask first:

- `gh issue create --title <title> --body-file <file>`
- `gh api --method POST repos/{owner}/{repo}/issues/<number>/sub_issues -F sub_issue_id=<id>`
- `git switch -c <branch>`, `git commit`, `git push -u origin <branch>`
- `gh pr create --title <title> --body-file <file>`

The session runs these commands in the repository of the requirement issue. `gh` fills `{owner}` and `{repo}` from that repository.
