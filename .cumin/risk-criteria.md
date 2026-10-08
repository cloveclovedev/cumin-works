# Risk criteria of cumin-works

Risk says who decides the merge. cumin merges a pull request with `risk/low`
by itself. A Maintainer decides the merge of `risk/medium` and `risk/high`.
These criteria replace the built-in ones for this repository.

| Risk | Criteria |
|---|---|
| `risk/low` | A change that cannot change how cumin behaves: tests only (`*_test.go`, `testdata/`, the fakes under `internal/platform/github/githubtest/`), documents only outside `docs/ja/requirements/` (design notes, development guides, guides, `docs/ja/evidence/`) with their diagrams, comments only, or a few lines whose effect is obvious. A change of non-test Go code is never low, even when it only moves code. |
| `risk/high` | A change that a revert cannot undo, or that moves a boundary of trust or of money: a deployment or CI setting (`.github/`), the setup of a repository (`scripts/setup-repo.sh`, `scripts/setup-repo/`), authentication, cryptography, tokens, the permissions of the GitHub Apps, a side effect on an external service, or the rules of cumin itself (`.cumin/`). |
| `risk/medium` | Everything that is not low or high: a change of non-test Go code, of the role instructions (`roles/`, `disciplines/`), or of the templates (`templates/`). |

When in doubt, take the higher one. A Maintainer decides the risk in the end.
