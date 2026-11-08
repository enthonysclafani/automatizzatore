import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { useId, useState } from "react";
import type { BuilderStatus, Draft } from "../api/types";
import {
  draftErrors,
  DraftStatusBadge,
  draftTitle,
  useStartDraft,
  WorkingNote,
} from "../components/domain/Builder";
import { Button } from "../components/ui/Button";
import { Card, CardBody, CardHeader } from "../components/ui/Card";
import { IconAlert, IconInfo, IconSparkle } from "../components/ui/icons";
import { Textarea } from "../components/ui/Input";
import { Page, PageHeader } from "../components/ui/Page";
import { EmptyState, ErrorState, Skeleton, SkeletonRows } from "../components/ui/States";
import { Table, Td, TdNum, Th, THead, Tr } from "../components/ui/Table";
import { Tabs } from "../components/ui/Tabs";
import { RelativeTime } from "../components/ui/Time";
import { formatEur } from "../lib/format";
import { builderQuery, draftsQuery, type DraftTab } from "../lib/queries";
import { atLeast } from "../lib/roles";
import { useWs, useWsRole } from "./WorkspaceGate";

export function BuilderPage() {
  const ws = useWs();
  const role = useWsRole();
  const allowed = atLeast(role, "builder");

  return (
    <Page wide>
      <PageHeader
        title="Builder"
        description="Describe an automation and the AI writes it as a draft. Test it with a dry run, then an admin activates it."
      />
      {allowed ? (
        <BuilderBody ws={ws} />
      ) : (
        <Card>
          <EmptyState
            icon={<IconSparkle size={24} />}
            title="The builder needs the builder role"
            hint="Ask a workspace admin to make you a builder, or ask a builder to write the automation for you."
          />
        </Card>
      )}
    </Page>
  );
}

function BuilderBody({ ws }: { ws: string }) {
  const status = useQuery(builderQuery(ws));
  const { start, pending } = useStartDraft(ws);

  return (
    <div className="space-y-4">
      {status.isError ? (
        <Card>
          <ErrorState error={status.error} onRetry={() => void status.refetch()} />
        </Card>
      ) : (
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
          <NewAutomation
            status={status.data}
            working={pending?.message !== undefined}
            disabled={pending !== null}
            onCreate={(message) => start({ message })}
          />
          <Templates
            status={status.data}
            pendingTemplate={pending?.template}
            disabled={pending !== null}
            onPick={(template) => void start({ template })}
          />
        </div>
      )}
      <DraftList ws={ws} />
    </div>
  );
}

const PLACEHOLDER =
  "Every Monday at 9, find article ideas, check them on OpenSEO, write them to a Google Doc and tell me on Slack";

function NewAutomation({
  status,
  working,
  disabled,
  onCreate,
}: {
  status: BuilderStatus | undefined;
  working: boolean;
  disabled: boolean;
  onCreate: (message: string) => Promise<unknown>;
}) {
  const [text, setText] = useState("");
  const id = useId();
  const hintId = `${id}-hint`;
  const enabled = status?.enabled ?? false;
  const empty = text.trim() === "";

  const submit = async (e: { preventDefault: () => void }) => {
    e.preventDefault();
    if (empty || !enabled || disabled) return;
    const d = await onCreate(text.trim());
    if (d) setText("");
  };

  return (
    <Card className="lg:col-span-2">
      <CardHeader
        title="New automation"
        description={status?.model ? `The builder uses ${status.model}.` : undefined}
      />
      <CardBody>
        <form onSubmit={(e) => void submit(e)} className="space-y-3">
          <label htmlFor={id} className="block text-[13px] font-medium text-fg">
            Describe what the automation should do
          </label>
          <Textarea
            id={id}
            rows={4}
            value={text}
            onChange={(e) => setText(e.target.value)}
            placeholder={PLACEHOLDER}
            aria-describedby={hintId}
            disabled={!status || !enabled || disabled}
            maxLength={20000}
            onKeyDown={(e) => {
              // Ctrl/Cmd+Enter creates; plain Enter is a newline in this longer field.
              if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) void submit(e);
            }}
          />
          {!status ? (
            <Skeleton className="h-4 w-64" />
          ) : !enabled ? (
            <p id={hintId} className="flex items-start gap-2 text-[13px] text-[var(--st-amber-fg)]">
              <IconAlert size={14} className="mt-0.5 shrink-0" />
              <span>{status.reason || "The AI builder is off. Start from a template instead."}</span>
            </p>
          ) : (
            <p id={hintId} className="text-[12px] text-muted">
              Say when it runs, what it reads, and where the result goes. Mention the connections to use. Ctrl+Enter creates
              it.
            </p>
          )}
          {working && (
            <WorkingNote>Writing the automation… this can take a minute. You can stay on this page.</WorkingNote>
          )}
          <div className="flex justify-end">
            <Button type="submit" variant="primary" icon={<IconSparkle size={14} />} loading={working} disabled={!enabled || empty || disabled}>
              {working ? "Writing…" : "Create"}
            </Button>
          </div>
        </form>
      </CardBody>
    </Card>
  );
}

