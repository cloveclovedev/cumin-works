---
name: requirement-change
description: Change the requirement documents of a repository by hand, with the Maintainer. Use when a requirement document or another file under a protected path must change, which no agent of cumin can write. One topic for one pull request, a replacement computed before any file is written, the size of the diff checked before the pull request opens and before it merges.
---

# Change the requirement documents by hand

An agent of cumin cannot change a protected path, so a person changes the requirement documents, with the help of the session. The Maintainer decides the content. The session drafts, writes, and checks. This skill keeps the change small and visible: a replacement over many files once emptied a file, because the file was opened for writing before the replacement was computed.

## Steps

1. Agree on the content. Read "What the session reads from the repository" first. Show the Maintainer the draft: each document, the text before, and the text after. Change no file before the Maintainer agrees to this draft in the conversation. A later change of the content needs a new agreement.
2. Take one topic for one pull request. Create a branch from the head of the default branch, never from another work branch. When the draft holds two topics, say so, and make two pull requests, one after the other.
3. Compute before you write. For a replacement over files, follow "A replacement over files": the new text of every file exists in memory or in a temporary file before any file of the repository changes.
4. Check the size of the diff with "The check of the diff". An emptied file or an unexpected file stops the work. Run the check before the pull request opens, and again before it merges.
5. Render the diagrams. When the change touches the source of a diagram, render it with the tooling that the repository documents, and commit the rendered SVG with its source. Never draw or edit an SVG by hand. Then run the check of step 4 again, because the rendering writes files.
6. Open the pull request in the form of the pull request template of the repository. Keep every heading of the template, and write the result of the check of step 4 where the template asks how the change was checked.
7. Merge only as "The merge" says.

## What the session reads from the repository

The session takes these facts from the repository where it runs. This skill holds none of them.

| Fact | Where the session reads it |
|---|---|
| The protected paths | The key `protected_paths` in `.cumin/config.toml` of the default branch |
| Which documents are requirement documents | The instructions of the repository and its document index |
| The tooling for the diagrams | The development guide or the instructions of the repository: the command, and where a rendered file goes |
| The pull request template | The instructions of the repository. A repository without its own template uses `templates/pull-request.md` of the checkout of cumin |
| The tests, the build, and the lint | The instructions of the repository |

- When the repository does not hold a fact, ask the Maintainer. Do not guess a path or a command.
- A change that touches no protected path does not need this skill: write an issue, and let cumin start its agent.

## A replacement over files

1. List the files that hold the old text, with a read-only search. Show the list and the count to the Maintainer.
2. For each file, read the whole file, and compute the new text into memory or into a temporary file outside the repository.
3. Check each new text before the write: it is not empty, and it differs from the old text only where the replacement applies.
4. Write each file only after step 3 passed for every file of the list.

- Never read and write the same file in one command. A shell redirect such as `<command> <file> > <file>` empties the file before the command reads it.
- Do not type a loop in the chat that writes files in place. Edit each file with the edit tool of the session, or write a short script that follows the four steps above, and show it first.
- A write to a file of the repository is not recorded by GitHub, and `git restore <file>` takes it back. A push is recorded.

## The check of the diff

Run `git diff --stat` and `git diff --numstat` against the default branch. Compare the result with the draft that the Maintainer agreed to.

| The check finds | What the session does |
|---|---|
| A file that is now empty, or a file that lost almost all of its lines | Stop. Say which file. Restore it, and find the cause before any new write |
| A file that the draft does not name | Stop. Say which file. Ask the Maintainer whether it belongs to the topic |
| A file of the draft that is missing in the diff | Stop. Say which file |
| Many more changed lines than the draft holds | Stop. Show the numbers |
| Only the files of the draft, with the expected size | Go on. Report the files and the numbers of lines |

- Empty output is not a pass. It means that nothing changed, or that the comparison is wrong. Say so.
- Before the merge, read the files and the head commit of the pull request from GitHub, not from the local branch: another push can change them.
- The check of the size does not replace the reading. Read the whole diff once before the pull request opens.

## The merge

- The session merges on the word of the Maintainer for this pull request. Ask with the number of the pull request in the question.
- Without the question, the session merges only inside the allowance that the Maintainer gave this session at its start: a pull request that this session opened and that changes documents only.
- That allowance never covers a pull request that changes a protected path, or text that an agent receives. A requirement document is under a protected path in most repositories, so such a pull request waits for the word of the Maintainer.
- An allowance of an earlier session does not hold.
- Run the check of step 4 on the pull request right before the merge. Merge the head commit that the check read, and no later one.
- Merge only when every required check passed on that head commit. A cancelled or queued check is not a pass.
- When the rules of the repository refuse the merge, say so and stop. Do not look for another way.

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

- `gh repo view --json defaultBranchRef`
- `git fetch origin`, `git status`, `git log`
- `git grep -n <text>`, `git grep -l <text>`
- `git show origin/<default branch>:.cumin/config.toml`
- `git diff --stat origin/<default branch>`, `git diff --numstat origin/<default branch>`
- `git diff origin/<default branch>`
- `gh pr view <number> --json files,additions,deletions,headRefOid,state`
- `gh pr diff <number>`
- `gh pr checks <number>`

Changes the local checkout only, so run after the Maintainer agreed to the draft:

- `git switch -c <branch> origin/<default branch>`
- The edit of each file of the draft
- The command of the repository that renders the diagrams
- The commands of the repository for the tests, the build, and the lint
- `git restore <file>`
- `git add <file>`, `git commit`

Recorded by GitHub, so ask first:

- `git push -u origin <branch>`
- `gh pr create --title <title> --body-file <file>`
- `gh pr edit <number> --body-file <file>`
- `gh pr merge <number> --match-head-commit <commit>`, with the merge method that the repository allows

The session runs these commands in the repository whose documents change.
