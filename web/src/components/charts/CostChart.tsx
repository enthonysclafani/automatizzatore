import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { formatDay, formatDayLong, formatEur, formatEurAxis } from "../../lib/format";
import { Table, Td, TdNum, Th, THead, Tr } from "../ui/Table";
import { ChartFrame, TooltipBox } from "./ChartFrame";
import { useChartPalette } from "./palette";

export interface CostDay {
  date: string;
  costEur: number;
}

/** Cost per day, one series (its title names it, so no legend). */
export function CostChart({
  data,
  height = 200,
  title = "Cost per day",
  summary,
}: {
  data: CostDay[];
  height?: number;
  title?: string;
  summary?: React.ReactNode;
}) {
  const p = useChartPalette();
  return (
    <ChartFrame
      title={title}
      height={height}
      summary={summary}
      empty={data.every((d) => d.costEur === 0) ? "No model costs in this period" : undefined}
      chart={
        <ResponsiveContainer width="100%" height="100%">
          <BarChart data={data} margin={{ top: 8, right: 4, bottom: 0, left: 0 }}>
            <CartesianGrid vertical={false} stroke={p.grid} />
            <XAxis
              dataKey="date"
              tickFormatter={formatDay}
              tick={{ fill: p.tick, fontSize: 11 }}
              tickLine={false}
              axisLine={{ stroke: p.axis }}
              interval="preserveStartEnd"
              minTickGap={16}
            />
            <YAxis
              tick={{ fill: p.tick, fontSize: 11 }}
              tickLine={false}
              axisLine={false}
              width={52}
              tickFormatter={(v: number) => formatEurAxis(v)}
            />
            <Tooltip
              cursor={{ fill: p.hover }}
              content={({ active, payload }) => {
                const row = payload?.[0]?.payload as CostDay | undefined;
                if (!active || !row) return null;
                return (
                  <TooltipBox
                    title={formatDayLong(row.date)}
                    rows={[{ label: "Cost", value: formatEur(row.costEur), color: p.cost }]}
                  />
                );
              }}
            />
            <Bar dataKey="costEur" name="Cost" fill={p.cost} maxBarSize={24} radius={[4, 4, 0, 0]} isAnimationActive={false} />
          </BarChart>
        </ResponsiveContainer>
      }
      table={
        <Table label={title}>
          <THead>
            <tr>
              <Th>Day</Th>
              <Th className="text-right">Cost</Th>
            </tr>
          </THead>
          <tbody>
            {[...data].reverse().map((d) => (
              <Tr key={d.date}>
                <Td>{formatDayLong(d.date)}</Td>
                <TdNum>{formatEur(d.costEur)}</TdNum>
              </Tr>
            ))}
          </tbody>
        </Table>
      }
    />
  );
}
