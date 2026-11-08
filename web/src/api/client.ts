// Typed client for the Pendolo HTTP API (docs/api.md).
// Same origin, cookie session; non-GET requests carry the CSRF header.

import type {
  ApiToken,
  Approval,
  AuditEntry,
  AutomationDetail,
  AutomationSummary,
  AutomationVersion,
  Autonomy,
  BuilderStatus,
  Connection,
  ConnectionInput,
  ConnectionTest,
  ConnectorCatalog,
  Costs,
  CreateDraftInput,
  Draft,
  DraftDetail,
  DraftStatus,
  DryRunStarted,
  LoginRequest,
  Me,
  Member,
  NewApiToken,
  Overview,
  Role,
  RunDetail,
  RunList,
  RunStatus,
  SecretInfo,
  SetupRequest,
  SetupStatus,
  SystemInfo,
  User,
  VersionList,
  WorkspaceRef,
  WorkspaceSettings,
} from "./types";

const BASE = "/api/v1";

/** Error thrown for every non-2xx answer. */
export class ApiError extends Error {
  readonly status: number;
  /** Human-readable message from the server. */
  readonly error: string;
  /** Machine-readable code, e.g. "totp_required". */
  readonly code: string | undefined;

  constructor(status: number, error: string, code?: string) {
    super(error);
    this.name = "ApiError";
    this.status = status;
    this.error = error;
    this.code = code;
  }
}

export function isApiError(err: unknown): err is ApiError {
  return err instanceof ApiError;
}

type UnauthenticatedHandler = (returnTo: string) => void;

let onUnauthenticated: UnauthenticatedHandler = (returnTo) => {
  window.location.assign(`/login?redirect=${encodeURIComponent(returnTo)}`);
};

/** Replace the default 401 behaviour (a full page load of /login). */
export function setUnauthenticatedHandler(handler: UnauthenticatedHandler): void {
  onUnauthenticated = handler;
}

const PUBLIC_PATHS = ["/login", "/setup"];

function currentPath(): string {
  return window.location.pathname + window.location.search;
}

type Method = "GET" | "POST" | "PUT" | "PATCH" | "DELETE";

async function request<T>(method: Method, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" };
  if (method !== "GET") headers["X-Pendolo-Request"] = "1";
  if (body !== undefined) headers["Content-Type"] = "application/json";

  let res: Response;
  try {
    res = await fetch(BASE + path, {
      method,
      headers,
      credentials: "same-origin",
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new ApiError(0, "Cannot reach the Pendolo server. Check that it is running, then retry.", "network");
  }

  if (res.status === 204) return undefined as T;

  const text = await res.text();
  let data: unknown = undefined;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = undefined;
    }
  }

  if (!res.ok) {
    const payload = (data ?? {}) as { error?: unknown; code?: unknown };
    const message =
      typeof payload.error === "string" && payload.error
        ? payload.error
        : `The server answered ${res.status} ${res.statusText}.`;
    const code = typeof payload.code === "string" ? payload.code : undefined;
    const err = new ApiError(res.status, message, code);
    if (res.status === 401 && code === "unauthenticated") {
      const here = window.location.pathname;
      if (!PUBLIC_PATHS.includes(here)) onUnauthenticated(currentPath());
    }
    throw err;
  }

  return data as T;
}

function qs(params: Record<string, string | number | undefined | null>): string {
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== "") sp.set(k, String(v));
  }
  const s = sp.toString();
  return s ? `?${s}` : "";
}

const enc = encodeURIComponent;
const ws = (id: string) => `/workspaces/${enc(id)}`;

export interface RunQuery {
  automation?: string;
  status?: RunStatus;
  limit?: number;
  before?: string;
}

export type ApprovalStatus = Approval["status"];

export interface SettingsPatch {
  name?: string;
  maxAutonomy?: Autonomy;
}

/** Without a status the server lists open and proposed drafts. */
export type DraftListStatus = DraftStatus | "all";

export interface UserPatch {
  name?: string;
  disabled?: boolean;
  isOwner?: boolean;
  password?: string;
}

