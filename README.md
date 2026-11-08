# Pendolo

> Working name. Automations that keep running.

Pendolo is a self-hosted automation engine. You describe what you want in plain language; an AI turns it into an automation made of readable files; Pendolo runs it on its own schedule, and a dashboard shows everything that happens.

- **The AI writes, the engine runs.** Scheduling never depends on an agent. A language model is called only by the steps that need judgment, so runs are cheap and predictable.
- **Agnostic.** Any model provider (Anthropic, OpenAI, Gemini, OpenRouter, Ollama), any connector protocol (native API, MCP, HTTP, IMAP, CLI).
- **Runs anywhere.** One binary and one container image for Linux, macOS and Windows, on amd64 and arm64.
- **Transparent.** Every run, step, model call, cost and approval is recorded.

**Status: phase 0.** The manifest format and its validator exist. The engine, dashboard and connectors come next; see the [roadmap](#roadmap).

## Try it

With Go 1.26:

```bash
go run ./cmd/pendolo validate templates
```

With Docker:

```bash
docker build -t pendolo .
docker run --rm -v "$PWD/templates:/automations:ro" pendolo validate /automations
```

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

The full format is in [docs/manifest.md](docs/manifest.md). Ready-made automations are in [templates/](templates):

| Template | What it does |
| --- | --- |
| [gmail-triage](templates/gmail-triage) | Labels new email and drafts replies, never sends. |
| [gmail-digest](templates/gmail-digest) | Sends a daily email digest to Slack. |
| [site-checks](templates/site-checks) | Checks uptime, TLS and server health; alerts only on problems. |
| [editorial-plan](templates/editorial-plan) | Finds article ideas, validates them on OpenSEO, writes the plan to Google Docs. |

## Commands

| Command | What it does |
| --- | --- |
| `pendolo validate [--format text\|json] [--strict] <path>...` | Checks manifests; a folder is searched for `automation.yaml` files. |
| `pendolo schema` | Prints the JSON Schema of the manifest format. |
| `pendolo version` | Prints version information. |

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
internal/version/     build information
spec/                 JSON Schema of the manifest (source of truth)
templates/            ready-made automations
docs/                 documentation
```

## Roadmap

| Phase | Scope | Done when |
| --- | --- | --- |
| 0. Foundations | Repo, CI, manifest schema, validator | A YAML manifest is validated from the CLI |
| 1. Engine | Scheduler, queue, http/shell/llm steps, SQLite, `pendolo run` | Site checks run for 7 days without stopping |
| 2. Dashboard | Login, OIDC, roles, workspaces, encrypted secrets, run timeline, costs | A viewer sees only their own workspace |
| 3. Connectors | Gmail with a setup wizard, Slack, Google Docs, MCP, agent and approval steps | Email triage and editorial plan run end to end |
| 4. Builder and v0.1 | Plain language to manifest, templates, multi-arch image, security review | A sentence becomes an active automation |

## License

Not decided yet. Until a license is added, all rights are reserved.
