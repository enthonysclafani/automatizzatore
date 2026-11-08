import { useId, useRef, type KeyboardEvent, type ReactNode } from "react";
import { cx } from "./cx";

export interface TabItem<T extends string> {
  value: T;
  label: ReactNode;
}

/** Accessible tab list (arrow keys move between tabs). Render the panel with tabPanelProps. */
export function Tabs<T extends string>({
  items,
  value,
  onChange,
  label,
  className,
}: {
  items: TabItem<T>[];
  value: T;
  onChange: (v: T) => void;
  label: string;
  className?: string;
}) {
  const base = useId();
  const refs = useRef<(HTMLButtonElement | null)[]>([]);

  const onKey = (e: KeyboardEvent<HTMLButtonElement>, i: number) => {
    let next = -1;
    if (e.key === "ArrowRight") next = (i + 1) % items.length;
    if (e.key === "ArrowLeft") next = (i - 1 + items.length) % items.length;
    if (e.key === "Home") next = 0;
    if (e.key === "End") next = items.length - 1;
    if (next >= 0) {
      e.preventDefault();
      const item = items[next];
      if (item) {
        onChange(item.value);
        refs.current[next]?.focus();
      }
    }
  };

  return (
    <div
      role="tablist"
      aria-label={label}
      className={cx("inline-flex rounded-md border border-line bg-surface-2 p-0.5", className)}
    >
      {items.map((it, i) => {
        const selected = it.value === value;
        return (
          <button
            key={it.value}
            ref={(el) => {
              refs.current[i] = el;
            }}
            type="button"
            role="tab"
            id={`${base}-tab-${it.value}`}
            aria-selected={selected}
            tabIndex={selected ? 0 : -1}
            onClick={() => onChange(it.value)}
            onKeyDown={(e) => onKey(e, i)}
            className={cx(
              "rounded px-2.5 py-1 text-[13px] font-medium transition-colors",
              selected ? "bg-surface text-fg shadow-xs" : "text-fg-2 hover:text-fg",
            )}
          >
            {it.label}
          </button>
        );
      })}
    </div>
  );
}
