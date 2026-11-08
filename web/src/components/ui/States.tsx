import type { ReactNode } from "react";
import { errorMessage, isApiError } from "../../api/client";
import { Button } from "./Button";
import { cx } from "./cx";
import { IconAlert, IconRefresh } from "./icons";

export function EmptyState({
  title,
  hint,
  icon,
  action,
  className,
}: {
  title: ReactNode;
  hint?: ReactNode;
  icon?: ReactNode;
  action?: ReactNode;
  className?: string;
}) {
  return (
    <div className={cx("flex flex-col items-center justify-center px-6 py-10 text-center", className)}>
      {icon && <div className="mb-3 text-muted">{icon}</div>}
      <p className="text-sm font-medium text-fg">{title}</p>
      {hint && <p className="mt-1 max-w-md text-[13px] text-muted">{hint}</p>}
      {action && <div className="mt-4">{action}</div>}
    </div>
  );
}

export function ErrorState({
  error,
  onRetry,
  className,
  title,
}: {
  error: unknown;
  onRetry?: () => void;
  className?: string;
  title?: string;
}) {
  const notFound = isApiError(error) && error.status === 404;
  const forbidden = isApiError(error) && error.status === 403;
  return (
    <div role="alert" className={cx("flex flex-col items-center justify-center px-6 py-10 text-center", className)}>
      <IconAlert size={22} className="mb-3 text-[var(--st-red-dot)]" />
      <p className="text-sm font-medium text-fg">
        {title ?? (notFound ? "Not found" : forbidden ? "No access" : "Could not load this")}
      </p>
      <p className="mt-1 max-w-md text-[13px] text-muted">
        {notFound
          ? "It may have been removed, or you are not a member of this workspace."
          : forbidden
            ? "Your role does not allow this. Ask a workspace admin."
            : errorMessage(error)}
      </p>
      {onRetry && !notFound && !forbidden && (
        <Button className="mt-4" size="sm" icon={<IconRefresh size={14} />} onClick={onRetry}>
          Retry
        </Button>
      )}
    </div>
  );
}

export function Skeleton({ className }: { className?: string }) {
  return <div aria-hidden="true" className={cx("skeleton h-4", className)} />;
}

export function SkeletonRows({ rows = 5, className }: { rows?: number; className?: string }) {
  return (
    <div className={cx("space-y-3 p-4", className)} aria-busy="true" aria-label="Loading">
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} className={i % 2 ? "w-4/5" : "w-full"} />
      ))}
    </div>
  );
}
