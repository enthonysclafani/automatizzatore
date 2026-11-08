-- Connections to outside services, per workspace. Credentials are sealed
-- with the master key; config holds only non-secret settings.
CREATE TABLE connections (
  workspace_id  TEXT NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
  name          TEXT NOT NULL,
  kind          TEXT NOT NULL,
  config        TEXT NOT NULL DEFAULT '{}',
  sealed        TEXT NOT NULL DEFAULT '',
  status        TEXT NOT NULL,
  error         TEXT NOT NULL DEFAULT '',
  account       TEXT NOT NULL DEFAULT '',
  updated_at    TEXT NOT NULL,
  updated_by    TEXT NOT NULL,
  PRIMARY KEY (workspace_id, name)
);

-- A run waiting for approval keeps its progress here, to resume later.
ALTER TABLE runs ADD COLUMN checkpoint TEXT NOT NULL DEFAULT '';

CREATE TABLE approvals (
  id            TEXT PRIMARY KEY,
  run_id        TEXT NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
  workspace     TEXT NOT NULL,
  automation    TEXT NOT NULL,
  step_path     TEXT NOT NULL,
  message       TEXT NOT NULL,
  detail        TEXT NOT NULL DEFAULT '{}',
  approvers     TEXT NOT NULL DEFAULT '[]',
  status        TEXT NOT NULL,
  on_timeout    TEXT NOT NULL DEFAULT 'reject',
  requested_at  TEXT NOT NULL,
  expires_at    TEXT NOT NULL,
  decided_at    TEXT,
  decided_by    TEXT NOT NULL DEFAULT '',
  comment       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX approvals_pending ON approvals (status, expires_at);
CREATE INDEX approvals_by_workspace ON approvals (workspace, requested_at);

-- The most autonomy automations of a workspace get.
ALTER TABLE workspaces ADD COLUMN max_autonomy TEXT NOT NULL DEFAULT 'auto';

-- Emails an email trigger has already turned into runs.
CREATE TABLE email_seen (
  automation   TEXT NOT NULL,
  trigger_key  TEXT NOT NULL,
  message_id   TEXT NOT NULL,
  seen_at      TEXT NOT NULL,
  PRIMARY KEY (automation, trigger_key, message_id)
);
