-- Drafts written with the AI builder. A draft never runs on its own: it
-- becomes an automation only when someone with the right role activates it.
CREATE TABLE drafts (
  id            TEXT PRIMARY KEY,
  workspace     TEXT NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
  -- metadata.name of the draft's manifest, '' until the model writes one.
  automation    TEXT NOT NULL DEFAULT '',
  -- The automation and version the draft edits; '' for a new automation.
  base_name     TEXT NOT NULL DEFAULT '',
  base_hash     TEXT NOT NULL DEFAULT '',
  files         TEXT NOT NULL DEFAULT '{}',
  problems      TEXT NOT NULL DEFAULT '[]',
  hash          TEXT NOT NULL DEFAULT '',
  status        TEXT NOT NULL,
  dry_run_id    TEXT NOT NULL DEFAULT '',
  dry_run_hash  TEXT NOT NULL DEFAULT '',
  cost_eur      REAL NOT NULL DEFAULT 0,
  created_by    TEXT NOT NULL,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL,
  proposed_by   TEXT NOT NULL DEFAULT '',
  proposed_at   TEXT,
  decided_by    TEXT NOT NULL DEFAULT '',
  decided_at    TEXT,
  comment       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX drafts_by_workspace ON drafts (workspace, status, updated_at);

-- The conversation that wrote a draft.
CREATE TABLE draft_messages (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  draft_id    TEXT NOT NULL REFERENCES drafts (id) ON DELETE CASCADE,
  role        TEXT NOT NULL,
  author      TEXT NOT NULL DEFAULT '',
  text        TEXT NOT NULL,
  -- The draft's hash after this message, so each reply links to a version.
  hash        TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL
);
CREATE INDEX draft_messages_by_draft ON draft_messages (draft_id, id);

-- Every version an automation has had, from the builder or from the folder.
CREATE TABLE automation_versions (
  automation    TEXT NOT NULL,
  hash          TEXT NOT NULL,
  workspace     TEXT NOT NULL,
  files         TEXT NOT NULL,
  source        TEXT NOT NULL,
  draft_id      TEXT NOT NULL DEFAULT '',
  activated_by  TEXT NOT NULL DEFAULT '',
  created_at    TEXT NOT NULL,
  PRIMARY KEY (automation, hash)
);
CREATE INDEX automation_versions_by_time ON automation_versions (automation, created_at);

-- Model calls made by the builder; runs record their own in llm_calls.
CREATE TABLE builder_calls (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  draft_id       TEXT NOT NULL REFERENCES drafts (id) ON DELETE CASCADE,
  workspace      TEXT NOT NULL,
  provider       TEXT NOT NULL,
  model          TEXT NOT NULL,
  input_tokens   INTEGER NOT NULL DEFAULT 0,
  output_tokens  INTEGER NOT NULL DEFAULT 0,
  cost_eur       REAL NOT NULL DEFAULT 0,
  price_known    INTEGER NOT NULL DEFAULT 1,
  latency_ms     INTEGER NOT NULL DEFAULT 0,
  error          TEXT NOT NULL DEFAULT '',
  created_at     TEXT NOT NULL
);
CREATE INDEX builder_calls_by_workspace ON builder_calls (workspace, created_at);
