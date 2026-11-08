import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { api, errorMessage, isApiError } from "../api/client";
import type { BuilderStatus, DraftDetail, DraftMessage, DryRunStarted, RunDetail, Step } from "../api/types";
import {
  builderErrorToast,
  DiffView,
  draftErrors,
  DraftProblemList,
  DraftStatusBadge,
  draftTitle,
  draftWarnings,
  FilesView,
  isClosed,
  problemPlace,
  WorkingNote,
} from "../components/domain/Builder";
import { Badge, Tag } from "../components/ui/Badge";
import { Button } from "../components/ui/Button";
import { Card, CardBody, CardHeader } from "../components/ui/Card";
import { cx } from "../components/ui/cx";
import { Dialog } from "../components/ui/Dialog";
import {
  IconAlert,
  IconCheck,
  IconCheckCircle,
  IconExternal,
  IconHourglass,
  IconInfo,
  IconPencil,
  IconPlay,
  IconPlus,
  IconSlash,
  IconTrash,
} from "../components/ui/icons";
import { Field, FormError, Input, Textarea } from "../components/ui/Input";
import { Page, PageHeader } from "../components/ui/Page";
import { EmptyState, ErrorState, Skeleton, SkeletonRows } from "../components/ui/States";
import { StatusBadge, StatusIcon, statusLabel } from "../components/ui/StatusBadge";
import { Tabs } from "../components/ui/Tabs";
import { RelativeTime } from "../components/ui/Time";
import { useToast } from "../components/ui/Toast";
import { diffFiles, sortPaths } from "../lib/diff";
import { formatEur, pluralize } from "../lib/format";
import { builderQuery, draftQuery, isActive, keys, runQuery } from "../lib/queries";
import { atLeast } from "../lib/roles";
import { useWs, useWsRole } from "./WorkspaceGate";

export function DraftPage() {
  const ws = useWs();
  const { draftId = "" } = useParams({ strict: false }) as { draftId?: string };
  const role = useWsRole();
  const allowed = atLeast(role, "builder");
  const q = useQuery({ ...draftQuery(ws, draftId), enabled: allowed });
  const status = useQuery({ ...builderQuery(ws), enabled: allowed });

  return (
    <Page wide>
      {!allowed ? (
        <>
          <PageHeader title="Draft" breadcrumb={<Crumb ws={ws} />} />
          <Card>
            <EmptyState
              title="Drafts need the builder role"
              hint="Ask a workspace admin to make you a builder to see and change drafts."
            />
          </Card>
        </>
      ) : q.isPending ? (
        <div aria-busy="true" aria-label="Loading draft">
          <Skeleton className="mb-2 h-3 w-16" />
          <Skeleton className="mb-6 h-7 w-72" />
          <Card>
            <SkeletonRows rows={8} />
          </Card>
        </div>
      ) : q.isError ? (
        <>
          <PageHeader title="Draft" breadcrumb={<Crumb ws={ws} />} />
          <Card>
            <ErrorState error={q.error} onRetry={() => void q.refetch()} />
          </Card>
        </>
      ) : (
        <DraftView ws={ws} draft={q.data} status={status.data} canActivate={status.data?.canActivate ?? atLeast(role, "admin")} />
      )}
    </Page>
  );
}

function Crumb({ ws }: { ws: string }) {
  return (
    <Link to="/w/$ws/builder" params={{ ws }} className="hover:text-fg hover:underline">
      Builder
    </Link>
  );
}

type PanelTab = "files" | "changes" | "problems";
type Dialogs = "dryrun" | "activate" | "discard" | null;