function Templates({
  status,
  pendingTemplate,
  disabled,
  onPick,
}: {
  status: BuilderStatus | undefined;
  pendingTemplate: string | undefined;
  disabled: boolean;
  onPick: (name: string) => void;
}) {
  const templates = [...(status?.templates ?? [])].sort((a, b) => a.name.localeCompare(b.name));
  return (
    <Card>
      <CardHeader title="Start from a template" description="A working automation to adapt. No model needed." />
      {!status ? (
        <SkeletonRows rows={3} />
      ) : templates.length === 0 ? (
        <EmptyState title="No templates" hint="This server has no bundled templates." />
      ) : (
        <ul className="max-h-80 divide-y divide-line overflow-y-auto">
          {templates.map((t) => (
            <li key={t.name} className="flex items-start gap-3 px-4 py-2.5">
              <div className="min-w-0 flex-1">
                <p className="font-mono text-[12.5px] font-medium break-all text-fg">{t.name}</p>
                {t.description && <p className="mt-0.5 text-[12.5px] text-fg-2">{t.description}</p>}
              </div>
              <Button
                size="sm"
                loading={pendingTemplate === t.name}
                disabled={disabled}
                onClick={() => onPick(t.name)}
                aria-label={`Use the template ${t.name}`}
              >
                Use
              </Button>
            </li>
          ))}
        </ul>
      )}
    </Card>
  );
}

function DraftList({ ws }: { ws: string }) {
  const [tab, setTab] = useState<DraftTab>("open");
  const q = useQuery(draftsQuery(ws, tab));
  const drafts = q.data?.drafts ?? [];

  return (
    <Card>
      <CardHeader
        title="Drafts"
        actions={
          <Tabs<DraftTab>
            label="Drafts"
            value={tab}
            onChange={setTab}
            items={[
              { value: "open", label: "In progress" },
              { value: "closed", label: "Closed" },
            ]}
          />
        }
      />
      <div role="tabpanel" aria-label={tab === "open" ? "Drafts in progress" : "Closed drafts"}>
        {q.isPending ? (
          <SkeletonRows rows={4} />
        ) : q.isError ? (
          <ErrorState error={q.error} onRetry={() => void q.refetch()} />
        ) : drafts.length === 0 ? (
          tab === "open" ? (
            <EmptyState
              icon={<IconSparkle size={22} />}
              title="No drafts in progress"
              hint="Describe an automation above, start from a template, or use Edit on an automation."
            />
          ) : (
            <EmptyState icon={<IconInfo size={22} />} title="No closed drafts" hint="Activated and discarded drafts show up here." />
          )
        ) : (
          <DraftTable ws={ws} drafts={drafts} />
        )}
      </div>
    </Card>
  );
}

function DraftTable({ ws, drafts }: { ws: string; drafts: Draft[] }) {
  const navigate = useNavigate();
  return (
    <Table label="Drafts">
      <THead>
        <tr>
          <Th>Automation</Th>
          <Th>Status</Th>
          <Th>Created by</Th>
          <Th>Updated</Th>
          <Th className="text-right">Cost</Th>
          <Th className="text-right">Errors</Th>
        </tr>
      </THead>
      <tbody>
        {drafts.map((d) => {
          const errors = draftErrors(d).length;
          return (
            <Tr
              key={d.id}
              className="cursor-pointer hover:bg-surface-2"
              onClick={(e) => {
                if ((e.target as HTMLElement).closest("a,button")) return;
                void navigate({ to: "/w/$ws/builder/$draftId", params: { ws, draftId: d.id } });
              }}
            >
              <Td className="max-w-[20rem] min-w-[10rem]">
                <Link
                  to="/w/$ws/builder/$draftId"
                  params={{ ws, draftId: d.id }}
                  className="block truncate font-medium text-fg hover:underline"
                >
                  {d.automation ? d.automation : <span className="text-muted italic">{draftTitle(d)}</span>}
                </Link>
                {d.baseName && <span className="block truncate text-[12px] text-muted">Edits {d.baseName}</span>}
              </Td>
              <Td>
                <DraftStatusBadge status={d.status} />
              </Td>
              <Td className="max-w-[14rem] truncate text-fg-2">{d.createdBy}</Td>
              <Td className="whitespace-nowrap text-fg-2">
                <RelativeTime iso={d.updatedAt} />
              </Td>
              <TdNum className="text-fg-2">{formatEur(d.costEur)}</TdNum>
              <TdNum>
                {errors > 0 ? (
                  <span className="text-[var(--st-red-fg)]">{errors}</span>
                ) : (
                  <span className="text-muted">0</span>
                )}
              </TdNum>
            </Tr>
          );
        })}
      </tbody>
    </Table>
  );
}
