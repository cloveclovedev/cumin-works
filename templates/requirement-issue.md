# Template: requirement issue

Written by: the Owner.
Read by: the Chief Engineer.
Labels: `cumin/type/requirement` marks the issue as a requirement issue. When the text is complete, add `cumin/status/ready`.

## Rules

- Write what you want and why. Do not write how to build it, unless the method is a constraint.
- Write each requirement as a rule that can be checked as true or false.
- Write one goal for each requirement issue. If you expect more than 12 implementation issues, split the requirement. The Chief Engineer stops and proposes a split when a requirement is larger.
- Write undecided things under "Open questions". The Chief Engineer stops and asks when a question changes the plan.

## Template

```markdown
## Goal
<!-- One or two sentences. Optional pattern: When <situation>, I want <action>, so I can <result>. -->

## Why
<!-- The problem today. Who has the problem. -->

## Requirements
<!-- Rules that the result must follow. One rule per line. -->
- [ ] ...

## Out of scope
<!-- Things that the Chief Engineer must not include. -->

## Constraints
<!-- Technology, compatibility, deadline, and documents that must be followed. Add links. -->

## Open questions
<!-- Things that you have not decided. -->
```
