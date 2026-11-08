import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { api, errorMessage, isApiError } from "../../api/client";
import type { CreateDraftInput, Draft, DraftDetail, DraftProblem, DraftStatus } from "../../api/types";
import { collapse, sortPaths, type FileDiff } from "../../lib/diff";
import { formatInt } from "../../lib/format";
import { keys } from "../../lib/queries";
import { Badge, type Tone } from "../ui/Badge";
import { CodeBlock } from "../ui/Copy";
import { cx } from "../ui/cx";
import { IconAlert, IconCheckCircle, IconHourglass, IconInfo, IconPencil, IconSlash } from "../ui/icons";
import { EmptyState } from "../ui/States";
import { useToast } from "../ui/Toast";

const STATUS: Record<DraftStatus, { tone: Tone; label: string }> = {
  open: { tone: "blue", label: "In progress" },
  proposed: { tone: "orange", label: "Proposed" },
  activated: { tone: "green", label: "Activated" },
  discarded: { tone: "gray", label: "Discarded" },
};

export function DraftStatusBadge({ status }: { status: DraftStatus }) {
  const s = STATUS[status];
  const icon =
    status === "open" ? (
      <IconPencil size={12} />
    ) : status === "proposed" ? (
      <IconHourglass size={12} />
    ) : status === "activated" ? (
      <IconCheckCircle size={12} />
    ) : (
      <IconSlash size={12} />
    );
  return (
    <Badge tone={s.tone} icon={icon} title={status === "proposed" ? "Waiting for an admin to activate it" : undefined}>
      {s.label}
    </Badge>
  );
}

/** The automation's name, or "Untitled" until the builder writes one. */
export function draftTitle(d: Pick<Draft, "automation">): string {
  return d.automation || "Untitled";
}

export function isClosed(d: Pick<Draft, "status">): boolean {
  return d.status === "activated" || d.status === "discarded";
}

export function draftErrors(d: Pick<Draft, "problems">): DraftProblem[] {
  return (d.problems ?? []).filter((p) => p.severity === "error");
}

export function draftWarnings(d: Pick<Draft, "problems">): DraftProblem[] {
  return (d.problems ?? []).filter((p) => p.severity === "warning");
}

/** "automation.yaml:12:3" from a problem's file and position. */
export function problemPlace(p: DraftProblem): string {
  const pos = p.line ? `${p.line}${p.column ? `:${p.column}` : ""}` : "";
  if (p.file && pos) return `${p.file}:${pos}`;
  return p.file || (pos ? `line ${pos}` : "");
}

/** Errors first, each with file:line and the manifest path. */
export function DraftProblemList({ problems }: { problems: DraftProblem[] }) {
  const sorted = [...problems].sort((a, b) => (a.severity === b.severity ? 0 : a.severity === "error" ? -1 : 1));
  return (
    <ul className="space-y-2">
      {sorted.map((p, i) => {
        const place = problemPlace(p);
        return (
          <li key={i} className="flex items-start gap-2 text-[13px]">
            {p.severity === "error" ? (
              <IconAlert size={14} className="mt-0.5 shrink-0 text-[var(--st-red-dot)]" />
            ) : (
              <IconInfo size={14} className="mt-0.5 shrink-0 text-[var(--st-amber-dot)]" />
            )}
            <span className="sr-only">{p.severity}: </span>
            <span className="min-w-0">
              <span className="break-words text-fg">{p.message}</span>
              {(place || p.path) && (
                <span className="tnum mt-0.5 block font-mono text-[12px] break-all text-muted">
                  {place}
                  {place && p.path ? " · " : ""}
                  {p.path}
                </span>
              )}
            </span>
          </li>
        );
      })}
    </ul>
  );
}

