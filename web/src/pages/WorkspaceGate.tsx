import { Link, Outlet, useParams } from "@tanstack/react-router";
import { useEffect } from "react";
import { buttonClass } from "../components/ui/Button";
import { Page } from "../components/ui/Page";
import { EmptyState } from "../components/ui/States";
import { useMe } from "../lib/queries";
import { roleIn } from "../lib/roles";
import { STORAGE_KEYS, writeStorage } from "../lib/storage";

export function useWs(): string {
  const { ws } = useParams({ strict: false }) as { ws?: string };
  return ws ?? "";
}

/** Checks membership and remembers the workspace for next time. */
export function WorkspaceGate() {
  const ws = useWs();
  const { data: me } = useMe();
  const role = roleIn(me, ws);

  useEffect(() => {
    if (role) writeStorage(STORAGE_KEYS.workspace, ws);
  }, [role, ws]);

  if (!role) {
    return (
      <Page>
        <div className="mt-16 rounded-lg border border-dashed border-line-strong bg-surface">
          <EmptyState
            title="Workspace not found"
            hint={`You are not a member of “${ws}”, or it does not exist. Ask an owner to add you.`}
            action={
              <Link to="/" className={buttonClass("secondary", "sm")}>
                Go to your workspaces
              </Link>
            }
          />
        </div>
      </Page>
    );
  }
  return <Outlet />;
}

export function useWsRole() {
  const ws = useWs();
  const { data: me } = useMe();
  return roleIn(me, ws);
}
