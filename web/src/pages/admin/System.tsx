import { useQuery } from "@tanstack/react-query";
import { api } from "../../api/client";
import type { SystemInfo } from "../../api/types";
import { Badge } from "../../components/ui/Badge";
import { Card, CardBody, CardHeader } from "../../components/ui/Card";
import { IconCheckCircle } from "../../components/ui/icons";
import { DefList, Page, PageHeader } from "../../components/ui/Page";
import { EmptyState, ErrorState, SkeletonRows } from "../../components/ui/States";
import { RelativeTime } from "../../components/ui/Time";
import { formatAbsolute, formatInt } from "../../lib/format";
import { keys, POLL_IDLE_MS, useMe } from "../../lib/queries";
import { ProblemList } from "../AutomationDetail";
import { AuditTable } from "../Audit";

export function SystemPage() {
  const { data: me } = useMe();
  const owner = Boolean(me?.isOwner);
  const q = useQuery({ queryKey: keys.system, queryFn: api.system, enabled: owner, refetchInterval: POLL_IDLE_MS });
  const audit = useQuery({
    queryKey: ["audit", "instance"],
    queryFn: () => api.instanceAudit(100),
    enabled: owner,
  });

  if (!owner) {
    return (
      <Page>
        <PageHeader title="System" />
        <Card>
          <EmptyState title="Owners only" hint="Ask an instance owner for system details." />
        </Card>
      </Page>
    );
  }

  return (
    <Page wide>
      <PageHeader title="System" description="The Pendolo server this dashboard is connected to." />
      {q.isPending ? (
        <Card>
          <SkeletonRows rows={6} />
        </Card>
      ) : q.isError ? (
        <Card>
          <ErrorState error={q.error} onRetry={() => void q.refetch()} />
        </Card>
      ) : (
        <SystemBody info={q.data} />
      )}

      <Card className="mt-4">
        <CardHeader title="Instance audit log" description="Latest 100 entries across all workspaces and sign-ins." />
        {audit.isPending ? (
          <SkeletonRows rows={5} />
        ) : audit.isError ? (
          <ErrorState error={audit.error} onRetry={() => void audit.refetch()} />
        ) : (
          <AuditTable entries={audit.data.entries} showWorkspace />
        )}
      </Card>
    </Page>
  );
}

function SystemBody({ info }: { info: SystemInfo }) {
  const h = info.health;
  const runnerOk = h.runner === "local" || h.runner === "ok";
  return (
    <div className="space-y-4">
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader
            title="Health"
            actions={h.ok ? <Badge tone="green">Healthy</Badge> : <Badge tone="red">Unhealthy</Badge>}
          />
          <CardBody>
            {!h.ok && h.reason && (
              <p role="alert" className="mb-4 rounded-md bg-[var(--st-red-bg)] px-3 py-2 text-[13px] text-[var(--st-red-fg)]">
                {h.reason}
              </p>
            )}
            <DefList
              className="lg:grid-cols-2"
              items={[
                {
                  label: "Runner",
                  value: (
                    <span className={runnerOk ? "" : "text-[var(--st-red-fg)]"}>
                      {h.runner === "local" ? "Local (same process)" : h.runner === "ok" ? "Separate runner, connected" : h.runner}
                    </span>
                  ),
                },
                { label: "Scheduler leader", value: h.leader ? "Yes" : "No (standby)" },
                {
                  label: "Last scheduler tick",
                  value: (
                    <span title={formatAbsolute(h.lastTick)}>
                      <RelativeTime iso={h.lastTick} />
                    </span>
                  ),
                },
                {
                  label: "Up since",
                  value: (
                    <span>
                      <RelativeTime iso={h.startedAt} />
                      <span className="tnum block text-[12px] text-muted">{formatAbsolute(h.startedAt)}</span>
                    </span>
                  ),
                },
                { label: "Running / queued", value: <span className="tnum">{formatInt(h.running)} / {formatInt(h.queued)}</span> },
                {
                  label: "Automations",
                  value: (
                    <span className="tnum">
                      {formatInt(h.automations)}
                      {h.invalid > 0 && <span className="text-[var(--st-red-fg)]"> · {formatInt(h.invalid)} invalid</span>}
                    </span>
                  ),
                },
              ]}
            />
          </CardBody>
        </Card>

        <Card>
          <CardHeader title="Server" />
          <CardBody>
            <DefList
              className="lg:grid-cols-2"
              items={[
                { label: "Version", value: <span className="font-mono text-[13px]">{info.version || h.version}</span> },
                { label: "Workers", value: <span className="tnum">{formatInt(info.workers)}</span> },
                { label: "Data folder", value: <code className="font-mono text-[12.5px] break-all">{info.dataDir}</code> },
                {
                  label: "Automations folder",
                  value: <code className="font-mono text-[12.5px] break-all">{info.automationsDir}</code>,
                },
                {
                  label: "Single sign-on (OIDC)",
                  value: info.oidc.enabled ? (
                    <span>
                      On
                      {info.oidc.issuer && <code className="block font-mono text-[12px] break-all text-muted">{info.oidc.issuer}</code>}
                    </span>
                  ) : (
                    <span className="text-muted">Off</span>
                  ),
                },
              ]}
            />
          </CardBody>
        </Card>
      </div>

      <Card>
        <CardHeader
          title="Invalid automations"
          description="These do not run until their manifest is fixed. Check with pendolo validate."
        />
        {info.invalid.length === 0 ? (
          <EmptyState icon={<IconCheckCircle size={22} />} title="All automations are valid" />
        ) : (
          <ul className="divide-y divide-line">
            {info.invalid.map((a) => (
              <li key={a.path} className="px-4 py-3">
                <p className="text-sm font-medium text-fg">{a.name}</p>
                <p className="mb-2 font-mono text-[12px] break-all text-muted">{a.path}</p>
                <ProblemList problems={a.problems} />
              </li>
            ))}
          </ul>
        )}
      </Card>
    </div>
  );
}
