import { useQueryClient } from "@tanstack/react-query";
import { Link, Outlet, useNavigate, useParams, useRouterState } from "@tanstack/react-router";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { api } from "../api/client";
import type { Me } from "../api/types";
import { cx } from "../components/ui/cx";
import {
  IconBolt,
  IconEuro,
  IconHome,
  IconKey,
  IconList,
  IconLogout,
  IconMenu,
  IconMonitor,
  IconMoon,
  IconScroll,
  IconServer,
  IconSun,
  IconUser,
  IconUsers,
  IconX,
} from "../components/ui/icons";
import { Select } from "../components/ui/Select";
import { useMe } from "../lib/queries";
import { atLeast, roleIn } from "../lib/roles";
import { readStorage, STORAGE_KEYS } from "../lib/storage";
import { setTheme, useThemeChoice, type ThemeChoice } from "../lib/theme";

/** The workspace in the URL, or the last one used. */
export function useCurrentWorkspace(me: Me | undefined): string | undefined {
  const params = useParams({ strict: false }) as { ws?: string };
  if (params.ws) return params.ws;
  const last = readStorage(STORAGE_KEYS.workspace);
  if (me && last && me.workspaces.some((w) => w.id === last)) return last;
  return me?.workspaces[0]?.id;
}

export function AppLayout() {
  const [drawerOpen, setDrawerOpen] = useState(false);
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const [lastPath, setLastPath] = useState(pathname);
  // Close the drawer whenever the page changes.
  if (pathname !== lastPath) {
    setLastPath(pathname);
    if (drawerOpen) setDrawerOpen(false);
  }

  useEffect(() => {
    if (!drawerOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setDrawerOpen(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [drawerOpen]);

  return (
    <div className="min-h-dvh md:flex">
      <a
        href="#main"
        className="sr-only z-50 rounded bg-surface px-3 py-2 focus:not-sr-only focus:fixed focus:top-2 focus:left-2"
      >
        Skip to content
      </a>

      {/* Mobile top bar */}
      <div className="sticky top-0 z-30 flex h-12 items-center gap-2 border-b border-line bg-surface px-3 md:hidden">
        <button
          type="button"
          onClick={() => setDrawerOpen(true)}
          aria-label="Open navigation"
          aria-expanded={drawerOpen}
          aria-controls="sidebar"
          className="rounded-md p-1.5 text-fg-2 hover:bg-surface-2"
        >
          <IconMenu size={20} />
        </button>
        <Brand />
      </div>

      {/* Desktop sidebar */}
      <aside className="hidden w-60 shrink-0 border-r border-line bg-surface md:block">
        <div className="sticky top-0 flex h-dvh flex-col">
          <Sidebar />
        </div>
      </aside>

      {/* Mobile drawer */}
      {drawerOpen && (
        <div className="fixed inset-0 z-40 md:hidden" role="dialog" aria-modal="true" aria-label="Navigation">
          <div className="absolute inset-0 bg-black/40" onClick={() => setDrawerOpen(false)} />
          <div id="sidebar" className="absolute inset-y-0 left-0 flex w-72 max-w-[85vw] flex-col bg-surface shadow-xl">
            <button
              type="button"
              onClick={() => setDrawerOpen(false)}
              aria-label="Close navigation"
              className="absolute top-3 right-3 rounded-md p-1 text-muted hover:bg-surface-2 hover:text-fg"
            >
              <IconX size={18} />
            </button>
            <Sidebar autoFocus />
          </div>
        </div>
      )}

      <main id="main" className="min-w-0 flex-1" tabIndex={-1}>
        <Outlet />
      </main>
    </div>
  );
}

function Brand() {
  return (
    <Link to="/" className="flex items-center gap-2 rounded px-1 text-[15px] font-semibold tracking-tight text-fg">
      <svg width="20" height="20" viewBox="0 0 32 32" aria-hidden="true">
        <rect width="32" height="32" rx="7" fill="var(--accent)" />
        <path d="M16 6v12" stroke="#fff" strokeWidth="2.5" strokeLinecap="round" />
        <circle cx="16" cy="22" r="4.5" fill="#fff" />
      </svg>
      Pendolo
    </Link>
  );
}

const WS_SECTIONS = ["automations", "runs", "costs", "secrets", "members", "audit"] as const;

function Sidebar({ autoFocus }: { autoFocus?: boolean }) {
  const { data: me } = useMe();
  const ws = useCurrentWorkspace(me);
  const role = ws ? roleIn(me, ws) : undefined;
  const navigate = useNavigate();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const firstRef = useRef<HTMLSelectElement>(null);

  useEffect(() => {
    if (autoFocus) firstRef.current?.focus();
  }, [autoFocus]);

  const switchWorkspace = (next: string) => {
    // Keep the section (runs, costs...) but drop ids that belong to the old workspace.
    const m = /^\/w\/[^/]+\/([^/]+)/.exec(pathname);
    const section = WS_SECTIONS.find((s) => s === m?.[1]);
    if (section) void navigate({ to: `/w/$ws/${section}`, params: { ws: next } });
    else void navigate({ to: "/w/$ws", params: { ws: next } });
  };

  return (
    <>
      <div className="flex h-14 shrink-0 items-center px-4">
        <Brand />
      </div>

      <nav aria-label="Main" className="flex-1 overflow-y-auto px-3 pb-3">
        {me && me.workspaces.length > 0 && ws && (
          <div className="mb-4">
            <label htmlFor="ws-switcher" className="mb-1 block px-1 text-[11px] font-medium tracking-wide text-muted uppercase">
              Workspace
            </label>
            <Select
              id="ws-switcher"
              ref={firstRef}
              compact
              value={ws}
              onChange={(e) => switchWorkspace(e.target.value)}
            >
              {me.workspaces.map((w) => (
                <option key={w.id} value={w.id}>
                  {w.name}
                </option>
              ))}
              {!me.workspaces.some((w) => w.id === ws) && <option value={ws}>{ws}</option>}
            </Select>
            {role && <p className="mt-1 px-1 text-[11px] text-muted">Your role: {role}</p>}
          </div>
        )}

        {ws && role && (
          <ul className="space-y-0.5">
            <NavItem to="/w/$ws" ws={ws} exact icon={<IconHome size={16} />}>
              Overview
            </NavItem>
            <NavItem to="/w/$ws/automations" ws={ws} icon={<IconBolt size={16} />}>
              Automations
            </NavItem>
            <NavItem to="/w/$ws/runs" ws={ws} icon={<IconList size={16} />}>
              Runs
            </NavItem>
            <NavItem to="/w/$ws/costs" ws={ws} icon={<IconEuro size={16} />}>
              Costs
            </NavItem>
            {atLeast(role, "builder") && (
              <NavItem to="/w/$ws/secrets" ws={ws} icon={<IconKey size={16} />}>
                Secrets
              </NavItem>
            )}
            {atLeast(role, "admin") && (
              <NavItem to="/w/$ws/members" ws={ws} icon={<IconUsers size={16} />}>
                Members
              </NavItem>
            )}
            {atLeast(role, "admin") && (
              <NavItem to="/w/$ws/audit" ws={ws} icon={<IconScroll size={16} />}>
                Audit
              </NavItem>
            )}
          </ul>
        )}

        {me?.isOwner && (
          <>
            <p className="mt-6 mb-1 px-1 text-[11px] font-medium tracking-wide text-muted uppercase">Instance</p>
            <ul className="space-y-0.5">
              <NavItem to="/admin/users" icon={<IconUsers size={16} />}>
                Users &amp; workspaces
              </NavItem>
              <NavItem to="/admin/system" icon={<IconServer size={16} />}>
                System
              </NavItem>
            </ul>
          </>
        )}
      </nav>

      <div className="shrink-0 space-y-1 border-t border-line p-3">
        <ul>
          <NavItem to="/account" icon={<IconUser size={16} />}>
            <span className="min-w-0 truncate">{me?.name || me?.email || "Account"}</span>
          </NavItem>
        </ul>
        <ThemeSwitch />
        <SignOutButton />
      </div>
    </>
  );
}

const itemClass =
  "flex items-center gap-2.5 rounded-md px-2 py-1.5 text-[13.5px] text-fg-2 hover:bg-surface-2 hover:text-fg";
const activeClass = "bg-surface-2 font-medium text-fg";

type WsTo =
  | "/w/$ws"
  | "/w/$ws/automations"
  | "/w/$ws/runs"
  | "/w/$ws/costs"
  | "/w/$ws/secrets"
  | "/w/$ws/members"
  | "/w/$ws/audit";

function NavItem(
  props:
    | { to: WsTo; ws: string; exact?: boolean; icon: ReactNode; children: ReactNode }
    | { to: "/admin/users" | "/admin/system" | "/account"; ws?: undefined; exact?: boolean; icon: ReactNode; children: ReactNode },
) {
  const inner = (
    <>
      <span className="shrink-0 text-muted">{props.icon}</span>
      {props.children}
    </>
  );
  return (
    <li>
      {props.ws !== undefined ? (
        <Link
          to={props.to}
          params={{ ws: props.ws }}
          className={itemClass}
          activeOptions={{ exact: props.exact ?? false, includeSearch: false }}
          activeProps={{ className: activeClass, "aria-current": "page" }}
        >
          {inner}
        </Link>
      ) : (
        <Link
          to={props.to}
          className={itemClass}
          activeOptions={{ includeSearch: false }}
          activeProps={{ className: activeClass, "aria-current": "page" }}
        >
          {inner}
        </Link>
      )}
    </li>
  );
}

const THEMES: { value: ThemeChoice; label: string; icon: ReactNode }[] = [
  { value: "system", label: "System theme", icon: <IconMonitor size={14} /> },
  { value: "light", label: "Light theme", icon: <IconSun size={14} /> },
  { value: "dark", label: "Dark theme", icon: <IconMoon size={14} /> },
];

function ThemeSwitch() {
  const choice = useThemeChoice();
  return (
    <div className="flex items-center justify-between px-2 py-1">
      <span className="text-[13px] text-fg-2" id="theme-label">
        Theme
      </span>
      <div role="group" aria-labelledby="theme-label" className="flex rounded-md border border-line bg-surface-2 p-0.5">
        {THEMES.map((t) => (
          <button
            key={t.value}
            type="button"
            aria-pressed={choice === t.value}
            aria-label={t.label}
            title={t.label}
            onClick={() => setTheme(t.value)}
            className={cx(
              "rounded p-1",
              choice === t.value ? "bg-surface text-fg shadow-xs" : "text-muted hover:text-fg",
            )}
          >
            {t.icon}
          </button>
        ))}
      </div>
    </div>
  );
}

function SignOutButton() {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);
  return (
    <button
      type="button"
      disabled={busy}
      className={cx(itemClass, "w-full")}
      onClick={async () => {
        setBusy(true);
        try {
          await api.logout();
        } catch {
          // Signed out already, or the server is down: either way leave the session.
        }
        queryClient.clear();
        setBusy(false);
        void navigate({ to: "/login" });
      }}
    >
      <span className="text-muted">
        <IconLogout size={16} />
      </span>
      Sign out
    </button>
  );
}
