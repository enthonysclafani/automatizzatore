# Running Pendolo

## With Docker Compose

```bash
cd deploy
mkdir -p automations secrets
cp pendolo.example.yaml pendolo.yaml        # then edit models and providers
cp -r ../templates/site-checks automations/ # start from a template (or from the dashboard's Builder)
sudo chown -R 10001:10001 automations      # the server writes activated drafts here
openssl rand -hex 24 > secrets/runner-token
openssl rand -base64 32 > secrets/pendolo-master-key
printf '%s' 'https://hooks.slack.com/services/...' > secrets/alerts-webhook
docker compose up -d
docker compose exec pendolo pendolo list
```

Compose starts two containers from the same image:

| Container | Does | Sees |
| --- | --- | --- |
| `pendolo` | Scheduler, queue, model calls, notifications, history, `/healthz`, webhooks, the AI builder | Database, configuration, secrets, automations (read-write, to activate drafts) |
| `runner` | Executes the scripts of shell steps | Automations (read-only) and its token; read-only file system, no Linux capabilities |

A script that misbehaves can only reach what the runner can. If the runner is down, `/healthz` reports it and shell steps fail with "runner unreachable". The image includes `bash`, `curl`, `jq`, `openssl` and `ssh` for scripts, runs as a non-root user, and has a Docker health check. The database lives in the `pendolo-data` volume. `deploy/smoke-test.sh` checks the whole stack; CI runs it on every change.

## The dashboard

Open `http://localhost:8080`. The first time, the dashboard asks for the setup token printed in the server log (`docker compose logs pendolo | grep setupToken`) and creates the owner. Then:

- **Builder**: describe an automation in plain language, or start from a template or an existing automation; see its files, problems and changes; test it with a dry run; propose it; activate it. See below.
- **Overview**: health, automations, runs of the last 24 hours, cost this month, 14 days of runs, recent failures, upcoming runs.
- **Automations**: state, triggers, last and next run; run now, pause, resume; the manifest as written; every version it has had, with the differences, and edit or restore through the builder.
- **Runs**: every run, filterable; each run step by step with logs, inputs, outputs, errors, and every model call with prompt, answer, tokens and cost.
- **Costs**: spending per day, per automation and per model.
- **Approvals**: runs waiting for a person, with what will happen if approved; approve or reject with a comment.
- **Connections**: Google, Slack, web search and MCP servers, with a setup guide for each, a test button and the actions each offers with their risk.
- **Settings**: the workspace's maximum autonomy.
- **Secrets**, **Members**, **Audit**: per workspace, for the roles allowed.
- **Users & workspaces**, **System**: for owners.
- **Account**: password, two-factor authentication, API tokens.

### Workspaces and roles

An automation belongs to the workspace named in its `metadata.workspace` (`default` if empty); a workspace is created the first time an automation names it, or by an owner. People see only the workspaces they are members of; owners see all.

| Role | Can |
| --- | --- |
| viewer | See automations, runs and costs |
| approver | Also approve or reject runs that wait for a person |
| builder | Also run, pause and resume automations, see secret names, and write, dry-run and propose automations with the builder |
| admin | Also approve, activate builder drafts, manage secrets, connections, members and settings, and read the audit log |
| owner (instance) | Everything, in every workspace, plus users and workspaces |

Every sign-in, change and secret read by an automation is recorded in the audit log. The API behind the dashboard is documented in [api.md](api.md); scripts use API tokens from the Account page.

### The AI builder

The builder turns a request such as "every Monday at 9, find article ideas, check them on OpenSEO, write them to a Google Doc and tell me on Slack" into an automation folder. It never runs or activates anything by itself:

1. **Draft.** The model writes the files, knowing the format, the templates, and the workspace's connections, actions, secret names and models. Pendolo checks every file: the schema, the steps, that connections and actions exist, that secrets are referenced as `secret://` and that nothing looks like a credential. When it finds errors, the model fixes them, up to twice. Keep talking to it to change the draft, or edit the files by hand.
2. **Dry run.** The draft runs once with only its reads for real: read actions, `GET` requests and model calls. Writes, messages, scripts and approvals are recorded as what they would do, and nobody is notified. A draft with an email trigger uses the newest matching email as input.
3. **Propose and activate.** A builder proposes the draft; an admin (or owner) activates it, which writes its folder into the automations folder in one step. From then on the scheduler runs it like any other automation.

Editing an automation works the same way, from its page: the draft starts from the current version, shows the differences, and cannot be activated if someone changed the automation in the meantime. Every version is kept, whether it came from the builder or from editing the folder by hand, and any version can be restored through a new draft. The builder needs a model (`builder.model`, `smart` by default; see [configuration.md](configuration.md)); without one, templates and hand edits still work. Its model calls appear in Costs as `(AI builder)`.

## Webhooks from the internet

Compose publishes port 8080 on localhost only. To reach the dashboard or receive webhooks from outside, put a reverse proxy with TLS in front of it (Caddy, Traefik, nginx, Cloudflare Tunnel) and set `server.trustProxy: true`, so sign-in cookies are marked secure and the audit log records real addresses. To expose only webhooks, forward only `/hooks/`. Always give webhook triggers a `secret`.

## Without Docker

```bash
make build
./bin/pendolo server --config pendolo.yaml
```

On macOS, keep the machine awake (System Settings, Energy: prevent automatic sleeping), or schedules stop with it.

## Everyday commands

| Command | What it does |
| --- | --- |
| `pendolo run <folder or name>` | Runs an automation once, now, in the foreground, and prints every step. |
| `pendolo trigger <name>` | Queues a run for the server. |
| `pendolo list` | Automations with their state (active, paused, invalid) and last run. |
| `pendolo runs --automation site-checks --status failed` | Recent runs, filtered. |
| `pendolo show <run-id> [--full]` | One run step by step: errors, logs, model calls, tokens and cost. |
| `pendolo pause <name>` / `resume <name>` | Stop or restart scheduling (and webhooks). Resuming does not replay the paused period. |

Every command that reads data accepts `--json`.

## How it keeps running

- **Schedules survive restarts.** The next fire time of every schedule is in the database. After a stop, each trigger's `misfire` policy decides what happens to occurrences that were missed: `run_once` (default) runs once, `run_all` runs each one (up to 100), `skip` drops them.
- **One scheduler at a time.** A lease in the database lets only one Pendolo enqueue runs, and each scheduled occurrence is enqueued at most once.
- **No stuck runs.** A run holds a lease while it executes. If Pendolo dies mid-run, the run is marked failed ("interrupted") when the lease expires, within a minute.
- **Graceful stop.** On SIGTERM, Pendolo stops starting runs, gives running ones 30 seconds to finish, then cancels them (status `cancelled`).
- **Edits are picked up live.** The automations folder is re-read every 30 seconds. A changed schedule starts fresh, an invalid manifest is reported in `pendolo list` and the logs and is not scheduled.
- **Heartbeat.** With `heartbeat.url`, Pendolo pings an external monitor only while healthy, so the monitor alerts you when Pendolo or its machine stops.

`GET /healthz` returns 200 with the scheduler state, queued and running runs, or 503 when the database fails or the scheduler stalls. `pendolo healthcheck` calls it for Docker.

## Fallback: system schedulers

Pendolo's own scheduler is the default everywhere. If you prefer the host's cron or systemd timers, call `pendolo run <name>` from them; the run is recorded like any other.
