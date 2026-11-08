import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMemo, useState } from "react";
import type { AutomationSummary } from "../api/types";
import { TriggerList } from "../components/domain/Triggers";
import { Badge } from "../components/ui/Badge";
import { Card } from "../components/ui/Card";
import { IconBolt } from "../components/ui/icons";
import { Input } from "../components/ui/Input";
import { Page, PageHeader } from "../components/ui/Page";
import { EmptyState, ErrorState, SkeletonRows } from "../components/ui/States";
import { StatusIcon, statusLabel } from "../components/ui/StatusBadge";
import { Table, Td, Th, THead, Tr } from "../components/ui/Table";
import { RelativeTime } from "../components/ui/Time";
import { automationsQuery } from "../lib/queries";
import { useWs } from "./WorkspaceGate";

export type AutomationState = "active" | "paused" | "invalid";

export function automationState(a: Pick<AutomationSummary, "valid" | "enabled">): AutomationState {
  if (!a.valid) return "invalid";
  return a.enabled ? "active" : "paused";
}

export function StateBadge({ automation }: { automation: Pick<AutomationSummary, "valid" | "enabled"> }) {
  const s = automationState(automation);
  if (s === "invalid") return <Badge tone="red">Invalid</Badge>;
  if (s === "paused") return <Badge tone="amber">Paused</Badge>;
  return <Badge tone="green">Active</Badge>;
}

export function AutomationsPage() {
  const ws = useWs();
  const q = useQuery(automationsQuery(ws));
  const navigate = useNavigate();
  const [filter, setFilter] = useState("");

  const rows = useMemo(() => {
    const list = q.data?.automations ?? [];
    const f = filter.trim().toLowerCase();
    const filtered = f
      ? list.filter((a) => a.name.toLowerCase().includes(f) || a.description.toLowerCase().includes(f))
      : list;
    return [...filtered].sort((a, b) => a.name.localeCompare(b.name));
  }, [q.data, filter]);

  return (
    <Page wide>
      <PageHeader
        title="Automations"
        description="Each automation is a folder with an automation.yaml. Pendolo picks up changes from the automations folder."
      />
      <Card>
        {q.isPending ? (
          <SkeletonRows rows={6} />
        ) : q.isError ? (
          <ErrorState error={q.error} onRetry={() => void q.refetch()} />
        ) : q.data.automations.length === 0 ? (
          <EmptyState
            icon={<IconBolt size={26} />}
            title="No automations in this workspace"
            hint="Add a folder with an automation.yaml to the automations folder (see docs/manifest.md), or copy one of the templates. It appears here within seconds."
          />
        ) : (
          <>
            <div className="border-b border-line p-3">
              <label htmlFor="automation-filter" className="sr-only">
                Filter automations
              </label>
              <Input
                id="automation-filter"
                type="search"
                placeholder="Filter by name or description"
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
                className="max-w-xs"
              />
            </div>
            {rows.length === 0 ? (
              <EmptyState title="No automation matches" hint="Try a shorter filter." />
            ) : (
              <Table label="Automations">
                <THead>
                  <tr>
                    <Th>Name</Th>
                    <Th>State</Th>
                    <Th>Triggers</Th>
                    <Th>Last run</Th>
                    <Th>Next run</Th>
                  </tr>
                </THead>
                <tbody>
                  {rows.map((a) => (
                    <Tr
                      key={a.name}
                      className="cursor-pointer hover:bg-surface-2"
                      onClick={(e) => {
                        if ((e.target as HTMLElement).closest("a,button")) return;
                        void navigate({ to: "/w/$ws/automations/$name", params: { ws, name: a.name } });
                      }}
                    >
                      <Td className="max-w-[20rem]">
                        <Link
                          to="/w/$ws/automations/$name"
                          params={{ ws, name: a.name }}
                          className="font-medium text-fg hover:underline"
                        >
                          {a.name}
                        </Link>
                        {a.description && <p className="truncate text-[12px] text-muted" title={a.description}>{a.description}</p>}
                      </Td>
                      <Td>
                        <StateBadge automation={a} />
                      </Td>
                      <Td className="min-w-[14rem] text-fg-2">
                        <TriggerList triggers={a.triggers} />
                      </Td>
                      <Td className="whitespace-nowrap">
                        {a.lastRun ? (
                          <Link
                            to="/w/$ws/runs/$id"
                            params={{ ws, id: a.lastRun.id }}
                            className="inline-flex items-center gap-1.5 text-fg-2 hover:text-fg hover:underline"
                            aria-label={`Last run: ${statusLabel(a.lastRun.status)}`}
                          >
                            <StatusIcon status={a.lastRun.status} />
                            <RelativeTime iso={a.lastRun.finishedAt ?? a.lastRun.startedAt ?? a.lastRun.queuedAt} />
                          </Link>
                        ) : (
                          <span className="text-muted">Never</span>
                        )}
                      </Td>
                      <Td className="whitespace-nowrap text-fg-2">
                        {a.nextRunAt ? <RelativeTime iso={a.nextRunAt} /> : <span className="text-muted">–</span>}
                      </Td>
                    </Tr>
                  ))}
                </tbody>
              </Table>
            )}
          </>
        )}
      </Card>
    </Page>
  );
}