function DraftView({
  ws,
  draft,
  status,
  canActivate,
}: {
  ws: string;
  draft: DraftDetail;
  status: BuilderStatus | undefined;
  canActivate: boolean;
}) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const [sending, setSending] = useState<{ text: string; after: number } | null>(null);
  const [proposing, setProposing] = useState(false);
  const [dialog, setDialog] = useState<Dialogs>(null);
  const [tab, setTab] = useState<PanelTab>("files");
  const [dryNote, setDryNote] = useState<DryRunStarted | null>(null);
  const hintId = useId();

  const files = draft.files ?? {};
  const fileCount = Object.keys(files).length;
  const problems = draft.problems ?? [];
  const errors = draftErrors(draft);
  const warnings = draftWarnings(draft);
  const closed = isClosed(draft);
  const busy = draft.busy || sending !== null;
  const stale = Boolean(draft.baseName && draft.currentHash && draft.currentHash !== draft.baseHash);
  const runId = draft.dryRunId;
  const dryRun = useQuery({ ...runQuery(ws, runId ?? ""), enabled: Boolean(runId) });
  const dryRunActive = isActive(dryRun.data?.run.status ?? draft.dryRun?.status);

  // Why the draft actions are off, most important first.
  const blocked = closed
    ? draft.status === "activated"
      ? "This draft is activated. Start a new draft to change the automation."
      : "This draft is discarded. Start a new draft to continue."
    : busy
      ? "The builder is working on this draft. Wait for its answer."
      : fileCount === 0
        ? "The draft has no files yet. Ask the builder for an automation, or add files with Edit."
        : errors.length > 0
          ? `Fix the ${pluralize(errors.length, "error")} in Problems first.`
          : undefined;
  const dryRunBlocked = blocked ?? (dryRunActive ? "A dry run is in progress. Wait for it to finish." : undefined);
  const activateBlocked =
    blocked ?? (stale ? "The automation changed since this draft started. Start a new draft from the current version." : undefined);
  const hint = canActivate ? activateBlocked ?? dryRunBlocked : dryRunBlocked;

  const refreshLists = () => queryClient.invalidateQueries({ queryKey: keys.drafts(ws) });
  const store = (d: DraftDetail) => {
    queryClient.setQueryData(keys.draft(ws, d.id), d);
    void refreshLists();
  };

  const send = async (text: string): Promise<boolean> => {
    setSending({ text, after: (draft.messages ?? []).length });
    try {
      store(await api.sendDraftMessage(ws, draft.id, text));
      return true;
    } catch (err) {
      toast({ tone: "error", ...builderErrorToast(err) });
      void queryClient.invalidateQueries({ queryKey: keys.draft(ws, draft.id) });
      return false;
    } finally {
      setSending(null);
    }
  };

  const propose = async () => {
    setProposing(true);
    try {
      store(await api.proposeDraft(ws, draft.id));
      void queryClient.invalidateQueries({ queryKey: keys.overview(ws) });
      toast({ tone: "success", title: "Proposed", description: "An admin can now review it and activate it." });
    } catch (err) {
      toast({ tone: "error", title: "Could not propose the draft", description: errorMessage(err) });
      void queryClient.invalidateQueries({ queryKey: keys.draft(ws, draft.id) });
    } finally {
      setProposing(false);
    }
  };

  const tabs: { value: PanelTab; label: ReactNode }[] = [
    { value: "files", label: `Files (${fileCount})` },
    ...(draft.baseFiles ? [{ value: "changes" as const, label: "Changes" }] : []),
    {
      value: "problems",
      label: (
        <span className="inline-flex items-center gap-1.5">
          Problems
          {errors.length > 0 ? (
            <span className="tnum rounded-full bg-[var(--st-red-bg)] px-1.5 text-[11.5px] leading-5 text-[var(--st-red-fg)]">
              {errors.length}
              <span className="sr-only"> {errors.length === 1 ? "error" : "errors"}</span>
            </span>
          ) : warnings.length > 0 ? (
            <span className="tnum rounded-full bg-[var(--st-amber-bg)] px-1.5 text-[11.5px] leading-5 text-[var(--st-amber-fg)]">
              {warnings.length}
              <span className="sr-only"> {warnings.length === 1 ? "warning" : "warnings"}</span>
            </span>
          ) : null}
        </span>
      ),
    },
  ];
  const current = tabs.some((t) => t.value === tab) ? tab : "files";

  return (
    <>
      <PageHeader
        breadcrumb={<Crumb ws={ws} />}
        title={
          <span className="flex flex-wrap items-center gap-2">
            <span className={draft.automation ? undefined : "text-fg-2 italic"}>{draftTitle(draft)}</span>
            <DraftStatusBadge status={draft.status} />
          </span>
        }
        description={
          <span className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[13px]">
            {draft.baseName && (
              <span>
                Edits{" "}
                <Link
                  to="/w/$ws/automations/$name"
                  params={{ ws, name: draft.baseName }}
                  className="font-medium text-accent hover:underline"
                >
                  {draft.baseName}
                </Link>
                {" ·"}
              </span>
            )}
            <span>
              Cost so far <span className="tnum font-medium text-fg">{formatEur(draft.costEur)}</span> ·
            </span>
            <span>
              Created by {draft.createdBy} <RelativeTime iso={draft.createdAt} />
            </span>
          </span>
        }
        actions={
          !closed && (
            <>
              <Button
                icon={<IconPlay size={14} />}
                disabled={Boolean(dryRunBlocked)}
                aria-describedby={hint ? hintId : undefined}
                onClick={() => setDialog("dryrun")}
              >
                Dry run
              </Button>
              {draft.status === "open" && (
                <Button
                  icon={<IconCheck size={14} />}
                  variant={canActivate ? "secondary" : "primary"}
                  disabled={Boolean(blocked)}
                  loading={proposing}
                  aria-describedby={hint ? hintId : undefined}
                  onClick={() => void propose()}
                >
                  Propose
                </Button>
              )}
              {canActivate && (
                <Button
                  variant="primary"
                  icon={<IconCheckCircle size={14} />}
                  disabled={Boolean(activateBlocked)}
                  aria-describedby={hint ? hintId : undefined}
                  onClick={() => setDialog("activate")}
                >
                  Activate
                </Button>
              )}
              <Button variant="ghost" icon={<IconTrash size={14} />} disabled={busy} onClick={() => setDialog("discard")}>
                Discard
              </Button>
            </>
          )
        }
      />

      <div className="space-y-4">
        {!closed && hint && (
          <p id={hintId} className="-mt-2 flex items-start gap-2 text-[13px] text-fg-2">
            <IconInfo size={14} className="mt-0.5 shrink-0 text-muted" />
            {hint}
          </p>
        )}

        <StatusBanner ws={ws} draft={draft} canActivate={canActivate} />

        {stale && !closed && (
          <div role="alert" className="flex gap-3 rounded-lg border border-[var(--st-amber-dot)]/50 bg-[var(--st-amber-bg)] px-4 py-3">
            <IconAlert size={16} className="mt-0.5 shrink-0 text-[var(--st-amber-dot)]" />
            <div className="min-w-0 text-[13px]">
              <p className="font-medium text-[var(--st-amber-fg)]">
                This automation changed since the draft started; start a new draft from the current version.
              </p>
              <p className="mt-0.5 text-fg-2">
                Activating this draft would undo those changes, so it is refused. Use Edit on{" "}
                {draft.baseName && (
                  <Link to="/w/$ws/automations/$name" params={{ ws, name: draft.baseName }} className="font-medium text-accent hover:underline">
                    {draft.baseName}
                  </Link>
                )}{" "}
                and copy what you need from this draft.
              </p>
            </div>
          </div>
        )}

        <div className="grid grid-cols-1 gap-4 lg:grid-cols-[minmax(0,5fr)_minmax(0,7fr)]">
          <Conversation
            draft={draft}
            status={status}
            closed={closed}
            sending={sending}
            onSend={send}
          />

          <div className="min-w-0 space-y-4">
            {(runId || dryNote) && (
              <DryRunCard
                ws={ws}
                draft={draft}
                runId={runId ?? dryNote?.runId}
                note={dryNote && dryNote.runId === (runId ?? dryNote.runId) ? dryNote.note : undefined}
                detail={dryRun.data}
                loading={dryRun.isPending}
                error={dryRun.error}
              />
            )}

            <Card className="min-w-0">
              <div className="border-b border-line px-4 py-3">
                <Tabs<PanelTab> label="Draft" value={current} onChange={setTab} items={tabs} />
              </div>
              <div role="tabpanel" aria-label={current === "files" ? "Files" : current === "changes" ? "Changes" : "Problems"}>
                {current === "files" ? (
                  <FilesPanel ws={ws} draft={draft} editBlocked={closed ? blocked : busy ? "The builder is working on this draft. Wait for its answer." : undefined} onSaved={store} />
                ) : current === "changes" && draft.baseFiles ? (
                  <ChangesPanel base={draft.baseFiles} files={files} baseName={draft.baseName ?? ""} />
                ) : (
                  <CardBody>
                    {problems.length === 0 ? (
                      <EmptyState
                        icon={<IconCheckCircle size={22} />}
                        title={fileCount === 0 ? "Nothing to check yet" : "No problems"}
                        hint={fileCount === 0 ? "Problems show up once the draft has files." : "The draft validates. Try a dry run before you propose it."}
                      />
                    ) : (
                      <div className="space-y-3">
                        <p className="text-[13px] text-fg-2">
                          {errors.length > 0
                            ? `${pluralize(errors.length, "error")} to fix before a dry run or activation. Ask the builder to fix them, or edit the files.`
                            : "Warnings do not block activation, but check them."}
                        </p>
                        <DraftProblemList problems={problems} />
                      </div>
                    )}
                  </CardBody>
                )}
              </div>
            </Card>
          </div>
        </div>
      </div>

      {dialog === "dryrun" && (
        <DryRunDialog
          ws={ws}
          draft={draft}
          onClose={() => setDialog(null)}
          onStarted={(r) => {
            setDryNote(r);
            queryClient.setQueryData(keys.draft(ws, draft.id), { ...draft, dryRunId: r.runId, dryRunHash: draft.hash });
            void queryClient.invalidateQueries({ queryKey: keys.draft(ws, draft.id) });
          }}
        />
      )}
      {dialog === "activate" && (
        <DecideDialog
          ws={ws}
          draft={draft}
          kind="activate"
          onClose={() => setDialog(null)}
          onDone={(d) => {
            store(d);
            void queryClient.invalidateQueries({ queryKey: keys.ws(ws) });
            toast({
              tone: "success",
              title: `Activated ${draftTitle(d)}`,
              description: "It runs on its own schedule from now on.",
              link: { to: `/w/${encodeURIComponent(ws)}/automations/${encodeURIComponent(d.automation)}`, label: "Open the automation" },
            });
          }}
        />
      )}
      {dialog === "discard" && (
        <DecideDialog
          ws={ws}
          draft={draft}
          kind="discard"
          onClose={() => setDialog(null)}
          onDone={(d) => {
            store(d);
            void queryClient.invalidateQueries({ queryKey: keys.overview(ws) });
            toast({ tone: "success", title: "Draft discarded" });
          }}
        />
      )}
    </>
  );
}

