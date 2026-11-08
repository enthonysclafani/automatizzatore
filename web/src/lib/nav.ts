import type { RunStatus } from "../api/types";

/** Only same-site paths are valid targets after sign-in. */
export function safeRedirect(value: unknown): string | undefined {
  if (typeof value !== "string") return undefined;
  if (!value.startsWith("/") || value.startsWith("//") || value.startsWith("/\\")) return undefined;
  if (value.startsWith("/login") || value.startsWith("/setup")) return undefined;
  return value;
}

export const RUN_STATUSES: RunStatus[] = ["queued", "running", "waiting", "succeeded", "failed", "cancelled"];

export interface RunsSearch {
  automation?: string;
  status?: RunStatus;
}

export const COST_PERIODS = [7, 30, 90] as const;
export type CostPeriod = (typeof COST_PERIODS)[number];
export const DEFAULT_COST_PERIOD: CostPeriod = 30;
