import { Link, Navigate } from "@tanstack/react-router";
import { buttonClass } from "../components/ui/Button";
import { IconBolt } from "../components/ui/icons";
import { Page } from "../components/ui/Page";
import { EmptyState } from "../components/ui/States";
import { useCurrentWorkspace } from "../layout/AppLayout";
import { useMe } from "../lib/queries";

/** "/" goes to the last workspace used, or explains how to get one. */
export function HomeRedirect() {
  const { data: me } = useMe();
  const ws = useCurrentWorkspace(me);
  if (ws) return <Navigate to="/w/$ws" params={{ ws }} replace />;
  return (
    <Page>
      <div className="mt-16 rounded-lg border border-dashed border-line-strong bg-surface">
        {me?.isOwner ? (
          <EmptyState
            icon={<IconBolt size={28} />}
            title="No workspaces yet"
            hint="A workspace groups automations, runs, secrets and members. Create the first one to get started."
            action={
              <Link to="/admin/users" className={buttonClass("primary", "md")}>
                Create a workspace
              </Link>
            }
          />
        ) : (
          <EmptyState
            icon={<IconBolt size={28} />}
            title="You are not in any workspace yet"
            hint="Ask an owner to add you to a workspace. You will see it here as soon as they do."
          />
        )}
      </div>
    </Page>
  );
}
