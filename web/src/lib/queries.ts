import { infiniteQueryOptions, queryOptions, useQuery } from "@tanstack/react-query";
import { api, isApiError } from "../api/client";
import type { Approval, Run, RunStatus } from "../api/types";

/** Fast polling while something is running, slow otherwise. */
export const POLL_ACTIVE_MS = 5000;
export const POLL_IDLE_MS = 30000;
/** A run waiting for approval: someone else may decide, so check now and then. */
export const POLL_WAITING_MS = 15000;

export function isActive(status: RunStatus | undefined): boolean {
  return status === "running" || status === "queued";
}

export const keys = {
  setup: ["setup"] as const,
  me: ["me"] as const,
  tokens: ["tokens"] as const,
  ws: (ws: string) => ["ws", ws] as const,
  overview: (ws: string) => ["ws", ws, "overview"] as const,
  automations: (ws: string) => ["ws", ws, "automations"] as const,
  automation: (ws: string, name: string) => ["ws", ws, "automation", name] as const,
  runs: (ws: string, automation?: string, status?: string) =>
    ["ws", ws, "runs", { automation: automation ?? "", status: status ?? "" }] as const,
  run: (ws: string, id: string) => ["ws", ws, "run", id] as const,
  costs: (ws: string, days: number) => ["ws", ws, "costs", days] as const,
  secrets: (ws: string) => ["ws", ws, "secrets"] as const,
  members: (ws: string) => ["ws", ws, "members"] as const,
  audit: (ws: string) => ["ws", ws, "audit"] as const,
  approvals: (ws: string) => ["ws", ws, "approvals"] as const,
  approvalList: (ws: string, status: string) => ["ws", ws, "approvals", status] as const,
  connections: (ws: string) => ["ws", ws, "connections"] as const,
  settings: (ws: string) => ["ws", ws, "settings"] as const,
  connectors: ["connectors"] as const,
  users: ["users"] as const,
  system: ["system"] as const,
};

export const setupQuery = queryOptions({
  queryKey: keys.setup,
  queryFn: api.getSetup,
  staleTime: 60_000,
});

export const meQuery = queryOptions({
  queryKey: keys.me,
  queryFn: api.me,
  staleTime: 60_000,
  retry: (count, err) => !(isApiError(err) && err.status === 401) && count < 2,
});

export function useMe() {
  return useQuery(meQuery);
}

export function overviewQuery(ws: string) {
  return queryOptions({
    queryKey: keys.overview(ws),
    queryFn: () => api.overview(ws),
    refetchInterval: (q) => {
      const d = q.state.data;
      return d && d.running + d.queued > 0 ? POLL_ACTIVE_MS : POLL_IDLE_MS;
    },
  });
}

export function automationsQuery(ws: string) {
  return queryOptions({
    queryKey: keys.automations(ws),
    queryFn: () => api.listAutomations(ws),
    refetchInterval: (q) =>
      q.state.data?.automations.some((a) => isActive(a.lastRun?.status)) ? POLL_ACTIVE_MS : POLL_IDLE_MS,
  });
}

export function automationQuery(ws: string, name: string) {
  return queryOptions({
    queryKey: keys.automation(ws, name),
    queryFn: () => api.getAutomation(ws, name),
    refetchInterval: (q) =>
      q.state.data?.recentRuns.some((r) => isActive(r.status)) ? POLL_ACTIVE_MS : POLL_IDLE_MS,
  });
}

export const RUNS_PAGE_SIZE = 50;

export function runsQuery(ws: string, automation?: string, status?: RunStatus) {
  return infiniteQueryOptions({
    queryKey: keys.runs(ws, automation, status),
    queryFn: ({ pageParam }) =>
      api.listRuns(ws, { automation, status, limit: RUNS_PAGE_SIZE, before: pageParam || undefined }),
    initialPageParam: "",
    getNextPageParam: (last) => last.next,
    refetchInterval: (q) => {
      const pages = q.state.data?.pages ?? [];
      const active = pages.some((p) => p.runs.some((r: Run) => isActive(r.status)));
      return active ? POLL_ACTIVE_MS : POLL_IDLE_MS;
    },
  });
}

export function runQuery(ws: string, id: string) {
  return queryOptions({
    queryKey: keys.run(ws, id),
    queryFn: () => api.getRun(ws, id),
    refetchInterval: (q) => {
      const status = q.state.data?.run.status;
      if (isActive(status)) return POLL_ACTIVE_MS;
      return status === "waiting" ? POLL_WAITING_MS : false;
    },
  });
}

export function costsQuery(ws: string, days: number) {
  return queryOptions({
    queryKey: keys.costs(ws, days),
    queryFn: () => api.costs(ws, days),
  });
}

/** Pending approvals; shared by the sidebar badge and the Approvals page. */
export function pendingApprovalsQuery(ws: string) {
  return queryOptions({
    queryKey: keys.approvalList(ws, "pending"),
    queryFn: () => api.listApprovals(ws, "pending"),
    refetchInterval: POLL_WAITING_MS,
  });
}

/** Every approval the server keeps; the Recent tab shows the decided ones. */
export function allApprovalsQuery(ws: string) {
  return queryOptions({
    queryKey: keys.approvalList(ws, "all"),
    queryFn: () => api.listApprovals(ws),
    refetchInterval: POLL_IDLE_MS,
  });
}

export function connectionsQuery(ws: string) {
  return queryOptions({
    queryKey: keys.connections(ws),
    queryFn: () => api.listConnections(ws),
  });
}

export const connectorsQuery = queryOptions({
  queryKey: keys.connectors,
  queryFn: api.connectors,
  staleTime: 5 * 60_000,
});

export function settingsQuery(ws: string) {
  return queryOptions({
    queryKey: keys.settings(ws),
    queryFn: () => api.getSettings(ws),
  });
}

/** Newest first by request time. */
export function sortApprovals(list: Approval[]): Approval[] {
  return [...list].sort((a, b) => b.requestedAt.localeCompare(a.requestedAt));
}
