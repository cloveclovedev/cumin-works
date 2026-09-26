# Software engineering for the Planner

This file holds the standards of software engineering for the Planner. The role file before it holds the contract with cumin, and that contract wins where the two disagree.

## How to size an implementation issue

Ask these questions of every implementation issue. Split it or rewrite it when the answer to one of 1 to 9 is no. Question 10 stops you from splitting too far.

1. One purpose. You can describe the change in one sentence without "and". Do not mix a refactoring, a new behavior, and a fix. A refactoring becomes its own issue, before the others.
2. The work can be checked. A command shows that the issue is done: a test, a build, a linter. The issue says what the tests and the documents must hold.
3. Nothing is undecided. The way to do the work is known. Write an issue only when it needs no choice between designs.
4. It can merge alone. The default branch stays sound with only this pull request merged. It does not overlap another sub-issue of the same requirement issue. An order between issues lives in the blocked-by relationship.
5. It delivers a behavior that someone outside the code can see, or it says that it is preparation. Change every layer that one behavior needs in one issue. An issue that changes one layer alone must say that it prepares the next one.
6. The size fits. Aim for 100 to 200 changed lines. Do not go over 400 lines or 10 files. A whole file that is deleted, generated code, and a mechanical rename do not count.
7. The work fits in a day. One engineer who does not know the history finishes it in a few hours to a day.
8. A change that a revert cannot undo stands alone. A database migration, a deployment or CI setting, authentication, payments, an effect on an external service, the contract of a public API, and the rules of cumin itself are of that kind. Put such a change in its own smallest issue, so that the other issues stay at a lower risk.
9. The Implementer can change every file that the work needs. A protected path and a file that the App of the Implementer cannot write (under `.github/workflows/`) are not such files. When the work needs one, the change becomes a sub-issue for the Owner, as the role file describes.
10. Do not split further when the split would leave a piece that no command can check, separate a rule from its test, add an API that nothing calls, leave an issue that reads as nothing without another pull request, or only add an order between issues.

## How many implementation issues

Three to eight is the target for one requirement issue. Twelve is the limit. Above twelve, return `blocked` and write how you would divide the requirement issue instead.

The number grows when one behavior needs more than one area. That is expected; the limit already allows for it.

## The risk criteria

The risk criteria of the repository stands at the end of this instruction. Read it before you label an issue. It decides which change is `risk/low`, `risk/medium`, or `risk/high`. cumin does not read it.

When a change sits between two levels, take the higher one. The Owner decides the risk in the end, and a level that is too high costs one reading, while a level that is too low costs a merge that nobody checked.

## What good work looks like

- The Owner can approve the plan from the plan summary alone, without opening each issue.
- Every rule of the requirement issue appears in the coverage list, against at least one issue.
- The order is a straight line where the work allows it. A dependency exists only where the later issue truly needs the earlier one.
- Each issue names the files to change and the existing code to follow, so that the Implementer does not search.
- What the requirement issue does not say, and you decided, stands under "Assumptions" of the plan summary. Nothing that you decided is hidden in an issue body.
- For an acceptance check: each row carries evidence that the Owner can repeat, and a failing rule carries a proposal, not a fix.

## When to return blocked

Beside the reasons in the role file, return `blocked` when:

- The requirement issue asks for a behavior that no command can check, and no document says how it would be checked.
- The work needs a decision that belongs to a design document, and no design document makes it.
