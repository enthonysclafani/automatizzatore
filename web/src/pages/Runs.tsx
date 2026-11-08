import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { getRouteApi } from "@tanstack/react-router";
import type { RunStatus } from "../api/types";
import { RunsTable } from "../components/domain/RunsTable";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { IconList } from "../components/ui/icons";
import { Page, PageHeader } from "../components/ui/Page";
import { Select } from "../components/ui/Select";
import { EmptyState, ErrorState, SkeletonRows } from "../components/ui/States";
import { statusLabel } from "../components/ui/StatusBadge";
import { RUN_STATUSES } from "../lib/nav";
import { automationsQuery, runsQuery } from "../lib/queries";
import { useWs } from "./WorkspaceGate";

const route = getRouteApi("/_app/w/$ws/runs");

export function RunsPage() {
  const ws = useWs();
  const search = route.useSearch();
  const navigate = route.useNavigate();
  const automations = useQuery(automationsQuery(ws));
  const q = useInfiniteQuery(runsQuery(ws, search.automation, search.status));

  const runs = q.data?.pages.flatMap((p) => p.runs) ?? [];
  const names = (automations.data?.automations ?? []).map((a) => a.name).sort();
  if (search.automation && !names.includes(search.automation)) names.unshift(search.automation);
  const filtered = Boolean(search.automation || search.status);

  return (
    <Page wide>
      <PageHeader title="Runs" description="Every run of every automation in this workspace, newest first." />

      <div className="mb-3 flex flex-wrap items-end gap-2">
        <div>
          <label htmlFor="runs-automation" className="mb-1 block text-[12px] text-muted">
            Automation
          </label>
          <Select
            id="runs-automation"
            compact
            className="w-56 max-w-full"
            value={search.automation ?? ""}
            onChange={(e) =>
              void navigate({ search: (s) => ({ ...s, automation: e.target.value || undefined }), replace: true })
            }
          >
            <option value="">All automations</option>
            {names.map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </Select>
        </div>
        <div>
          <label htmlFor="runs-status" className="mb-1 block text-[12px] text-muted">
            Status
          </label>
          <Select
            id="runs-status"
            compact
            className="w-40"
            value={search.status ?? ""}
            onChange={(e) =>
              void navigate({
                search: (s) => ({ ...s, status: (e.target.value || undefined) as RunStatus | undefined }),
                replace: true,
              })
            }
          >
            <option value="">Any status</option>
            {RUN_STATUSES.map((s) => (
              <option key={s} value={s}>
                {statusLabel(s)}
              </option>
            ))}
          </Select>
        </div>
        {filtered && (
          <Button size="sm" variant="ghost" onClick={() => void navigate({ search: {}, replace: true })}>
            Clear filters
          </Button>
        )}
      </div>

      <Card>
        {q.isPending ? (
          <SkeletonRows rows={8} />
        ) : q.isError ? (
          <ErrorState error={q.error} onRetry={() => void q.refetch()} />
        ) : runs.length === 0 ? (
          <EmptyState
            icon={<IconList size={24} />}
            title={filtered ? "No runs match these filters" : "No runs yet"}
            hint={
              filtered
                ? "Clear the filters or pick another automation."
                : "Runs appear here when a schedule, webhook or Run now starts an automation."
            }
          />
        ) : (
          <>
            <RunsTable ws={ws} runs={runs} />
            <div className="flex items-center justify-between gap-2 border-t border-line px-4 py-2.5 text-[12.5px] text-muted">
              <span>
                {runs.length} {runs.length === 1 ? "run" : "runs"} shown
              </span>
              {q.hasNextPage ? (
                <Button size="sm" loading={q.isFetchingNextPage} onClick={() => void q.fetchNextPage()}>
                  Load more
                </Button>
              ) : (
                <span>End of history</span>
              )}
            </div>
          </>
        )}
      </Card>
    </Page>
  );
}
