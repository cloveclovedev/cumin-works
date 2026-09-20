# cumin-works

cumin-works is a workflow engine written in Go. It polls GitHub, moves issues between states by changing labels, and starts agents (headless Claude Code) to plan, implement, and review. The engine follows fixed rules only. It contains no AI judgment. Every judgment belongs to an agent or to the Owner.

The product name is cumin-works. The command name is `cumin`. The requirement documents call the running engine "cumin".

The goal of v0.1 (2026-10-01) is that the rebuild of the `peppercheck` product can run on this engine.

## Read first

All documents are in Japanese under `docs/ja/`. Start at `docs/ja/index.md` and follow the links to every document that your task touches. Read them before you change code. Two documents need special care:

- `docs/ja/requirements/workflow/issue-states.md` is the source of truth for every cumin action. Its tables number the actions (R1, I1, Q1, ...).
- `docs/ja/requirements/backlog.md` lists what is decided to be out of v0.1. Do not build those things.

`templates/` holds the English templates for text on GitHub. The headings in the templates are a contract between cumin, the agents, and the Owner.

## Rules that you must not break

- Do not change anything under `docs/ja/requirements/` without the Owner's approval in the conversation. If the implementation needs a requirement change, stop and ask first. `overview.md` is written by the Owner only.
- This repository is public. Do not write employer information, personal circumstances, concrete quota numbers, or local absolute paths in documents, code, tests, fixtures, commit messages, or pull requests.
- Some local files are excluded from git on purpose. Never use `git add -f`. Do not copy the content of an excluded file into a tracked file.
- Do not commit to `main`. Create a branch from `main` for every change.
- Ask the Owner before any action that affects something outside this machine: push, pull request, issue, label, comment, merge, GitHub App or ruleset change, Discord message.
- Do not read or print secret values: Keychain items, private keys, tokens, webhook URLs, `.env` files. Tests generate their own keys.
- Do not commit `*-plan.md` working documents.

## How we work

- Simple first. Look for the simplest way that meets the requirement. v1 stopped because it became too complex (a local database, a job state machine with many states, several subsystems in one binary, very long design documents). When you bring a design from v1, write the reason.
- Requirement first. Every behavior traces to a row in a requirement document. If no row covers the behavior, ask.
- Document first. Documents stay current with the code, so that another person can read what works today.
- Test first. Each requirement document ends with a table "上位要件のテスト" (tests of the top-level requirements). These tables are the starting point for acceptance tests. Prefer a few tests that prove a requirement over many unit tests.
- Verify external APIs in the official documentation before you use them: GitHub REST API, Claude Code CLI, Go libraries. Do not rely on memory. Say which page you checked. Separate verified facts from your own reasoning.

## Design constraints

- State lives on GitHub: issues, labels, pull requests, reviews, comments. cumin keeps locally only what it can lose without losing work (agent session IDs, check-fix counts, latest quota usage, quota allowance). No local database.
- cumin decides completion from facts on GitHub, never from what an agent reports. `done` from an agent only means "start checking".
- cumin changes the status label before it starts an agent. It never requests the same work twice.
- The same state must always produce the same action. Keep the decision logic as pure functions from a snapshot of GitHub facts to actions. Keep I/O (GitHub, CLI, Keychain, Discord, git) at the edges.
- Use the row numbers (R1, I3, Q1, ...) in code comments, log fields, and test names, so that code and requirements stay linked.
- Configuration is TOML. Do not hard-code values that the settings table in `cumin-core.md` lists. Secrets are in the macOS Keychain, never in files or in the repository.
- Agents start with `--setting-sources project`, receive only the token of their own GitHub App, and never receive the Owner's credentials.
- Differences between agent CLIs stay inside one adapter. v0.1 supports Claude Code only.

## Code

- Go, standard library first: `net/http`, `encoding/json`, `log/slog`, `os/exec`, `crypto/*`, `context`. The only third-party dependency expected is a TOML parser. Ask before you add another dependency.
- Module path: `github.com/cloveclovedev/cumin-works`.
- The architecture is a lightweight clean architecture, organized by feature. Dependencies point inward: I/O adapters depend on the rules, and the rules depend on nothing external. Keep it lighter than a textbook clean architecture: no use-case classes, no repository interfaces, and no mapping layers unless a real need exists. The next rules say how to apply this.
- The layout below is the plan, not a skeleton to create at once. Create each directory in the pull request that first puts code in it. Do not create empty packages. Change the plan here when the code shows a better split.
- Where shared code goes. Apply the questions in this order:
  1. Does one feature own it? Keep it in that feature's package (`internal/<feature>/`). Example: the Claude Code adapter belongs to `internal/agent/`.
  2. Is it shared and provider-neutral? Put it in `internal/core/`. The test: when the provider changes, only configuration changes. Example: config loading, logging.
  3. Is it shared and provider-specific? Put it in `internal/platform/`. The provider's types (SDK objects, HTTP DTOs) stop at this boundary. Example: GitHub, macOS Keychain.
  4. Does it wire implementations together? Put it in `cmd/cumin/`.
- `internal/platform/*` may import `internal/core/*`. `internal/core/*` never imports `internal/platform/*`. Feature packages never import each other's adapters.
- Inside a feature package, separate roles by file: pure rules and models (`domain.go`), orchestration (`service.go`), outbound I/O (`store.go` or a file named for the external system). The pure rules import no HTTP client, no `os/exec`, and no provider type.
- Do not add an interface unless a second implementation or a real testing need exists.

```text
cmd/cumin/                  entry point and subcommands (run, status, quota allow)
internal/core/config/       TOML settings, defaults, merge with the repository's .cumin/
internal/platform/github/   REST client and GitHub App authentication
internal/platform/keychain/ macOS Keychain access
internal/workflow/          rules R*, I*, Q* as pure decisions, and the polling loop
internal/agent/             worktree, token, role instructions, CLI adapter, result validation
internal/quota/             thresholds, time bands, allowance
internal/notify/            Owner notifications (Discord webhook)
internal/followup/          follow-up note after a merge
roles/                      role instructions for agents (English)
templates/                  templates for text on GitHub (English)
```

- Logs are structured (`log/slog`, JSON) on stdout. Never log tokens, keys, webhook URLs, or quota numbers at info level.
- Code, comments, test names, commit messages, and pull requests are in English.

## Tests

- Acceptance tests run with `go test ./...` and need no network and no quota. They use a fake GitHub (`httptest`) behind the real client and a fake agent CLI executable.
- Name acceptance tests after the requirement row, for example `TestCore01_ReadyIssueIsRequestedOnce`.
- Live tests (real GitHub Apps, real Claude Code) run only against the sandbox repository, only when an environment variable enables them, and only after the Owner agrees. They use quota.

## Commands

```sh
go build ./...
go vet ./...
go test -race ./...
gofmt -l .                   # must print nothing
scripts/render-diagrams.sh   # after you change a .puml file; commit the SVG too
```

## Documents

- Documents are in Japanese under `docs/ja/`. Do not use bold text. Use headings, lists, and tables.
- Development procedures go under `docs/ja/development/`. Update them in the same pull request as the code.
- Diagrams are PlantUML. Keep the `.puml` next to the document and render SVG with the script.

## Commits and pull requests

- Conventional Commits: `<type>(<scope>): <description>`. Types: `feat`, `fix`, `chore`, `docs`, `refactor`, `test`, `ci`, `perf`, `style`.
- One purpose for each pull request. Aim for the size in `docs/ja/requirements/policies/issue-sizing.md`.
- The pull request description says which requirement rows and which acceptance tests it covers, and whether documents changed.
