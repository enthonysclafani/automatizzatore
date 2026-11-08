import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { useState, type ReactNode } from "react";
import type { Approval, EffectiveRole, LLMCall, RunDetail, Step } from "../api/types";
import {
  ApprovalDetail,
  ApprovalStatusBadge,
  ApprovalTimes,
  DecideActions,
  DecisionNote,
} from "../components/domain/Approvals";
import { triggerTypeLabel } from "../components/domain/Triggers";
import { dotColor, Tag } from "../components/ui/Badge";
import { Card, CardBody, CardHeader } from "../components/ui/Card";
import { CopyButton } from "../components/ui/Copy";
import { cx } from "../components/ui/cx";
import { IconAlert, IconChevronRight, IconHourglass, IconInfo, IconSparkle } from "../components/ui/icons";
import { JsonDetails } from "../components/ui/JsonDetails";
import { DefList, Page, PageHeader } from "../components/ui/Page";
import { EmptyState, ErrorState, Skeleton, SkeletonRows } from "../components/ui/States";
import { StatusBadge, StatusIcon, statusLabel, statusTone } from "../components/ui/StatusBadge";
import { RelativeTime } from "../components/ui/Time";
import {
  durationBetween,
  formatAbsolute,
  formatDuration,
  formatEur,
  formatInt,
  isEmptyValue,
  parseTime,
  pluralize,
  prettyJson,
} from "../lib/format";
import { useNow } from "../lib/now";
import { isActive, runQuery } from "../lib/queries";
import { useWs, useWsRole } from "./WorkspaceGate";

export function RunDetailPage() {
  const ws = useWs();
  const { id = "" } = useParams({ strict: false }) as { id?: string };
  const q = useQuery(runQuery(ws, id));

  return (
    <Page wide>
      {q.isPending ? (
        <div aria-busy="true" aria-label="Loading run">
          <Skeleton className="mb-2 h-3 w-16" />
          <Skeleton className="mb-6 h-7 w-72" />
          <Card>
            <SkeletonRows rows={8} />
          </Card>
        </div>
      ) : q.isError ? (
        <>
          <PageHeader title="Run" breadcrumb={<Crumb ws={ws} />} />
          <Card>
            <ErrorState error={q.error} onRetry={() => void q.refetch()} />
          </Card>
        </>
      ) : (
        <RunView ws={ws} detail={q.data} refreshing={q.isFetching} />
      )}
    </Page>
  );
}

