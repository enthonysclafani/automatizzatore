# Automation manifest (`pendolo/v1`)

An automation is a folder. Its `automation.yaml` says when it runs, what it does and how much it may do on its own. Prompts and scripts live next to it.

```
my-automation/
  automation.yaml   # this file
  prompts/          # prompts used by llm and agent steps
  scripts/          # scripts used by shell steps
```

The machine-readable definition is [`spec/pendolo-v1.schema.json`](../spec/pendolo-v1.schema.json). Print it with `pendolo schema` and point your editor or AI assistant at it. Check a manifest with:

```bash
pendolo validate path/to/automation.yaml
```

## Top level

| Field | Required | Meaning |
| --- | --- | --- |
| `apiVersion` | yes | Always `pendolo/v1`. |
| `kind` | yes | Always `Automation`. |
| `metadata.name` | yes | Lowercase letters, digits and hyphens. Unique within a workspace. |
| `metadata.description` | no | One or two sentences for people. |
| `metadata.workspace` | no | The workspace (client or project) it belongs to; `default` when empty. It decides who sees the automation in the dashboard and which dashboard secrets it can use. |
| `metadata.labels` | no | Free key/value labels. |
| `triggers` | yes | At least one trigger. |
| `autonomy` | no | `observe`, `draft`, `ask` (default) or `auto`. |
| `budget` | no | `maxCostEur` per run and an overall `timeout`. |
| `notifications` | no | Who hears about runs: `on` (`always`, `failure`, `anomaly`, `never`) and `channels` such as `slack:#ops` or `email:me@example.com`. |
| `steps` | yes | The work, in order. |

## Triggers

Each trigger has exactly one key.

| Trigger | Fields | Example |
| --- | --- | --- |
| `schedule` | `cron` (5 fields or `@daily`), `timezone` (IANA), `misfire` (`run_once`, `skip`, `run_all`) | `{ cron: "0 8,16 * * *", timezone: Europe/Rome }` |
| `webhook` | optional `secret` (a `secret://` reference) | `{}` |
| `email` | `connection`, optional `query` and `interval` | `{ connection: gmail, query: "is:unread", interval: 5m }` |
| `manual` | none | `{}` |

`misfire` decides what happens to runs missed while Pendolo was down. Without `timezone` a schedule runs in UTC, and the validator warns about it.

A `webhook` trigger listens at `POST /hooks/<automation name>`. The caller proves it knows the secret with `Authorization: Bearer <secret>`, or signs the body with HMAC-SHA256 in `X-Pendolo-Signature` or `X-Hub-Signature-256` (`sha256=<hex>`, the format GitHub uses). The run receives `trigger.body` (text, up to 1 MiB), `trigger.json` (when the body is JSON), `trigger.headers` (lower-case, credentials removed) and `trigger.query`. The answer is `202` with the run id. Without `secret` anyone who can reach Pendolo can start the automation, and the validator warns about it.

An `email` trigger checks a Google connection every `interval` (default 5m, at least 1m) for messages matching `query` (Gmail search syntax, default `in:inbox`) and starts one run per new message, up to 20 per check. The first check only remembers what is already in the mailbox. The run reads the message as `trigger.email`: `id`, `threadId`, `from`, `to`, `cc`, `subject`, `date`, `snippet`, `body` (plain text, up to 100 KB), `labels`.

## Autonomy

Every connector action declares a risk: **read**; **write**, a change that stays reversible (a label, a draft, a new document); **send**, something that reaches other people (an email, a chat message); **destructive**, a deletion or overwrite. The automation's level decides what each risk may do:

| Level | Read | Write | Send to others | Destructive |
| --- | --- | --- | --- | --- |
| `observe` | allowed | blocked | blocked | blocked |
| `draft` | allowed | allowed | becomes a draft (e.g. `gmail.send` saves a draft), or blocked when there is no draft form | blocked |
| `ask` (default) | allowed | allowed | waits for approval | waits for approval |
| `auto` | allowed | allowed | allowed | waits for approval |

A workspace sets a maximum level (Settings page) that no automation can exceed. When a `connector` step waits for approval, the run pauses with status `waiting` until an approver decides on the Approvals page; approved, it resumes at that step; rejected or expired (after 24 hours), it is cancelled. Inside an `agent` step, tools that would need approval are refused instead, since an agent cannot pause halfway through. Runs pause only between top-level steps, so an action that needs approval cannot be inside a `foreach`.

## Steps

Every step has an `id` (lowercase letters, digits, underscores; unique in the whole automation) and a `type`. All types accept:

| Field | Meaning |
| --- | --- |
| `name` | A label for people. |
| `if` | A single expression; the step is skipped when it is false. |
| `timeout` | A duration such as `30s` or `5m`. |
| `retry` | `{ max: 2, backoff: 30s }` |
| `continueOnError` | Keep going when this step fails; later steps can read `steps.<id>.failed`. |

| Type | Required | Optional | What it does |
| --- | --- | --- | --- |
| `connector` | `action` (`<connector>.<action>`) | `connection`, `with` | Calls a connected service. |
| `http` | `request.url` | `request.method`, `headers`, `body`, `expect.status`, `expect.maxLatency`, `expect.bodyContains` | Sends an HTTP request and checks the response. |
| `shell` | `run` or `script` | `args`, `env` | Runs a script in the isolated runner. |
| `llm` | `prompt` or `promptText` | `model`, `system`, `input`, `outputSchema`, `temperature` | One model call, optionally with structured output. |
| `agent` | `prompt` or `promptText`, `tools` | `model`, `maxIterations`, `maxCostEur`, `outputSchema` | A model that loops with the listed tools until it is done or out of budget. |
| `approval` | `message` | `approvers`, `onTimeout` | Waits for a person to approve. |
| `foreach` | `items`, `steps` | `concurrency` | Runs nested steps once per item. |

