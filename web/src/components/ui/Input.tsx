import { forwardRef, useId, type InputHTMLAttributes, type ReactNode, type TextareaHTMLAttributes } from "react";
import { cx } from "./cx";

export const controlClass =
  "block w-full rounded-md border border-line-strong bg-surface px-3 text-sm text-fg placeholder:text-muted shadow-xs focus:border-accent focus:outline-2 focus:outline-offset-0 focus:outline-ring/40 disabled:opacity-60 aria-[invalid=true]:border-danger";

export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(function Input(
  { className, ...rest },
  ref,
) {
  return <input ref={ref} className={cx(controlClass, "h-9", className)} {...rest} />;
});

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaHTMLAttributes<HTMLTextAreaElement>>(
  function Textarea({ className, ...rest }, ref) {
    return <textarea ref={ref} className={cx(controlClass, "py-2", className)} {...rest} />;
  },
);

/**
 * A label, a control and an optional hint or error. The render prop receives the
 * ids to wire up so screen readers announce hint and error with the control.
 */
export function Field({
  label,
  hint,
  error,
  children,
  className,
}: {
  label: ReactNode;
  hint?: ReactNode;
  error?: ReactNode;
  className?: string;
  children: (ids: { id: string; describedBy: string | undefined; invalid: boolean }) => ReactNode;
}) {
  const id = useId();
  const hintId = `${id}-hint`;
  const errId = `${id}-err`;
  const describedBy = [hint ? hintId : "", error ? errId : ""].filter(Boolean).join(" ") || undefined;
  return (
    <div className={cx("space-y-1.5", className)}>
      <label htmlFor={id} className="block text-[13px] font-medium text-fg">
        {label}
      </label>
      {children({ id, describedBy, invalid: Boolean(error) })}
      {hint && !error && (
        <p id={hintId} className="text-[12px] text-muted">
          {hint}
        </p>
      )}
      {error && (
        <p id={errId} className="text-[12px] text-danger" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}

/** Inline form-level error. */
export function FormError({ children }: { children: ReactNode }) {
  if (!children) return null;
  return (
    <div role="alert" className="rounded-md border border-danger/40 bg-danger-soft px-3 py-2 text-[13px] text-fg">
      {children}
    </div>
  );
}
