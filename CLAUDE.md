# Pendolo

Self-hosted automation engine in Go. The AI writes automations as files; the engine runs them on its own schedule. See README.md for the overview and roadmap.

## Commands

- `make check`: tests (with -race), golangci-lint, shellcheck, template validation. Run before every commit.
- `go run ./cmd/pendolo validate --strict templates`: validate the bundled templates.

## Map

- `internal/manifest`: format and validation. `internal/engine`: executes runs. `internal/scheduler`: schedules to queued runs. `internal/store`: SQLite (migrations in `internal/store/migrations`, append-only). `internal/llm`: model gateway. `internal/server`: the long-running process. `cmd/pendolo`: CLI.
- Docs for users: `docs/manifest.md`, `docs/configuration.md`, `docs/running.md`.

## Rules

- `spec/pendolo-v1.schema.json` is the source of truth for the manifest format. When it changes, update `internal/manifest/manifest.go` (Go types), `docs/manifest.md` and the templates in the same change.
- Checks a JSON Schema cannot express go in `internal/manifest/semantic.go`, each with a test case in `validate_test.go`.
- Schema patterns use RE2 syntax: no lookahead or backreferences.
- Every template in `templates/` must pass `pendolo validate --strict`; tests enforce it.
- Code, comments, docs and templates are in English. Messages are short and say how to fix the problem.
- Dependencies: standard library first. Add a module only when it saves real work, and pin it.
- Never put secrets in manifests or tests; use `secret://<name>` references.
- Secret values must never reach a model prompt, a log or a stored record unredacted. Shell steps get a minimal environment.
- Never interpolate expressions into shell code; values reach scripts through `env`.
- The Anthropic adapter uses the official Go SDK; other providers go through the OpenAI-compatible adapter.
- Schema changes to the database go in a new numbered migration file, never by editing an applied one.
