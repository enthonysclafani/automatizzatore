import { useState, type ReactNode } from "react";
import type { ActionInfo, Connection, Risk } from "../../api/types";
import { Badge, type Tone } from "../ui/Badge";
import { IconAlert, IconCheckCircle, IconKey } from "../ui/icons";

// Risk colors are the same wherever an action is listed; the label always says the risk.
const RISK: Record<Risk, { tone: Tone; label: string; hint: string }> = {
  read: { tone: "gray", label: "read", hint: "Only reads data" },
  write: { tone: "blue", label: "write", hint: "Changes data you own (labels, drafts, documents)" },
  send: { tone: "amber", label: "send", hint: "Sends something to other people" },
  destructive: { tone: "red", label: "destructive", hint: "Deletes or cannot be undone; always asks for approval" },
};

export function RiskBadge({ risk }: { risk: Risk }) {
  const r = RISK[risk] ?? RISK.write;
  return (
    <Badge tone={r.tone} title={r.hint}>
      <span className="sr-only">risk: </span>
      {r.label}
    </Badge>
  );
}

export function ConnectionStatusBadge({ status }: { status: Connection["status"] }) {
  if (status === "ready") {
    return (
      <Badge tone="green" icon={<IconCheckCircle size={12} />}>
        Ready
      </Badge>
    );
  }
  if (status === "needs_auth") {
    return (
      <Badge tone="amber" icon={<IconKey size={12} />}>
        Needs sign-in
      </Badge>
    );
  }
  return (
    <Badge tone="red" icon={<IconAlert size={12} />}>
      Error
    </Badge>
  );
}

const VISIBLE_ACTIONS = 6;
const RISK_ORDER: Record<Risk, number> = { destructive: 0, send: 1, write: 2, read: 3 };

/** Action names with their risk and, when there is one, the draft alternative. */
export function ActionList({ actions, empty }: { actions: ActionInfo[]; empty?: ReactNode }) {
  const [all, setAll] = useState(false);
  if (actions.length === 0) return empty ? <p className="text-[13px] text-muted">{empty}</p> : null;
  // Riskiest first: those are the ones people need to see.
  const sorted = [...actions].sort((a, b) => RISK_ORDER[a.risk] - RISK_ORDER[b.risk] || a.name.localeCompare(b.name));
  const shown = all ? sorted : sorted.slice(0, VISIBLE_ACTIONS);
  return (
    <div>
      <ul className="divide-y divide-line rounded-md border border-line">
        {shown.map((a) => (
          <li key={a.name} className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 px-3 py-1.5">
            <code className="font-mono text-[12.5px] break-all text-fg">{a.name}</code>
            <RiskBadge risk={a.risk} />
            {a.draftAlternative && (
              <span className="text-[12px] text-fg-2">
                → draft: <code className="font-mono">{a.draftAlternative}</code>
              </span>
            )}
            {a.description && <span className="w-full text-[12px] text-muted">{a.description}</span>}
          </li>
        ))}
      </ul>
      {sorted.length > VISIBLE_ACTIONS && (
        <button
          type="button"
          onClick={() => setAll((v) => !v)}
          aria-expanded={all}
          className="mt-1.5 rounded text-[12.5px] font-medium text-accent hover:underline"
        >
          {all ? "Show fewer actions" : `Show all ${sorted.length} actions`}
        </button>
      )}
    </div>
  );
}

const URL_RE = /(https?:\/\/[^\s<>"']+)/g;

/** Plain text with http(s) URLs turned into links that open in a new tab. */
export function Linkify({ text }: { text: string }) {
  const parts = text.split(URL_RE);
  return (
    <>
      {parts.map((part, i) => {
        if (i % 2 === 0) return part;
        // Keep sentence punctuation out of the link.
        const m = /^(.*?)([.,;:!?)]*)$/.exec(part);
        const url = m?.[1] ?? part;
        const tail = m?.[2] ?? "";
        return (
          <span key={i}>
            <a href={url} target="_blank" rel="noopener noreferrer" className="break-all text-accent underline">
              {url}
              <span className="sr-only"> (opens in a new tab)</span>
            </a>
            {tail}
          </span>
        );
      })}
    </>
  );
}

/** Lowercase letters, digits, "_" and "-", starting with a letter (the server's rule). */
export const CONNECTION_NAME_RE = /^[a-z][a-z0-9_-]{0,62}$/;

export const DEFAULT_CONNECTION_NAME: Record<string, string> = {
  google: "google",
  slack: "slack",
  websearch: "web",
  mcp: "",
};
