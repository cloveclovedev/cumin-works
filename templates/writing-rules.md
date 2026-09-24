# Writing rules for GitHub text

These rules apply to every issue, pull request, review, comment, and commit message that an agent writes. The readers are the Owner, who is not a native English speaker, and other agents.

1. Write short sentences. Use 20 words or fewer for instructions and 25 words or fewer for descriptions. Put one idea in each sentence.
2. Use active voice and present tense. Say who does what. Use the imperative for steps.
3. Use simple, common words. Use one verb instead of a phrasal verb when you can. Write "remove", not "get rid of".
4. Use one term for one thing. Use the same term every time. Do not use synonyms.
5. Do not use idioms, slang, humor, or references to one culture.
6. Keep the small words: "that", "who", "the", "a", "then". Do not write sentence fragments.
7. Make every "it", "this", and "they" clear. If the meaning is not clear, repeat the noun.
8. Prefer positive statements. Avoid negatives and double negatives. Put "only" directly before the word that it limits.
9. Use lists and tables for steps, options, and conditions. Do not join more than two clauses with "and", "or", or "but".
10. Define each abbreviation at first use. Write dates as `2026-09-19`. Put code, paths, and commands in backticks.

## Length and diagrams

The Owner reads most of this text on a phone, once, between other things. Short and visual wins.

11. Keep to the length limits. Implementation issue: 40 lines. Pull request description: 40 lines. Decision request: 20 lines. Plan summary and acceptance check: the table, plus 15 lines. Any other comment to the Owner: 15 lines. Folded blocks (`<details>`) do not count. Put command output, long lists, and logs inside `<details>`.
12. Never write an identifier alone: a rule or row of a document, a ticket, a state name. Add its meaning in five words or fewer, every time: "I3 (checks passed, to reviewing)", "rule 4 (one issue, one pull request)".
13. Show a flow, a state, or a structure as a diagram before the text. Use an image that the repository holds (`![...](https://raw.githubusercontent.com/<owner>/<repo>/<commit>/docs/.../<name>.svg)`) when it is readable on a phone without zoom, else a text diagram in a code block, at most 40 characters wide and 12 lines tall. Mermaid does not render in the GitHub app; do not use it. The text says only what the diagram cannot show.
14. Put the point in the first two lines. One table beats three paragraphs. Do not repeat what the issue, the design note, or the template already says; link to it.

Do not use bold text. Use headings, lists, and tables for structure.

Keep the section headings of each template exactly as written. If a section has no content, write "None". Do not delete the section.
