import { useQuery, useQueryClient } from "@tanstack/react-query";
import { getRouteApi } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { api, errorMessage } from "../api/client";
import type { Connection, ConnectorKindInfo } from "../api/types";
import { ConnectionDialog } from "../components/domain/ConnectionDialog";
import { ActionList, ConnectionStatusBadge } from "../components/domain/Connections";
import { Tag } from "../components/ui/Badge";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { ConfirmDialog } from "../components/ui/Dialog";
import {
  IconAlert,
  IconCheckCircle,
  IconExternal,
  IconPencil,
  IconPlug,
  IconPlus,
  IconTrash,
  IconX,
} from "../components/ui/icons";
import { Page, PageHeader } from "../components/ui/Page";
import { EmptyState, ErrorState, Skeleton } from "../components/ui/States";
import { RelativeTime } from "../components/ui/Time";
import { useToast } from "../components/ui/Toast";
import { connectionsQuery, connectorsQuery, keys } from "../lib/queries";
import { atLeast } from "../lib/roles";
import { useWs, useWsRole } from "./WorkspaceGate";

const route = getRouteApi("/_app/w/$ws/connections");

type Notice = { tone: "success" | "error"; text: string };

export function ConnectionsPage() {
  const ws = useWs();
  const role = useWsRole();
  const canEdit = atLeast(role, "admin");
  const canTest = atLeast(role, "builder");
  const q = useQuery(connectionsQuery(ws));
  const catalogQ = useQuery(connectorsQuery);
  const queryClient = useQueryClient();
  const toast = useToast();
  const search = route.useSearch();
  const navigate = route.useNavigate();

  // The OAuth callback sends the browser back with ?connected=<name> or ?error=<message>.
  const [notice, setNotice] = useState<Notice | null>(() =>
    search.connected
      ? { tone: "success", text: `Connected ${search.connected}. Automations can use it now.` }
      : search.error
        ? { tone: "error", text: search.error }
        : null,
  );
  useEffect(() => {
    if (search.connected === undefined && search.error === undefined) return;
    if (search.connected) void queryClient.invalidateQueries({ queryKey: keys.connections(ws) });
    void navigate({ search: {}, replace: true });
  }, [search.connected, search.error, navigate, queryClient, ws]);

  const [editing, setEditing] = useState<{ existing?: Connection } | null>(null);
  const [deleting, setDeleting] = useState<Connection | null>(null);

  const connections = [...(q.data?.connections ?? [])].sort((a, b) => a.name.localeCompare(b.name));
  const names = connections.map((c) => c.name);
  const catalog = catalogQ.data;
  const kindOf = (c: Connection) => catalog?.kinds.find((k) => k.kind === c.kind);

  const signIn = async (name: string) => {
    const { url } = await api.startOAuth(ws, name);
    window.location.assign(url);
  };

  return (
    <Page>
      <PageHeader
        title="Connections"
        description={
          <>
            Accounts and servers automations can act on, with <code className="rounded bg-surface-2 px-1 font-mono text-[12.5px]">connection: &lt;name&gt;</code>.
            What they may do also depends on the automation's autonomy.
          </>
        }
        actions={
          canEdit && (
            <Button
              variant="primary"
              icon={<IconPlus size={14} />}
              disabled={!catalog}
              loading={catalogQ.isPending}
              onClick={() => setEditing({})}
            >
              Add connection
            </Button>
          )
        }
      />

      {notice && (
        <div
          role={notice.tone === "error" ? "alert" : "status"}
          className={
            notice.tone === "error"
              ? "mb-4 flex items-start gap-3 rounded-lg border border-[var(--st-red-dot)]/40 bg-[var(--st-red-bg)] px-4 py-3"
              : "mb-4 flex items-start gap-3 rounded-lg border border-[var(--st-green-dot)]/40 bg-[var(--st-green-bg)] px-4 py-3"
          }
        >
          {notice.tone === "error" ? (
            <IconAlert size={16} className="mt-0.5 shrink-0 text-[var(--st-red-dot)]" />
          ) : (
            <IconCheckCircle size={16} className="mt-0.5 shrink-0 text-[var(--st-green-dot)]" />
          )}
          <div className="min-w-0 flex-1 text-[13px] text-fg">
            {notice.tone === "error" && <p className="font-medium text-[var(--st-red-fg)]">Sign-in did not finish</p>}
            <p className="break-words">{notice.text}</p>
            {notice.tone === "error" && <p className="mt-0.5 text-fg-2">Check the setup steps, then use Reconnect on the connection.</p>}
          </div>
          <button
            type="button"
            onClick={() => setNotice(null)}
            aria-label="Dismiss"
            className="-mr-1 rounded p-0.5 text-muted hover:text-fg"
          >
            <IconX size={14} />
          </button>
        </div>
      )}

      {catalogQ.isError && canEdit && (
        <div role="alert" className="mb-4 rounded-lg border border-line bg-surface px-4 py-3 text-[13px] text-fg-2">
          Could not load the list of connectors: {errorMessage(catalogQ.error)}{" "}
          <button type="button" className="font-medium text-accent hover:underline" onClick={() => void catalogQ.refetch()}>
            Retry
          </button>
        </div>
      )}

      {q.isPending ? (
        <div className="space-y-3" aria-busy="true" aria-label="Loading connections">
          {Array.from({ length: 2 }, (_, i) => (
            <div key={i} className="space-y-3 rounded-lg border border-line bg-surface p-4">
              <Skeleton className="h-4 w-40" />
              <Skeleton className="h-3 w-64 max-w-full" />
              <Skeleton className="h-20 w-full" />
            </div>
          ))}
        </div>
      ) : q.isError ? (
        <Card>
          <ErrorState error={q.error} onRetry={() => void q.refetch()} />
        </Card>
      ) : connections.length === 0 ? (
        <Card>
          <EmptyState
            icon={<IconPlug size={24} />}
            title="No connections yet"
            hint={
              canEdit
                ? "Connect Google, Slack, web search or an MCP server so automations can read and act on them."
                : "An admin can connect Google, Slack, web search or an MCP server here."
            }
            action={
              canEdit && catalog ? (
                <Button variant="primary" icon={<IconPlus size={14} />} onClick={() => setEditing({})}>
                  Add connection
                </Button>
              ) : undefined
            }
          />
        </Card>
      ) : (
        <ul className="space-y-3">
          {connections.map((c) => (
            <li key={c.name}>
              <ConnectionCard
                ws={ws}
                conn={c}
                kind={kindOf(c)}
                canEdit={canEdit}
                canTest={canTest}
                canEditNow={Boolean(catalog)}
                onEdit={() => setEditing({ existing: c })}
                onDelete={() => setDeleting(c)}
                onSignIn={signIn}
              />
            </li>
          ))}
        </ul>
      )}

      {editing && catalog && (
        <ConnectionDialog
          ws={ws}
          catalog={catalog}
          existing={editing.existing}
          names={names}
          onClose={() => setEditing(null)}
          onSignIn={signIn}
        />
      )}
      <ConfirmDialog
        open={deleting !== null}
        onClose={() => setDeleting(null)}
        title={`Delete ${deleting?.name ?? ""}?`}
        danger
        confirmLabel="Delete connection"
        message={
          <>
            Automations that use <code className="font-mono">connection: {deleting?.name}</code> will fail until you add
            it again. Saved credentials are deleted. This cannot be undone.
          </>
        }
        onConfirm={async () => {
          if (!deleting) return;
          await api.deleteConnection(ws, deleting.name);
          await queryClient.invalidateQueries({ queryKey: keys.connections(ws) });
          toast({ tone: "success", title: `Deleted ${deleting.name}` });
        }}
      />
    </Page>
  );
}

