# Configuration

Pendolo reads `pendolo.yaml` (or the file in `$PENDOLO_CONFIG`, or `--config`). Every field has a default, so Pendolo also starts without a file. A complete example is in [deploy/pendolo.example.yaml](../deploy/pendolo.example.yaml).

| Field | Default | Meaning |
| --- | --- | --- |
| `dataDir` | `data` | Folder of the SQLite database (`pendolo.db`). Back it up. |
| `automationsDir` | `automations` | One sub-folder per automation, each with an `automation.yaml`. |
| `workers` | `4` | Runs executed at the same time. Runs of the same automation never overlap. |
| `server.listen` | `:8080` | Address of the dashboard, the API (`/api/v1`), `/healthz` and webhook triggers (`/hooks/<name>`). |
| `server.publicURL` | `http://localhost:8080` | How people reach Pendolo: OAuth redirects (`<publicURL>/api/v1/oauth/callback`) and links in notifications use it. Also `PENDOLO_PUBLIC_URL`. |
| `server.trustProxy` | `false` | Read the client address and `https` from `X-Forwarded-For` and `X-Forwarded-Proto`. Turn on only behind a reverse proxy. |
| `auth.sessionTTL` | `336h` (14 days) | How long a browser stays signed in without activity. |
| `auth.oidc` | off | Single sign-on; see below. |
| `secrets.masterKeyFile` | `/run/secrets/pendolo-master-key` | Base64 key that encrypts the secrets saved in the dashboard. |
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
| `builder.model` | `smart` | The model alias (or `provider/model`) the AI builder writes automations with. Use a strong model. |
| `builder.disabled` | `false` | Turn the AI builder off; templates and hand edits in the dashboard still work. |
| `providers` | none | Language model APIs, by name. |
| `models` | none | Aliases used by automations (`fast`, `smart`, `local`...). |

Environment variables override the file: `PENDOLO_DATA_DIR`, `PENDOLO_AUTOMATIONS_DIR`, `PENDOLO_WORKERS`, `PENDOLO_LISTEN`.

## Secrets

A manifest refers to a secret as `secret://<name>`. Pendolo looks for a file `<secrets.dir>/<name>`, then for the variable `<envPrefix><NAME>`, where the name is upper-cased and `-` and `.` become `_`. So `secret://server-ssh-key` is read from `/run/secrets/server-ssh-key` or `PENDOLO_SECRET_SERVER_SSH_KEY`.

Secret values are injected only where a step uses them (HTTP URLs, headers and bodies; shell `env`), never into a model's prompt, and they are masked as `[secret:<name>]` in every stored output, log and error. Shell steps get a minimal environment: they never see Pendolo's own variables. Encrypted storage and 1Password arrive in phase 2.

## The master key

Secrets saved in the dashboard and two-factor seeds are encrypted with AES-256-GCM under a 32-byte master key. Pendolo reads it from `PENDOLO_MASTER_KEY`, then from `secrets.masterKeyFile`. With neither, it generates `<dataDir>/master.key` on first start and logs a warning. Create one yourself with `pendolo keygen` or `openssl rand -base64 32`, and keep it apart from database backups: whoever has both can read every secret. Losing it makes the stored secrets unreadable (set them again).

When an automation asks for `secret://name`, Pendolo looks first in the dashboard secrets of the automation's workspace, then in the files and environment variables above. Workspaces never see each other's dashboard secrets; file and environment secrets are shared by the whole instance.

## Sign-in and single sign-on

While no owner exists, the server logs a one-time **setup token**. Open the dashboard and create the first owner with it, or run `pendolo users create --email you@example.com --owner` on the server. Owners then create users and add them to workspaces with a role.

Passwords are hashed with Argon2id and need at least 12 characters. Users can turn on two-factor authentication (TOTP, any authenticator app) in their Account page. Sign-in is rate limited per address and per email. `pendolo users password --email ...` resets a password and turns two-factor off, for a locked-out user.

Any OpenID Connect provider works for single sign-on: Google Workspace, Microsoft Entra ID, Clerk, Authentik, Keycloak, Zitadel, Okta.

```yaml
auth:
  oidc:
    issuer: https://accounts.google.com
    clientID: 1234.apps.googleusercontent.com
    clientSecret: secret://oidc-client-secret
    redirectURL: https://pendolo.example.com/api/v1/auth/oidc/callback
    label: Google                # the button says "Sign in with Google"
    autoCreate: true             # create unknown users from these domains...
    allowedDomains: [example.com]
```

Users are matched by email. A user created by single sign-on starts without workspaces until an owner or a workspace admin adds them; with `autoCreate: false`, only users an owner created can sign in.

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
