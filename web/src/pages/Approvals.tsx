import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { ApprovalCard } from "../components/domain/Approvals";
import { Card } from "../components/ui/Card";
import { IconCheckCircle, IconHourglass } from "../components/ui/icons";
import { Page, PageHeader } from "../components/ui/Page";
import { EmptyState, ErrorState, Skeleton } from "../components/ui/States";
import { Tabs } from "../components/ui/Tabs";
import { allApprovalsQuery, pendingApprovalsQuery, sortApprovals } from "../lib/queries";
import { useWs, useWsRole } from "./WorkspaceGate";

type Tab = "pending" | "recent";

export function ApprovalsPage() {
  const ws = useWs();
  const role = useWsRole();
  const [tab, setTab] = useState<Tab>("pending");
  const pending = useQuery(pendingApprovalsQuery(ws));
  const all = useQuery({ ...allApprovalsQuery(ws), enabled: tab === "recent" });

  const pendingList = sortApprovals(pending.data?.approvals ?? []);
  const recentList = sortApprovals((all.data?.approvals ?? []).filter((a) => a.status !== "pending"));
  const q = tab === "pending" ? pending : all;
  const list = tab === "pending" ? pendingList : recentList;
  const count = pending.data ? pendingList.length : undefined;

  return (
    <Page>
      <PageHeader
        title="Approvals"
        description="Steps that wait for a person before they send, publish or delete something."
      />
      <Tabs<Tab>
        label="Approvals"
        className="mb-4"
        value={tab}
        onChange={setTab}
        items={[
          { value: "pending", label: count ? `Pending (${count})` : "Pending" },
          { value: "recent", label: "Recent" },
        ]}
      />
      <div role="tabpanel" aria-label={tab === "pending" ? "Pending approvals" : "Recent decisions"}>
        {q.isPending ? (
          <div className="space-y-3" aria-busy="true" aria-label="Loading approvals">
            {Array.from({ length: 2 }, (_, i) => (
              <div key={i} className="space-y-3 rounded-lg border border-line bg-surface p-4">
                <Skeleton className="h-4 w-48" />
                <Skeleton className="h-3 w-72 max-w-full" />
                <Skeleton className="h-16 w-full" />
              </div>
            ))}
          </div>
        ) : q.isError ? (
          <Card>
            <ErrorState error={q.error} onRetry={() => void q.refetch()} />
          </Card>
        ) : list.length === 0 ? (
          <Card>
            {tab === "pending" ? (
              <EmptyState
                icon={<IconCheckCircle size={24} />}
                title="Nothing waits for approval"
                hint="When a step needs a person (an approval step, or a send or delete under autonomy “ask”), it shows up here."
              />
            ) : (
              <EmptyState
                icon={<IconHourglass size={24} />}
                title="No decisions yet"
                hint="Approved, rejected and expired requests show up here."
              />
            )}
          </Card>
        ) : (
          <ul className="space-y-3">
            {list.map((a) => (
              <li key={a.id}>
                <ApprovalCard ws={ws} approval={a} role={role} />
              </li>
            ))}
          </ul>
        )}
      </div>
    </Page>
  );
}