/** A file list and a read-only viewer of the selected file. */
export function FilesView({
  files,
  problems = [],
  label = "Files",
}: {
  files: Record<string, string>;
  problems?: DraftProblem[];
  label?: string;
}) {
  const paths = sortPaths(Object.keys(files));
  const [picked, setPicked] = useState<string | undefined>(paths[0]);
  const selected = picked !== undefined && picked in files ? picked : paths[0];
  if (paths.length === 0) {
    return <EmptyState title="No files yet" hint="Ask the builder for an automation, or add files with Edit." />;
  }
  const errorsIn = (path: string) => problems.filter((p) => p.severity === "error" && p.file === path).length;
  return (
    <div className="grid min-w-0 grid-cols-1 gap-3 md:grid-cols-[12rem_minmax(0,1fr)]">
      <ul aria-label={label} className="flex min-w-0 flex-wrap gap-1 md:flex-col md:flex-nowrap">
        {paths.map((p) => {
          const n = errorsIn(p);
          return (
            <li key={p} className="min-w-0">
              <button
                type="button"
                aria-pressed={p === selected}
                onClick={() => setPicked(p)}
                className={cx(
                  "flex w-full min-w-0 items-center gap-1.5 rounded-md px-2 py-1 text-left font-mono text-[12.5px]",
                  p === selected ? "bg-surface-2 font-medium text-fg" : "text-fg-2 hover:bg-surface-2 hover:text-fg",
                )}
              >
                <span className="min-w-0 truncate" title={p}>
                  {p}
                </span>
                {n > 0 && (
                  <span className="ml-auto shrink-0 rounded-full bg-[var(--st-red-bg)] px-1.5 text-[11px] leading-4 text-[var(--st-red-fg)]">
                    {n}
                    <span className="sr-only"> {n === 1 ? "error" : "errors"}</span>
                  </span>
                )}
              </button>
            </li>
          );
        })}
      </ul>
      {selected !== undefined && (
        <div className="min-w-0">
          <p className="mb-1.5 font-mono text-[12px] break-all text-muted">
            {selected} · {formatInt(files[selected]?.split("\n").length ?? 0)} lines
          </p>
          <CodeBlock code={files[selected] ?? ""} label={selected} maxHeight="36rem" />
        </div>
      )}
    </div>
  );
}

const FILE_STATUS: Record<FileDiff["status"], { tone: Tone; label: string }> = {
  added: { tone: "green", label: "New file" },
  removed: { tone: "red", label: "Deleted" },
  changed: { tone: "blue", label: "Changed" },
  same: { tone: "gray", label: "Unchanged" },
};

/** Added and removed lines per file, with unchanged stretches folded. */
export function DiffView({ diffs, emptyHint }: { diffs: FileDiff[]; emptyHint?: string }) {
  const changed = diffs.filter((d) => d.status !== "same");
  const same = diffs.length - changed.length;
  if (changed.length === 0) {
    return <EmptyState title="No changes" hint={emptyHint ?? "The files are the same."} />;
  }
  return (
    <div className="space-y-3">
      <p className="text-[12.5px] text-fg-2">
        {changed.length} {changed.length === 1 ? "file" : "files"} changed
        {same > 0 && <span className="text-muted"> · {same} unchanged</span>}
      </p>
      {changed.map((d) => (
        <section key={d.path} aria-label={`Changes in ${d.path}`} className="min-w-0 rounded-md border border-line">
          <header className="flex flex-wrap items-center gap-2 border-b border-line bg-surface-2 px-3 py-1.5">
            <span className="min-w-0 font-mono text-[12.5px] font-medium break-all text-fg">{d.path}</span>
            <Badge tone={FILE_STATUS[d.status].tone}>{FILE_STATUS[d.status].label}</Badge>
            <span className="tnum ml-auto text-[12px]">
              <span className="text-[var(--st-green-fg)]">+{d.added}</span>{" "}
              <span className="text-[var(--st-red-fg)]">−{d.removed}</span>
              <span className="sr-only">
                {" "}
                ({d.added} lines added, {d.removed} removed)
              </span>
            </span>
          </header>
          <DiffLines diff={d} />
        </section>
      ))}
    </div>
  );
}