function ConnectionCard({
  ws,
  conn,
  kind,
  canEdit,
  canTest,
  canEditNow,
  onEdit,
  onDelete,
  onSignIn,
}: {
  ws: string;
  conn: Connection;
  kind: ConnectorKindInfo | undefined;
  canEdit: boolean;
  canTest: boolean;
  canEditNow: boolean;
  onEdit: () => void;
  onDelete: () => void;
  onSignIn: (name: string) => Promise<void>;
}) {
  const [testing, setTesting] = useState(false);
  const [signingIn, setSigningIn] = useState(false);
  const queryClient = useQueryClient();
  const toast = useToast();
  const headingId = `conn-${conn.name}`;
  const label = kind?.label ?? conn.kind;
  const oauth = kind?.oauth ?? false;

  const test = async () => {
    setTesting(true);
    try {
      const r = await api.testConnection(ws, conn.name);
      toast(
        r.ok
          ? { tone: "success", title: `${conn.name} works`, description: r.detail }
          : { tone: "error", title: `${conn.name} test failed`, description: r.detail || "No detail from the server." },
      );
    } catch (err) {
      toast({ tone: "error", title: `Could not test ${conn.name}`, description: errorMessage(err) });
    } finally {
      setTesting(false);
      // A test may change the status (e.g. an expired sign-in).
      void queryClient.invalidateQueries({ queryKey: keys.connections(ws) });
    }
  };

  const signIn = async () => {
    setSigningIn(true);
    try {
      await onSignIn(conn.name);
    } catch (err) {
      toast({ tone: "error", title: "Could not start the sign-in", description: errorMessage(err) });
      setSigningIn(false);
    }
  };

  return (
    <article aria-labelledby={headingId} className="space-y-3 rounded-lg border border-line bg-surface p-4">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 id={headingId} className="flex flex-wrap items-center gap-2">
            <span className="font-mono text-[15px] font-semibold break-all text-fg">{conn.name}</span>
            <Tag>{label}</Tag>
            <ConnectionStatusBadge status={conn.status} />
          </h2>
          <p className="mt-1 text-[12.5px] text-fg-2">
            {conn.account && (
              <>
                <span className="text-fg">{conn.account}</span>
                {" · "}
              </>
            )}
            Updated <RelativeTime iso={conn.updatedAt} /> by {conn.updatedBy}
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          {canEdit && oauth && (
            <Button
              size="sm"
              variant={conn.status === "needs_auth" ? "primary" : "secondary"}
              icon={<IconExternal size={14} />}
              loading={signingIn}
              onClick={() => void signIn()}
            >
              {conn.status === "needs_auth" ? `Sign in with ${label}` : "Reconnect"}
            </Button>
          )}
          {canTest && (
            <Button size="sm" loading={testing} onClick={() => void test()} aria-label={`Test ${conn.name}`}>
              Test
            </Button>
          )}
          {canEdit && (
            <>
              <Button
                size="sm"
                variant="ghost"
                icon={<IconPencil size={14} />}
                onClick={onEdit}
                disabled={!canEditNow}
                aria-label={`Edit ${conn.name}`}
              >
                Edit
              </Button>
              <Button
                size="sm"
                variant="ghost"
                icon={<IconTrash size={14} />}
                onClick={onDelete}
                aria-label={`Delete ${conn.name}`}
              >
                Delete
              </Button>
            </>
          )}
        </div>
      </header>

      {conn.status === "error" && (
        <div className="rounded-md border border-[var(--st-red-dot)]/40 bg-[var(--st-red-bg)] px-3 py-2 text-[13px]">
          <p className="font-medium text-[var(--st-red-fg)]">The connection does not work</p>
          <p className="mt-0.5 break-words whitespace-pre-wrap text-fg">{conn.error || "No detail from the server."}</p>
          <p className="mt-0.5 text-fg-2">
            {canEdit ? "Check its settings with Edit" : "Ask an admin to check its settings"}
            {oauth && canEdit ? ", or use Reconnect." : "."}
          </p>
        </div>
      )}
      {conn.status === "needs_auth" && (
        <p className="rounded-md border border-[var(--st-amber-dot)]/40 bg-[var(--st-amber-bg)] px-3 py-2 text-[13px] text-fg">
          Saved, but nobody has signed in yet. {canEdit ? `Use “Sign in with ${label}” to finish.` : "Ask an admin to sign in."}
        </p>
      )}

      <div>
        <h3 className="mb-1.5 text-[12.5px] font-medium text-fg-2">
          Actions{conn.actions.length > 0 ? ` (${conn.actions.length})` : ""}
        </h3>
        <ActionList
          actions={conn.actions}
          empty={
            conn.kind === "mcp"
              ? "No tools found: the server could not be reached. Use Test to see why."
              : "This connection offers no actions."
          }
        />
      </div>
    </article>
  );
}
