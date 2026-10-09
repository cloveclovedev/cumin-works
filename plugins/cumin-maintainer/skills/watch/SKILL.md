---
name: watch
description: Watch for an issue that waits for a Maintainer, beside cumin. Use when the Maintainer asks the session to watch, to wait for the next plan, pull request, acceptance, or decision request, or to say when something new waits. A script reads the monitor file of cumin in the background and reports only the new entries.
---

# Watch for an issue that waits for a person

cumin writes each issue that waits for a Maintainer into the list `waiting` of the monitor file `~/.local/state/cumin/monitor.json`. This skill starts one script that waits for a new entry of that list. The script reads only that local file. The session types no loop of its own, and it does not ask GitHub what waits.

## Steps

1. Start `${CLAUDE_SKILL_DIR}/wait-for-waiting.sh --seen ~/.local/state/cumin/watch-seen` in the background, with the background option of the shell tool. Start one script at a time. Do not replace it with a loop or with a read of GitHub typed in the chat.
2. Tell the Maintainer in one line that the watch runs, and go on with the other work of the session.
3. When the script ends, read its exit code with the table under "What the script says". Only the exit code 0 means that a new entry waits. Empty output never means "nothing waits".
4. After the exit code 0, report each printed entry to the Maintainer: the kind, the issue or the pull request with its meaning, and the address. Name the skill of its kind from "The skill of each kind".
5. Start the script again as in step 1, with the same `--seen` file. Then take up the entries, one at a time, in the order that the Maintainer names.
6. Stop the watch when the Maintainer says so, or at the end of the session: stop the background script, and start no new one.

## What the script says

The script prints one line for each new entry, with a tab between the fields: the kind, the repository, the number of the issue, the title, the address. The address of a `merge-decision` entry is the pull request.

| Exit code | Meaning | What the session does |
|---|---|---|
| 0 | At least one new entry is printed | Go on with step 4 |
| 2 | A wrong argument, or the `--seen` file cannot be written | Say so, with the message. Correct the argument, and start again once |
| 124 | The time limit ended with no new entry. The message holds "time limit" | Read the message as the list below says, then start again as in step 1 |

- The default time limit is 1500 seconds. `--timeout <seconds>` changes it. Every wait has a limit, so a script that hangs shows.
- At the exit code 124, the message names a last read that failed: a missing file, an empty file, a file that is not JSON, or a file that is not a monitor file of version 1. Tell the Maintainer, because the watch saw nothing during that time.
- At the exit code 124, the message names a `last_poll.at` that is more than 180 seconds old. cumin may not run. Tell the Maintainer, and propose `cumin status`. `--stale <seconds>` changes the limit for a Host that polls at a longer interval.
- A message of the exit code 124 with neither of the two means that nothing new waits. Start again without a word to the Maintainer.
- The script skips a read that fails. Such a read reports nothing as new, and it keeps the `--seen` file.
- The `--seen` file holds the entries that wait and that the script reported. An entry that leaves the list and comes back is new again. An issue whose kind changes is a new entry.
- The first call with a new `--seen` file prints every entry that waits. Do not remove the `--seen` file to see the list again: the skill `session-start` reads the whole list.

## The skill of each kind

| `kind` | What waits | Skill |
|---|---|---|
| `plan-review` | A plan of the Planner waits for the review | `plan-review` |
| `merge-decision` | A pull request waits for the merge decision | `merge-decision` |
| `decision` | A stopped issue waits for an answer | `decision-request` |
| `acceptance` | A requirement issue waits for the acceptance | No skill accepts. Report the acceptance check to the Maintainer. Use `acceptance-leftovers` when the check lists open work |

- An entry with another `kind` comes from a newer cumin. Report it as it is, and name no skill.
- The watch only reports. Each skill of the table asks its own questions before an action that GitHub records.

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

- `${CLAUDE_SKILL_DIR}/wait-for-waiting.sh --seen ~/.local/state/cumin/watch-seen`
- `${CLAUDE_SKILL_DIR}/wait-for-waiting.sh --seen ~/.local/state/cumin/watch-seen --timeout <seconds> --stale <seconds>`
- `cumin status`

The script reads the monitor file `~/.local/state/cumin/monitor.json` and calls neither `gh` nor the network. It writes one file, the `--seen` file `~/.local/state/cumin/watch-seen`, which only this script reads. It changes neither the monitor file nor the settings, the binary, or the process of cumin. This skill runs no command that GitHub records.