function DiffLines({ diff }: { diff: FileDiff }) {
  const rows = collapse(diff.lines);
  return (
    <div tabIndex={0} aria-label={`Diff of ${diff.path}`} className="max-h-[36rem] overflow-auto">
      <table className="w-full border-collapse font-mono text-[12px] leading-[1.6]">
        <tbody>
          {rows.map((r, i) => {
            if (r.kind === "gap") {
              return (
                <tr key={i} className="bg-surface-2/60 text-muted">
                  <td colSpan={3} className="px-3 py-0.5 text-[11.5px] italic">
                    {r.count} unchanged {r.count === 1 ? "line" : "lines"}
                  </td>
                </tr>
              );
            }
            const cls =
              r.kind === "add"
                ? "bg-[var(--st-green-bg)]"
                : r.kind === "del"
                  ? "bg-[var(--st-red-bg)]"
                  : "";
            const sign = r.kind === "add" ? "+" : r.kind === "del" ? "−" : " ";
            return (
              <tr key={i} className={cls}>
                <td className="tnum w-10 px-2 text-right align-top text-muted select-none">
                  {r.kind !== "add" ? r.oldNo : ""}
                </td>
                <td className="tnum w-10 px-2 text-right align-top text-muted select-none">
                  {r.kind !== "del" ? r.newNo : ""}
                </td>
                <td className="pr-3 whitespace-pre text-fg">
                  <span
                    aria-hidden="true"
                    className={cx(
                      "inline-block w-4 select-none",
                      r.kind === "add" && "text-[var(--st-green-fg)]",
                      r.kind === "del" && "text-[var(--st-red-fg)]",
                    )}
                  >
                    {sign}
                  </span>
                  {r.kind !== "same" && <span className="sr-only">{r.kind === "add" ? "added: " : "removed: "}</span>}
                  {r.text}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

/** What the builder is doing during a slow request. */
export function WorkingNote({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div
      role="status"
      className={cx(
        "flex items-center gap-2.5 rounded-md border border-[var(--st-blue-dot)]/40 bg-[var(--st-blue-bg)] px-3 py-2 text-[13px] text-fg",
        className,
      )}
    >
      <span aria-hidden="true" className="relative flex h-2.5 w-2.5 shrink-0">
        <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-[var(--st-blue-dot)] opacity-60" />
        <span className="relative inline-flex h-2.5 w-2.5 rounded-full bg-[var(--st-blue-dot)]" />
      </span>
      <span className="min-w-0">{children}</span>
    </div>
  );
}

const GENERIC_FAILURE = "The builder request failed";

/** A toast for a failed builder request, saying what to do next. */
export function builderErrorToast(err: unknown): { title: string; description: string } {
  if (isApiError(err)) {
    switch (err.code) {
      case "busy":
        return {
          title: "The builder is already working on this draft",
          description: "Wait for its answer, then send your message again.",
        };
      case "builder_failed":
        return { title: "The builder could not answer", description: err.error };
      case "builder_off":
        return {
          title: "The AI builder is off",
          description: "No model is configured. Start from a template or edit the files by hand.",
        };
      case "draft_closed":
        return { title: "This draft is closed", description: "It was activated or discarded. Start a new draft to make changes." };
    }
  }
  return { title: GENERIC_FAILURE, description: errorMessage(err) };
}

/**
 * Starts a draft (from a description, a template, an automation or one of its
 * versions) and opens it. A description makes the request slow: the model
 * writes the first version before the server answers.
 */
export function useStartDraft(ws: string) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const toast = useToast();
  const [pending, setPending] = useState<CreateDraftInput | null>(null);
  // If the user leaves the page during a slow request, do not pull them back: offer a link instead.
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  const start = async (input: CreateDraftInput): Promise<DraftDetail | undefined> => {
    setPending(input);
    try {
      const d = await api.createDraft(ws, input);
      queryClient.setQueryData(keys.draft(ws, d.id), d);
      void queryClient.invalidateQueries({ queryKey: keys.drafts(ws) });
      if (mounted.current) {
        void navigate({ to: "/w/$ws/builder/$draftId", params: { ws, draftId: d.id } });
      } else {
        toast({
          tone: "success",
          title: `Draft ready: ${draftTitle(d)}`,
          link: { to: `/w/${encodeURIComponent(ws)}/builder/${encodeURIComponent(d.id)}`, label: "Open the draft" },
        });
      }
      return d;
    } catch (err) {
      const t = builderErrorToast(err);
      if (isApiError(err) && err.code === "builder_failed") {
        // The draft exists even though the model failed: it is in the list.
        t.description += " The draft is saved under In progress; open it to try again.";
        void queryClient.invalidateQueries({ queryKey: keys.drafts(ws) });
      } else if (isApiError(err) && err.code === "not_editable") {
        t.title = "This automation cannot be edited here";
        t.description = err.error;
      } else if (t.title === GENERIC_FAILURE) {
        t.title = "Could not start the draft";
      }
      toast({ tone: "error", ...t });
      return undefined;
    } finally {
      if (mounted.current) setPending(null);
    }
  };

  return { start, pending };
}
