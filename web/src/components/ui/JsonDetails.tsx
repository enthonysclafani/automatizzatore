import { formatInt, prettyJson } from "../../lib/format";
import { CopyButton } from "./Copy";
import { cx } from "./cx";
import { IconChevronRight } from "./icons";

/** Collapsible, copyable pretty JSON. */
export function JsonDetails({
  label,
  value,
  defaultOpen,
  className,
}: {
  label: string;
  value: unknown;
  defaultOpen?: boolean;
  className?: string;
}) {
  const text = prettyJson(value);
  return (
    <details className={cx("group rounded-md border border-line bg-surface", className)} open={defaultOpen}>
      <summary className="flex cursor-pointer list-none items-center gap-1.5 px-3 py-1.5 text-[12px] font-medium text-fg-2 select-none hover:text-fg [&::-webkit-details-marker]:hidden">
        <IconChevronRight size={12} className="transition-transform group-open:rotate-90" />
        {label}
        <span className="font-normal text-muted">{typeof value === "string" ? `${formatInt(value.length)} chars` : ""}</span>
      </summary>
      <div className="relative border-t border-line">
        <div className="absolute top-2 right-2">
          <CopyButton text={text} />
        </div>
        <pre
          tabIndex={0}
          aria-label={label}
          className="max-h-96 overflow-auto p-3 pr-24 font-mono text-[12px] leading-relaxed whitespace-pre-wrap break-words text-fg"
        >
          {text}
        </pre>
      </div>
    </details>
  );
}
