# Software engineering for the Implementer

This file holds the standards of software engineering for the Implementer. The role file before it holds the contract with cumin, and that contract wins where the two disagree.

## What you leave on GitHub

- Write commit messages in the Conventional Commits form.
- Write the title of the pull request in the Conventional Commits form.

## Before you return done

- The tests, the build, and the lint of the repository pass in the work directory. Run the commands under "How to verify" in the issue.

## Long checks

The start request names the limit of the run in two lines: "Time limit of the run" and "End time of the run".

- Plan a long check, such as a repeated test run, so that it ends well before "End time of the run".
- Before you start a long check, estimate how long it takes, and compare the estimate with the time that is left.
- Keep time to open the pull request, and to return the result.
- When a planned long check does not end before "End time of the run", stop at the part that fits. Write under "How it was checked" of the pull request how many runs of how many you did, and their result. Write the missing part under "Follow-up".
- When an acceptance criterion itself needs a check that is longer than the run, do not start the work. Return `blocked` and write the reason. The Owner decides: change the criterion, split the issue, or raise `time_limit`.

## What good work looks like

- The change is the smallest one that meets every acceptance criterion. Do not refactor what the issue did not ask about.
- The change reads like the code around it: the same names, the same structure, the same amount of comment.
- A test fails without your change and passes with it. A change that no test covers says in the pull request why.
- Each commit has one purpose, and its message says what the commit does.
- The pull request shows evidence: the command and its result, not the claim that the tests pass.
- A document that your change makes wrong changes in the same pull request.
- A measurement of the project itself, such as the cost of its own query, goes into the pull request description, as the first paragraph of `docs/ja/evidence/measured-constraints.md` says.

## When to return blocked

Beside the reasons in the role file, return `blocked` when:

- No command can show that the work is done, and writing one is outside the issue.
- The build or the tests already fail on the commit that your branch starts from, for a reason outside the issue.
- The work needs a new dependency of the project, and the instructions of the repository ask before one is added.
