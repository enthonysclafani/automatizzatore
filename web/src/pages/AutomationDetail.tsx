import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { useState } from "react";
import { api } from "../api/client";
import type { AutomationDetail, Problem } from "../api/types";
import { RunsTable } from "../components/domain/RunsTable";
import { TriggerList } from "../components/domain/Triggers";
import { Tag } from "../components/ui/Badge";
import { Button } from "../components/ui/Button";
import { Card, CardBody, CardHeader } from "../components/ui/Card";
import { CodeBlock } from "../components/ui/Copy";
import { ConfirmDialog } from "../components/ui/Dialog";
import { IconAlert, IconInfo, IconList, IconPause, IconPlay } from "../components/ui/icons";
import { DefList, Page, PageHeader } from "../components/ui/Page";
import { EmptyState, ErrorState, Skeleton, SkeletonRows } from "../components/ui/States";
import { RelativeTime } from "../components/ui/Time";
import { useToast } from "../components/ui/Toast";
import { formatAbsolute, formatEur } from "../lib/format";
import { automationQuery, keys } from "../lib/queries";
import { atLeast } from "../lib/roles";
import { StateBadge } from "./Automations";
import { useWs, useWsRole } from "./WorkspaceGate";

const AUTONOMY_HINT: Record<AutomationDetail["autonomy"], string> = {
  observe: "Reads only; never writes or sends.",
  draft: "Writes and sends only as drafts.",
  ask: "Asks for approval before sending or destructive actions.",
  auto: "Acts on its own; asks only before destructive actions.",
};

export function AutomationDetailPage() {
  const ws = useWs();
  const { name = "" } = useParams({ strict: false }) as { name?: string };
  const role = useWsRole();
  const q = useQuery(automationQuery(ws, name));

  return (
    <Page wide>
      {q.isPending ? (
        <div aria-busy="true" aria-label="Loading automation">
          <Skeleton className="mb-2 h-3 w-24" />
          <Skeleton className="mb-6 h-7 w-64" />
          <Card>
            <SkeletonRows rows={6} />
          </Card>
        </div>
      ) : q.isError ? (
        <>
          <PageHeader title={name} breadcrumb={<Crumb ws={ws} />} />
          <Card>
            <ErrorState error={q.error} onRetry={() => void q.refetch()} />
          </Card>
        </>
      ) : (
        <Detail ws={ws} a={q.data} canBuild={atLeast(role, "builder")} />
      )}
    </Page>
  );
}

function Crumb({ ws }: { ws: string }) {
  return (
    <Link to="/w/$ws/automations" params={{ ws }} className="hover:text-fg hover:underline">
      Automations
    </Link>
  );
}

type Action = "trigger" | "pause" | "resume";

