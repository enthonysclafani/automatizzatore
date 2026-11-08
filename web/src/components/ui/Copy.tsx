import { useEffect, useState } from "react";
import { Button } from "./Button";
import { cx } from "./cx";
import { IconCheck, IconCopy } from "./icons";

async function writeClipboard(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

export function CopyButton({
  text,
  label = "Copy",
  className,
  size = "sm",
}: {
  text: string;
  label?: string;
  className?: string;
  size?: "sm" | "md";
}) {
  const [state, setState] = useState<"idle" | "copied" | "failed">("idle");

  useEffect(() => {
    if (state === "idle") return;
    const t = window.setTimeout(() => setState("idle"), 2000);
    return () => window.clearTimeout(t);
  }, [state]);

  return (
    <Button
      size={size}
      className={className}
      icon={state === "copied" ? <IconCheck size={14} /> : <IconCopy size={14} />}
      onClick={async () => setState((await writeClipboard(text)) ? "copied" : "failed")}
      aria-live="polite"
    >
      {state === "copied" ? "Copied" : state === "failed" ? "Select and copy" : label}
    </Button>
  );
}

/** Read-only monospace block with a copy button. */
export function CodeBlock({
  code,
  label,
  className,
  maxHeight = "32rem",
  copy = true,
}: {
  code: string;
  label?: string;
  className?: string;
  maxHeight?: string;
  copy?: boolean;
}) {
  return (
    <div className={cx("relative rounded-md border border-line bg-surface-2", className)}>
      {copy && (
        <div className="absolute top-2 right-2 z-10">
          <CopyButton text={code} />
        </div>
      )}
      <pre
        tabIndex={0}
        aria-label={label}
        style={{ maxHeight }}
        className="overflow-auto p-3 pr-24 font-mono text-[12.5px] leading-relaxed whitespace-pre text-fg"
      >
        {code}
      </pre>
    </div>
  );
}