/** Proposed, activated or discarded: who did it, when, and what comes next. */
function StatusBanner({ ws, draft, canActivate }: { ws: string; draft: DraftDetail; canActivate: boolean }) {
  if (draft.status === "open") return null;
  const tone =
    draft.status === "proposed"
      ? "border-[var(--st-orange-dot)]/60 bg-[var(--st-orange-bg)]"
      : draft.status === "activated"
        ? "border-[var(--st-green-dot)]/40 bg-[var(--st-green-bg)]"
        : "border-line bg-surface-2";
  return (
    <div role="status" className={cx("flex gap-3 rounded-lg border px-4 py-3", tone)}>
      {draft.status === "proposed" ? (
        <IconHourglass size={16} className="mt-0.5 shrink-0 text-[var(--st-orange-fg)]" />
      ) : draft.status === "activated" ? (
        <IconCheckCircle size={16} className="mt-0.5 shrink-0 text-[var(--st-green-dot)]" />
      ) : (
        <IconSlash size={16} className="mt-0.5 shrink-0 text-muted" />
      )}
      <div className="min-w-0 text-[13px]">
        {draft.status === "proposed" ? (
          <>
            <p className="font-medium text-[var(--st-orange-fg)]">
              Proposed by {draft.proposedBy ?? "a builder"} <RelativeTime iso={draft.proposedAt} />
            </p>
            <p className="mt-0.5 text-fg-2">
              {canActivate
                ? "Check the files, the changes and the dry run, then use Activate."
                : "Waiting for an admin to activate it."}{" "}
              Any change moves it back to In progress, to be proposed again.
            </p>
          </>
        ) : draft.status === "activated" ? (
          <>
            <p className="font-medium text-[var(--st-green-fg)]">
              Activated by {draft.decidedBy ?? "an admin"} <RelativeTime iso={draft.decidedAt} />
            </p>
            <p className="mt-0.5 text-fg-2">
              It runs on its own schedule.{" "}
              {draft.automation && (
                <Link
                  to="/w/$ws/automations/$name"
                  params={{ ws, name: draft.automation }}
                  className="font-medium text-accent hover:underline"
                >
                  Open {draft.automation}
                </Link>
              )}
            </p>
          </>
        ) : (
          <p className="font-medium text-fg">
            Discarded by {draft.decidedBy ?? "someone"} <RelativeTime iso={draft.decidedAt} />
          </p>
        )}
        {draft.comment && (
          <blockquote className="mt-1 border-l-2 border-line-strong pl-2.5 break-words whitespace-pre-wrap text-fg">
            {draft.comment}
          </blockquote>
        )}
      </div>
    </div>
  );
}

