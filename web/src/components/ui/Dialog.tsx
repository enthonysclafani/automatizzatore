import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import { errorMessage } from "../../api/client";
import { Button } from "./Button";
import { cx } from "./cx";
import { IconX } from "./icons";
import { FormError } from "./Input";

/** Modal dialog on top of the native <dialog>: focus trap, Escape and backdrop click close it. */
export function Dialog({
  open,
  onClose,
  title,
  description,
  children,
  footer,
  onSubmit,
  size = "md",
}: {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  description?: ReactNode;
  children?: ReactNode;
  footer?: ReactNode;
  /** When set, the body is a form and Enter submits it. */
  onSubmit?: (e: FormEvent<HTMLFormElement>) => void;
  size?: "sm" | "md" | "lg";
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const returnFocus = useRef<HTMLElement | null>(null);

  // Give focus back to whatever opened the dialog when it unmounts while open.
  // Closing a mounted <dialog> restores focus natively, and StrictMode's simulated
  // unmount keeps the element in the document, so both are skipped.
  useEffect(() => {
    if (!open) return;
    const el = ref.current;
    return () => {
      const back = returnFocus.current;
      window.setTimeout(() => {
        if (el?.isConnected) return;
        if (back?.isConnected) back.focus();
      }, 0);
    };
  }, [open]);

  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (open && !d.open) {
      returnFocus.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      d.showModal();
      // React's autoFocus runs before showModal and is lost; focus the intended control now.
      const target =
        d.querySelector<HTMLElement>("[data-autofocus]") ??
        d.querySelector<HTMLElement>("input:not([type=hidden]):not([disabled]), select:not([disabled]), textarea:not([disabled])");
      target?.focus();
    }
    if (!open && d.open) d.close();
  }, [open]);

  const width = size === "sm" ? "max-w-sm" : size === "lg" ? "max-w-2xl" : "max-w-lg";
  const content = (
    <>
      <div className="flex items-start justify-between gap-3 border-b border-line px-5 py-4">
        <div className="min-w-0">
          <h2 className="text-base font-semibold text-fg">{title}</h2>
          {description && <p className="mt-1 text-[13px] text-fg-2">{description}</p>}
        </div>
        <button
          type="button"
          onClick={onClose}
          aria-label="Close"
          className="-mt-1 -mr-1 rounded-md p-1 text-muted hover:bg-surface-2 hover:text-fg"
        >
          <IconX size={16} />
        </button>
      </div>
      {children && <div className="space-y-4 px-5 py-4">{children}</div>}
      {footer && <div className="flex flex-wrap justify-end gap-2 border-t border-line px-5 py-3">{footer}</div>}
    </>
  );

  return (
    <dialog
      ref={ref}
      onCancel={(e) => {
        e.preventDefault();
        onClose();
      }}
      onClick={(e) => {
        if (e.target === ref.current) onClose();
      }}
      className={cx(
        "m-auto w-[calc(100%-2rem)] rounded-lg border border-line bg-surface p-0 text-fg shadow-xl backdrop:bg-black/40",
        width,
      )}
    >
      {open &&
        (onSubmit ? (
          <form
            noValidate={false}
            onSubmit={(e) => {
              e.preventDefault();
              onSubmit(e);
            }}
          >
            {content}
          </form>
        ) : (
          content
        ))}
    </dialog>
  );
}

/** Ask before doing something; shows the server error inline if it fails. */
export function ConfirmDialog({
  open,
  onClose,
  title,
  message,
  confirmLabel,
  danger,
  onConfirm,
}: {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  message: ReactNode;
  confirmLabel: string;
  danger?: boolean;
  onConfirm: () => Promise<unknown>;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const close = () => {
    if (busy) return;
    setError(null);
    onClose();
  };

  return (
    <Dialog
      open={open}
      onClose={close}
      title={title}
      size="sm"
      onSubmit={async () => {
        setBusy(true);
        setError(null);
        try {
          await onConfirm();
          setBusy(false);
          onClose();
        } catch (err) {
          setBusy(false);
          setError(errorMessage(err));
        }
      }}
      footer={
        <>
          <Button onClick={close} disabled={busy}>
            Cancel
          </Button>
          <Button type="submit" variant={danger ? "danger" : "primary"} loading={busy} data-autofocus>
            {confirmLabel}
          </Button>
        </>
      }
    >
      <div className="text-sm text-fg-2">{message}</div>
      <FormError>{error}</FormError>
    </Dialog>
  );
}
