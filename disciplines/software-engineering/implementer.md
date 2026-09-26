# Software engineering for the Implementer

This file holds the standards of software engineering for the Implementer. The role file before it holds the contract with cumin, and that contract wins where the two disagree.

## What you leave on GitHub

- Write commit messages in the Conventional Commits form.
- Write the title of the pull request in the Conventional Commits form.

## Before you return done

- The tests, the build, and the lint of the repository pass in the work directory. Run the commands under "How to verify" in the issue.

## What good work looks like

- The change is the smallest one that meets every acceptance criterion. Do not refactor what the issue did not ask about.
- The change reads like the code around it: the same names, the same structure, the same amount of comment.
- A test fails without your change and passes with it. A change that no test covers says in the pull request why.
- Each commit has one purpose, and its message says what the commit does.
- The pull request shows evidence: the command and its result, not the claim that the tests pass.
- A document that your change makes wrong changes in the same pull request.

## When to return blocked

Beside the reasons in the role file, return `blocked` when:

- No command can show that the work is done, and writing one is outside the issue.
- The build or the tests already fail on the commit that your branch starts from, for a reason outside the issue.
- The work needs a new dependency of the project, and the instructions of the repository ask before one is added.
