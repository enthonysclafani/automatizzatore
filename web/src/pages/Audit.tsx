import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../api/client";
import type { AuditEntry } from "../api/types";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { IconScroll } from "../components/ui/icons";
import { Page, PageHeader } from "../components/ui/Page";
import { EmptyState, ErrorState, SkeletonRows } from "../components/ui/States";
import { Table, Td, Th, THead, Tr } from "../components/ui/Table";
import { compactJson, formatAbsolute } from "../lib/format";
import { keys } from "../lib/queries";
import { atLeast } from "../lib/roles";
import { useWs, useWsRole } from "./WorkspaceGate";

const PAGE = 200;
const MAX = 1000;

export function AuditPage() {
  const ws = useWs();
  const role = useWsRole();
  const canAdmin = atLeast(role, "admin");
  const [limit, setLimit] = useState(PAGE);
  const q = useQuery({
    queryKey: [...keys.audit(ws), limit],
    queryFn: () => api.workspaceAudit(ws, limit),
    enabled: canAdmin,
    placeholderData: (prev) => prev,
  });

  return (
    <Page wide>
      <PageHeader title="Audit log" description="Who did what in this workspace, newest first." />
      <Card>
        {!canAdmin ? (
          <EmptyState title="No access" hint="Only workspace admins can see the audit log." />
        ) : q.isPending ? (
          <SkeletonRows rows={8} />
        ) : q.isError ? (
          <ErrorState error={q.error} onRetry={() => void q.refetch()} />
        ) : (
          <AuditTable
            entries={q.data.entries}
            footer={
              q.data.entries.length >= limit && limit < MAX ? (
                <Button size="sm" loading={q.isFetching} onClick={() => setLimit((l) => Math.min(MAX, l + PAGE))}>
                  Show more
                </Button>
              ) : null
            }
          />
        )}
      </Card>
    </Page>
  );
}

export function AuditTable({
  entries,
  footer,
  showWorkspace,
}: {
  entries: AuditEntry[];
  footer?: React.ReactNode;
  showWorkspace?: boolean;
}) {
  if (entries.length === 0) {
    return (
      <EmptyState
        icon={<IconScroll size={24} />}
        title="Nothing recorded yet"
        hint="Sign-ins, triggers, secret and member changes appear here."
      />
    );
  }
  return (
    <>
      <Table label="Audit log">
        <THead>
          <tr>
            <Th>Time</Th>
            <Th>Actor</Th>
            <Th>Action</Th>
            {showWorkspace && <Th>Workspace</Th>}
            <Th>Target</Th>
            <Th>Detail</Th>
          </tr>
        </THead>
        <tbody>
          {entries.map((e) => {
            const detail = compactJson(e.detail);
            return (
              <Tr key={e.id}>
                <Td className="tnum whitespace-nowrap text-fg-2">{formatAbsolute(e.at)}</Td>
                <Td className="max-w-[14rem] truncate text-fg" title={e.ip ? `${e.actor} from ${e.ip}` : e.actor}>
                  {e.actor}
                </Td>
                <Td className="whitespace-nowrap">
                  <code className="font-mono text-[12px] text-fg">{e.action}</code>
                </Td>
                {showWorkspace && <Td className="text-fg-2">{e.workspace ?? "–"}</Td>}
                <Td className="max-w-[14rem] truncate text-fg-2" title={e.target}>
                  {e.target ?? "–"}
                </Td>
                <Td className="max-w-[28rem]">
                  {detail && detail !== "{}" ? (
                    <code className="line-clamp-2 font-mono text-[11.5px] break-all text-muted" title={detail}>
                      {detail}
                    </code>
                  ) : (
                    <span className="text-muted">–</span>
                  )}
                </Td>
              </Tr>
            );
          })}
        </tbody>
      </Table>
      <div className="flex items-center justify-between border-t border-line px-4 py-2.5 text-[12.5px] text-muted">
        <span>{entries.length} entries</span>
        {footer}
      </div>
    </>
  );
}
