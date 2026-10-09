---
name: hand-over
description: Write the hand-over note for the next session beside cumin. Use at the end of a session of a Maintainer or the Operator, or when the Maintainer asks to hand over. The note holds the state, the open decisions, what waits for the Maintainer, and what the session learned. The skill session-start reads it.
---

# Hand over to the next session

The next session starts with no memory of this one. This skill writes one note that the skill `session-start` reads. The note replaces the note of the session before.

## Steps

1. Read the state again: run `cumin status`, and read the open pull requests and the tree of each requirement issue in work. Write no state from memory.
2. Read the old note `~/.local/state/cumin/hand-over.md` when the file exists. Keep an item of it only when the item is still true.
3. Draft the note with the four headings under "Headings of the hand-over note". Follow "What the note holds".
4. Show the draft to the Maintainer, and ask before the write. The write changes a file on the Host.
5. Check that the directory `~/.local/state/cumin/` exists. Without it, cumin never ran on this Host: say so, show the draft in the conversation, and stop. Write the whole draft to the temporary file `~/.local/state/cumin/hand-over.md.tmp`. Check that the file is not empty. Then rename it to `~/.local/state/cumin/hand-over.md`.
6. Say where the note is, and that the next session reads it with the skill `session-start`.

## Headings of the hand-over note

The note has these four headings, in this order, and no other heading of this level:

1. `## State`
2. `## Open decisions`
3. `## Waiting for the Maintainer`
4. `## What the session learned`

The skill `session-start` reads the same four headings. A test of the plugin compares the two lists.

## What the note holds

The note starts with the title `# Hand-over note` and one line with the time of the write in UTC (Coordinated Universal Time), from `date -u`.

| Heading | What goes under it |
|---|---|
| `## State` | The tree of each requirement issue in work: the requirement issue, its sub-issues, their pull requests, each with its meaning and its state. Then what this session started and did not end. |
| `## Open decisions` | Each question that has no answer yet: the question, the options, the recommendation of the session, and who decides. |
| `## Waiting for the Maintainer` | Each item that waits for the Maintainer: a plan, a merge decision, an acceptance, a decision request, with its address and what the session found on it. |
| `## What the session learned` | Facts that the next session needs and that no document holds yet: a step that failed and its reason, a preference of the Maintainer on how to work. Name the document that should hold a fact, when one should. |

- Write "None" under a heading that has no item.
- Never write the allowance of the session into the note: what the Maintainer allowed this session to do without asking. The allowance ends with the session, and the next session asks again. A preference under "What the session learned" says how to work, never what the session may do without asking.
- Write no secret into the note: no token, no key, no webhook address, and no quota number.
- The note is a local file on the Host. cumin does not read it, and it is in no repository. Commit it nowhere.
- Write the note in the language of the Maintainer. Keep the four headings in English.
- The note is no record of the work. GitHub holds the record: link to an issue or a pull request, and do not copy its text.

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
- `date -u`
- `cat ~/.local/state/cumin/hand-over.md`
- `test -d ~/.local/state/cumin`
- `test -s ~/.local/state/cumin/hand-over.md.tmp`
- `gh pr list --repo <owner>/<repository> --state open --limit 200`
- `gh issue view <number> --repo <owner>/<repository>`
- `gh api repos/<owner>/<repository>/issues/<number>/parent`
- `gh api --paginate repos/<owner>/<repository>/issues/<number>/sub_issues`

Changes the Host, so ask first:

- The write of the draft to `~/.local/state/cumin/hand-over.md.tmp`
- `mv ~/.local/state/cumin/hand-over.md.tmp ~/.local/state/cumin/hand-over.md`

This skill runs no command that GitHub records. `<owner>/<repository>` comes from the output of `cumin status`.