// ---- Conversation ----

function Conversation({
  draft,
  status,
  closed,
  sending,
  onSend,
}: {
  draft: DraftDetail;
  status: BuilderStatus | undefined;
  closed: boolean;
  sending: { text: string; after: number } | null;
  onSend: (text: string) => Promise<boolean>;
}) {
  const [text, setText] = useState("");
  const listRef = useRef<HTMLDivElement>(null);
  const id = useId();
  const hintId = `${id}-hint`;
  const messages = draft.messages ?? [];
  // The server saves the user's message before the model answers; hide the local copy once it shows up.
  const pendingShown =
    sending !== null && !messages.slice(sending.after).some((m) => m.role === "user" && m.text === sending.text);
  const working = sending !== null || draft.busy;
  const enabled = status?.enabled ?? true;
  const disabledReason = closed
    ? `This draft is ${draft.status}. Start a new draft to make changes.`
    : !enabled
      ? status?.reason || "The AI builder is off. Edit the files by hand instead."
      : working
        ? "Wait for the builder's answer before you send another message."
        : undefined;

  // Keep the newest message in view.
  useEffect(() => {
    const el = listRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [messages.length, pendingShown, working]);

  const submit = async () => {
    const t = text.trim();
    if (!t || disabledReason) return;
    setText("");
    const ok = await onSend(t);
    // Keep what was typed when it failed, so it can be sent again.
    if (!ok) setText((cur) => (cur === "" ? t : cur));
  };

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      void submit();
    }
  };

  return (
    <Card className="flex min-w-0 flex-col">
      <CardHeader title="Conversation" description="Ask for the automation, then for changes. Every answer updates the files." />
      <div
        ref={listRef}
        role="log"
        aria-label="Conversation with the builder"
        tabIndex={0}
        className="max-h-[32rem] min-h-[10rem] flex-1 space-y-3 overflow-y-auto px-4 py-3 lg:max-h-[calc(100dvh-20rem)]"
      >
        {messages.length === 0 && !pendingShown ? (
          <p className="py-6 text-center text-[13px] text-muted">
            No messages yet. Describe what the automation should do.
          </p>
        ) : (
          messages.map((m) => <MessageItem key={m.id} message={m} />)
        )}
        {pendingShown && sending && (
          <MessageItem message={{ id: -1, role: "user", text: sending.text, createdAt: new Date().toISOString() }} pending />
        )}
        {working && (
          <WorkingNote>
            {sending
              ? "Writing the automation… this can take a minute."
              : "The builder is working on this draft. Its answer shows up here."}
          </WorkingNote>
        )}
      </div>
      <form
        className="space-y-2 border-t border-line px-4 py-3"
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
      >
        <label htmlFor={id} className="block text-[13px] font-medium text-fg">
          Message to the builder
        </label>
        <Textarea
          id={id}
          rows={3}
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={onKeyDown}
          disabled={Boolean(disabledReason)}
          aria-describedby={hintId}
          maxLength={20000}
          placeholder={closed ? undefined : "e.g. Also send me the list by email, and skip ideas older than a month"}
        />
        <div className="flex flex-wrap items-center justify-between gap-2">
          <p id={hintId} className="min-w-0 flex-1 text-[12px] text-muted">
            {disabledReason ?? "Enter sends, Shift+Enter adds a line."}
          </p>
          <Button type="submit" variant="primary" size="sm" loading={sending !== null} disabled={Boolean(disabledReason) || !text.trim()}>
            Send
          </Button>
        </div>
      </form>
    </Card>
  );
}