/** The run is paused: show what it waits for, with the decision buttons inline. */
function WaitingBanner({ ws, approvals, role }: { ws: string; approvals: Approval[]; role: EffectiveRole | undefined }) {
  const pending = approvals.filter((a) => a.status === "pending");
  return (
    <section
      aria-labelledby="waiting-title"
      className="rounded-lg border-2 border-[var(--st-orange-dot)]/60 bg-[var(--st-orange-bg)] px-4 py-3"
    >
      <h2 id="waiting-title" className="flex items-center gap-2 text-sm font-semibold text-[var(--st-orange-fg)]">
        <IconHourglass size={16} className="shrink-0" />
        Waiting for approval
      </h2>
      {pending.length === 0 ? (
        <p className="mt-1 text-[13px] text-fg">
          The run is paused until someone decides.{" "}
          <Link to="/w/$ws/approvals" params={{ ws }} className="font-medium text-accent hover:underline">
            Open Approvals
          </Link>
        </p>
      ) : (
        <ul className="mt-2 space-y-3">
          {pending.map((a) => (
            <li key={a.id} className="space-y-2 rounded-md border border-line bg-surface p-3">
              <p className="text-[12.5px] text-fg-2">
                Step <span className="font-mono text-fg">{a.stepPath}</span>
              </p>
              <p className="text-sm break-words whitespace-pre-wrap text-fg">{a.message}</p>
              <ApprovalTimes approval={a} />
              <ApprovalDetail detail={a.detail} />
              <DecideActions ws={ws} approval={a} role={role} />
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function Crumb({ ws }: { ws: string }) {
  return (
    <Link to="/w/$ws/runs" params={{ ws }} className="hover:text-fg hover:underline">
      Runs
    </Link>
  );
}

function RunView({ ws, detail, refreshing }: { ws: string; detail: RunDetail; refreshing: boolean }) {
  const { run, llmCalls } = detail;
  const approvals = detail.approvals ?? [];
  const role = useWsRole();
  const now = useNow();
  const active = isActive(run.status);
  const duration = run.startedAt ? durationBetween(run.startedAt, run.finishedAt, now) : undefined;
  const steps = [...detail.steps].sort((a, b) => a.seq - b.seq);
  const dry = run.triggerType === "dry_run";

  return (
    <>
      <PageHeader
        breadcrumb={<Crumb ws={ws} />}
        title={
          <span className="flex flex-wrap items-center gap-2">
            {dry ? (
              // A dry run may test a draft that is not an automation yet.
              <span>{run.automation || "Untitled draft"}</span>
            ) : (
              <Link
                to="/w/$ws/automations/$name"
                params={{ ws, name: run.automation }}
                className="hover:underline"
              >
                {run.automation}
              </Link>
            )}
            <StatusBadge status={run.status} />
            {dry && <Tag>Dry run</Tag>}
          </span>
        }
        description={
          <span className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[13px]">
            <span className="font-mono text-muted">{run.id}</span>
            {active && (
              <span className="text-muted" aria-live="polite">
                · refreshing every 5s{refreshing ? "…" : ""}
              </span>
            )}
          </span>
        }
      />

      <div className="space-y-4">
        {dry && (
          <div role="note" className="flex gap-3 rounded-lg border border-line bg-surface-2 px-4 py-3 text-[13px]">
            <IconInfo size={16} className="mt-0.5 shrink-0 text-muted" />
            <p className="min-w-0 text-fg-2">
              <span className="font-medium text-fg">A dry run of a builder draft.</span> Reads ran for real; writes,
              messages and scripts were only recorded, and approvals were skipped.
            </p>
          </div>
        )}

        {run.status === "waiting" && <WaitingBanner ws={ws} approvals={approvals} role={role} />}

        {run.error && (
          <div role="alert" className="flex gap-3 rounded-lg border border-[var(--st-red-dot)]/40 bg-[var(--st-red-bg)] px-4 py-3">
            <IconAlert size={16} className="mt-0.5 shrink-0 text-[var(--st-red-dot)]" />
            <div className="min-w-0">
              <p className="text-sm font-medium text-[var(--st-red-fg)]">The run failed</p>
              <p className="mt-0.5 text-[13px] break-words whitespace-pre-wrap text-fg">{run.error}</p>
            </div>
          </div>
        )}

        <Card>
          <CardBody>
            <DefList
              className="lg:grid-cols-4"
              items={[
                {
                  label: "Trigger",
                  value: (
                    <span>
                      {triggerTypeLabel[run.triggerType] ?? run.triggerType}
                      {run.triggerKey && <span className="ml-1 font-mono text-[12px] text-muted">{run.triggerKey}</span>}
                    </span>
                  ),
                },
                {
                  label: "Started",
                  value: run.startedAt ? (
                    <span>
                      <RelativeTime iso={run.startedAt} />
                      <span className="tnum block text-[12px] text-muted">{formatAbsolute(run.startedAt)}</span>
                    </span>
                  ) : (
                    <span className="text-muted">Not yet, queued <RelativeTime iso={run.queuedAt} /></span>
                  ),
                },
                {
                  label: "Duration",
                  value: (
                    <span className="tnum">
                      {duration === undefined ? "–" : formatDuration(duration)}
                      {run.finishedAt && (
                        <span className="block text-[12px] text-muted">ended {formatAbsolute(run.finishedAt)}</span>
                      )}
                    </span>
                  ),
                },
                { label: "Cost", value: <span className="tnum">{formatEur(run.costEur)}</span> },
                ...(run.scheduledFor
                  ? [{ label: "Scheduled for", value: <span className="tnum">{formatAbsolute(run.scheduledFor)}</span> }]
                  : []),
                { label: "Queued", value: <span className="tnum">{formatAbsolute(run.queuedAt)}</span> },
                {
                  label: "Anomalies",
                  value:
                    run.anomalies > 0 ? (
                      <span className="text-[var(--st-amber-fg)]">
                        {pluralize(run.anomalies, "step")} failed without stopping the run
                      </span>
                    ) : (
                      <span className="text-muted">None</span>
                    ),
                },
                { label: "Worker", value: run.worker ? <span className="font-mono text-[12.5px]">{run.worker}</span> : <span className="text-muted">–</span> },
                {
                  label: "Manifest version",
                  value: (
                    <span className="font-mono text-[12.5px]" title={run.manifestHash}>
                      {run.manifestHash.slice(0, 12) || "–"}
                    </span>
                  ),
                },
              ]}
            />
            {!isEmptyValue(run.triggerData) && (
              <JsonDetails label="Trigger data" value={run.triggerData} className="mt-4" />
            )}
          </CardBody>
        </Card>

        {approvals.length > 0 && (
          <Card>
            <CardHeader
              title="Approvals"
              description={`${approvals.length} requested by this run, oldest first`}
            />
            <ol className="divide-y divide-line">
              {approvals.map((a) => (
                <li key={a.id} className="space-y-1.5 px-4 py-3">
                  <div className="flex flex-wrap items-center gap-2">
                    <ApprovalStatusBadge status={a.status} />
                    <span className="font-mono text-[12.5px] text-fg">{a.stepPath}</span>
                  </div>
                  <p className="text-[13px] break-words whitespace-pre-wrap text-fg">{a.message}</p>
                  <ApprovalTimes approval={a} />
                  <DecisionNote approval={a} />
                </li>
              ))}
            </ol>
          </Card>
        )}

        <Card>
          <CardHeader
            title="Steps"
            description={steps.length > 0 ? `${steps.length} recorded, in order` : undefined}
          />
          {steps.length === 0 ? (
            <EmptyState
              title={run.status === "queued" ? "Waiting for a worker" : "No steps recorded"}
              hint={
                run.status === "queued"
                  ? "Steps appear here as soon as the run starts."
                  : "The run ended before its first step. See the error above."
              }
            />
          ) : (
            <StepTimeline steps={steps} runStart={run.startedAt} runEnd={run.finishedAt} now={now} />
          )}
        </Card>

        <Card>
          <CardHeader
            title="Model calls"
            description={
              llmCalls.length > 0
                ? `${llmCalls.length} ${llmCalls.length === 1 ? "call" : "calls"}, ${formatEur(llmCalls.reduce((s, c) => s + c.costEur, 0))}`
                : undefined
            }
          />
          {llmCalls.length === 0 ? (
            <EmptyState
              icon={<IconSparkle size={20} />}
              title="No model calls"
              hint="llm and agent steps record each call here, with tokens, cost and the full prompt."
            />
          ) : (
            <ul className="divide-y divide-line">
              {llmCalls.map((c) => (
                <LlmCallRow key={c.id} call={c} />
              ))}
            </ul>
          )}
        </Card>
      </div>
    </>
  );
}

/** Depth from foreach nesting: "each[2].say" is one level deep. */
function stepDepth(path: string): number {
  return (path.match(/\[\d+\]\./g) ?? []).length;
}

function stepLabel(path: string): { index?: string; name: string } {
  const m = /\[(\d+)\]\.([^.[\]]+)$/.exec(path);
  if (m) return { index: m[1], name: m[2] ?? path };
  return { name: path };
}

function StepTimeline({
  steps,
  runStart,
  runEnd,
  now,
}: {
  steps: Step[];
  runStart?: string;
  runEnd?: string;
  now: number;
}) {
  const firstStart = Math.min(...steps.map((s) => parseTime(s.startedAt) ?? Infinity));
  const start = parseTime(runStart) ?? (Number.isFinite(firstStart) ? firstStart : now);
  const lastEnd = Math.max(...steps.map((s) => parseTime(s.finishedAt) ?? now));
  const end = parseTime(runEnd) ?? Math.max(now, lastEnd);
  const span = Math.max(1, end - start);

  return (
    <ol className="divide-y divide-line">
      {steps.map((s) => (
        <StepRow key={`${s.seq}-${s.path}`} step={s} start={start} span={span} now={now} />
      ))}
    </ol>
  );
}

function StepRow({ step, start, span, now }: { step: Step; start: number; span: number; now: number }) {
  const [open, setOpen] = useState(step.status === "failed");
  const depth = stepDepth(step.path);
  const label = stepLabel(step.path);
  const s = parseTime(step.startedAt) ?? start;
  const e = parseTime(step.finishedAt) ?? now;
  const dur = Math.max(0, e - s);
  const left = Math.min(100, Math.max(0, ((s - start) / span) * 100));
  const width = Math.min(100 - left, Math.max(0.5, (dur / span) * 100));
  // A skipped step never ran: no bar, no duration.
  const showBar = step.status !== "skipped";
  const durationText = step.status === "skipped" ? "–" : formatDuration(dur);
  const attempts =
    step.attempts > 1 ? (
      <span className="text-[var(--st-amber-fg)]" title="Attempts, including retries">
        {step.attempts} attempts
      </span>
    ) : null;
  const hasDetails = Boolean(step.error || step.logs || !isEmptyValue(step.input) || !isEmptyValue(step.output));
  const panelId = `step-${step.seq}`;

  return (
    <li>
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        aria-controls={panelId}
        disabled={!hasDetails}
        className="block w-full px-4 py-2 text-left hover:bg-surface-2 disabled:cursor-default disabled:hover:bg-transparent md:grid md:grid-cols-[minmax(0,20rem)_minmax(0,1fr)_14rem] md:items-center md:gap-x-3"
      >
        <span className="flex min-w-0 items-center gap-2" style={{ paddingLeft: `${depth * 1.25}rem` }}>
          <IconChevronRight
            size={14}
            className={cx("shrink-0 text-muted transition-transform", open && "rotate-90", !hasDetails && "invisible")}
          />
          <StatusIcon status={step.status} />
          <span className="sr-only">{statusLabel(step.status)}</span>
          <span className="min-w-0 truncate font-mono text-[12.5px] text-fg" title={step.path}>
            {label.index !== undefined && <span className="text-muted">[{label.index}] </span>}
            {label.name}
          </span>
          <Tag className="shrink-0">{step.type}</Tag>
          <span className="tnum ml-auto shrink-0 pl-2 text-[12px] text-fg-2 md:hidden">{durationText}</span>
        </span>

        <span className="mt-1.5 block md:mt-0" aria-hidden="true" style={{ paddingLeft: `${depth * 1.25}rem` }}>
          <span className="relative block h-2 rounded-full bg-surface-3">
            {showBar && (
              <span
                className={cx("absolute inset-y-0 rounded-full", dotColor[statusTone[step.status]])}
                style={{ left: `${left}%`, width: `${width}%`, minWidth: 3 }}
              />
            )}
          </span>
        </span>

        {/* Narrow screens: attempts and cost on their own line. */}
        {(step.attempts > 1 || step.costEur > 0) && (
          <span className="tnum mt-1 flex justify-end gap-3 text-[12px] text-fg-2 md:hidden">
            {attempts}
            {step.costEur > 0 && <span>{formatEur(step.costEur)}</span>}
          </span>
        )}

        <span className="tnum hidden items-center justify-end gap-3 text-[12px] whitespace-nowrap text-fg-2 md:flex">
          {attempts}
          <span className="w-16 text-right">{durationText}</span>
          <span className="w-20 text-right">{step.costEur > 0 ? formatEur(step.costEur) : <span className="text-muted">–</span>}</span>
        </span>
      </button>

      {open && hasDetails && (
        <div id={panelId} className="space-y-3 border-t border-line bg-surface-2/50 px-4 py-3" style={{ paddingLeft: `${1 + depth * 1.25 + 1.5}rem` }}>
          <p className="tnum text-[12px] text-muted">
            {step.stepId !== step.path && <>Step <code className="font-mono">{step.stepId}</code> · </>}
            started {formatAbsolute(step.startedAt)}
            {step.finishedAt && <> · ended {formatAbsolute(step.finishedAt)}</>}
          </p>
          {step.error && (
            <div className="rounded-md border border-[var(--st-red-dot)]/40 bg-[var(--st-red-bg)] px-3 py-2">
              <p className="text-[12px] font-medium text-[var(--st-red-fg)]">Error</p>
              <p className="mt-0.5 font-mono text-[12.5px] break-words whitespace-pre-wrap text-fg">{step.error}</p>
            </div>
          )}
          {step.logs && (
            <Section title="Logs" copy={step.logs}>
              <pre
                tabIndex={0}
                aria-label="Step logs"
                className="max-h-80 overflow-auto rounded-md border border-line bg-surface p-3 font-mono text-[12px] leading-relaxed whitespace-pre text-fg"
              >
                {step.logs}
              </pre>
            </Section>
          )}
          {!isEmptyValue(step.input) && <JsonDetails label="Input" value={step.input} />}
          {!isEmptyValue(step.output) && <JsonDetails label="Output" value={step.output} defaultOpen={step.status === "succeeded"} />}
        </div>
      )}
    </li>
  );
}

function Section({ title, copy, children }: { title: string; copy?: string; children: ReactNode }) {
  return (
    <div>
      <div className="mb-1 flex items-center justify-between">
        <p className="text-[12px] font-medium text-fg-2">{title}</p>
        {copy && <CopyButton text={copy} />}
      </div>
      {children}
    </div>
  );
}

function str(obj: unknown, key: string): string | undefined {
  if (obj && typeof obj === "object" && key in obj) {
    const v = (obj as Record<string, unknown>)[key];
    if (typeof v === "string") return v;
    if (v !== undefined && v !== null) return prettyJson(v);
  }
  return undefined;
}

function LlmCallRow({ call }: { call: LLMCall }) {
  const [open, setOpen] = useState(false);
  const system = str(call.request, "system");
  const user = str(call.request, "user") ?? str(call.request, "prompt") ?? str(call.request, "messages");
  const panelId = `llm-${call.id}`;
  return (
    <li>
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        aria-controls={panelId}
        className="flex w-full flex-col gap-1 px-4 py-2.5 text-left hover:bg-surface-2 sm:flex-row sm:items-center sm:gap-4"
      >
        <span className="flex w-full min-w-0 items-center gap-2 sm:w-auto sm:flex-1">
          <IconChevronRight size={14} className={cx("shrink-0 text-muted transition-transform", open && "rotate-90")} />
          {call.error && <IconAlert size={14} className="shrink-0 text-[var(--st-red-dot)]" aria-label="Failed" />}
          <span className="min-w-0">
            <span className="block truncate text-[13px] font-medium text-fg">{call.model}</span>
            <span className="block truncate text-[12px] text-muted">
              {call.provider} · step <span className="font-mono">{call.stepPath}</span>
            </span>
          </span>
        </span>
        <span className="tnum flex flex-wrap items-center gap-x-4 gap-y-1 pl-6 text-[12px] text-fg-2 sm:shrink-0 sm:pl-0">
          <span title="Input tokens / output tokens">
            {formatInt(call.inputTokens)} in / {formatInt(call.outputTokens)} out
          </span>
          <span>{formatDuration(call.latencyMs)}</span>
          <span className="w-24 text-right">
            {call.priceKnown ? (
              formatEur(call.costEur)
            ) : (
              <span className="text-[var(--st-amber-fg)]" title="Add a price for this model in the settings to track its cost">
                price not configured
              </span>
            )}
          </span>
        </span>
      </button>
      {open && (
        <div id={panelId} className="space-y-3 border-t border-line bg-surface-2/50 px-4 py-3 sm:pl-10">
          <p className="tnum text-[12px] text-muted">{formatAbsolute(call.createdAt)}</p>
          {call.error && (
            <div className="rounded-md border border-[var(--st-red-dot)]/40 bg-[var(--st-red-bg)] px-3 py-2">
              <p className="text-[12px] font-medium text-[var(--st-red-fg)]">Error</p>
              <p className="mt-0.5 font-mono text-[12.5px] break-words whitespace-pre-wrap text-fg">{call.error}</p>
            </div>
          )}
          {system && <Prompt title="System prompt" text={system} />}
          {user && <Prompt title="User prompt" text={user} />}
          <Prompt title="Response" text={call.response || "(empty)"} />
          <JsonDetails label="Full request" value={call.request} />
        </div>
      )}
    </li>
  );
}

function Prompt({ title, text }: { title: string; text: string }) {
  return (
    <Section title={title} copy={text}>
      <pre
        tabIndex={0}
        aria-label={title}
        className="max-h-80 overflow-auto rounded-md border border-line bg-surface p-3 font-mono text-[12px] leading-relaxed break-words whitespace-pre-wrap text-fg"
      >
        {text}
      </pre>
    </Section>
  );
}
