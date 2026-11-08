// Human-readable summaries for common cron expressions. Anything unusual falls back to the raw text.

const DOW = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

const MACROS: Record<string, string> = {
  "@yearly": "Every year on Jan 1 at 00:00",
  "@annually": "Every year on Jan 1 at 00:00",
  "@monthly": "Every month on day 1 at 00:00",
  "@weekly": "Every Sunday at 00:00",
  "@daily": "Every day at 00:00",
  "@midnight": "Every day at 00:00",
  "@hourly": "Every hour",
};

function isInt(s: string): boolean {
  return /^\d+$/.test(s);
}

function list(field: string): number[] | undefined {
  const parts = field.split(",");
  const out: number[] = [];
  for (const p of parts) {
    if (isInt(p)) out.push(Number(p));
    else return undefined;
  }
  return out;
}

function pad(n: number): string {
  return String(n).padStart(2, "0");
}

function describeDow(field: string): string | undefined {
  if (field === "*") return undefined;
  if (field === "1-5") return "Mon–Fri";
  if (field === "0,6" || field === "6,0") return "Sat and Sun";
  const range = /^(\d)-(\d)$/.exec(field);
  if (range) {
    const a = DOW[Number(range[1]) % 7];
    const b = DOW[Number(range[2]) % 7];
    return a && b ? `${a}–${b}` : undefined;
  }
  const days = list(field);
  if (!days) return undefined;
  const names = days.map((d) => DOW[d % 7]);
  if (names.some((n) => n === undefined)) return undefined;
  return names.join(", ");
}

/** E.g. "Every day at 08:00, 16:00" for "0 8,16 * * *"; undefined when not simple. */
export function describeCron(expr: string): string | undefined {
  const e = expr.trim();
  if (e.startsWith("@")) {
    const every = /^@every\s+(.+)$/.exec(e);
    if (every) return `Every ${every[1]}`;
    return MACROS[e];
  }
  const f = e.split(/\s+/);
  if (f.length !== 5) return undefined;
  const [min, hour, dom, mon, dow] = f as [string, string, string, string, string];

  const stepMin = /^\*\/(\d+)$/.exec(min);
  if (stepMin && hour === "*" && dom === "*" && mon === "*" && dow === "*") {
    return stepMin[1] === "1" ? "Every minute" : `Every ${stepMin[1]} minutes`;
  }
  if (min === "*" && hour === "*" && dom === "*" && mon === "*" && dow === "*") return "Every minute";
  if (isInt(min) && hour === "*" && dom === "*" && mon === "*" && dow === "*") {
    return min === "0" ? "Every hour" : `Every hour at :${pad(Number(min))}`;
  }
  const stepHour = /^\*\/(\d+)$/.exec(hour);
  if (isInt(min) && stepHour && dom === "*" && mon === "*" && dow === "*") {
    return `Every ${stepHour[1]} hours at :${pad(Number(min))}`;
  }

  const mins = list(min);
  const hours = list(hour);
  if (!mins || !hours || mon !== "*") return undefined;
  if (mins.length * hours.length > 6) return undefined;
  const times: string[] = [];
  for (const h of hours) for (const m of mins) times.push(`${pad(h)}:${pad(m)}`);
  const at = `at ${times.join(", ")}`;

  if (dom === "*" && dow === "*") return `Every day ${at}`;
  if (dom === "*") {
    const d = describeDow(dow);
    return d ? `${d} ${at}` : undefined;
  }
  if (dow === "*") {
    const days = list(dom);
    if (!days) return undefined;
    return `Monthly on day ${days.join(", ")} ${at}`;
  }
  return undefined;
}
