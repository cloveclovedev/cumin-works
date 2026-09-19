# Template: reply to a review comment

Written by: the Implementer.
Read by: the Reviewer and the Owner.

## Rules

- Reply to every blocking comment. Write one reply for each comment thread, as a reply to the first comment of the thread.
- Start the reply with one status: `Fixed`, `Not changed`, `Deferred`, or `Answer`.
- If the Reviewer did not understand the code, first make the code clearer. Then reply.
- To disagree, give facts: a test result, a document, or a concrete problem with the other approach. Then ask one question.
- Do not repeat the same disagreement. If the Reviewer keeps the comment as blocking after your reply, fix it or leave it. After round 3, the Owner decides.
- For a non-blocking comment: fix it in the same round only if the fix is a few lines and is inside the scope of the issue. Then reply with `Fixed`. Otherwise leave it without a reply. cumin lists open non-blocking comments on the requirement issue after the merge.
- Do not resolve the comment thread.

## Template

When you fixed the problem:

```markdown
Fixed in <commit SHA>. <One sentence: what changed.>
```

When you disagree:

```markdown
Not changed. I used <X> because <reason: a fact, a document link, or a test result>.
<Y> would <concrete problem>.
Do you think that <Y> is still better for <the goal>? If yes, I will change it.
```

When the work is outside the scope of this pull request:

```markdown
Deferred. <Why it is outside the scope of this pull request.>
```

When the comment is a question:

```markdown
Answer: <the answer>. I also added a code comment in <commit SHA>, so that future readers can see this.
```
