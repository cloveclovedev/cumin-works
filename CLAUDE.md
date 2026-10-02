# cumin-works

cumin-works is a workflow engine written in Go. It polls GitHub, moves issues between states by changing labels, and starts agents (headless Claude Code) to plan, implement, and review. The engine follows fixed rules only. It contains no AI judgment. Every judgment belongs to an agent or to the Owner.

The product name is cumin-works. The command name is `cumin`. The requirement documents call the running engine "cumin".

The first goal is that the rebuild of the `peppercheck` product runs on this engine.

## Read first

All documents are in Japanese under `docs/ja/`. Start at `docs/ja/index.md` and follow the links to every document that your task touches. Read them before you change code. Two documents need special care:

- `docs/ja/requirements/workflow/issue-states.md` is the source of truth for every cumin action.
- `docs/ja/requirements/backlog.md` lists what is decided to be out of scope for now. Do not build those things.

`roles/` holds the contract of each role with cumin, `disciplines/<discipline>/` holds the standards of one field of work for each role and the built-in risk criteria, and `templates/` holds the English templates for text on GitHub. `templates/embed.go` embeds the templates for the code. The headings in the templates are a contract between cumin, the agents, and the Owner.

## Rules that you must not break

- Do not change anything under `docs/ja/requirements/` without the Owner's approval in the conversation. If the implementation needs a requirement change, stop and ask first. `overview.md` is written by the Owner only.
- This repository is public. Do not write employer information, personal circumstances, concrete quota numbers, or local absolute paths in documents, code, tests, fixtures, commit messages, or pull requests.
- Do not commit to `main`. Create a branch from `main` for every change.
- Do not read or print secret values: Keychain items, private keys, tokens, webhook URLs, `.env` files. Tests generate their own keys.

## How we work

- Simple first. Look for the simplest way that meets the requirement.
- Requirement first. Every behavior traces to a row in a requirement document. If no row covers the behavior, ask.
- Document first. Documents stay current with the code, so that another person can read what works today.
- Test first. Each requirement document ends with a table titled "上位要件のテスト" (tests of the top-level requirements). These tables are the starting point for acceptance tests. Prefer a few tests that prove a requirement over many unit tests.
- Verify external APIs in the official documentation before you use them: GitHub REST and GraphQL API, Claude Code CLI, Go libraries. Do not rely on memory. Say which page you checked. Separate verified facts from your own reasoning.

## Design constraints

- State lives on GitHub: issues, labels, pull requests, reviews, comments. cumin keeps locally only what it can lose without losing work (see `docs/ja/designs/cumin-core.md`). No local database.
- cumin decides completion from facts on GitHub, never from what an agent reports. `done` from an agent only means "start checking".
- cumin changes the status label before it starts an agent. It never requests the same work twice.
- The same state must always produce the same action. Keep the decision logic as pure functions from a snapshot of GitHub facts to actions. Keep I/O (GitHub, CLI, Keychain, Discord, git) at the edges.
- Name each cumin action in plain English words in code, comments, log lines, and test names (for example "verify done", "wait for the checks"). Never use a row code of `issue-states.md` (such as R1 or I3) alone.
- Configuration is TOML. Do not hard-code values that the settings table in `cumin-core.md` lists. Secrets are in the macOS Keychain, never in files or in the repository.
- An agent receives only the token of its own GitHub App, never the Owner's credentials, and starts isolated from the Host user's settings (`docs/ja/designs/agent-run.md`).
- Differences between agent CLIs stay inside one adapter in `internal/agent/`.

## Code

- Go, standard library first. Do not rebuild a large or security-sensitive component that a well-maintained library already provides: propose the library with your reasons and ask before you add it.
- Module path: `github.com/cloveclovedev/cumin-works`.
- The architecture is a lightweight clean architecture, organized by feature. Dependencies point inward: I/O adapters depend on the rules, and the rules depend on nothing external. Keep it lighter than a textbook clean architecture: no use-case classes, no repository interfaces, and no mapping layers unless a real need exists. The next rules say how to apply this.
- Where shared code goes. Apply the questions in this order:
  1. Does one feature own it? Keep it in that feature's package (`internal/<feature>/`). Example: the Claude Code adapter belongs to `internal/agent/`.
  2. Is it shared and provider-neutral? Put it in `internal/core/`. The test: when the provider changes, only configuration changes. Example: config loading, logging.
  3. Is it shared and provider-specific? Put it in `internal/platform/`. The provider's types (SDK objects, HTTP DTOs) stop at this boundary. Example: GitHub, macOS Keychain.
  4. Does it wire implementations together? Put it in `cmd/cumin/`.
  5. Is it text that an agent receives (a role file, a discipline file, a template)? It is neither `internal/core/` nor `internal/platform/`. Keep it at the top level (`roles/`, `disciplines/`, `templates/`), embedded by its own package, so that it is easy to find and to review.
- `internal/platform/*` may import `internal/core/*`. `internal/core/*` never imports `internal/platform/*`. Feature packages never import each other's adapters.
- Inside a feature package, separate roles by file: pure rules and models (`domain.go`), orchestration (`service.go`), outbound I/O (`store.go` or a file named for the external system). The pure rules import no HTTP client, no `os/exec`, and no provider type.
- Do not add an interface unless a second implementation or a real testing need exists.
- The packages and their files are listed in `docs/ja/designs/code-layout.md`. Do not create empty packages. Update that document in the pull request that changes the layout.
- Logs are structured (`log/slog`, JSON) on stdout. Never log tokens, keys, webhook URLs, or quota numbers at info level.
- Code, comments, test names, commit messages, and pull requests are in English.

## Tests

- Acceptance tests run with `go test ./...` and need no network and no quota. They use a fake GitHub (`httptest`) behind the real client and a fake agent CLI executable.
- Name each acceptance test by what it proves, for example `TestReadyIssueIsRequestedOnce`.
- Make every test give the same result on any machine, at any time, and in any order.
- Live tests (real GitHub Apps, real Claude Code) run only against the sandbox repository and only when an environment variable enables them. They may use quota without asking; ask the Owner first only for a run that takes hours.

## Commands

```sh
go build ./...
go vet ./...
go test -race ./...
gofmt -l .                   # must print nothing
scripts/render-diagrams.sh   # after you change a .puml file; commit the SVG too
```

## Documents

- Documents are in Japanese under `docs/ja/`. Before you open a pull request, find every document that the change makes wrong, and update it in the same pull request.
- A design document describes the current design only, as `docs/ja/development/design-documents.md` says.
- Diagrams are PlantUML with English labels. Keep the `.puml` next to the document, render the SVG with the script, and commit both.

## Commits and pull requests

- Conventional Commits, one purpose for each pull request, and the pull request template in `templates/pull-request.md`.
