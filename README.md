# Pendolo

> Working name. Automations that keep running.

Pendolo is a self-hosted automation engine. You describe what you want in plain language; an AI turns it into an automation made of readable files; Pendolo runs it on its own schedule, and a dashboard shows everything that happens.

- **The AI writes, the engine runs.** Scheduling never depends on an agent. A language model is called only by the steps that need judgment, so runs are cheap and predictable.
- **Agnostic.** Any model provider (Anthropic, OpenAI, Gemini, OpenRouter, Ollama), any connector protocol (native API, MCP, HTTP, IMAP, CLI).
- **Runs anywhere.** One binary and one container image for Linux, macOS and Windows, on amd64 and arm64.
- **Transparent.** Every run, step, model call, cost and approval is recorded.

**Status: phase 1.** The engine runs: scheduler, run queue, `http`, `shell`, `llm` and `foreach` steps, any model provider, secrets, webhook notifications and full run history in SQLite, driven from the command line. The dashboard and the connectors come next; see the [roadmap](#roadmap).

## Try it

```bash
make build
mkdir -p automations && cp -r templates/site-checks automations/
./bin/pendolo run automations/site-checks    # once, now, with every step printed
./bin/pendolo server                         # keep it running on schedule
```

With Docker Compose, see [docs/running.md](docs/running.md).

## An automation

```yaml
apiVersion: pendolo/v1
kind: Automation
metadata:
  name: site-checks
triggers:
  - schedule: { cron: "0 8,16 * * *", timezone: Europe/Rome }
autonomy: auto
steps:
  - id: homepage
    type: http
    request: { url: "https://example.com/" }
    expect: { status: [200], maxLatency: 2s }
```

The full format is in [docs/manifest.md](docs/manifest.md), the server settings in [docs/configuration.md](docs/configuration.md), and how to run and operate Pendolo in [docs/running.md](docs/running.md). Ready-made automations are in [templates/](templates):

| Template | What it does |
| --- | --- |
| [site-checks](templates/site-checks) | Checks uptime, TLS and server health; alerts only on problems. Runs today. |
| [gmail-triage](templates/gmail-triage) | Labels new email and drafts replies, never sends. Needs the phase 3 connectors. |
| [gmail-digest](templates/gmail-digest) | Sends a daily email digest to Slack. Needs the phase 3 connectors. |
| [editorial-plan](templates/editorial-plan) | Finds article ideas, validates them on OpenSEO, writes the plan to Google Docs. Needs the phase 3 connectors. |

## Commands

| Command | What it does |
| --- | --- |
| `pendolo server` | Runs the scheduler, the workers and the health endpoint. |
| `pendolo run <folder or name>` | Runs an automation once, now, and prints every step. |
| `pendolo trigger <name>` | Queues a run for the server. |
| `pendolo list` | Lists automations and their state. |
| `pendolo runs` | Lists recent runs. |
| `pendolo show <run-id>` | Shows one run step by step, with logs, model calls and cost. |
| `pendolo pause <name>` / `resume <name>` | Stops or restarts scheduling. |
| `pendolo validate [--strict] <path>...` | Checks manifests. |
| `pendolo schema` | Prints the JSON Schema of the manifest format. |
| `pendolo healthcheck` | Exits 0 when the server is healthy. |

## Development

```bash
make check   # tests, lint, shellcheck, template validation
make build   # bin/pendolo
make docker  # container image
```

Layout:

```
cmd/pendolo/          command-line entry point
internal/manifest/    manifest types and validation
internal/engine/      runs steps: http, shell, llm, foreach; retries, timeouts, budget
internal/scheduler/   turns schedules into queued runs
internal/store/       SQLite: queue, schedule state, run history
internal/llm/         model gateway: Anthropic and OpenAI-compatible adapters
internal/expr/        ${ ... } expressions
internal/secrets/     secret:// references and redaction
internal/registry/    loads the automations folder
internal/notify/      run notifications
internal/server/      wires it all into a long-running process
internal/config/      pendolo.yaml
spec/                 JSON Schema of the manifest (source of truth)
templates/            ready-made automations
deploy/               Docker Compose and an example configuration
docs/                 documentation
```

## Roadmap

| Phase | Scope | Done when |
| --- | --- | --- |
| 0. Foundations (done) | Repo, CI, manifest schema, validator | A YAML manifest is validated from the CLI |
| 1. Engine (built, on trial) | Scheduler, queue, http/shell/llm steps, SQLite, `pendolo run` | Site checks run for 7 days without stopping |
| 2. Dashboard | Login, OIDC, roles, workspaces, encrypted secrets, run timeline, costs | A viewer sees only their own workspace |
| 3. Connectors | Gmail with a setup wizard, Slack, Google Docs, MCP, agent and approval steps | Email triage and editorial plan run end to end |
| 4. Builder and v0.1 | Plain language to manifest, templates, multi-arch image, security review | A sentence becomes an active automation |

## License

Not decided yet. Until a license is added, all rights are reserved.
