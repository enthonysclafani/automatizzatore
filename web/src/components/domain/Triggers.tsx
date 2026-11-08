import type { TriggerSummary } from "../../api/types";
import { describeCron } from "../../lib/cron";
import { Tag } from "../ui/Badge";
import { IconCalendar, IconHand, IconMail, IconWebhook } from "../ui/icons";

export function triggerText(t: TriggerSummary): string {
  switch (t.type) {
    case "schedule": {
      const human = t.cron ? describeCron(t.cron) : undefined;
      const tz = t.timezone || "UTC";
      return `${human ?? t.cron ?? "Schedule"} (${tz})`;
    }
    case "webhook":
      return `Webhook ${t.webhookPath ?? ""}`.trim();
    case "email":
      return "Email";
    case "manual":
      return "Manual";
  }
}

function TriggerIcon({ type }: { type: TriggerSummary["type"] }) {
  const cls = "shrink-0 text-muted";
  if (type === "schedule") return <IconCalendar size={13} className={cls} />;
  if (type === "webhook") return <IconWebhook size={13} className={cls} />;
  if (type === "email") return <IconMail size={13} className={cls} />;
  return <IconHand size={13} className={cls} />;
}

/** One line per trigger; the raw cron stays available on hover. */
export function TriggerList({ triggers, detailed }: { triggers: TriggerSummary[]; detailed?: boolean }) {
  if (triggers.length === 0) return <span className="text-muted">None</span>;
  return (
    <ul className="space-y-0.5">
      {triggers.map((t, i) => {
        const human = t.type === "schedule" && t.cron ? describeCron(t.cron) : undefined;
        return (
          <li key={i} className="flex min-w-0 items-center gap-1.5">
            <TriggerIcon type={t.type} />
            {t.type === "schedule" ? (
              <span className="min-w-0" title={t.cron ? `cron: ${t.cron}` : undefined}>
                {human ?? <code className="font-mono text-[12px]">{t.cron}</code>}
                <span className="text-muted"> · {t.timezone || "UTC"}</span>
                {detailed && human && <code className="ml-2 font-mono text-[12px] text-muted">{t.cron}</code>}
              </span>
            ) : t.type === "webhook" ? (
              <span className="min-w-0">
                <span className="text-fg-2">Webhook </span>
                <code className="font-mono text-[12px] break-all">{t.webhookPath ?? ""}</code>
                {detailed && (
                  <span className="ml-2">
                    {t.hasSecret ? <Tag>signed</Tag> : <Tag title="Anyone who can reach Pendolo can start it">no secret</Tag>}
                  </span>
                )}
              </span>
            ) : (
              <span>{t.type === "email" ? "Email" : "Manual"}</span>
            )}
          </li>
        );
      })}
    </ul>
  );
}

export const triggerTypeLabel: Record<string, string> = {
  schedule: "Schedule",
  webhook: "Webhook",
  manual: "Manual",
  email: "Email",
  dry_run: "Dry run",
};
