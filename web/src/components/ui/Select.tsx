import { forwardRef, type SelectHTMLAttributes } from "react";
import { cx } from "./cx";
import { controlClass } from "./Input";

export const Select = forwardRef<HTMLSelectElement, SelectHTMLAttributes<HTMLSelectElement> & { compact?: boolean }>(
  function Select({ className, compact, children, ...rest }, ref) {
    return (
      <div className={cx("relative", className)}>
        <select
          ref={ref}
          className={cx(controlClass, compact ? "h-8 text-[13px]" : "h-9", "appearance-none pr-8")}
          {...rest}
        >
          {children}
        </select>
        <svg
          aria-hidden="true"
          className="pointer-events-none absolute top-1/2 right-2.5 -translate-y-1/2 text-muted"
          width="14"
          height="14"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
        >
          <path d="M6 9l6 6 6-6" />
        </svg>
      </div>
    );
  },
);
