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

export type RunStatus = "queued" | "running" | "succeeded" | "failed" | "cancelled";
export type StepStatus = "running" | "succeeded" | "failed" | "skipped";

export interface Run {
  id: string;
  automation: string;
  workspace: string;
  manifestHash: string;
  triggerType: "schedule" | "webhook" | "manual" | "email";
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
  costMonthEur: number;
  /** Last 14 days, oldest first, every day present (zeros included). Dates are YYYY-MM-DD. */
  daily: { date: string; succeeded: number; failed: number; costEur: number }[];
  /** Up to 5 most recent failed runs. */
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
