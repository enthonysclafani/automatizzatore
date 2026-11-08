import { Link, useNavigate } from "@tanstack/react-router";
import type { Run } from "../../api/types";
import { durationBetween, formatDuration, formatEur, pluralize } from "../../lib/format";
import { useNow } from "../../lib/now";
import { StatusBadge } from "../ui/StatusBadge";
import { Table, Td, TdNum, Th, THead, Tr } from "../ui/Table";
import { RelativeTime } from "../ui/Time";
import { triggerTypeLabel } from "./Triggers";

export function runNote(run: Run): string {
  if (run.error) return run.error;
  if (run.anomalies > 0) return `${pluralize(run.anomalies, "step")} failed`;
  return "";
}

export function RunsTable({ ws, runs, showAutomation = true }: { ws: string; runs: Run[]; showAutomation?: boolean }) {
  const now = useNow();
  const navigate = useNavigate();
  return (
    <Table label="Runs">
      <THead>
        <tr>
          <Th>Status</Th>
          {showAutomation && <Th>Automation</Th>}
          <Th>Trigger</Th>
          <Th>Started</Th>
          <Th className="text-right">Duration</Th>
          <Th className="text-right">Cost</Th>
          <Th>Note</Th>
        </tr>
      </THead>
      <tbody>
        {runs.map((r) => {
          const dur = r.startedAt ? durationBetween(r.startedAt, r.finishedAt, now) : undefined;
          const note = runNote(r);
          return (
            <Tr
              key={r.id}
              className="cursor-pointer hover:bg-surface-2"
              onClick={(e) => {
                if ((e.target as HTMLElement).closest("a,button")) return;
                void navigate({ to: "/w/$ws/runs/$id", params: { ws, id: r.id } });
              }}
            >
              <Td>
                <Link
                  to="/w/$ws/runs/$id"
                  params={{ ws, id: r.id }}
                  aria-label={`Run ${r.id} of ${r.automation}, ${r.status}`}
                  className="rounded"
                >
                  <StatusBadge status={r.status} />
                </Link>
              </Td>
              {showAutomation && (
                <Td className="max-w-[16rem] truncate font-medium text-fg">
                  {r.triggerType === "dry_run" ? (
                    // A dry run tests a draft, which may not be an automation yet.
                    r.automation
                  ) : (
                    <Link
                      to="/w/$ws/automations/$name"
                      params={{ ws, name: r.automation }}
                      className="hover:underline"
                    >
                      {r.automation}
                    </Link>
                  )}
                </Td>
              )}
              <Td className="whitespace-nowrap text-fg-2">
                {triggerTypeLabel[r.triggerType] ?? r.triggerType}
              </Td>
              <Td className="whitespace-nowrap text-fg-2">
                {r.startedAt ? (
                  <RelativeTime iso={r.startedAt} />
                ) : (
                  <span>
                    queued <RelativeTime iso={r.queuedAt} />
                  </span>
                )}
              </Td>
              <TdNum className="text-fg-2">{dur === undefined ? "–" : formatDuration(dur)}</TdNum>
              <TdNum className="text-fg-2">{formatEur(r.costEur)}</TdNum>
              <Td className="max-w-[22rem] min-w-[8rem]">
                {note && (
                  <span
                    className={`line-clamp-1 ${r.error ? "text-[var(--st-red-fg)]" : "text-[var(--st-amber-fg)]"}`}
                    title={note}
                  >
                    {note}
                  </span>
                )}
              </Td>
            </Tr>
          );
        })}
      </tbody>
    </Table>
  );
}