function Detail({ ws, a, canBuild }: { ws: string; a: AutomationDetail; canBuild: boolean }) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const [confirm, setConfirm] = useState<Action | null>(null);

  const refresh = () => queryClient.invalidateQueries({ queryKey: keys.ws(ws) });

  const run = async (action: Action) => {
    if (action === "trigger") {
      const { runId } = await api.triggerAutomation(ws, a.name);
      toast({
        tone: "success",
        title: "Run queued",
        description: `${a.name} will start as soon as a worker is free.`,
        link: { to: `/w/${encodeURIComponent(ws)}/runs/${encodeURIComponent(runId)}`, label: "Open the run" },
      });
    } else if (action === "pause") {
      await api.pauseAutomation(ws, a.name);
      toast({ tone: "success", title: "Automation paused", description: "Schedules stop until you resume it." });
    } else {
      await api.resumeAutomation(ws, a.name);
      toast({ tone: "success", title: "Automation resumed", description: "Its schedules are active again." });
    }
    await refresh();
  };

  const errors = a.problems.filter((p) => p.severity === "error");
  const warnings = a.problems.filter((p) => p.severity === "warning");

  return (
    <>
      <PageHeader
        breadcrumb={<Crumb ws={ws} />}
        title={
          <span className="flex flex-wrap items-center gap-2">
            {a.name}
            <StateBadge automation={a} />
          </span>
        }
        description={a.description || <span className="text-muted">No description</span>}
        actions={
          canBuild && (
            <>
              <Button
                variant="primary"
                icon={<IconPlay size={14} />}
                disabled={!a.valid}
                title={a.valid ? undefined : "Fix the problems below before running it"}
                onClick={() => setConfirm("trigger")}
              >
                Run now
              </Button>
              {a.enabled ? (
                <Button icon={<IconPause size={14} />} onClick={() => setConfirm("pause")}>
                  Pause
                </Button>
              ) : (
                <Button icon={<IconPlay size={14} />} onClick={() => setConfirm("resume")}>
                  Resume
                </Button>
              )}
            </>
          )
        }
      />

      <ConfirmDialog
        open={confirm === "trigger"}
        onClose={() => setConfirm(null)}
        title={`Run ${a.name} now?`}
        message="Pendolo queues one run right away, outside its schedule. It can call models and external services, and it counts toward costs."
        confirmLabel="Run now"
        onConfirm={() => run("trigger")}
      />
      <ConfirmDialog
        open={confirm === "pause"}
        onClose={() => setConfirm(null)}
        title={`Pause ${a.name}?`}
        message="Scheduled and webhook runs stop until you resume it. Runs already in progress finish."
        confirmLabel="Pause"
        onConfirm={() => run("pause")}
      />
      <ConfirmDialog
        open={confirm === "resume"}
        onClose={() => setConfirm(null)}
        title={`Resume ${a.name}?`}
        message="Its schedules and webhook become active again. Missed runs follow the misfire setting."
        confirmLabel="Resume"
        onConfirm={() => run("resume")}
      />

      <div className="space-y-4">
        {a.problems.length > 0 && (
          <ProblemsCard errors={errors} warnings={warnings} path={a.path} />
        )}

        <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
          <Card className="lg:col-span-2">
            <CardHeader title="Settings" />
            <CardBody>
              <DefList
                items={[
                  {
                    label: "Triggers",
                    value: <TriggerList triggers={a.triggers} detailed />,
                    wide: true,
                  },
                  {
                    label: "Next run",
                    value: a.nextRunAt ? (
                      <span>
                        <RelativeTime iso={a.nextRunAt} />{" "}
                        <span className="tnum text-muted">· {formatAbsolute(a.nextRunAt)}</span>
                      </span>
                    ) : (
                      <span className="text-muted">Not scheduled</span>
                    ),
                  },
                  {
                    label: "Autonomy",
                    value: (
                      <span>
                        <span className="font-medium">{a.autonomy}</span>
                        <span className="block text-[12px] text-muted">{AUTONOMY_HINT[a.autonomy]}</span>
                      </span>
                    ),
                  },
                  {
                    label: "Budget per run",
                    value:
                      a.budget?.maxCostEur !== undefined ? (
                        <span className="tnum">{formatEur(a.budget.maxCostEur)}</span>
                      ) : (
                        <span className="text-muted">No limit</span>
                      ),
                  },
                  {
                    label: "Timeout",
                    value: a.budget?.timeout ? (
                      <span className="font-mono text-[13px]">{a.budget.timeout}</span>
                    ) : (
                      <span className="text-muted">Default</span>
                    ),
                  },
                  {
                    label: "Folder",
                    value: <code className="font-mono text-[12.5px] break-all">{a.path}</code>,
                  },
                ]}
              />
            </CardBody>
          </Card>

          <Card>
            <CardHeader title="Steps" description={`${a.steps.length} in order`} />
            {a.steps.length === 0 ? (
              <EmptyState title="No steps" hint="Add steps to automation.yaml." />
            ) : (
              <ol className="divide-y divide-line">
                {a.steps.map((s, i) => (
                  <li key={s.id} className="flex items-center gap-3 px-4 py-2">
                    <span className="tnum w-5 shrink-0 text-right text-[12px] text-muted">{i + 1}</span>
                    <div className="min-w-0 flex-1">
                      <p className="truncate font-mono text-[12.5px] text-fg">{s.id}</p>
                      {s.name && <p className="truncate text-[12px] text-muted">{s.name}</p>}
                    </div>
                    <Tag>{s.type}</Tag>
                  </li>
                ))}
              </ol>
            )}
          </Card>
        </div>

        <Card>
          <CardHeader
            title="Recent runs"
            actions={
              <Link
                to="/w/$ws/runs"
                params={{ ws }}
                search={{ automation: a.name }}
                className="inline-flex items-center gap-1 text-[13px] text-accent hover:underline"
              >
                <IconList size={14} />
                All runs
              </Link>
            }
          />
          {a.recentRuns.length === 0 ? (
            <EmptyState
              title="No runs yet"
              hint={canBuild ? "Use Run now to try it, or wait for its trigger." : "Runs appear here once its trigger fires."}
            />
          ) : (
            <RunsTable ws={ws} runs={a.recentRuns} showAutomation={false} />
          )}
        </Card>

        <Card>
          <CardHeader
            title="Manifest"
            description={
              <>
                <code className="font-mono">automation.yaml</code>, read-only. Edit the file to change it.
              </>
            }
          />
          <CardBody>
            <CodeBlock code={a.manifest} label="automation.yaml" />
          </CardBody>
        </Card>
      </div>
    </>
  );
}

export function ProblemLocation({ p }: { p: Problem }) {
  const loc = p.line ? `${p.line}${p.column ? `:${p.column}` : ""}` : "";
  if (!loc && !p.path) return null;
  return (
    <span className="tnum font-mono text-[12px] text-muted">
      {loc}
      {loc && p.path ? " " : ""}
      {p.path}
    </span>
  );
}

export function ProblemList({ problems }: { problems: Problem[] }) {
  return (
    <ul className="space-y-1.5">
      {problems.map((p, i) => (
        <li key={i} className="flex items-start gap-2 text-[13px]">
          {p.severity === "error" ? (
            <IconAlert size={14} className="mt-0.5 shrink-0 text-[var(--st-red-dot)]" />
          ) : (
            <IconInfo size={14} className="mt-0.5 shrink-0 text-[var(--st-amber-dot)]" />
          )}
          <span className="sr-only">{p.severity}: </span>
          <span className="min-w-0">
            <ProblemLocation p={p} /> <span className="break-words text-fg">{p.message}</span>
          </span>
        </li>
      ))}
    </ul>
  );
}

function ProblemsCard({ errors, warnings, path }: { errors: Problem[]; warnings: Problem[]; path: string }) {
  return (
    <Card className={errors.length > 0 ? "border-[var(--st-red-dot)]/50" : ""}>
      <CardHeader
        title={errors.length > 0 ? "This automation does not run" : "Warnings"}
        description={
          errors.length > 0 ? (
            <>
              Fix these problems in <code className="font-mono">{path}</code>. Check with{" "}
              <code className="font-mono">pendolo validate</code>.
            </>
          ) : (
            "It runs, but these may cause trouble. pendolo validate --strict treats them as errors."
          )
        }
      />
      <CardBody>
        <ProblemList problems={[...errors, ...warnings]} />
      </CardBody>
    </Card>
  );
}
