import type { ReactNode } from "react";
import { cx } from "./cx";

export function PageHeader({
  title,
  description,
  actions,
  breadcrumb,
  className,
}: {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  breadcrumb?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cx("mb-5", className)}>
      {breadcrumb && <div className="mb-1.5 text-[13px] text-muted">{breadcrumb}</div>}
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h1 className="text-xl font-semibold tracking-tight break-words text-fg">{title}</h1>
          {description && <div className="mt-1 text-sm text-fg-2">{description}</div>}
        </div>
        {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
      </div>
    </div>
  );
}

export function Page({ children, wide }: { children: ReactNode; wide?: boolean }) {
  return <div className={cx("mx-auto w-full px-4 py-6 sm:px-6 lg:px-8", wide ? "max-w-[1400px]" : "max-w-6xl")}>{children}</div>;
}

/** Definition list for "label: value" summaries. */
export function DefList({
  items,
  className,
}: {
  items: { label: ReactNode; value: ReactNode; wide?: boolean }[];
  className?: string;
}) {
  return (
    <dl className={cx("grid grid-cols-1 gap-x-6 gap-y-3 sm:grid-cols-2 lg:grid-cols-3", className)}>
      {items.map((it, i) => (
        <div key={i} className={cx("min-w-0", it.wide && "sm:col-span-full")}>
          <dt className="text-[12px] text-muted">{it.label}</dt>
          <dd className="mt-0.5 text-sm break-words text-fg">{it.value}</dd>
        </div>
      ))}
    </dl>
  );
}
