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
| `metadata.workspace` | no | The workspace (client or project) it belongs to. |
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

## Autonomy

The level combines with the risk each connector action declares (read, write, send, destructive):

| Level | Read | Write | Send to others | Destructive |
| --- | --- | --- | --- | --- |
| `observe` | allowed | blocked | blocked | blocked |
| `draft` | allowed | as a draft | as a draft | blocked |
| `ask` | allowed | allowed | asks for approval | asks for approval |
| `auto` | allowed | allowed | allowed | asks for approval |

A workspace sets a maximum level that no automation can exceed.

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

Values can include expressions written as `${ ... }`:

| Name | Meaning |
| --- | --- |
| `steps.<id>.output` | The output of an earlier step. Also `.failed`, `.succeeded`, `.skipped`. |
| `trigger` | What started the run, such as `trigger.email.id`. |
| `item`, `index` | The current element inside a `foreach`. |
| `date`, `now` | The run's date and timestamp. |
| `automation` | The automation's own metadata. |

The validator checks that every expression starts from one of these names, that `steps.<id>` names a step that runs earlier, and that `item` and `index` appear only inside a `foreach`. A step inside a `foreach` is not visible outside it; use the `foreach` step's output instead. `if` and `items` must be a single expression. The evaluator itself arrives with the engine in phase 1.

## Secrets

Never write a secret in a manifest. Refer to it as `secret://<name>`; the value is injected into the step that uses it at run time, never into a model's context, and masked in logs.

## What the validator checks

- The YAML is one document, without merge keys or duplicate keys.
- The manifest matches the JSON Schema.
- Step ids are unique, and every `steps.<id>` reference points to an earlier, visible step.
- Cron expressions parse and time zones exist.
- Prompt and script paths stay inside the automation folder and exist.
- Each `outputSchema` is a valid JSON Schema.

It warns, without failing, when a schedule has no time zone or when steps call a model without `budget.maxCostEur`. `pendolo validate --strict` turns warnings into failures.
