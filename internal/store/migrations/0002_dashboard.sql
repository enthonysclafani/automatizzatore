-- Workspaces group automations, runs, secrets and members (one per client
-- or project). Automations name theirs in metadata.workspace.
CREATE TABLE workspaces (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  created_at  TEXT NOT NULL
);
INSERT INTO workspaces (id, name, created_at)
  VALUES ('default', 'Default', strftime('%Y-%m-%dT%H:%M:%S.000000Z', 'now'));

ALTER TABLE automations ADD COLUMN workspace TEXT NOT NULL DEFAULT 'default';
ALTER TABLE runs ADD COLUMN workspace TEXT NOT NULL DEFAULT 'default';
CREATE INDEX runs_by_workspace ON runs (workspace, queued_at);

CREATE TABLE users (
  id             TEXT PRIMARY KEY,
  email          TEXT NOT NULL UNIQUE COLLATE NOCASE,
  name           TEXT NOT NULL DEFAULT '',
  password_hash  TEXT NOT NULL DEFAULT '',
  totp_secret    TEXT NOT NULL DEFAULT '',  -- sealed with the master key
  totp_enabled   INTEGER NOT NULL DEFAULT 0,
  totp_last      INTEGER NOT NULL DEFAULT 0, -- last accepted time step, against replay
  is_owner       INTEGER NOT NULL DEFAULT 0,
  disabled       INTEGER NOT NULL DEFAULT 0,
  created_at     TEXT NOT NULL,
  last_login_at  TEXT
);

CREATE TABLE memberships (
  user_id       TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  workspace_id  TEXT NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
  role          TEXT NOT NULL,
  PRIMARY KEY (user_id, workspace_id)
);

-- Only hashes of session and API tokens are stored.
CREATE TABLE sessions (
  token_hash    TEXT PRIMARY KEY,
  user_id       TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  created_at    TEXT NOT NULL,
  expires_at    TEXT NOT NULL,
  last_seen_at  TEXT NOT NULL,
  ip            TEXT NOT NULL DEFAULT '',
  user_agent    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sessions_by_user ON sessions (user_id);

CREATE TABLE api_tokens (
  id            TEXT PRIMARY KEY,
  user_id       TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  name          TEXT NOT NULL,
  token_hash    TEXT NOT NULL UNIQUE,
  created_at    TEXT NOT NULL,
  expires_at    TEXT,
  last_used_at  TEXT
);

-- Secret values sealed with the master key (AES-256-GCM).
CREATE TABLE secrets (
  workspace_id  TEXT NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
  name          TEXT NOT NULL,
  sealed        TEXT NOT NULL,
  updated_at    TEXT NOT NULL,
  updated_by    TEXT NOT NULL,
  PRIMARY KEY (workspace_id, name)
);

CREATE TABLE audit_log (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  at         TEXT NOT NULL,
  actor      TEXT NOT NULL,
  action     TEXT NOT NULL,
  workspace  TEXT NOT NULL DEFAULT '',
  target     TEXT NOT NULL DEFAULT '',
  detail     TEXT NOT NULL DEFAULT '{}',
  ip         TEXT NOT NULL DEFAULT ''
);
CREATE INDEX audit_by_workspace ON audit_log (workspace, id);
