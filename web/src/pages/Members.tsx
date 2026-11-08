import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, errorMessage } from "../api/client";
import type { Member, Role } from "../api/types";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { ConfirmDialog, Dialog } from "../components/ui/Dialog";
import { IconPlus, IconUsers } from "../components/ui/icons";
import { Field, FormError, Input } from "../components/ui/Input";
import { Page, PageHeader } from "../components/ui/Page";
import { Select } from "../components/ui/Select";
import { EmptyState, ErrorState, SkeletonRows } from "../components/ui/States";
import { Table, Td, Th, THead, Tr } from "../components/ui/Table";
import { useToast } from "../components/ui/Toast";
import { keys, useMe } from "../lib/queries";
import { atLeast, ROLE_HINT, ROLES } from "../lib/roles";
import { useWs, useWsRole } from "./WorkspaceGate";

export function MembersPage() {
  const ws = useWs();
  const role = useWsRole();
  const { data: me } = useMe();
  const canAdmin = atLeast(role, "admin");
  const q = useQuery({ queryKey: keys.members(ws), queryFn: () => api.listMembers(ws), enabled: canAdmin });
  const queryClient = useQueryClient();
  const toast = useToast();
  const [adding, setAdding] = useState(false);
  const [removing, setRemoving] = useState<Member | null>(null);
  const [pending, setPending] = useState<string | null>(null);

  const members = [...(q.data?.members ?? [])].sort((a, b) => (a.name || a.email).localeCompare(b.name || b.email));

  const changeRole = async (m: Member, next: Role) => {
    setPending(m.userId);
    try {
      await api.putMember(ws, m.email, next);
      await queryClient.invalidateQueries({ queryKey: keys.members(ws) });
      if (m.userId === me?.id) await queryClient.invalidateQueries({ queryKey: keys.me });
      toast({ tone: "success", title: `${m.name || m.email} is now ${next}` });
    } catch (err) {
      toast({ tone: "error", title: "Could not change the role", description: errorMessage(err) });
    } finally {
      setPending(null);
    }
  };

  return (
    <Page>
      <PageHeader
        title="Members"
        description="Who can see and change this workspace. Instance owners can always access every workspace."
        actions={
          canAdmin && (
            <Button variant="primary" icon={<IconPlus size={14} />} onClick={() => setAdding(true)}>
              Add member
            </Button>
          )
        }
      />
      <Card>
        {!canAdmin ? (
          <EmptyState title="No access" hint="Only workspace admins can manage members." />
        ) : q.isPending ? (
          <SkeletonRows rows={4} />
        ) : q.isError ? (
          <ErrorState error={q.error} onRetry={() => void q.refetch()} />
        ) : members.length === 0 ? (
          <EmptyState
            icon={<IconUsers size={24} />}
            title="No members yet"
            hint="Add people by the email of their Pendolo account. An owner creates accounts under Users & workspaces."
          />
        ) : (
          <Table label="Members">
            <THead>
              <tr>
                <Th>Name</Th>
                <Th>Email</Th>
                <Th>Role</Th>
                <Th className="text-right">
                  <span className="sr-only">Actions</span>
                </Th>
              </tr>
            </THead>
            <tbody>
              {members.map((m) => (
                <Tr key={m.userId}>
                  <Td className="font-medium text-fg">
                    {m.name || <span className="text-muted">–</span>}
                    {m.userId === me?.id && <span className="ml-1.5 text-[12px] font-normal text-muted">(you)</span>}
                  </Td>
                  <Td className="max-w-[16rem] truncate text-fg-2">{m.email}</Td>
                  <Td>
                    <Select
                      compact
                      className="w-32"
                      aria-label={`Role of ${m.name || m.email}`}
                      value={m.role}
                      disabled={pending === m.userId}
                      onChange={(e) => void changeRole(m, e.target.value as Role)}
                    >
                      {ROLES.map((r) => (
                        <option key={r} value={r}>
                          {r}
                        </option>
                      ))}
                    </Select>
                  </Td>
                  <Td className="text-right">
                    <Button size="sm" variant="ghost" onClick={() => setRemoving(m)}>
                      Remove
                    </Button>
                  </Td>
                </Tr>
              ))}
            </tbody>
          </Table>
        )}
      </Card>

      {canAdmin && (
        <div className="mt-4 rounded-lg border border-line bg-surface px-4 py-3">
          <p className="mb-2 text-[12.5px] font-medium text-fg-2">Roles</p>
          <dl className="grid grid-cols-1 gap-x-6 gap-y-1 text-[12.5px] sm:grid-cols-2">
            {ROLES.map((r) => (
              <div key={r} className="flex gap-2">
                <dt className="w-16 shrink-0 font-medium text-fg">{r}</dt>
                <dd className="text-muted">{ROLE_HINT[r]}</dd>
              </div>
            ))}
          </dl>
        </div>
      )}

      {adding && <AddMemberDialog ws={ws} onClose={() => setAdding(false)} />}
      <ConfirmDialog
        open={removing !== null}
        onClose={() => setRemoving(null)}
        title={`Remove ${removing?.name || removing?.email || ""}?`}
        danger
        confirmLabel="Remove"
        message={
          removing?.userId === me?.id
            ? "You will lose access to this workspace unless you are an instance owner."
            : "They lose access to this workspace right away. Their account stays."
        }
        onConfirm={async () => {
          if (!removing) return;
          await api.removeMember(ws, removing.userId);
          await queryClient.invalidateQueries({ queryKey: keys.members(ws) });
          if (removing.userId === me?.id) await queryClient.invalidateQueries({ queryKey: keys.me });
          toast({ tone: "success", title: `Removed ${removing.name || removing.email}` });
        }}
      />
    </Page>
  );
}

function AddMemberDialog({ ws, onClose }: { ws: string; onClose: () => void }) {
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<Role>("viewer");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const queryClient = useQueryClient();
  const toast = useToast();

  return (
    <Dialog
      open
      onClose={() => !busy && onClose()}
      title="Add member"
      description="The person needs a Pendolo account. If they are already a member, this changes their role."
      onSubmit={async () => {
        if (!email.trim()) return;
        setBusy(true);
        setError(null);
        try {
          await api.putMember(ws, email.trim(), role);
          await queryClient.invalidateQueries({ queryKey: keys.members(ws) });
          toast({ tone: "success", title: `Added ${email.trim()} as ${role}` });
          onClose();
        } catch (err) {
          setError(errorMessage(err));
          setBusy(false);
        }
      }}
      footer={
        <>
          <Button onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button type="submit" variant="primary" loading={busy} disabled={!email.trim()}>
            Add member
          </Button>
        </>
      }
    >
      <Field label="Email">
        {({ id }) => (
          <Input id={id} type="email" required autoFocus value={email} onChange={(e) => setEmail(e.target.value)} />
        )}
      </Field>
      <Field label="Role" hint={ROLE_HINT[role]}>
        {({ id, describedBy }) => (
          <Select id={id} aria-describedby={describedBy} value={role} onChange={(e) => setRole(e.target.value as Role)}>
            {ROLES.map((r) => (
              <option key={r} value={r}>
                {r}
              </option>
            ))}
          </Select>
        )}
      </Field>
      <FormError>{error}</FormError>
    </Dialog>
  );
}
