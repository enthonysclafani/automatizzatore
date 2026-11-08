// API contract between the Pendolo server and the dashboard.
// The server implements exactly these shapes; see docs/api.md for endpoints.
// Times are RFC 3339 strings in UTC. Money is EUR.

export type Role = "admin" | "builder" | "approver" | "viewer";
/** Role in a workspace as seen by the current user; instance owners see "owner" everywhere. */
export type EffectiveRole = Role | "owner";

export interface ApiError {
  error: string;
  /** Machine-readable code, e.g. "totp_required", "invalid_credentials", "forbidden", "not_found". */
  code?: string;
}

export interface WorkspaceRef {
  id: string;
  name: string;
  role: EffectiveRole;
}

export interface Me {
  id: string;
  email: string;
  name: string;
  isOwner: boolean;
  totpEnabled: boolean;
  workspaces: WorkspaceRef[];
}

export interface SetupStatus {
  /** True when no owner exists: the dashboard shows the setup form. */
  needsSetup: boolean;
  oidc: { enabled: boolean; label: string };
}

export interface LoginRequest {
  email: string;
  password: string;
  /** Six-digit code, required when the user has two-factor authentication. */
  code?: string;
}

export interface SetupRequest {
  /** One-time token printed in the server log while no user exists. */
  token: string;
  email: string;
  name: string;
  password: string;
}

export interface Problem {
  severity: "error" | "warning";
  path?: string;
  line?: number;
  column?: number;
  message: string;
}

export interface Health {
  ok: boolean;
  reason?: string;
  version: string;
  startedAt: string;
  lastTick: string;
  leader: boolean;
  automations: number;
  invalid: number;
  running: number;
  queued: number;
  /** "local", "ok", or the reason the runner is unreachable. */
  runner: string;
}

/** "waiting": paused until a person approves or rejects (see Approval). */
export type RunStatus = "queued" | "running" | "waiting" | "succeeded" | "failed" | "cancelled";
export type StepStatus = "running" | "waiting" | "succeeded" | "failed" | "skipped";

export interface Run {
  id: string;
  automation: string;
  workspace: string;
  manifestHash: string;
  /** "dry_run": a test of a builder draft; only reads happened (see Draft). */
  triggerType: "schedule" | "webhook" | "manual" | "email" | "dry_run";
  triggerKey?: string;
  scheduledFor?: string;
  triggerData: unknown;
  status: RunStatus;
  error?: string;
  /** Steps that failed without stopping the run (continueOnError). */
  anomalies: number;
  queuedAt: string;
  startedAt?: string;
  finishedAt?: string;
  costEur: number;
  worker?: string;
}

export interface Step {
  runId: string;
  seq: number;
  /** "check", or "each[2].say" for steps inside a foreach. */
  path: string;
  stepId: string;
  type: "connector" | "http" | "shell" | "llm" | "agent" | "approval" | "foreach";
  status: StepStatus;
  attempts: number;
  startedAt: string;
  finishedAt?: string;
  input: unknown;
  output: unknown;
  error?: string;
  logs?: string;
  costEur: number;
}

export interface LLMCall {
  id: number;
  runId: string;
  stepPath: string;
  provider: string;
  model: string;
  /** { model, system, user, schema?, maxTokens, ... } as sent to the provider. */
  request: unknown;
  response: string;
  inputTokens: number;
  outputTokens: number;
  costEur: number;
  priceKnown: boolean;
  latencyMs: number;
  error?: string;
  createdAt: string;
}

export interface RunDetail {
  run: Run;
  steps: Step[];
  llmCalls: LLMCall[];
  /** Approvals requested by this run, oldest first. */
  approvals: Approval[];
}

export interface RunList {
  runs: Run[];
  /** Pass as ?before= to get the next page; absent on the last page. */
  next?: string;
}

export interface TriggerSummary {
  type: "schedule" | "webhook" | "email" | "manual";
  cron?: string;
  timezone?: string;
  /** For webhook triggers: the path to POST to, e.g. "/hooks/deploy". */
  webhookPath?: string;
  hasSecret?: boolean;
}

export interface AutomationSummary {
  name: string;
  workspace: string;
  description: string;
  path: string;
  valid: boolean;
  enabled: boolean;
  problems: Problem[];
  triggers: TriggerSummary[];
  nextRunAt?: string;
  lastRun?: Run;
}

export interface AutomationDetail extends AutomationSummary {
  /** The automation.yaml as written. */
  manifest: string;
  autonomy: "observe" | "draft" | "ask" | "auto";
  budget?: { maxCostEur?: number; timeout?: string };
  steps: { id: string; type: Step["type"]; name?: string }[];
  recentRuns: Run[];
}

