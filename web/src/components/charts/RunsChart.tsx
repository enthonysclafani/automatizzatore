import { Bar, BarChart, BarStack, CartesianGrid, Rectangle, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import type { BarShapeProps } from "recharts";
import { formatDay, formatDayLong, formatInt } from "../../lib/format";
import { Table, Td, TdNum, Th, THead, Tr } from "../ui/Table";
import { ChartFrame, TooltipBox } from "./ChartFrame";
import { useChartPalette } from "./palette";

export interface RunsDay {
  date: string;
  succeeded: number;
  failed: number;
}

const GAP = 2;

/** Top segment of the stack leaves a 2px surface gap above the segment below it. */
function FailedShape(props: BarShapeProps) {
  const below = (props.payload as RunsDay | undefined)?.succeeded ?? 0;
  const height = below > 0 ? Math.max(0, props.height - GAP) : props.height;
  if (height <= 0) return null;
  return <Rectangle x={props.x} y={props.y} width={props.width} height={height} fill={props.fill} />;
}

/** Runs per day, succeeded and failed stacked. */
export function RunsChart({ data, height = 220 }: { data: RunsDay[]; height?: number }) {
  const p = useChartPalette();
  const total = data.reduce((s, d) => s + d.succeeded + d.failed, 0);
  return (
    <ChartFrame
      title="Runs per day"
      height={height}
      legend={[
        { label: "Succeeded", color: p.succeeded },
        { label: "Failed", color: p.failed },
      ]}
      summary={<span className="text-[12px] text-muted">{formatInt(total)} runs in 14 days</span>}
      empty={total === 0 ? "No runs in the last 14 days" : undefined}
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
              allowDecimals={false}
              tick={{ fill: p.tick, fontSize: 11 }}
              tickLine={false}
              axisLine={false}
              width={36}
              tickFormatter={(v: number) => formatInt(v)}
            />
            <Tooltip
              cursor={{ fill: p.hover }}
              content={({ active, payload }) => {
                const row = payload?.[0]?.payload as RunsDay | undefined;
                if (!active || !row) return null;
                return (
                  <TooltipBox
                    title={formatDayLong(row.date)}
                    rows={[
                      { label: "Succeeded", value: formatInt(row.succeeded), color: p.succeeded },
                      { label: "Failed", value: formatInt(row.failed), color: p.failed },
                    ]}
                  />
                );
              }}
            />
            <BarStack radius={[4, 4, 0, 0]}>
              <Bar dataKey="succeeded" name="Succeeded" fill={p.succeeded} maxBarSize={24} isAnimationActive={false} />
              <Bar
                dataKey="failed"
                name="Failed"
                fill={p.failed}
                maxBarSize={24}
                isAnimationActive={false}
                shape={FailedShape}
              />
            </BarStack>
          </BarChart>
        </ResponsiveContainer>
      }
      table={
        <Table label="Runs per day">
          <THead>
            <tr>
              <Th>Day</Th>
              <Th className="text-right">Succeeded</Th>
              <Th className="text-right">Failed</Th>
            </tr>
          </THead>
          <tbody>
            {[...data].reverse().map((d) => (
              <Tr key={d.date}>
                <Td>{formatDayLong(d.date)}</Td>
                <TdNum>{formatInt(d.succeeded)}</TdNum>
                <TdNum>{formatInt(d.failed)}</TdNum>
              </Tr>
            ))}
          </tbody>
        </Table>
      }
    />
  );
}
