import { useRouter } from "@tanstack/react-router";
import { createContext, useCallback, useContext, useMemo, useRef, useState, type ReactNode } from "react";
import { cx } from "./cx";
import { IconCheckCircle, IconInfo, IconX, IconXCircle } from "./icons";

export interface ToastInput {
  title: string;
  description?: string;
  tone?: "success" | "error" | "info";
  /** A link inside the app, e.g. to the run just started. */
  link?: { to: string; label: string };
  durationMs?: number;
}

interface ToastItem extends ToastInput {
  id: number;
}

const ToastContext = createContext<((t: ToastInput) => void) | null>(null);

export function useToast(): (t: ToastInput) => void {
  const ctx = useContext(ToastContext);
  if (!ctx) throw new Error("useToast must be used inside ToastProvider");
  return ctx;
}

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<ToastItem[]>([]);
  const nextId = useRef(1);
  const router = useRouter();

  const dismiss = useCallback((id: number) => {
    setItems((list) => list.filter((t) => t.id !== id));
  }, []);

  const push = useCallback(
    (t: ToastInput) => {
      const id = nextId.current++;
      setItems((list) => [...list.slice(-3), { ...t, id }]);
      window.setTimeout(() => dismiss(id), t.durationMs ?? (t.tone === "error" ? 8000 : 6000));
    },
    [dismiss],
  );

  const value = useMemo(() => push, [push]);

  return (
    <ToastContext.Provider value={value}>
      {children}
      <div
        aria-live="polite"
        aria-atomic="false"
        className="pointer-events-none fixed inset-x-0 bottom-0 z-50 flex flex-col items-center gap-2 p-4 sm:items-end"
      >
        {items.map((t) => (
          <div
            key={t.id}
            role={t.tone === "error" ? "alert" : "status"}
            className="pointer-events-auto flex w-full max-w-sm items-start gap-3 rounded-lg border border-line bg-surface px-4 py-3 shadow-lg"
          >
            {t.tone === "error" ? (
              <IconXCircle size={18} className="mt-0.5 shrink-0 text-[var(--st-red-dot)]" />
            ) : t.tone === "success" ? (
              <IconCheckCircle size={18} className="mt-0.5 shrink-0 text-[var(--st-green-dot)]" />
            ) : (
              <IconInfo size={18} className="mt-0.5 shrink-0 text-accent" />
            )}
            <div className="min-w-0 flex-1 text-sm">
              <p className="font-medium text-fg">{t.title}</p>
              {t.description && <p className="mt-0.5 text-[13px] break-words text-fg-2">{t.description}</p>}
              {t.link && (
                <a
                  href={t.link.to}
                  onClick={(e) => {
                    e.preventDefault();
                    if (t.link) router.history.push(t.link.to);
                    dismiss(t.id);
                  }}
                  className={cx("mt-1 inline-block text-[13px] font-medium text-accent hover:underline")}
                >
                  {t.link.label}
                </a>
              )}
            </div>
            <button
              type="button"
              onClick={() => dismiss(t.id)}
              aria-label="Dismiss notification"
              className="-mr-1 rounded p-0.5 text-muted hover:text-fg"
            >
              <IconX size={14} />
            </button>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}
