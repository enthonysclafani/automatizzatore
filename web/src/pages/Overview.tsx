import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";
import type { Health, Overview } from "../api/types";
import { CostChart } from "../components/charts/CostChart";
import { RunsChart } from "../components/charts/RunsChart";
import { runNote } from "../components/domain/RunsTable";
import { dotColor, type Tone } from "../components/ui/Badge";
import { Card, CardBody, CardHeader } from "../components/ui/Card";
import { cx } from "../components/ui/cx";
import { IconCheckCircle, IconChevronRight, IconClock, IconHourglass, IconSparkle } from "../components/ui/icons";
import { Page, PageHeader } from "../components/ui/Page";
import { EmptyState, ErrorState, Skeleton } from "../components/ui/States";
import { StatusIcon } from "../components/ui/StatusBadge";
import { RelativeTime } from "../components/ui/Time";
import { formatAbsolute, formatEur, formatInt } from "../lib/format";
import { overviewQuery } from "../lib/queries";
import { useMe } from "../lib/queries";
import { atLeast } from "../lib/roles";
import { useWs, useWsRole } from "./WorkspaceGate";

export function OverviewPage() {
  const ws = useWs();
  const { data: me } = useMe();
  const wsName = me?.workspaces.find((w) => w.id === ws)?.name ?? ws;
  const q = useQuery(overviewQuery(ws));

  return (
    <Page wide>
      <PageHeader title="Overview" description={wsName} />
      {q.isPending ? (
        <OverviewSkeleton />
      ) : q.isError ? (
        <Card>
          <ErrorState error={q.error} onRetry={() => void q.refetch()} />
        </Card>
      ) : (
        <OverviewBody ws={ws} data={q.data} />
      )}
    </Page>
  );
}

function OverviewSkeleton() {
  return (
    <div className="space-y-4" aria-busy="true" aria-label="Loading overview">
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-5">
        {Array.from({ length: 5 }, (_, i) => (
          <div key={i} className="rounded-lg border border-line bg-surface p-4">
            <Skeleton className="mb-3 h-3 w-20" />
            <Skeleton className="h-7 w-16" />
          </div>
        ))}
      </div>
      <div className="rounded-lg border border-line bg-surface p-4">
        <Skeleton className="h-56 w-full" />
      </div>
    </div>
  );
}

