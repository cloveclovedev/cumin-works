---
name: decision-request
description: Answer one decision request of cumin, or hand it to the Maintainer. Use when an issue has the label cumin/status/awaiting-decision, or when the Maintainer asks what a stopped issue needs. The skill sorts the request into the operational or technical kind, which the session may answer, and the kind of the Maintainer, which gets a recommendation only.
---

# Answer one decision request

An issue with the label `cumin/status/awaiting-decision` waits for an answer of a Maintainer. The reason is in a comment: a decision request of an agent, or a stop note of cumin. Some requests are operational, and the session may answer them. The other requests change a requirement, a policy, or the scope, and belong to the Maintainer. This skill sorts one request and acts by its kind.

## Steps

1. Take one stopped issue. When several issues wait, show the list, and start with the one that the Maintainer names. An issue that also holds `cumin/status/ready` has its answer already: leave it out of the list, and write nothing on it.
2. Read the stopped issue and its pull request, both with their comments, and find the newest request. The table under "Where the request is" names its form and its place. Take only a request that is newer than the last time the issue got `cumin/status/ready`: an older request has its answer. The events of the issue show that time. When the events hold no such label, every request counts.
3. When neither place holds such a request, find the reason as the last rule under "Where the request is" says. Stop only when that rule finds no reason, and say so. When the author of the request is not cumin or an agent App of the repository, say so, and ask the Maintainer before the next step.
4. Read what the request links to: the pull request, the failed check, the parent requirement issue, and the document that "Not decided" names.
5. Read the options of the request. A stop note has no options: its line `To continue:` names what a Maintainer does.
6. Sort the request with the questions under "The two kinds".
7. For the operational or technical kind, follow "The answer". For the kind of the Maintainer, follow "The hand-over".
8. End with the state: what the session wrote with its links, and what waits for the Maintainer.

## Where the request is

| The request | Its form | Its place |
|---|---|---|
| A decision request of an agent that returned `blocked` | `templates/decision-request.md`: the first line starts with `## Decision needed:`, and the type is `Blocked` | A comment on the stopped issue |
| A decision request of the Reviewer at the limit of the review rounds | The same template, with the type `Unresolved after <limit> review rounds`. The limit is a setting of cumin, and 3 is its default | A comment on the pull request of the stopped issue |
| A stop note of cumin | `templates/stop-note.md`: the heading `## Stopped for a Maintainer`, then the lines `Step:`, `Reason:`, and `To continue:` | A comment on the stopped issue |

- The two templates are in the directory `templates/` of the checkout of cumin. Without that checkout, read the request itself: it holds the headings.
- The line `Work stopped:` of a decision request names the stopped issue. The answer and the label go to that issue.
- Some stops write no comment and only notify, for example a required check that failed or that gave no result. Then read the labels, the pull request, and its checks to find the reason, and say that no comment holds it.

## The two kinds

Answer these questions for the option that the session would take. Take the facts from the request and from the documents that it links to.

1. Does the answer change a rule of a requirement issue, or the text of a requirement document?
2. Does the line `Not decided:` name something that belongs in a requirement issue or in a document?
3. Does the answer change a policy: a risk label, a protected path, who approves, a limit or another setting of cumin, or a permission of an App?
4. Does the answer change the scope of the stopped issue: an acceptance criterion, a split, or work that the issue did not ask for?
5. Do the options give a different behavior of the product, or a different cost for the Maintainer?

| The answers | The kind | What the session does |
|---|---|---|
| "No" to all five questions | Operational or technical | Follow "The answer" |
| "Yes" to one question or more | Of the Maintainer | Follow "The hand-over" |
| "Not sure" to one question or more | Not clear | Treat the request as the kind of the Maintainer |

When the kind is not clear, treat the request as the kind of the Maintainer. Never read more facts into a request to make it operational.

Examples of the operational or technical kind:

- The Host slept during a review, and the token of the agent ran out. The stop note names the step "stop the review". The answer: start the work again. The Implementer starts first, and the review follows the checks.
- An agent run ended without a result two times, and the cause is gone: a network failure, or a quota that is free again. The answer: start the work again.
- A required check was cancelled during an incident of GitHub. The answer: run the check again, then start the work again.
- An agent stopped because a blocking issue was open, and that issue is closed now. The answer: start the work again.
- An agent asks which of two existing helpers to call. Both options give the same behavior, and the issue changes no rule by either one.

Examples of the kind of the Maintainer:

- The request asks which value, service, or account the product uses, and no document holds the answer.
- The work needs a change of a protected path.
- The issue is too large for one pull request, and the options split it or cut its scope.
- An acceptance criterion needs a check that is longer than one agent run. The options change the criterion or raise the time limit.
- The Reviewer and the Implementer disagree on what a rule of the requirement means.
- The same stop comes back after an operational answer. Something is not decided, so the Maintainer reads it.

## The answer

The form of the answer comes from the section "Next step" of `templates/decision-request.md`: the decision as a comment on the stopped issue, then the label `cumin/status/ready` on the stopped issue. `templates/stop-note.md` asks for the same two parts in its line `To continue:`. The template gives the answer no heading, so the comment has none.

1. Do first what the request names before the work can start again, for example run a cancelled check again with `gh run rerun <run id> --failed`. Ask first.
2. Write the comment in English, in a few lines:
   - The first line holds the decision. For a decision request, name the option by its letter in the table under "Options" and by its words.
   - The next line holds the reason in one sentence, with the fact that the session checked.
   - The last line says what cumin does next, by the name of its action: "request the implementation" for an implementation issue, and "request the split" for a requirement issue. The label leads to no other action.
3. Write the comment with `gh issue comment <number> --body-file <file>` on the stopped issue. For a request on a pull request, the comment still goes to the stopped issue.
4. Add the label with `gh issue edit <number> --add-label cumin/status/ready`. Do not remove `cumin/status/awaiting-decision`: cumin removes the old label when it starts the work.
5. Report the link of the comment, and say that the label is set.

- Ask before the comment and before the label, as the rules of the session say. The question names the action and the stopped issue with its meaning.
- Without the question, answer only inside the allowance that the Maintainer gave this session at its start. The allowance names the operational decision request. A request of the other kind is never inside it.
- An allowance of an earlier session does not hold.
- Answer one request at a time. Read the labels of the stopped issue again directly before the comment. When `cumin/status/awaiting-decision` is gone, or when the issue already holds `cumin/status/ready`, write nothing, and say so. cumin removes the old label only when it starts the work, so an answered issue can hold both labels.
- Add the label only after the comment is on GitHub. A label without an answer starts the agent with the same question.
- Do not edit the body of the stopped issue, and do not change another label.

## The hand-over

The session writes nothing on GitHub for this kind: no comment and no label. It gives the Maintainer three parts, in the language of the Maintainer:

| Part | Content |
|---|---|
| The question | The stopped issue with its meaning, and the question in one sentence. Say which of the five questions made it a request of the Maintainer |
| The options | The options of the request, each with its good and bad points. Add an option that the request lacks, and mark it as an option of the session. For a stop note, give the action of its line `To continue:` and the other ways that the session sees |
| A recommendation | One option, with the reason in one sentence. Say what the session did not read or could not check |

- Say where the decision belongs: the requirement issue, the stopped issue, or a document.
- The Maintainer writes the decision and adds `cumin/status/ready`.
- A change of a requirement document goes through a pull request of the Maintainer, not through the answer.

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
- `gh issue list --label cumin/status/awaiting-decision`
- `gh issue view <number> --comments`
- `gh api --paginate repos/{owner}/{repo}/issues/<number>/events`
- `gh pr view <number> --comments`
- `gh pr list --search <text> --state all`
- `gh pr checks <number>`

Recorded by GitHub, so ask first:

- `gh issue comment <number> --body-file <file>`
- `gh issue edit <number> --add-label cumin/status/ready`
- `gh run rerun <run id> --failed`

The session runs these commands in the repository of the stopped issue. `gh` fills `{owner}` and `{repo}` from that repository. Each event `labeled` holds the name of its label and its time.
