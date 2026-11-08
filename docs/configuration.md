# Configuration

Pendolo reads `pendolo.yaml` (or the file in `$PENDOLO_CONFIG`, or `--config`). Every field has a default, so Pendolo also starts without a file. A complete example is in [deploy/pendolo.example.yaml](../deploy/pendolo.example.yaml).

| Field | Default | Meaning |
| --- | --- | --- |
| `dataDir` | `data` | Folder of the SQLite database (`pendolo.db`). Back it up. |
| `automationsDir` | `automations` | One sub-folder per automation, each with an `automation.yaml`. |
| `workers` | `4` | Runs executed at the same time. Runs of the same automation never overlap. |
| `server.listen` | `:8080` | Address of `/healthz` and of webhook triggers (`/hooks/<name>`). |
| `runner.url` | none | A `pendolo runner` that executes shell steps; without it, scripts run inside the server. |
| `runner.token` | none | Token shared with the runner, usually `secret://runner-token`. |
| `notify.slack.token` | none | Slack bot token for `slack:` channels. |
| `notify.telegram.botToken` | none | Telegram bot token for `telegram:` channels. |
| `notify.email` | none | SMTP server for `email:` channels: `host`, `port`, `security` (`starttls`, `tls`, `none`), `username`, `password`, `from`. |
| `secrets.dir` | `/run/secrets` | Folder with one file per secret. |
| `secrets.envPrefix` | `PENDOLO_SECRET_` | Prefix of environment variables holding secrets. |
| `heartbeat.url` | none | URL pinged while Pendolo is healthy; may be a `secret://` reference. |
| `heartbeat.interval` | `5m` | How often the heartbeat is sent. |
| `retention.runs` | `2160h` (90 days) | How long run history is kept. |
| `providers` | none | Language model APIs, by name. |
| `models` | none | Aliases used by automations (`fast`, `smart`, `local`...). |

Environment variables override the file: `PENDOLO_DATA_DIR`, `PENDOLO_AUTOMATIONS_DIR`, `PENDOLO_WORKERS`, `PENDOLO_LISTEN`.

## Secrets

A manifest refers to a secret as `secret://<name>`. Pendolo looks for a file `<secrets.dir>/<name>`, then for the variable `<envPrefix><NAME>`, where the name is upper-cased and `-` and `.` become `_`. So `secret://server-ssh-key` is read from `/run/secrets/server-ssh-key` or `PENDOLO_SECRET_SERVER_SSH_KEY`.

Secret values are injected only where a step uses them (HTTP URLs, headers and bodies; shell `env`), never into a model's prompt, and they are masked as `[secret:<name>]` in every stored output, log and error. Shell steps get a minimal environment: they never see Pendolo's own variables. Encrypted storage and 1Password arrive in phase 2.

## The runner

`pendolo runner` executes shell steps for a server. It needs the automations folder (read-only is enough) and a token of at least 16 characters, from `PENDOLO_RUNNER_TOKEN` or the file in `--token-file` (default `/run/secrets/runner-token`). It listens on `:8090` (`--listen`). It has no database, no configuration and no secrets: a script receives only the values its step passes in `env`. The server sends each job with the automation's folder relative to `automationsDir`, so both must mount the same automations.

`pendolo run` started by hand always executes scripts locally, in the foreground.

## Language models

```yaml
providers:
  anthropic:
    type: anthropic              # Claude, through the official Anthropic SDK
    apiKey: secret://anthropic-api-key
  openrouter:
    type: openai                 # any OpenAI-compatible API
    baseURL: https://openrouter.ai/api/v1
    apiKey: secret://openrouter-api-key
  ollama:
    type: openai
    baseURL: http://ollama:11434/v1   # local models, no key

models:
  smart:
    provider: anthropic
    model: claude-opus-5-5
    fallbacks: default           # Anthropic: retry policy refusals on a fallback model
    effort: medium               # Anthropic: low, medium, high, xhigh, max
    price: { input: 4.00, output: 20.00 }   # EUR per million tokens
  local:
    provider: ollama
    model: llama3.3
```

A step names an alias (`model: smart`) or a provider and model directly (`model: ollama/llama3.3`). Without `model`, a step uses `smart`. Switching an alias to another provider changes every automation that uses it, without editing them.

With `outputSchema`, the answer must be JSON matching the schema. Claude receives the schema as a structured-output constraint; OpenAI-compatible APIs receive it as `response_format`. Pendolo validates the answer either way and asks once more, with the error, if it does not match.

`price` drives `budget.maxCostEur`: a run stops when its model calls cost more than the budget. Without a price, calls count as free and `pendolo show` marks them "price not configured".