function OverviewBody({ ws, data }: { ws: string; data: Overview }) {
  const a = data.automations;
  const r = data.runs24h;
  const monthName = new Intl.DateTimeFormat(undefined, { month: "long" }).format(new Date());
  const role = useWsRole();
  // Only builders and up can open the builder; admins and owners activate.
  const showProposed = data.proposedDrafts > 0 && atLeast(role, "builder");
  const canActivate = atLeast(role, "admin");
  return (
    <div className="space-y-4">
      {data.waitingApprovals > 0 && (
        <Link
          to="/w/$ws/approvals"
          params={{ ws }}
          className="flex items-center gap-3 rounded-lg border-2 border-[var(--st-orange-dot)]/60 bg-[var(--st-orange-bg)] px-4 py-3 hover:border-[var(--st-orange-dot)]"
        >
          <IconHourglass size={20} className="shrink-0 text-[var(--st-orange-fg)]" />
          <span className="min-w-0 flex-1">
            <span className="block text-sm font-semibold text-[var(--st-orange-fg)]">
              {data.waitingApprovals === 1
                ? "1 step is waiting for approval"
                : `${formatInt(data.waitingApprovals)} steps are waiting for approval`}
            </span>
            <span className="block text-[12.5px] text-fg-2">Their runs are paused until someone approves or rejects.</span>
          </span>
          <span className="flex shrink-0 items-center gap-1 text-[13px] font-medium text-accent">
            Review <IconChevronRight size={14} />
          </span>
        </Link>
      )}
      {showProposed && (
        <Link
          to="/w/$ws/builder"
          params={{ ws }}
          className="flex items-center gap-3 rounded-lg border-2 border-[var(--st-orange-dot)]/60 bg-[var(--st-orange-bg)] px-4 py-3 hover:border-[var(--st-orange-dot)]"
        >
          <IconSparkle size={20} className="shrink-0 text-[var(--st-orange-fg)]" />
          <span className="min-w-0 flex-1">
            <span className="block text-sm font-semibold text-[var(--st-orange-fg)]">
              {data.proposedDrafts === 1
                ? "1 draft is waiting for activation"
                : `${formatInt(data.proposedDrafts)} drafts are waiting for activation`}
            </span>
            <span className="block text-[12.5px] text-fg-2">
              {canActivate
                ? "Builders proposed them. Check each one and activate it, or discard it."
                : "An admin has to activate them before they run."}
            </span>
          </span>
          <span className="flex shrink-0 items-center gap-1 text-[13px] font-medium text-accent">
            {canActivate ? "Review" : "Open"} <IconChevronRight size={14} />
          </span>
        </Link>
      )}
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-5">
        <HealthTile health={data.health} />
        <Tile
          label="Automations"
          value={formatInt(a.total)}
          detail={
            <>
              {formatInt(a.active)} active · {formatInt(a.paused)} paused
              {a.invalid > 0 ? (
                <>
                  {" · "}
                  <Link to="/w/$ws/automations" params={{ ws }} className="text-[var(--st-red-fg)] hover:underline">
                    {formatInt(a.invalid)} invalid
                  </Link>
                </>
              ) : (
                <> · 0 invalid</>
              )}
            </>
          }
          href={{ to: "/w/$ws/automations", ws }}
        />
        <Tile
          label="Runs, last 24h"
          value={
            <span className="flex items-baseline gap-3">
              <span>{formatInt(r.succeeded)}</span>
              <span className={cx("text-base", r.failed > 0 ? "text-[var(--st-red-fg)]" : "text-muted")}>
                {formatInt(r.failed)} failed
              </span>
            </span>
          }
          detail={
            <>
              succeeded
              {r.cancelled > 0 && <> · {formatInt(r.cancelled)} cancelled</>}
              {r.withAnomalies > 0 && (
                <span className="text-[var(--st-amber-fg)]"> · {formatInt(r.withAnomalies)} with anomalies</span>
              )}
            </>
          }
          href={{ to: "/w/$ws/runs", ws }}
        />
        <Tile
          label="Running now"
          value={formatInt(data.running)}
          detail={
            <>
              {formatInt(data.queued)} queued
              {data.waitingApprovals > 0 && (
                <span className="text-[var(--st-orange-fg)]"> · {formatInt(data.waitingApprovals)} waiting</span>
              )}
            </>
          }
          dot={data.running > 0 ? "blue" : undefined}
          href={{ to: "/w/$ws/runs", ws }}
        />
        <Tile
          label={`Cost in ${monthName}`}
          value={formatEur(data.costMonthEur)}
          detail="All automations, this month"
          href={{ to: "/w/$ws/costs", ws }}
        />
      </div>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader title="Runs per day" description="Last 14 days" />
          <CardBody>
            <RunsChart data={data.daily} />
          </CardBody>
        </Card>
        <Card>
          <CardHeader title="Cost per day" description="Last 14 days" />
          <CardBody>
            <CostChart data={data.daily} />
          </CardBody>
        </Card>
      </div>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader
            title="Recent failures"
            actions={
              <Link
                to="/w/$ws/runs"
                params={{ ws }}
                search={{ status: "failed" }}
                className="text-[13px] text-accent hover:underline"
              >
                All failed runs
              </Link>
            }
          />
          {data.recentFailures.length === 0 ? (
            <EmptyState
              icon={<IconCheckCircle size={22} />}
              title="No failed runs"
              hint="Failed runs show up here with their error."
            />
          ) : (
            <ul className="divide-y divide-line">
              {data.recentFailures.map((run) => (
                <li key={run.id}>
                  <Link
                    to="/w/$ws/runs/$id"
                    params={{ ws, id: run.id }}
                    className="flex items-start gap-3 px-4 py-2.5 hover:bg-surface-2"
                  >
                    <StatusIcon status={run.status} className="mt-0.5" />
                    <div className="min-w-0 flex-1">
                      <div className="flex items-baseline justify-between gap-3">
                        <span className="truncate text-[13.5px] font-medium text-fg">{run.automation}</span>
                        <RelativeTime iso={run.finishedAt ?? run.startedAt ?? run.queuedAt} className="shrink-0 text-[12px] text-muted" />
                      </div>
                      <p className="line-clamp-2 text-[12.5px] break-words text-fg-2">{runNote(run) || "No error message"}</p>
                    </div>
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </Card>
        <Card>
          <CardHeader title="Upcoming runs" />
          {data.upcoming.length === 0 ? (
            <EmptyState
              icon={<IconClock size={22} />}
              title="Nothing scheduled"
              hint="Automations with a schedule trigger appear here. Paused or invalid ones do not run."
            />
          ) : (
            <ul className="divide-y divide-line">
              {data.upcoming.map((u, i) => (
                <li key={`${u.automation}-${u.at}-${i}`}>
                  <Link
                    to="/w/$ws/automations/$name"
                    params={{ ws, name: u.automation }}
                    className="flex items-center justify-between gap-3 px-4 py-2.5 hover:bg-surface-2"
                  >
                    <span className="truncate text-[13.5px] font-medium text-fg">{u.automation}</span>
                    <span className="flex shrink-0 items-baseline gap-2 text-[12.5px]">
                      <RelativeTime iso={u.at} className="font-medium text-fg-2" />
                      <span className="tnum hidden text-muted sm:inline">{formatAbsolute(u.at)}</span>
                    </span>
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </Card>
      </div>
    </div>
  );
}

type TileHref = { to: "/w/$ws/automations" | "/w/$ws/runs" | "/w/$ws/costs"; ws: string };

function Tile({
  label,
  value,
  detail,
  dot,
  href,
  className,
}: {
  label: string;
  value: ReactNode;
  detail?: ReactNode;
  dot?: Tone;
  href?: TileHref;
  className?: string;
}) {
  return (
    <div className={cx("relative rounded-lg border border-line bg-surface p-4", className)}>
      <p className="flex items-center gap-1.5 text-[12.5px] text-muted">
        {dot && <span aria-hidden="true" className={cx("h-2 w-2 rounded-full", dotColor[dot])} />}
        {href ? (
          <Link to={href.to} params={{ ws: href.ws }} className="hover:text-fg hover:underline">
            {label}
          </Link>
        ) : (
          label
        )}
      </p>
      <div className="mt-1.5 text-2xl font-semibold tracking-tight text-fg">{value}</div>
      {detail && <p className="mt-1 text-[12px] text-fg-2">{detail}</p>}
    </div>
  );
}

function HealthTile({ health }: { health: Health }) {
  const runnerOk = health.runner === "local" || health.runner === "ok";
  const ok = health.ok && runnerOk;
  return (
    <Tile
      label="Health"
      dot={ok ? "green" : "red"}
      value={
        <span className={ok ? "text-fg" : "text-[var(--st-red-fg)]"}>{health.ok ? "Healthy" : "Unhealthy"}</span>
      }
      detail={
        <>
          {!health.ok && health.reason && <span className="block text-[var(--st-red-fg)]">{health.reason}</span>}
          <span className={runnerOk ? "" : "text-[var(--st-red-fg)]"}>
            Runner: {health.runner === "local" ? "local" : health.runner === "ok" ? "connected" : health.runner}
          </span>
          {" · "}
          <span title={formatAbsolute(health.lastTick)}>
            tick <RelativeTime iso={health.lastTick} />
          </span>
        </>
      }
      className="col-span-2 lg:col-span-1"
    />
  );
}
