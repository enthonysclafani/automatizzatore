import type { ReactNode } from "react";
import { setTheme, useResolvedTheme } from "../lib/theme";

export function AuthShell({ title, subtitle, children }: { title: string; subtitle?: ReactNode; children: ReactNode }) {
  const theme = useResolvedTheme();
  return (
    <div className="flex min-h-dvh flex-col items-center justify-center px-4 py-10">
      <div className="w-full max-w-sm">
        <div className="mb-6 flex items-center justify-center gap-2 text-lg font-semibold tracking-tight text-fg">
          <svg width="26" height="26" viewBox="0 0 32 32" aria-hidden="true">
            <rect width="32" height="32" rx="7" fill="var(--accent)" />
            <path d="M16 6v12" stroke="#fff" strokeWidth="2.5" strokeLinecap="round" />
            <circle cx="16" cy="22" r="4.5" fill="#fff" />
          </svg>
          Pendolo
        </div>
        <div className="rounded-lg border border-line bg-surface p-6 shadow-sm">
          <h1 className="text-lg font-semibold text-fg">{title}</h1>
          {subtitle && <div className="mt-1 text-[13px] text-fg-2">{subtitle}</div>}
          <div className="mt-5">{children}</div>
        </div>
        <div className="mt-4 text-center">
          <button
            type="button"
            className="text-[12px] text-muted hover:text-fg"
            onClick={() => setTheme(theme === "dark" ? "light" : "dark")}
          >
            {theme === "dark" ? "Use light theme" : "Use dark theme"}
          </button>
        </div>
      </div>
    </div>
  );
}