function MessageItem({ message, pending }: { message: DraftMessage; pending?: boolean }) {
  if (message.role === "event") {
    return (
      <div className="px-2 text-center text-[12px] text-muted">
        <p className="break-words whitespace-pre-wrap">{message.text}</p>
        <p className="mt-0.5">
          {message.author && <>{message.author} · </>}
          <RelativeTime iso={message.createdAt} />
        </p>
      </div>
    );
  }
  const user = message.role === "user";
  return (
    <div className={cx("flex", user ? "justify-end" : "justify-start")}>
      <div
        className={cx(
          "max-w-[90%] min-w-0 rounded-lg px-3 py-2 text-[13.5px]",
          user ? "bg-accent-soft text-fg" : "border border-line bg-surface-2 text-fg",
          pending && "opacity-70",
        )}
      >
        <p className="mb-0.5 text-[11.5px] font-medium text-muted">
          {user ? message.author || "You" : "Builder"}
          <span className="font-normal">
            {" · "}
            {pending ? "sending…" : <RelativeTime iso={message.createdAt} />}
          </span>
        </p>
        <p className="break-words whitespace-pre-wrap">{message.text}</p>
      </div>
    </div>
  );
}

// ---- Files ----

interface EditFile {
  key: number;
  path: string;
  content: string;
}

function toEditList(files: Record<string, string>): EditFile[] {
  return sortPaths(Object.keys(files)).map((path, i) => ({ key: i, path, content: files[path] ?? "" }));
}

/** Problems with the edited paths, or null when they can be saved. */
function pathProblem(list: EditFile[]): string | null {
  const seen = new Set<string>();
  for (const f of list) {
    const p = f.path.trim();
    if (!p) return "Every file needs a path, e.g. prompts/summary.md.";
    if (p.startsWith("/") || p.split("/").includes("..")) return `${p}: use a path inside the automation folder, without / at the start or "..".`;
    if (seen.has(p)) return `${p} appears twice. Give each file its own path.`;
    seen.add(p);
  }
  return null;
}

