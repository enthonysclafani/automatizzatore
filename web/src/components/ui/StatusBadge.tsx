import type { RunStatus, StepStatus } from "../../api/types";
import { Badge, textTone, type Tone } from "./Badge";
import { cx } from "./cx";
import { IconCheckCircle, IconClock, IconSkip, IconSlash, IconSpinner, IconXCircle } from "./icons";

type AnyStatus = RunStatus | StepStatus;

export const statusTone: Record<AnyStatus, Tone> = {
  succeeded: "green",
  failed: "red",
  running: "blue",
  queued: "gray",
  cancelled: "amber",
  skipped: "gray",
};

const labels: Record<AnyStatus, string> = {
  succeeded: "Succeeded",
  failed: "Failed",
  running: "Running",
  queued: "Queued",
  cancelled: "Cancelled",
  skipped: "Skipped",
};

export function StatusIcon({ status, size = 14, className }: { status: AnyStatus; size?: number; className?: string }) {
  const cls = cx(textTone[statusTone[status]], "shrink-0", className);
  switch (status) {
    case "succeeded":
      return <IconCheckCircle size={size} className={cls} />;
    case "failed":
      return <IconXCircle size={size} className={cls} />;
    case "running":
      return <IconSpinner size={size} className={cls} />;
    case "queued":
      return <IconClock size={size} className={cls} />;
    case "cancelled":
      return <IconSlash size={size} className={cls} />;
    case "skipped":
      return <IconSkip size={size} className={cls} />;
  }
}

export function StatusBadge({ status, className }: { status: AnyStatus; className?: string }) {
  return (
    <Badge tone={statusTone[status]} className={className} icon={<StatusIcon status={status} size={12} className="text-current" />}>
      {labels[status]}
    </Badge>
  );
}

export function statusLabel(status: AnyStatus): string {
  return labels[status];
}