export const api = {
  // Setup and authentication
  getSetup: () => request<SetupStatus>("GET", "/setup"),
  setup: (body: SetupRequest) => request<Me>("POST", "/setup", body),
  login: (body: LoginRequest) => request<Me>("POST", "/auth/login", body),
  logout: () => request<void>("POST", "/auth/logout"),
  me: () => request<Me>("GET", "/auth/me"),
  /** Full-page navigation target, not a fetch. */
  oidcLoginUrl: `${BASE}/auth/oidc/login`,
  changePassword: (current: string, next: string) =>
    request<void>("POST", "/auth/password", { current, new: next }),
  totpSetup: () => request<{ secret: string; uri: string }>("POST", "/auth/totp/setup"),
  totpEnable: (code: string) => request<void>("POST", "/auth/totp/enable", { code }),
  totpDisable: (password: string) => request<void>("POST", "/auth/totp/disable", { password }),

  // API tokens
  listTokens: () => request<{ tokens: ApiToken[] }>("GET", "/tokens"),
  createToken: (name: string, expiresInDays?: number) =>
    request<NewApiToken>("POST", "/tokens", expiresInDays ? { name, expiresInDays } : { name }),
  revokeToken: (id: string) => request<void>("DELETE", `/tokens/${enc(id)}`),

  // Workspace
  overview: (w: string) => request<Overview>("GET", `${ws(w)}/overview`),
  listAutomations: (w: string) =>
    request<{ automations: AutomationSummary[] }>("GET", `${ws(w)}/automations`),
  getAutomation: (w: string, name: string) =>
    request<AutomationDetail>("GET", `${ws(w)}/automations/${enc(name)}`),
  triggerAutomation: (w: string, name: string) =>
    request<{ runId: string }>("POST", `${ws(w)}/automations/${enc(name)}/trigger`),
  pauseAutomation: (w: string, name: string) =>
    request<void>("POST", `${ws(w)}/automations/${enc(name)}/pause`),
  resumeAutomation: (w: string, name: string) =>
    request<void>("POST", `${ws(w)}/automations/${enc(name)}/resume`),
  listRuns: (w: string, q: RunQuery = {}) =>
    request<RunList>(
      "GET",
      `${ws(w)}/runs${qs({ automation: q.automation, status: q.status, limit: q.limit, before: q.before })}`,
    ),
  getRun: (w: string, id: string) => request<RunDetail>("GET", `${ws(w)}/runs/${enc(id)}`),
  costs: (w: string, days: number) => request<Costs>("GET", `${ws(w)}/costs${qs({ days })}`),
  listSecrets: (w: string) => request<{ secrets: SecretInfo[] }>("GET", `${ws(w)}/secrets`),
  putSecret: (w: string, name: string, value: string) =>
    request<void>("PUT", `${ws(w)}/secrets/${enc(name)}`, { value }),
  deleteSecret: (w: string, name: string) => request<void>("DELETE", `${ws(w)}/secrets/${enc(name)}`),
  listMembers: (w: string) => request<{ members: Member[] }>("GET", `${ws(w)}/members`),
  putMember: (w: string, email: string, role: Role) =>
    request<void>("PUT", `${ws(w)}/members`, { email, role }),
  removeMember: (w: string, userId: string) =>
    request<void>("DELETE", `${ws(w)}/members/${enc(userId)}`),
  workspaceAudit: (w: string, limit?: number) =>
    request<{ entries: AuditEntry[] }>("GET", `${ws(w)}/audit${qs({ limit })}`),

  // Connections
  connectors: () => request<ConnectorCatalog>("GET", "/connectors"),
  listConnections: (w: string) => request<{ connections: Connection[] }>("GET", `${ws(w)}/connections`),
  putConnection: (w: string, name: string, body: ConnectionInput) =>
    request<Connection>("PUT", `${ws(w)}/connections/${enc(name)}`, body),
  deleteConnection: (w: string, name: string) => request<void>("DELETE", `${ws(w)}/connections/${enc(name)}`),
  testConnection: (w: string, name: string) =>
    request<ConnectionTest>("POST", `${ws(w)}/connections/${enc(name)}/test`),
  /** Returns the provider URL; send the browser there with window.location. */
  startOAuth: (w: string, name: string) =>
    request<{ url: string }>("POST", `${ws(w)}/connections/${enc(name)}/oauth/start`),

  // Approvals
  /** Without a status the server returns every approval it keeps (pending and decided). */
  listApprovals: (w: string, status?: ApprovalStatus) =>
    request<{ approvals: Approval[] }>("GET", `${ws(w)}/approvals${qs({ status })}`),
  approve: (w: string, id: string, comment?: string) =>
    request<Approval>("POST", `${ws(w)}/approvals/${enc(id)}/approve`, comment ? { comment } : {}),
  reject: (w: string, id: string, comment?: string) =>
    request<Approval>("POST", `${ws(w)}/approvals/${enc(id)}/reject`, comment ? { comment } : {}),

  // AI builder
  builder: (w: string) => request<BuilderStatus>("GET", `${ws(w)}/builder`),
  listDrafts: (w: string, status?: DraftListStatus) =>
    request<{ drafts: Draft[] }>("GET", `${ws(w)}/drafts${qs({ status })}`),
  /** Slow (10-90 s) when body.message is set: the model writes the first version. */
  createDraft: (w: string, body: CreateDraftInput) => request<DraftDetail>("POST", `${ws(w)}/drafts`, body),
  getDraft: (w: string, id: string) => request<DraftDetail>("GET", `${ws(w)}/drafts/${enc(id)}`),
  /** Slow like createDraft: one builder turn. */
  sendDraftMessage: (w: string, id: string, text: string) =>
    request<DraftDetail>("POST", `${ws(w)}/drafts/${enc(id)}/messages`, { text }),
  /** Replaces every file of the draft; the answer carries the new problems. */
  putDraftFiles: (w: string, id: string, files: Record<string, string>) =>
    request<DraftDetail>("PUT", `${ws(w)}/drafts/${enc(id)}/files`, { files }),
  /** Starts the run in the background; poll getRun until it ends. */
  dryRunDraft: (w: string, id: string, triggerData?: Record<string, unknown>) =>
    request<DryRunStarted>("POST", `${ws(w)}/drafts/${enc(id)}/dry-run`, triggerData ? { triggerData } : {}),
  proposeDraft: (w: string, id: string) => request<DraftDetail>("POST", `${ws(w)}/drafts/${enc(id)}/propose`),
  discardDraft: (w: string, id: string, comment?: string) =>
    request<DraftDetail>("POST", `${ws(w)}/drafts/${enc(id)}/discard`, comment ? { comment } : {}),
  /** hash: the version of the files the admin reviewed; 409 "changed" if the draft moved on since. */
  activateDraft: (w: string, id: string, hash: string, comment?: string) =>
    request<DraftDetail>("POST", `${ws(w)}/drafts/${enc(id)}/activate`, comment ? { hash, comment } : { hash }),
  listVersions: (w: string, name: string) =>
    request<VersionList>("GET", `${ws(w)}/automations/${enc(name)}/versions`),
  getVersion: (w: string, name: string, hash: string) =>
    request<AutomationVersion>("GET", `${ws(w)}/automations/${enc(name)}/versions/${enc(hash)}`),

  // Workspace settings
  getSettings: (w: string) => request<WorkspaceSettings>("GET", `${ws(w)}/settings`),
  patchSettings: (w: string, patch: SettingsPatch) =>
    request<WorkspaceSettings>("PATCH", `${ws(w)}/settings`, patch),

  // Instance (owners)
  createWorkspace: (id: string, name: string) => request<WorkspaceRef>("POST", "/workspaces", { id, name }),
  listUsers: () => request<{ users: User[] }>("GET", "/users"),
  createUser: (body: { email: string; name: string; password: string; isOwner: boolean }) =>
    request<User>("POST", "/users", body),
  updateUser: (id: string, patch: UserPatch) => request<User>("PATCH", `/users/${enc(id)}`, patch),
  instanceAudit: (limit?: number) => request<{ entries: AuditEntry[] }>("GET", `/audit${qs({ limit })}`),
  system: () => request<SystemInfo>("GET", "/system"),
};

/** A short message for any thrown value, suitable for the UI. */
export function errorMessage(err: unknown): string {
  if (isApiError(err)) {
    if (err.status === 403) return err.error || "You do not have permission for this. Ask a workspace admin.";
    if (err.status === 429) return err.error || "Too many attempts. Wait a minute and try again.";
    return err.error;
  }
  if (err instanceof Error) return err.message;
  return "Something went wrong. Try again.";
}
