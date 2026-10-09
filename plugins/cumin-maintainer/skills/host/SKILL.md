---
name: host
description: Call the general tools of the Host of cumin. Use when the Maintainer asks whether cumin is running, asks to replace the binary of cumin after a merge, or asks to run the live scenario E2E-1 on the sandbox.
---

# Call the general tools of the Host

The checkout of cumin holds three general tools under `scripts/`. A person runs them from a terminal, and this skill runs the same tools. The skill adds no step of its own: when a tool fails, the session reports its message, and does not do the step by hand. The options and the steps of each tool are in `$CUMIN_SOURCE_DIR/docs/ja/guides/host-tools.md`.

## The checkout that holds the tools

- The environment variable `CUMIN_SOURCE_DIR` names the checkout of cumin that holds `scripts/`.
- When `CUMIN_SOURCE_DIR` is not set, ask the Maintainer for the path, and set the variable first in the same command: `export CUMIN_SOURCE_DIR=<path>; <command>`. An assignment in front of the command alone does not work, because the shell expands the variable in the words of the command before it applies the assignment. Do not search the disk for a checkout.
- The binary is built from that checkout. The session edits no file there. The only change is `git -C "$CUMIN_SOURCE_DIR" pull --ff-only`, after the Maintainer agrees.

## The three tools

| Tool | When to call it | Ask first |
|---|---|---|
| `cumin-health.sh` | The Maintainer asks "is it running", or a watch reports an old last poll | No. The tool only reads |
| `replace-binary.sh` | A merge changed code outside the tests, and cumin must run the new binary | Yes. The tool stops cumin and replaces its binary |
| `live-scenario.sh` | The Maintainer asks for the live scenario E2E-1 | Yes. The tool stops cumin and points the Host at the sandbox |

- The question names the tool and what it changes on the Host. One answer covers one call.
- `live-scenario.sh` needs the sandbox repository and the Host settings file for the sandbox. Ask the Maintainer for both. The skill holds no name of a repository.
- `replace-binary.sh --dry-run` only reads. Offer it when the Maintainer wants to see the steps first.
- Before `replace-binary.sh`, the checkout must be at the head of its default branch. The Maintainer confirms the merged change, then the session asks and runs `git -C "$CUMIN_SOURCE_DIR" pull --ff-only`.
- `replace-binary.sh` and `live-scenario.sh` wait for the current runs of the agents, for up to one hour. Start them in the background, with the background option of the shell tool.

## How to read the exit code

| Tool | 0 | 1 | 2 |
|---|---|---|---|
| `cumin-health.sh` | The last poll is new and has no error | cumin may not run: the last line starts with `error:` and says why | A wrong option |
| `replace-binary.sh` | All five steps passed: the new binary runs, and two polls have no error | A step failed. The last `step <n> of 5` line names the step, and the `error:` line says the state of cumin | A wrong option |
| `live-scenario.sh` | The test passed, and the Host is back | Anything else. Read the lines `test:` and `host:` when they are there, else the `error:` line | A wrong option |

- Report the exit code and the last lines as they are. Only the exit code 0 is a success. Empty output is not a success.
- A message that holds "time limit" means that a wait of the tool ended at its limit. Say so, and do not start the tool again without the Maintainer.
- After the exit code 1 of `replace-binary.sh` or of `live-scenario.sh`, a message can name the command that brings cumin back. Show that command to the Maintainer. Run it only on their word.
- A `host: failed` line of `live-scenario.sh` means that the Host may still point at the sandbox. Tell the Maintainer in the first line of the report.

## The new binary before the next approval

A merged change of code outside the tests does not reach the Host by itself: cumin runs the old binary until `replace-binary.sh` passes.

- After such a merge, replace the binary before the session approves the next pull request, and before it proposes an approval to the Maintainer.
- A merge that changes only tests or only documents needs no new binary.
- When the binary is not replaced, say so in the report of the next pull request. cumin would check and merge that pull request with the old rules.

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

- `"$CUMIN_SOURCE_DIR"/scripts/cumin-health.sh`
- `"$CUMIN_SOURCE_DIR"/scripts/cumin-health.sh --wait-polls <n> --timeout <seconds>`
- `"$CUMIN_SOURCE_DIR"/scripts/replace-binary.sh --dry-run`

Changes the Host, so ask first:

- `git -C "$CUMIN_SOURCE_DIR" pull --ff-only`
- `"$CUMIN_SOURCE_DIR"/scripts/replace-binary.sh`
- `"$CUMIN_SOURCE_DIR"/scripts/live-scenario.sh --repo <owner>/<sandbox> --config <file>`

This skill runs no command that GitHub records. The options of the three tools are in `$CUMIN_SOURCE_DIR/docs/ja/guides/host-tools.md`.