function FilesPanel({
  ws,
  draft,
  editBlocked,
  onSaved,
}: {
  ws: string;
  draft: DraftDetail;
  editBlocked: string | undefined;
  onSaved: (d: DraftDetail) => void;
}) {
  const [editing, setEditing] = useState<EditFile[] | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const nextKey = useRef(1000);
  const toast = useToast();
  const files = draft.files ?? {};

  if (!editing) {
    return (
      <CardBody className="space-y-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <p className="min-w-0 text-[12.5px] text-muted">{editBlocked ?? "Read-only. Use Edit to change the files by hand."}</p>
          <Button
            size="sm"
            icon={<IconPencil size={14} />}
            disabled={Boolean(editBlocked)}
            onClick={() => {
              setError(null);
              setEditing(toEditList(files));
            }}
          >
            Edit
          </Button>
        </div>
        <FilesView files={files} problems={draft.problems ?? []} />
      </CardBody>
    );
  }

  const update = (key: number, patch: Partial<EditFile>) =>
    setEditing((list) => (list ?? []).map((f) => (f.key === key ? { ...f, ...patch } : f)));

  const save = async () => {
    const problem = pathProblem(editing);
    if (problem) {
      setError(problem);
      return;
    }
    setSaving(true);
    setError(null);
    try {
      const out: Record<string, string> = {};
      for (const f of editing) out[f.path.trim()] = f.content;
      const d = await api.putDraftFiles(ws, draft.id, out);
      onSaved(d);
      setEditing(null);
      const n = draftErrors(d).length;
      toast(
        n > 0
          ? { tone: "info", title: "Saved", description: `The files have ${pluralize(n, "error")}. See Problems.` }
          : { tone: "success", title: "Saved", description: "The files validate." },
      );
    } catch (err) {
      setError(isApiError(err) && err.code === "busy" ? "The builder is working on this draft. Wait for its answer, then save again." : errorMessage(err));
    } finally {
      setSaving(false);
    }
  };

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void save();
      }}
    >
      <CardBody className="space-y-4">
        <p className="text-[12.5px] text-fg-2">
          Saving replaces every file of the draft and checks them again. Secrets go in as{" "}
          <code className="font-mono">secret://name</code>, never as values.
        </p>
        {editing.length === 0 && <p className="text-[13px] text-muted">No files. Add automation.yaml to start.</p>}
        {editing.map((f, i) => (
          <fieldset key={f.key} className="min-w-0 space-y-2 rounded-md border border-line p-3">
            <legend className="sr-only">File {i + 1}</legend>
            <div className="flex items-end gap-2">
              <Field label="Path" className="min-w-0 flex-1">
                {({ id }) => (
                  <Input
                    id={id}
                    value={f.path}
                    onChange={(e) => update(f.key, { path: e.target.value })}
                    placeholder="prompts/summary.md"
                    className="font-mono text-[12.5px]"
                    spellCheck={false}
                    autoComplete="off"
                  />
                )}
              </Field>
              <Button
                variant="ghost"
                icon={<IconTrash size={14} />}
                aria-label={`Delete ${f.path || "this file"}`}
                onClick={() => setEditing((list) => (list ?? []).filter((x) => x.key !== f.key))}
              >
                <span className="hidden sm:inline">Delete</span>
              </Button>
            </div>
            <Field label={`Content of ${f.path || "the file"}`}>
              {({ id }) => (
                <Textarea
                  id={id}
                  value={f.content}
                  onChange={(e) => update(f.key, { content: e.target.value })}
                  rows={Math.min(30, Math.max(6, f.content.split("\n").length + 1))}
                  className="font-mono text-[12.5px] leading-relaxed"
                  spellCheck={false}
                  wrap="off"
                  autoCapitalize="off"
                />
              )}
            </Field>
          </fieldset>
        ))}
        <Button
          size="sm"
          icon={<IconPlus size={14} />}
          onClick={() => setEditing((list) => [...(list ?? []), { key: nextKey.current++, path: "", content: "" }])}
        >
          Add file
        </Button>
        <FormError>{error}</FormError>
      </CardBody>
      <div className="flex flex-wrap justify-end gap-2 border-t border-line px-4 py-3">
        <Button onClick={() => setEditing(null)} disabled={saving}>
          Cancel
        </Button>
        <Button type="submit" variant="primary" loading={saving}>
          Save
        </Button>
      </div>
    </form>
  );
}

function ChangesPanel({ base, files, baseName }: { base: Record<string, string>; files: Record<string, string>; baseName: string }) {
  const diffs = useMemo(() => diffFiles(base, files), [base, files]);
  return (
    <CardBody>
      <p className="mb-3 text-[12.5px] text-fg-2">
        Compared with the version of <span className="font-mono text-fg">{baseName}</span> this draft started from.
      </p>
      <DiffView diffs={diffs} emptyHint={`The draft is the same as ${baseName}.`} />
    </CardBody>
  );
}

// ---- Dry run ----

