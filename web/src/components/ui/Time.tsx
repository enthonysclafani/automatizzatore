import { formatAbsolute, formatRelative } from "../../lib/format";
import { useNow } from "../../lib/now";

/** Relative time ("5m ago", "in 3h") with the absolute time on hover. */
export function RelativeTime({ iso, className }: { iso: string | undefined | null; className?: string }) {
  const now = useNow();
  if (!iso) return <span className={className}>–</span>;
  return (
    <time dateTime={iso} title={formatAbsolute(iso)} className={className}>
      {formatRelative(iso, now)}
    </time>
  );
}
