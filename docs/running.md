# Running Pendolo

## With Docker Compose

```bash
cd deploy
mkdir -p automations secrets
cp pendolo.example.yaml pendolo.yaml        # then edit models and providers
cp -r ../templates/site-checks automations/ # start from a template
printf '%s' 'https://hooks.slack.com/services/...' > secrets/alerts-webhook
docker compose up -d
docker compose exec pendolo pendolo list
```

The image includes `bash`, `curl`, `jq`, `openssl` and `ssh` for shell steps, runs as a non-root user, and has a Docker health check. The database lives in the `pendolo-data` volume.

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
| `pendolo pause <name>` / `resume <name>` | Stop or restart scheduling. Resuming does not replay the paused period. |

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
