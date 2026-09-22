# Risk criteria

Risk says who decides the merge. cumin merges a pull request with `risk/low`
by itself. The Owner decides the merge of `risk/medium` and `risk/high`.

| Risk | Criteria |
|---|---|
| `risk/low` | A few lines whose effect is obvious. |
| `risk/high` | A change that a revert cannot undo, or that moves a boundary of trust or of money: a database migration, a deployment or CI setting, authentication, cryptography, session handling, payments, a side effect on an external service, the contract of a public API, or the rules of cumin itself (`.cumin/`). |
| `risk/medium` | Everything that is not low or high. |

When in doubt, take the higher one. The Owner decides the risk in the end.
