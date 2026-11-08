import type { ReactNode } from "react";
import { cx } from "./cx";

export type Tone = "green" | "red" | "blue" | "amber" | "gray";

const tones: Record<Tone, string> = {
  green: "bg-[var(--st-green-bg)] text-[var(--st-green-fg)]",
  red: "bg-[var(--st-red-bg)] text-[var(--st-red-fg)]",
  blue: "bg-[var(--st-blue-bg)] text-[var(--st-blue-fg)]",
  amber: "bg-[var(--st-amber-bg)] text-[var(--st-amber-fg)]",
  gray: "bg-[var(--st-gray-bg)] text-[var(--st-gray-fg)]",
};

export const dotColor: Record<Tone, string> = {
  green: "bg-[var(--st-green-dot)]",
  red: "bg-[var(--st-red-dot)]",
  blue: "bg-[var(--st-blue-dot)]",
  amber: "bg-[var(--st-amber-dot)]",
  gray: "bg-[var(--st-gray-dot)]",
};

export const textTone: Record<Tone, string> = {
  green: "text-[var(--st-green-fg)]",
  red: "text-[var(--st-red-fg)]",
  blue: "text-[var(--st-blue-fg)]",
  amber: "text-[var(--st-amber-fg)]",
  gray: "text-[var(--st-gray-fg)]",
};

export function Badge({
  tone = "gray",
  icon,
  children,
  className,
  title,
}: {
  tone?: Tone;
  icon?: ReactNode;
  children: ReactNode;
  className?: string;
  title?: string;
}) {
  return (
    <span
      title={title}
      className={cx(
        "inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium whitespace-nowrap leading-5",
        tones[tone],
        className,
      )}
    >
      {icon}
      {children}
    </span>
  );
}

/** A neutral outlined tag, e.g. for step types and roles. */
export function Tag({ children, className, title }: { children: ReactNode; className?: string; title?: string }) {
  return (
    <span
      title={title}
      className={cx(
        "inline-flex items-center rounded border border-line px-1.5 text-[12px] leading-5 text-fg-2 whitespace-nowrap",
        className,
      )}
    >
      {children}
    </span>
  );
}