function DryRunCard({
  ws,
  draft,
  runId,
  note,
  detail,
  loading,
  error,
}: {
  ws: string;
  draft: DraftDetail;
  runId: string | undefined;
  note: string | undefined;
  detail: RunDetail | undefined;
  loading: boolean;
  error: unknown;
}) {
  const stale = Boolean(draft.dryRunHash && draft.dryRunHash !== draft.hash);
  const steps = [...(detail?.steps ?? [])].sort((a, b) => a.seq - b.seq);
  return (
    <Card>
      <CardHeader
        title={
          <span className="flex flex-wrap items-center gap-2">
            Dry run
            {detail && <StatusBadge status={detail.run.status} />}
            {stale && (
              <Badge tone="amber" title="The files changed after this dry run">
                Stale
              </Badge>
            )}
          </span>
        }
        description="Reads happen for real; writes, messages and scripts are only recorded."
        actions={
          runId && (
            <Link
              to="/w/$ws/runs/$id"
              params={{ ws, id: runId }}
              className="inline-flex items-center gap-1 text-[13px] text-accent hover:underline"
            >
              <IconExternal size={14} />
              Full run
            </Link>
          )
        }
      />
      <CardBody className="space-y-3">
        {stale && (
          <p className="text-[12.5px] text-[var(--st-amber-fg)]">
            The files changed after this dry run. Run it again to test the current version.
          </p>
        )}
        {note && (
          <p className="flex items-start gap-2 text-[12.5px] text-fg-2">
            <IconInfo size={14} className="mt-0.5 shrink-0 text-muted" />
            {note}
          </p>
        )}
        {loading ? (
          <SkeletonRows rows={3} className="p-0" />
        ) : error ? (
          <p role="alert" className="text-[13px] text-[var(--st-red-fg)]">
            Could not load the dry run: {errorMessage(error)}
          </p>
        ) : detail ? (
          <>
            {detail.run.error && (
              <p className="rounded-md border border-[var(--st-red-dot)]/40 bg-[var(--st-red-bg)] px-3 py-2 text-[13px] break-words whitespace-pre-wrap text-fg">
                {detail.run.error}
              </p>
            )}
            {steps.length === 0 ? (
              <p className="text-[13px] text-muted">
                {isActive(detail.run.status) ? "Starting… steps show up here as they run." : "No steps recorded."}
              </p>
            ) : (
              <ol className="divide-y divide-line rounded-md border border-line" aria-label="Dry run steps">
                {steps.map((s) => (
                  <DryStep key={`${s.seq}-${s.path}`} step={s} />
                ))}
              </ol>
            )}
            <p className="tnum text-[12px] text-muted">
              Cost {formatEur(detail.run.costEur)}
              {isActive(detail.run.status) && " · refreshing every 5s"}
            </p>
          </>
        ) : null}
      </CardBody>
    </Card>
  );
}

function DryStep({ step }: { step: Step }) {
  const simulated = step.logs?.startsWith("dry run:");
  const detail = step.error ?? (simulated ? step.logs?.replace(/^dry run: ?/, "") : undefined);
  return (
    <li className="px-3 py-1.5">
      <div className="flex min-w-0 items-center gap-2">
        <StatusIcon status={step.status} />
        <span className="sr-only">{statusLabel(step.status)}</span>
        <span className="min-w-0 truncate font-mono text-[12.5px] text-fg" title={step.path}>
          {step.path}
        </span>
        <Tag className="shrink-0">{step.type}</Tag>
        {simulated && (
          <Tag className="shrink-0" title="A real run would do this; the dry run only recorded it">
            simulated
          </Tag>
        )}
        {step.costEur > 0 && <span className="tnum ml-auto shrink-0 text-[12px] text-fg-2">{formatEur(step.costEur)}</span>}
      </div>
      {detail && (
        <p
          className={cx(
            "mt-0.5 line-clamp-2 pl-6 text-[12px] break-words",
            step.error ? "text-[var(--st-red-fg)]" : "text-muted",
          )}
          title={detail}
        >
          {detail}
        </p>
      )}
    </li>
  );
}

