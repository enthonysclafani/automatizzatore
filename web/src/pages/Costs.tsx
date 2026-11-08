import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { getRouteApi, Link } from "@tanstack/react-router";
import type { Costs } from "../api/types";
import { CostChart } from "../components/charts/CostChart";
import { Card, CardBody, CardHeader } from "../components/ui/Card";
import { cx } from "../components/ui/cx";
import { IconEuro } from "../components/ui/icons";
import { Page, PageHeader } from "../components/ui/Page";
import { EmptyState, ErrorState, Skeleton } from "../components/ui/States";
import { Table, Td, TdNum, Th, THead, Tr } from "../components/ui/Table";
import { Tabs } from "../components/ui/Tabs";
import { formatEur, formatInt } from "../lib/format";
import { COST_PERIODS, DEFAULT_COST_PERIOD, type CostPeriod } from "../lib/nav";
import { costsQuery } from "../lib/queries";
import { useWs } from "./WorkspaceGate";

const route = getRouteApi("/_app/w/$ws/costs");

export function CostsPage() {
  const ws = useWs();
  const search = route.useSearch();
  const navigate = route.useNavigate();
  const days: CostPeriod = search.days ?? DEFAULT_COST_PERIOD;
  const q = useQuery({ ...costsQuery(ws, days), placeholderData: keepPreviousData });

  return (
    <Page wide>
      <PageHeader
        title="Costs"
        description="Model calls priced with the rates in the server settings. Amounts in euro."
        actions={
          <Tabs
            label="Period"
            value={String(days)}
            onChange={(v) =>
              void navigate({ search: { days: Number(v) === DEFAULT_COST_PERIOD ? undefined : (Number(v) as CostPeriod) }, replace: true })
            }
            items={COST_PERIODS.map((d) => ({ value: String(d), label: `${d} days` }))}
          />
        }
      />
      {q.isPending ? (
        <div className="space-y-4" aria-busy="true" aria-label="Loading costs">
          <Card>
            <CardBody>
              <Skeleton className="mb-3 h-3 w-24" />
              <Skeleton className="mb-6 h-10 w-40" />
              <Skeleton className="h-52 w-full" />
            </CardBody>
          </Card>
        </div>
      ) : q.isError ? (
        <Card>
          <ErrorState error={q.error} onRetry={() => void q.refetch()} />
        </Card>
      ) : (
        <div className={cx("space-y-4 transition-opacity", q.isPlaceholderData && "opacity-60")}>
          <CostsBody ws={ws} data={q.data} />
        </div>
      )}
    </Page>
  );
}

function CostsBody({ ws, data }: { ws: string; data: Costs }) {
  const unknown = data.byModel.filter((m) => !m.priceKnown);
  const byAutomation = [...data.byAutomation].sort((a, b) => b.costEur - a.costEur);
  const byModel = [...data.byModel].sort((a, b) => b.costEur - a.costEur);
  const avg = data.days > 0 ? data.totalEur / data.days : 0;

  return (
    <>
      <Card>
        <CardBody>
          <p className="text-[12.5px] text-muted">Total, last {data.days} days</p>
          <p className="mt-1 text-5xl font-semibold tracking-tight text-fg">{formatEur(data.totalEur)}</p>
          <p className="mt-1 text-[12.5px] text-fg-2">{formatEur(avg)} per day on average</p>
          {unknown.length > 0 && (
            <p className="mt-3 rounded-md bg-[var(--st-amber-bg)] px-3 py-2 text-[12.5px] text-[var(--st-amber-fg)]">
              {unknown.length === 1 ? "One model has" : `${unknown.length} models have`} no price configured, so the
              total is too low. Add a price for {unknown.map((m) => m.model).join(", ")} in the server settings.
            </p>
          )}
          <div className="mt-5">
            <CostChart data={data.daily} height={240} />
          </div>
        </CardBody>
      </Card>

      <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
        <Card>
          <CardHeader title="By automation" />
          {byAutomation.length === 0 ? (
            <EmptyState icon={<IconEuro size={22} />} title="No costs in this period" hint="Only runs that call a model cost money." />
          ) : (
            <Table label="Cost by automation">
              <THead>
                <tr>
                  <Th>Automation</Th>
                  <Th className="text-right">Runs</Th>
                  <Th className="text-right">Cost</Th>
                  <Th className="text-right">Share</Th>
                </tr>
              </THead>
              <tbody>
                {byAutomation.map((a) => (
                  <Tr key={a.automation}>
                    <Td className="max-w-[16rem] truncate">
                      <Link
                        to="/w/$ws/automations/$name"
                        params={{ ws, name: a.automation }}
                        className="font-medium text-fg hover:underline"
                      >
                        {a.automation}
                      </Link>
                    </Td>
                    <TdNum>{formatInt(a.runs)}</TdNum>
                    <TdNum>{formatEur(a.costEur)}</TdNum>
                    <TdNum className="text-muted">
                      {data.totalEur > 0 ? `${Math.round((a.costEur / data.totalEur) * 100)}%` : "–"}
                    </TdNum>
                  </Tr>
                ))}
              </tbody>
            </Table>
          )}
        </Card>

        <Card>
          <CardHeader title="By model" />
          {byModel.length === 0 ? (
            <EmptyState icon={<IconEuro size={22} />} title="No model calls in this period" hint="llm and agent steps appear here." />
          ) : (
            <Table label="Cost by model">
              <THead>
                <tr>
                  <Th>Model</Th>
                  <Th className="text-right">Calls</Th>
                  <Th className="text-right">Tokens in</Th>
                  <Th className="text-right">Tokens out</Th>
                  <Th className="text-right">Cost</Th>
                </tr>
              </THead>
              <tbody>
                {byModel.map((m) => (
                  <Tr key={`${m.provider}/${m.model}`}>
                    <Td className="max-w-[16rem]">
                      <span className="block truncate font-medium text-fg">{m.model}</span>
                      <span className="block text-[12px] text-muted">{m.provider}</span>
                    </Td>
                    <TdNum>{formatInt(m.calls)}</TdNum>
                    <TdNum>{formatInt(m.inputTokens)}</TdNum>
                    <TdNum>{formatInt(m.outputTokens)}</TdNum>
                    <TdNum>
                      {m.priceKnown ? (
                        formatEur(m.costEur)
                      ) : (
                        <span className="text-[var(--st-amber-fg)]" title="Add a price for this model in the server settings">
                          price not configured
                        </span>
                      )}
                    </TdNum>
                  </Tr>
                ))}
              </tbody>
            </Table>
          )}
        </Card>
      </div>
    </>
  );
}
