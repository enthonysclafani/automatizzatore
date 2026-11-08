import { useState, type ReactNode } from "react";
import { Tabs } from "../ui/Tabs";

export interface LegendItem {
  label: string;
  color: string;
}

/** Legend row (rect swatches; text stays in text colors). */
export function Legend({ items }: { items: LegendItem[] }) {
  if (items.length < 2) return null;
  return (
    <ul className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[12px] text-fg-2" aria-label="Legend">
      {items.map((it) => (
        <li key={it.label} className="flex items-center gap-1.5">
          <span aria-hidden="true" className="inline-block h-2.5 w-2.5 rounded-[2px]" style={{ background: it.color }} />
          {it.label}
        </li>
      ))}
    </ul>
  );
}

/** Chart with a Chart/Table switch so no value is reachable only by hovering. */
export function ChartFrame({
  title,
  legend,
  chart,
  table,
  height,
  summary,
  empty,
}: {
  title: string;
  legend?: LegendItem[];
  chart: ReactNode;
  table: ReactNode;
  height: number;
  summary?: ReactNode;
  /** Shown instead of an empty plot when there is nothing to draw. */
  empty?: ReactNode;
}) {
  const [view, setView] = useState<"chart" | "table">("chart");
  return (
    <div>
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
          {summary}
          {legend && <Legend items={legend} />}
        </div>
        <Tabs
          label={`${title}: view`}
          value={view}
          onChange={setView}
          items={[
            { value: "chart", label: "Chart" },
            { value: "table", label: "Table" },
          ]}
        />
      </div>
      {view === "chart" && empty ? (
        <div className="flex items-center justify-center rounded-md border border-dashed border-line text-center text-[13px] text-muted" style={{ height }}>
          {empty}
        </div>
      ) : view === "chart" ? (
        <div role="img" aria-label={`${title}. Switch to Table for the values.`} style={{ height }}>
          {chart}
        </div>
      ) : (
        <div className="overflow-y-auto rounded-md border border-line" style={{ maxHeight: Math.max(height, 240) }}>
          {table}
        </div>
      )}
    </div>
  );
}

export function TooltipBox({ title, rows }: { title: string; rows: { label: string; value: string; color: string }[] }) {
  return (
    <div className="min-w-[140px] rounded-md border border-line bg-surface px-3 py-2 text-[12px] shadow-lg">
      <p className="mb-1 text-muted">{title}</p>
      {rows.map((r) => (
        <div key={r.label} className="flex items-center justify-between gap-4">
          <span className="flex items-center gap-1.5 text-fg-2">
            <span aria-hidden="true" className="inline-block h-0.5 w-3 rounded" style={{ background: r.color }} />
            {r.label}
          </span>
          <span className="tnum font-semibold text-fg">{r.value}</span>
        </div>
      ))}
    </div>
  );
}