function DryRunDialog({
  ws,
  draft,
  onClose,
  onStarted,
}: {
  ws: string;
  draft: DraftDetail;
  onClose: () => void;
  onStarted: (r: DryRunStarted) => void;
}) {
  const [sample, setSample] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [jsonError, setJsonError] = useState<string | null>(null);

  return (
    <Dialog
      open
      onClose={() => !busy && onClose()}
      title={`Dry run ${draftTitle(draft)}?`}
      description="It runs the draft once now. Reads happen for real (read actions, GET requests, model calls, which cost money). Writes, messages and scripts are only recorded, and approvals are skipped."
      onSubmit={async () => {
        let triggerData: Record<string, unknown> | undefined;
        if (sample.trim()) {
          try {
            const v: unknown = JSON.parse(sample);
            if (!v || typeof v !== "object" || Array.isArray(v)) throw new Error("not an object");
            triggerData = v as Record<string, unknown>;
          } catch {
            setJsonError('Write a JSON object, e.g. {"subject": "Invoice"}, or leave it empty.');
            return;
          }
        }
        setJsonError(null);
        setBusy(true);
        setError(null);
        try {
          onStarted(await api.dryRunDraft(ws, draft.id, triggerData));
          onClose();
        } catch (err) {
          setError(errorMessage(err));
          setBusy(false);
        }
      }}
      footer={
        <>
          <Button onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" icon={<IconPlay size={14} />} loading={busy} data-autofocus>
            Start dry run
          </Button>
        </>
      }
    >
      <Field
        label="Sample trigger data (optional)"
        hint="JSON the steps see as trigger.*. Leave empty: for an email trigger Pendolo uses the newest matching email."
        error={jsonError}
      >
        {({ id, describedBy, invalid }) => (
          <Textarea
            id={id}
            rows={4}
            value={sample}
            onChange={(e) => setSample(e.target.value)}
            aria-describedby={describedBy}
            aria-invalid={invalid}
            className="font-mono text-[12.5px]"
            spellCheck={false}
            placeholder='{"subject": "Invoice 42"}'
          />
        )}
      </Field>
      <FormError>{error}</FormError>
    </Dialog>
  );
}

// ---- Activate and discard ----

function DecideDialog({
  ws,
  draft,
  kind,
  onClose,
  onDone,
}: {
  ws: string;
  draft: DraftDetail;
  kind: "activate" | "discard";
  onClose: () => void;
  onDone: (d: DraftDetail) => void;
}) {
  const [comment, setComment] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // The version on screen when the dialog opened: activation applies to it
  // only, so files changed meanwhile are never activated unseen.
  const [reviewedHash] = useState(draft.hash);
  const queryClient = useQueryClient();
  const activate = kind === "activate";
  const name = draftTitle(draft);
  const warnings = draftWarnings(draft);
  const dryRunMissing = !draft.dryRunId || draft.dryRunHash !== draft.hash;

  return (
    <Dialog
      open
      onClose={() => !busy && onClose()}
      size={activate ? "md" : "sm"}
      title={activate ? `Activate ${name}?` : "Discard this draft?"}
      onSubmit={async () => {
        setBusy(true);
        setError(null);
        const text = comment.trim() || undefined;
        try {
          const d = activate ? await api.activateDraft(ws, draft.id, reviewedHash, text) : await api.discardDraft(ws, draft.id, text);
          onDone(d);
          onClose();
        } catch (err) {
          setBusy(false);
          setError(errorMessage(err));
          // The draft may have changed (problems re-checked, closed by someone else).
          void queryClient.invalidateQueries({ queryKey: keys.draft(ws, draft.id) });
        }
      }}
      footer={
        <>
          <Button onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button type="submit" variant={activate ? "primary" : "danger"} loading={busy}>
            {activate ? "Activate" : "Discard draft"}
          </Button>
        </>
      }
    >
      {activate ? (
        <div className="space-y-3 text-[13px] text-fg-2">
          <p>
            Pendolo writes the files to the automations folder
            {draft.baseName ? (
              <>
                , replacing <span className="font-mono text-fg">{draft.baseName}</span>
              </>
            ) : (
              <> as a new automation</>
            )}
            . <span className="font-medium text-fg">It starts running on its own schedule and triggers</span>, with real
            writes and messages.
          </p>
          {dryRunMissing && (
            <p className="flex items-start gap-2 text-[var(--st-amber-fg)]">
              <IconAlert size={14} className="mt-0.5 shrink-0" />
              {draft.dryRunId ? "The last dry run tested an older version of the files." : "This version has no dry run yet."}
            </p>
          )}
          {warnings.length > 0 && (
            <div>
              <p className="mb-1 font-medium text-fg">{pluralize(warnings.length, "warning")}</p>
              <ul className="max-h-40 space-y-1 overflow-y-auto">
                {warnings.map((w, i) => (
                  <li key={i} className="flex items-start gap-2">
                    <IconInfo size={14} className="mt-0.5 shrink-0 text-[var(--st-amber-dot)]" />
                    <span className="min-w-0 break-words">
                      {problemPlace(w) && <span className="font-mono text-[12px] text-muted">{problemPlace(w)} </span>}
                      {w.message}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      ) : (
        <p className="text-[13px] text-fg-2">
          The draft closes and nothing is written. The conversation stays readable under Closed. This cannot be undone.
        </p>
      )}
      <Field label="Comment (optional)" hint="Saved with the decision and shown in the conversation.">
        {({ id, describedBy }) => (
          <Textarea
            id={id}
            rows={2}
            value={comment}
            onChange={(e) => setComment(e.target.value)}
            aria-describedby={describedBy}
            maxLength={2000}
            data-autofocus
          />
        )}
      </Field>
      <FormError>{error}</FormError>
    </Dialog>
  );
}
