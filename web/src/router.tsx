import type { QueryClient } from "@tanstack/react-query";
import {
  createRootRouteWithContext,
  createRoute,
  createRouter,
  lazyRouteComponent,
  Outlet,
  redirect,
} from "@tanstack/react-router";
import { isApiError } from "./api/client";
import type { RunStatus } from "./api/types";
import { ToastProvider } from "./components/ui/Toast";
import { AppLayout } from "./layout/AppLayout";
import { COST_PERIODS, DEFAULT_COST_PERIOD, RUN_STATUSES, safeRedirect, type CostPeriod, type RunsSearch } from "./lib/nav";
import { meQuery, setupQuery } from "./lib/queries";
import { SystemPage } from "./pages/admin/System";
import { UsersPage } from "./pages/admin/Users";
import { ApprovalsPage } from "./pages/Approvals";
import { AuditPage } from "./pages/Audit";
import { AutomationDetailPage } from "./pages/AutomationDetail";
import { AutomationsPage } from "./pages/Automations";
import { ConnectionsPage } from "./pages/Connections";
import { HomeRedirect } from "./pages/HomeRedirect";
import { LoginPage } from "./pages/Login";
import { MembersPage } from "./pages/Members";
import { NotFoundPage } from "./pages/NotFound";
import { RunDetailPage } from "./pages/RunDetail";
import { RunsPage } from "./pages/Runs";
import { SecretsPage } from "./pages/Secrets";
import { SettingsPage } from "./pages/Settings";
import { SetupPage } from "./pages/Setup";
import { WorkspaceGate } from "./pages/WorkspaceGate";

export interface RouterContext {
  queryClient: QueryClient;
}

const rootRoute = createRootRouteWithContext<RouterContext>()({
  component: () => (
    <ToastProvider>
      <Outlet />
    </ToastProvider>
  ),
  notFoundComponent: NotFoundPage,
});

const setupRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/setup",
  component: SetupPage,
});

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/login",
  validateSearch: (s: Record<string, unknown>): { redirect?: string; error?: string } => {
    const out: { redirect?: string; error?: string } = {};
    const r = safeRedirect(s.redirect);
    if (r) out.redirect = r;
    // The server sends single sign-on failures back as /login?error=<message>.
    if (typeof s.error === "string" && s.error) out.error = s.error.slice(0, 300);
    return out;
  },
  component: LoginPage,
});

/** Everything behind sign-in. */
const appRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: "_app",
  beforeLoad: async ({ context, location }) => {
    const setup = await context.queryClient.ensureQueryData(setupQuery).catch(() => undefined);
    if (setup?.needsSetup) throw redirect({ to: "/setup" });
    try {
      const me = await context.queryClient.ensureQueryData(meQuery);
      return { me };
    } catch (err) {
      if (isApiError(err) && err.status === 401) {
        throw redirect({ to: "/login", search: { redirect: safeRedirect(location.href) } });
      }
      throw err;
    }
  },
  component: AppLayout,
});

const homeRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/",
  component: HomeRedirect,
});

const wsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "w/$ws",
  component: WorkspaceGate,
});

const overviewRoute = createRoute({
  getParentRoute: () => wsRoute,
  path: "/",
  // Charts (recharts) load only with the pages that draw them.
  component: lazyRouteComponent(() => import("./pages/Overview"), "OverviewPage"),
});

const automationsRoute = createRoute({
  getParentRoute: () => wsRoute,
  path: "automations",
  component: AutomationsPage,
});

const automationRoute = createRoute({
  getParentRoute: () => wsRoute,
  path: "automations/$name",
  component: AutomationDetailPage,
});

const runsRoute = createRoute({
  getParentRoute: () => wsRoute,
  path: "runs",
  validateSearch: (s: Record<string, unknown>): RunsSearch => {
    const out: RunsSearch = {};
    if (typeof s.automation === "string" && s.automation) out.automation = s.automation;
    if (typeof s.status === "string" && (RUN_STATUSES as string[]).includes(s.status)) {
      out.status = s.status as RunStatus;
    }
    return out;
  },
  component: RunsPage,
});

const runRoute = createRoute({
  getParentRoute: () => wsRoute,
  path: "runs/$id",
  component: RunDetailPage,
});

const costsRoute = createRoute({
  getParentRoute: () => wsRoute,
  path: "costs",
  validateSearch: (s: Record<string, unknown>): { days?: CostPeriod } => {
    const d = Number(s.days);
    return (COST_PERIODS as readonly number[]).includes(d) && d !== DEFAULT_COST_PERIOD ? { days: d as CostPeriod } : {};
  },
  component: lazyRouteComponent(() => import("./pages/Costs"), "CostsPage"),
});

const secretsRoute = createRoute({
  getParentRoute: () => wsRoute,
  path: "secrets",
  component: SecretsPage,
});

const membersRoute = createRoute({
  getParentRoute: () => wsRoute,
  path: "members",
  component: MembersPage,
});

const auditRoute = createRoute({
  getParentRoute: () => wsRoute,
  path: "audit",
  component: AuditPage,
});

const approvalsRoute = createRoute({
  getParentRoute: () => wsRoute,
  path: "approvals",
  component: ApprovalsPage,
});

const connectionsRoute = createRoute({
  getParentRoute: () => wsRoute,
  path: "connections",
  // The OAuth callback comes back with ?connected=<name> or ?error=<message>; the page shows it and clears it.
  validateSearch: (s: Record<string, unknown>): { connected?: string; error?: string } => {
    const out: { connected?: string; error?: string } = {};
    if (typeof s.connected === "string" && s.connected) out.connected = s.connected.slice(0, 100);
    if (typeof s.error === "string" && s.error) out.error = s.error.slice(0, 300);
    return out;
  },
  component: ConnectionsPage,
});

const settingsRoute = createRoute({
  getParentRoute: () => wsRoute,
  path: "settings",
  component: SettingsPage,
});

const usersRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "admin/users",
  component: UsersPage,
});

const systemRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "admin/system",
  component: SystemPage,
});

const accountRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "account",
  component: lazyRouteComponent(() => import("./pages/Account"), "AccountPage"),
});

const routeTree = rootRoute.addChildren([
  setupRoute,
  loginRoute,
  appRoute.addChildren([
    homeRoute,
    wsRoute.addChildren([
      overviewRoute,
      automationsRoute,
      automationRoute,
      runsRoute,
      runRoute,
      costsRoute,
      secretsRoute,
      membersRoute,
      auditRoute,
      approvalsRoute,
      connectionsRoute,
      settingsRoute,
    ]),
    usersRoute,
    systemRoute,
    accountRoute,
  ]),
]);

export function makeRouter(queryClient: QueryClient) {
  return createRouter({
    routeTree,
    context: { queryClient },
    defaultPreload: "intent",
    defaultPreloadStaleTime: 0,
    scrollRestoration: true,
  });
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof makeRouter>;
  }
}
