-- Automations found in the automations folder, with their validation state.
CREATE TABLE automations (
  name       TEXT PRIMARY KEY,
  path       TEXT NOT NULL,
  hash       TEXT NOT NULL,
  valid      INTEGER NOT NULL,
  problems   TEXT NOT NULL DEFAULT '[]',
  enabled    INTEGER NOT NULL DEFAULT 1,
  loaded_at  TEXT NOT NULL
);

-- When each schedule trigger fires next. trigger_key changes when the
-- schedule changes, so an edited schedule starts fresh.
CREATE TABLE trigger_state (
  automation        TEXT NOT NULL,
  trigger_key       TEXT NOT NULL,
  next_run_at       TEXT NOT NULL,
  last_scheduled_at TEXT,
  PRIMARY KEY (automation, trigger_key)
);

-- Runs double as the work queue.
CREATE TABLE runs (
  id             TEXT PRIMARY KEY,
  automation     TEXT NOT NULL,
  manifest_hash  TEXT NOT NULL DEFAULT '',
  trigger_type   TEXT NOT NULL,
  trigger_key    TEXT NOT NULL DEFAULT '',
  scheduled_for  TEXT,
  trigger_data   TEXT NOT NULL DEFAULT '{}',
  status         TEXT NOT NULL,
  error          TEXT NOT NULL DEFAULT '',
  anomalies      INTEGER NOT NULL DEFAULT 0,
  queued_at      TEXT NOT NULL,
  started_at     TEXT,
  finished_at    TEXT,
  cost_eur       REAL NOT NULL DEFAULT 0,
  worker         TEXT NOT NULL DEFAULT '',
  lease_until    TEXT
);
-- A scheduled occurrence is enqueued at most once, even with two schedulers.
CREATE UNIQUE INDEX runs_scheduled_once ON runs (automation, trigger_key, scheduled_for)
  WHERE scheduled_for IS NOT NULL;
CREATE INDEX runs_queue ON runs (status, queued_at);
CREATE INDEX runs_by_automation ON runs (automation, queued_at);

-- One row per step execution (a step inside foreach appears once per item).
CREATE TABLE steps (
  run_id       TEXT NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
  seq          INTEGER NOT NULL,
  path         TEXT NOT NULL,
  step_id      TEXT NOT NULL,
  type         TEXT NOT NULL,
  status       TEXT NOT NULL,
  attempts     INTEGER NOT NULL DEFAULT 0,
  started_at   TEXT NOT NULL,
  finished_at  TEXT,
  input        TEXT NOT NULL DEFAULT 'null',
  output       TEXT NOT NULL DEFAULT 'null',
  error        TEXT NOT NULL DEFAULT '',
  logs         TEXT NOT NULL DEFAULT '',
  cost_eur     REAL NOT NULL DEFAULT 0,
  PRIMARY KEY (run_id, seq)
);

-- Every call to a language model, with prompt, answer, tokens and cost.
CREATE TABLE llm_calls (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id         TEXT NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
  step_path      TEXT NOT NULL,
  provider       TEXT NOT NULL,
  model          TEXT NOT NULL,
  request        TEXT NOT NULL,
  response       TEXT NOT NULL,
  input_tokens   INTEGER NOT NULL DEFAULT 0,
  output_tokens  INTEGER NOT NULL DEFAULT 0,
  cost_eur       REAL NOT NULL DEFAULT 0,
  price_known    INTEGER NOT NULL DEFAULT 1,
  latency_ms     INTEGER NOT NULL DEFAULT 0,
  error          TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL
);
CREATE INDEX llm_calls_by_run ON llm_calls (run_id);

-- Leases that keep two schedulers from working at the same time.
CREATE TABLE locks (
  name   TEXT PRIMARY KEY,
  owner  TEXT NOT NULL,
  until  TEXT NOT NULL
);