export interface Overview {
  health: Health;
  automations: { total: number; active: number; paused: number; invalid: number };
  runs24h: { succeeded: number; failed: number; cancelled: number; withAnomalies: number };
  running: number;
  queued: number;
  /** Pending approvals in this workspace. */
  waitingApprovals: number;
  /** Builder drafts proposed and waiting for an admin to activate them. */
  proposedDrafts: number;
  costMonthEur: number;
  /** Last 14 days, oldest first, every day present (zeros included). Dates are YYYY-MM-DD. */
  daily: { date: string; succeeded: number; failed: number; costEur: number }[];
  /** Up to 5 most recent failed runs, dry runs left out. runs24h and daily leave dry runs out too. */
  recentFailures: Run[];
  /** Up to 10 next scheduled runs. */
  upcoming: { automation: string; at: string }[];
}

export interface Costs {
  days: number;
  totalEur: number;
  /** Oldest first, every day present. */
  daily: { date: string; costEur: number }[];
  byAutomation: { automation: string; costEur: number; runs: number }[];
  byModel: {
    provider: string;
    model: string;
    costEur: number;
    inputTokens: number;
    outputTokens: number;
    calls: number;
    priceKnown: boolean;
  }[];
}

export interface SecretInfo {
  name: string;
  updatedAt: string;
  updatedBy: string;
}

export interface Member {
  userId: string;
  email: string;
  name: string;
  role: Role;
}

export interface User {
  id: string;
  email: string;
  name: string;
  isOwner: boolean;
  disabled: boolean;
  totpEnabled: boolean;
  createdAt: string;
  lastLoginAt?: string;
}

export interface ApiToken {
  id: string;
  name: string;
  createdAt: string;
  lastUsedAt?: string;
  expiresAt?: string;
}

export interface NewApiToken extends ApiToken {
  /** Shown once; the server keeps only a hash. */
  token: string;
}

export interface AuditEntry {
  id: number;
  at: string;
  /** Email of the user, "api-token:<name>", "system" or "cli". */
  actor: string;
  /** e.g. "auth.login", "automation.trigger", "secret.update", "member.add". */
  action: string;
  workspace?: string;
  target?: string;
  detail?: Record<string, unknown>;
  ip?: string;
}

export interface SystemInfo {
  health: Health;
  version: string;
  dataDir: string;
  automationsDir: string;
  workers: number;
  invalid: { name: string; path: string; problems: Problem[] }[];
  oidc: { enabled: boolean; issuer?: string };
}

// ---- Phase 3: connections, approvals, workspace settings ----

export type Autonomy = "observe" | "draft" | "ask" | "auto";
export type Risk = "read" | "write" | "send" | "destructive";
export type ConnectionKind = "google" | "slack" | "websearch" | "mcp";

export interface ActionInfo {
  /** e.g. "gmail.search"; for MCP connections "<connection name>.<tool>". */
  name: string;
  description: string;
  risk: Risk;
  /** With autonomy "draft", this action runs instead (e.g. gmail.send -> gmail.create_draft). */
  draftAlternative?: string;
}

export interface ConnectorField {
  name: string;
  label: string;
  type: "string" | "string[]" | "map";
  /** Secret fields are write-only: sent in ConnectionInput.secrets, never returned. */
  secret: boolean;
  required: boolean;
  help?: string;
  options?: string[];
  default?: unknown;
}

export interface ConnectorKindInfo {
  kind: ConnectionKind;
  label: string;
  description: string;
  fields: ConnectorField[];
  /** True when the connection needs a browser sign-in after saving (Google). */
  oauth: boolean;
  /** Steps shown in the setup wizard, in order. Plain text, may contain URLs. */
  setupSteps: string[];
  /** Actions every connection of this kind provides (empty for MCP: discovered per server). */
  actions: ActionInfo[];
}

export interface ConnectorCatalog {
  kinds: ConnectorKindInfo[];
  /** The redirect URI to register with OAuth providers, e.g. "https://pendolo.example.com/api/v1/oauth/callback". */
  oauthRedirectURL: string;
}

export interface Connection {
  /** Used in automations as `connection: <name>` (and as the action prefix for MCP). */
  name: string;
  kind: ConnectionKind;
  /** Non-secret settings, by field name. */
  config: Record<string, unknown>;
  /** Names of secret fields that hold a value; values are never returned. For MCP, the header names. */
  secretsSet: string[];
  /** needs_auth: saved but the OAuth sign-in has not been completed. */
  status: "ready" | "needs_auth" | "error";
  error?: string;
  /** The signed-in account, when the provider tells us (Google email, Slack team). */
  account?: string;
  updatedAt: string;
  updatedBy: string;
  /** What the connection can do. For MCP, discovered from the server (empty when unreachable). */
  actions: ActionInfo[];
}