`model` is an alias such as `fast`, `smart` or `local`. The settings map aliases to real providers and models, so switching provider never touches an automation.

## Expressions

Values can include expressions written as `${ ... }`. A value that is exactly one expression keeps its type (a list stays a list); text around expressions makes the result a string, with lists and objects written as JSON.

| Name | Meaning |
| --- | --- |
| `steps.<id>.output` | The output of an earlier step. Also `.failed`, `.succeeded`, `.skipped`, `.status`, `.error`. |
| `trigger` | What started the run: `trigger.type`, `trigger.scheduledFor`, and per-trigger data. |
| `item`, `index` | The current element inside a `foreach`. |
| `date` | The run's date (YYYY-MM-DD) in the time zone of the automation's schedule. |
| `now` | The run's start time (RFC 3339, UTC). |
| `automation` | `automation.name`, `automation.description`, `automation.workspace`, `automation.runId`. |

The language is [expr](https://expr-lang.org): dots for fields, `[0]` for list items, `== != < >`, `&& || !`, `cond ? a : b`, and functions such as `len()`, `upper()`, `join()`, `filter()`. Examples:

```yaml
if: "${steps.homepage.failed || steps.tls.failed}"
text: "${steps.report.succeeded ? steps.report.output : 'check failed'}"
COUNT: "${len(steps.fetch.output.json.items)}"
```

The validator parses every expression and checks that names exist, that `steps.<id>` points to a step that runs earlier, and that `item` and `index` appear only inside a `foreach`. A step inside a `foreach` is not visible outside it; use the `foreach` step's output instead. `if` and `items` must be a single expression.

**Shell code is never interpolated.** In a shell step's `run`, `${VAR}` is the shell's own syntax and Pendolo leaves it alone. Pass values through `env` instead, which keeps them from being pasted into code:

```yaml
- id: report
  type: shell
  run: 'echo "status $STATUS"'
  env: { STATUS: "${steps.homepage.output.status}" }
```

### Step outputs

| Type | `output` |
| --- | --- |
| `http` | `status`, `ok`, `latencyMs`, `headers`, `body` (first 1 MiB), `json` when the response is JSON |
| `shell` | `exitCode`, `stdout`, `stderr` (1 MiB each), `json` when stdout is JSON |
| `llm` | The answer as text, or the parsed object when `outputSchema` is set |
| `foreach` | A list with, for each item, the outputs of its nested steps by id |
| `connector` | What the action returns (see [connectors.md](connectors.md)) |
| `agent` | The object passed to `finish` when `outputSchema` is set, else the final text |
| `approval` | `approved`, `by`, `comment`, `at` |

An `http` step fails on a status of 400 or more unless `expect.status` says otherwise. A `shell` step fails on a non-zero exit code. Shell steps run in the separate runner container when one is configured (see [running.md](running.md)). A failed step stops the run unless it has `continueOnError: true`; the run then still succeeds, and the failure counts as an anomaly for notifications.

### Available today

All step types run. `foreach` runs up to `concurrency` items at a time (default 1); the first failing item stops the others.

- **connector**: `action` is `<connector>.<action>` (e.g. `gmail.apply_labels`) or `<MCP connection>.<tool>`; `with` is its input, checked against the action's schema; `connection` picks a connection when the workspace has several of the kind. See [connectors.md](connectors.md).
- **agent**: a model that works in a loop with the `tools` you list (connector actions), up to `maxIterations` turns (default 20) and `maxCostEur`. With `outputSchema`, it ends by calling a `finish` tool whose input must match the schema. What tools return is presented as data, never as instructions.
- **approval**: pauses the run until someone with a role in `approvers` (default approver and admin) approves; `timeout` (default 24h) and `onTimeout` (`reject` by default) say what happens if nobody does. `message` may use expressions.

### Notifications

| Channel | Example | Needs |
| --- | --- | --- |
| `webhook:` | `webhook:secret://alerts-webhook` | Nothing: a JSON POST with `text` and `content`, which Slack, Mattermost and Discord incoming webhooks accept |
| `slack:` | `slack:#ops` | `notify.slack.token`, a bot token with `chat:write` |
| `telegram:` | `telegram:-1001234567890` | `notify.telegram.botToken` |
| `email:` | `email:ops@example.com` | `notify.email` (SMTP) |

## Secrets

Never write a secret in a manifest. Refer to it as `secret://<name>`; the value is injected into the step that uses it at run time, never into a model's context, and masked in logs. Pendolo looks for the name among the workspace's secrets (the Secrets page), then among the instance's files and environment: the `default` workspace may use all of those, other workspaces only the ones in `secrets.shared` (see [configuration.md](configuration.md)). Dry runs never resolve secrets.

## What the validator checks

- The YAML is one document, without merge keys or duplicate keys.
- The manifest matches the JSON Schema.
- Step ids are unique, and every `steps.<id>` reference points to an earlier, visible step.
- Cron expressions parse and time zones exist.
- Prompt and script paths stay inside the automation folder and exist.
- Each `outputSchema` is a valid JSON Schema.

It warns, without failing, when a schedule has no time zone or when steps call a model without `budget.maxCostEur`. `pendolo validate --strict` turns warnings into failures.
