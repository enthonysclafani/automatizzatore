// Formatting helpers: money, numbers, durations, times.

const eurSmall = new Intl.NumberFormat("en-IE", {
  style: "currency",
  currency: "EUR",
  minimumFractionDigits: 4,
  maximumFractionDigits: 4,
});
const eurLarge = new Intl.NumberFormat("en-IE", {
  style: "currency",
  currency: "EUR",
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
});
const eurAxis = new Intl.NumberFormat("en-IE", {
  style: "currency",
  currency: "EUR",
  maximumSignificantDigits: 3,
});

/** €0.0000 below €1, €0.00 from €1 up. */
export function formatEur(value: number | undefined | null): string {
  const v = value ?? 0;
  return Math.abs(v) < 1 ? eurSmall.format(v) : eurLarge.format(v);
}

/** Compact euro value for chart axes. */
export function formatEurAxis(value: number): string {
  if (value === 0) return "€0";
  return eurAxis.format(value);
}

const intFmt = new Intl.NumberFormat("en-US");
export function formatInt(value: number | undefined | null): string {
  return intFmt.format(value ?? 0);
}

const compactFmt = new Intl.NumberFormat("en-US", { notation: "compact", maximumFractionDigits: 1 });
export function formatCompact(value: number): string {
  return Math.abs(value) < 10000 ? intFmt.format(value) : compactFmt.format(value);
}

/** Duration in milliseconds, e.g. "850 ms", "12.3 s", "4m 05s", "1h 02m". */
export function formatDuration(ms: number | undefined | null): string {
  if (ms === undefined || ms === null || !Number.isFinite(ms) || ms < 0) return "–";
  if (ms < 1000) return `${Math.round(ms)} ms`;
  const s = ms / 1000;
  if (s < 60) return `${s < 10 ? s.toFixed(1) : Math.round(s)} s`;
  const totalS = Math.round(s);
  const m = Math.floor(totalS / 60);
  const rs = totalS % 60;
  if (m < 60) return `${m}m ${String(rs).padStart(2, "0")}s`;
  const h = Math.floor(m / 60);
  const rm = m % 60;
  if (h < 48) return `${h}h ${String(rm).padStart(2, "0")}m`;
  const d = Math.floor(h / 24);
  return `${d}d ${h % 24}h`;
}

export function parseTime(iso: string | undefined | null): number | undefined {
  if (!iso) return undefined;
  const t = Date.parse(iso);
  return Number.isNaN(t) ? undefined : t;
}

/** Milliseconds between two RFC 3339 times; open-ended ranges use `now`. */
export function durationBetween(start?: string, end?: string, now?: number): number | undefined {
  const s = parseTime(start);
  if (s === undefined) return undefined;
  const e = parseTime(end) ?? now;
  if (e === undefined) return undefined;
  return Math.max(0, e - s);
}

const absFmt = new Intl.DateTimeFormat(undefined, {
  year: "numeric",
  month: "short",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
});
const shortFmt = new Intl.DateTimeFormat(undefined, {
  month: "short",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
});

/** Local date and time with seconds, for tooltips and detail views. */
export function formatAbsolute(iso: string | undefined | null): string {
  const t = parseTime(iso);
  return t === undefined ? "–" : absFmt.format(t);
}

export function formatShort(iso: string | undefined | null): string {
  const t = parseTime(iso);
  return t === undefined ? "–" : shortFmt.format(t);
}

/** "in 3h", "5m ago", "just now". */
export function formatRelative(iso: string | undefined | null, now: number): string {
  const t = parseTime(iso);
  if (t === undefined) return "–";
  const diff = t - now;
  const abs = Math.abs(diff);
  const future = diff > 0;
  if (abs < 45_000) return future ? "in a few seconds" : "just now";
  let n: number;
  let unit: string;
  if (abs < 3_600_000) {
    n = Math.round(abs / 60_000);
    unit = "m";
  } else if (abs < 86_400_000) {
    n = Math.round(abs / 3_600_000);
    unit = "h";
  } else if (abs < 30 * 86_400_000) {
    n = Math.round(abs / 86_400_000);
    unit = "d";
  } else {
    return formatShort(iso);
  }
  return future ? `in ${n}${unit}` : `${n}${unit} ago`;
}

const dayFmt = new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric", timeZone: "UTC" });
const weekdayFmt = new Intl.DateTimeFormat(undefined, {
  weekday: "short",
  month: "short",
  day: "numeric",
  timeZone: "UTC",
});

/** "Nov 8" for a YYYY-MM-DD date. */
export function formatDay(date: string): string {
  const t = Date.parse(`${date}T00:00:00Z`);
  return Number.isNaN(t) ? date : dayFmt.format(t);
}

export function formatDayLong(date: string): string {
  const t = Date.parse(`${date}T00:00:00Z`);
  return Number.isNaN(t) ? date : weekdayFmt.format(t);
}

/** Pretty JSON; strings stay as they are. */
export function prettyJson(value: unknown): string {
  if (value === undefined) return "";
  if (typeof value === "string") return value;
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

/** One-line JSON for compact table cells. */
export function compactJson(value: unknown): string {
  if (value === undefined || value === null) return "";
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}

export function isEmptyValue(value: unknown): boolean {
  if (value === undefined || value === null) return true;
  if (typeof value === "string") return value.length === 0;
  if (Array.isArray(value)) return value.length === 0;
  if (typeof value === "object") return Object.keys(value as object).length === 0;
  return false;
}

export function pluralize(n: number, one: string, many = `${one}s`): string {
  return `${formatInt(n)} ${n === 1 ? one : many}`;
}