export interface ConnectionInput {
  kind: ConnectionKind;
  config: Record<string, unknown>;
  /**
   * Only the secrets to set or replace; omitted ones keep their value, and an
   * empty string removes one. For MCP, keys are header names
   * (e.g. {"Authorization": "Bearer ..."}).
   */
  secrets?: Record<string, string>;
}

export interface ConnectionTest {
  ok: boolean;
  detail: string;
}

export interface Approval {
  id: string;
  runId: string;
  workspace: string;
  automation: string;
  stepPath: string;
  message: string;
  /** What runs if approved: { action?, connection?, input?, ... } with secrets masked. */
  detail: unknown;
  /** Roles allowed to decide (owners always can). */
  approvers: Role[];
  status: "pending" | "approved" | "rejected" | "expired";
  onTimeout: "reject" | "approve";
  requestedAt: string;
  expiresAt: string;
  decidedAt?: string;
  decidedBy?: string;
  comment?: string;
}

export interface WorkspaceSettings {
  id: string;
  name: string;
  /** The most autonomy any automation in this workspace gets, whatever its manifest says. */
  maxAutonomy: Autonomy;
}

// ---- AI builder (phase 4) ----

/** GET /workspaces/{ws}/builder: builder role and up. */
export interface BuilderStatus {
  /** False when no model is configured; templates and hand edits still work. */
  enabled: boolean;
  model?: string;
  /** Why the builder is off and how to turn it on. */
  reason?: string;
  templates: { name: string; description: string }[];
  /** Whether the caller may activate drafts (admin and owner). */
  canActivate: boolean;
}

/** open: being written. proposed: waiting for an admin. activated and discarded are final. */
export type DraftStatus = "open" | "proposed" | "activated" | "discarded";

/** A finding about a draft. file is the file it is in (e.g. "automation.yaml"); path a JSON pointer in the manifest. */
export interface DraftProblem extends Problem {
  file?: string;
}

/** "event": something that happened to the draft (manual edit, dry run, proposal, activation), written by Pendolo. */
export interface DraftMessage {
  id: number;
  role: "user" | "assistant" | "event";
  author?: string;
  text: string;
  /** The draft's hash after this message: each reply points to a version of the files. */
  hash?: string;
  createdAt: string;
}

export interface Draft {
  id: string;
  workspace: string;
  /** metadata.name of the draft; "" until the model writes one. */
  automation: string;
  /** Set when the draft edits an existing automation: its name and the version it started from. */
  baseName?: string;
  baseHash?: string;
  /** Files by path ("automation.yaml", "prompts/x.md"). Empty in list responses. */
  files: Record<string, string> | null;
  problems: DraftProblem[];
  /** Hash of the files, as versions and runs show it. */
  hash: string;
  status: DraftStatus;
  /** The latest dry run, and the draft hash it ran on (stale when it differs from hash). */
  dryRunId?: string;
  dryRunHash?: string;
  /** Spent on model calls by the builder for this draft. */
  costEur: number;
  createdBy: string;
  createdAt: string;
  updatedAt: string;
  proposedBy?: string;
  proposedAt?: string;
  decidedBy?: string;
  decidedAt?: string;
  comment?: string;
}

/** GET /drafts/{id} and every draft action. */
export interface DraftDetail extends Draft {
  files: Record<string, string>;
  messages: DraftMessage[];
  /** Files of the version the draft edits, for a diff. Absent for a new automation. */
  baseFiles?: Record<string, string>;
  /** The automation's version now; differs from baseHash when it changed since the draft started (activation then fails with 409 stale). */
  currentHash?: string;
  dryRun?: Run;
  /** True while the builder works on the draft (another message is refused with 409 busy). */
  busy: boolean;
}

/** POST /drafts. Give message (needs the builder), template, or automation (optionally with version, to restore it). message may combine with template or automation. */
export interface CreateDraftInput {
  message?: string;
  template?: string;
  automation?: string;
  version?: string;
}

export interface DryRunStarted {
  runId: string;
  /** e.g. which email it used as sample input, or why there is none. */
  note?: string;
}

export interface AutomationVersion {
  automation: string;
  hash: string;
  workspace: string;
  /** "builder": activated from a draft; "folder": found in the automations folder (edited by hand or first seen). */
  source: "builder" | "folder";
  draftId?: string;
  activatedBy?: string;
  createdAt: string;
  /** Only in GET .../versions/{hash}. */
  files?: Record<string, string>;
}

export interface VersionList {
  /** The version running now. */
  current: string;
  /** Newest first. */
  versions: AutomationVersion[];
}
