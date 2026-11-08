import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { api, errorMessage, type UserPatch } from "../../api/client";
import type { User } from "../../api/types";
import { Badge } from "../../components/ui/Badge";
import { Button } from "../../components/ui/Button";
import { Card, CardHeader } from "../../components/ui/Card";
import { ConfirmDialog, Dialog } from "../../components/ui/Dialog";
import { IconPlus, IconShield, IconUsers } from "../../components/ui/icons";
import { Field, FormError, Input } from "../../components/ui/Input";
import { Page, PageHeader } from "../../components/ui/Page";
import { EmptyState, ErrorState, SkeletonRows } from "../../components/ui/States";
import { Table, Td, Th, THead, Tr } from "../../components/ui/Table";
import { RelativeTime } from "../../components/ui/Time";
import { useToast } from "../../components/ui/Toast";
import { keys, useMe } from "../../lib/queries";

const MIN_PASSWORD = 12;
const WS_ID_RE = /^[a-z0-9][a-z0-9-]{0,62}$/;

type Pending =
  | { kind: "disable" | "enable" | "grant" | "revoke"; user: User }
  | { kind: "password"; user: User }
  | null;

export function UsersPage() {
  const { data: me } = useMe();
  const q = useQuery({ queryKey: keys.users, queryFn: api.listUsers, enabled: Boolean(me?.isOwner) });
  const queryClient = useQueryClient();
  const toast = useToast();
  const [creatingUser, setCreatingUser] = useState(false);
  const [creatingWs, setCreatingWs] = useState(false);
  const [pending, setPending] = useState<Pending>(null);

  if (!me?.isOwner) {
    return (
      <Page>
        <PageHeader title="Users & workspaces" />
        <Card>
          <EmptyState title="Owners only" hint="Ask an instance owner to manage users and workspaces." />
        </Card>
      </Page>
    );
  }

  const users = [...(q.data?.users ?? [])].sort((a, b) => a.email.localeCompare(b.email));

  const patch = async (user: User, body: UserPatch, done: string) => {
    await api.updateUser(user.id, body);
    await queryClient.invalidateQueries({ queryKey: keys.users });
    if (user.id === me.id) await queryClient.invalidateQueries({ queryKey: keys.me });
    toast({ tone: "success", title: done });
  };

  const label = (u: User) => u.name || u.email;

  return (
    <Page wide>
      <PageHeader
        title="Users & workspaces"
        description="Accounts on this Pendolo instance. Add people to a workspace from its Members page."
        actions={
          <>
            <Button icon={<IconPlus size={14} />} onClick={() => setCreatingWs(true)}>
              New workspace
            </Button>
            <Button variant="primary" icon={<IconPlus size={14} />} onClick={() => setCreatingUser(true)}>
              New user
            </Button>
          </>
        }
      />

      <div className="space-y-4">
        <Card>
          <CardHeader title="Users" description={q.data ? `${users.length} accounts` : undefined} />
          {q.isPending ? (
            <SkeletonRows rows={5} />
          ) : q.isError ? (
            <ErrorState error={q.error} onRetry={() => void q.refetch()} />
          ) : users.length === 0 ? (
            <EmptyState icon={<IconUsers size={24} />} title="No users" hint="Create the first account with New user." />
          ) : (
            <Table label="Users">
              <THead>
                <tr>
                  <Th>User</Th>
                  <Th>Status</Th>
                  <Th>2FA</Th>
                  <Th>Last sign-in</Th>
                  <Th>Created</Th>
                  <Th className="text-right">
                    <span className="sr-only">Actions</span>
                  </Th>
                </tr>
              </THead>
              <tbody>
                {users.map((u) => (
                  <Tr key={u.id} className={u.disabled ? "opacity-70" : undefined}>
                    <Td className="max-w-[18rem]">
                      <span className="flex items-center gap-1.5 font-medium text-fg">
                        <span className="truncate">{u.name || u.email}</span>
                        {u.isOwner && (
                          <Badge tone="blue" icon={<IconShield size={11} />}>
                            Owner
                          </Badge>
                        )}
                        {u.id === me.id && <span className="text-[12px] font-normal text-muted">(you)</span>}
                      </span>
                      {u.name && <span className="block truncate text-[12px] text-muted">{u.email}</span>}
                    </Td>
                    <Td>{u.disabled ? <Badge tone="amber">Disabled</Badge> : <Badge tone="green">Active</Badge>}</Td>
                    <Td className="text-fg-2">{u.totpEnabled ? "On" : <span className="text-muted">Off</span>}</Td>
                    <Td className="whitespace-nowrap text-fg-2">
                      {u.lastLoginAt ? <RelativeTime iso={u.lastLoginAt} /> : <span className="text-muted">Never</span>}
                    </Td>
                    <Td className="whitespace-nowrap text-fg-2">
                      <RelativeTime iso={u.createdAt} />
                    </Td>
                    <Td className="text-right whitespace-nowrap">
                      <Button size="sm" variant="ghost" onClick={() => setPending({ kind: "password", user: u })}>
                        Reset password
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={u.id === me.id}
                        title={u.id === me.id ? "You cannot change your own owner role" : undefined}
                        onClick={() => setPending({ kind: u.isOwner ? "revoke" : "grant", user: u })}
                      >
                        {u.isOwner ? "Remove owner" : "Make owner"}
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={u.id === me.id}
                        title={u.id === me.id ? "You cannot disable your own account" : undefined}
                        onClick={() => setPending({ kind: u.disabled ? "enable" : "disable", user: u })}
                      >
                        {u.disabled ? "Enable" : "Disable"}
                      </Button>
                    </Td>
                  </Tr>
                ))}
              </tbody>
            </Table>
          )}
        </Card>

        <Card>
          <CardHeader title="Workspaces" description="Workspaces you can open. Each one has its own automations, secrets and members." />
          {me.workspaces.length === 0 ? (
            <EmptyState title="No workspaces yet" hint="Create one with New workspace, then add members to it." />
          ) : (
            <Table label="Workspaces">
              <THead>
                <tr>
                  <Th>Name</Th>
                  <Th>ID</Th>
                  <Th className="text-right">
                    <span className="sr-only">Actions</span>
                  </Th>
                </tr>
              </THead>
              <tbody>
                {me.workspaces.map((w) => (
                  <Tr key={w.id}>
                    <Td className="font-medium text-fg">{w.name}</Td>
                    <Td className="font-mono text-[12.5px] text-fg-2">{w.id}</Td>
                    <Td className="text-right whitespace-nowrap">
                      <Link to="/w/$ws/members" params={{ ws: w.id }} className="text-[13px] text-accent hover:underline">
                        Members
                      </Link>
                      <Link to="/w/$ws" params={{ ws: w.id }} className="ml-4 text-[13px] text-accent hover:underline">
                        Open
                      </Link>
                    </Td>
                  </Tr>
                ))}
              </tbody>
            </Table>
          )}
        </Card>
      </div>

      {creatingUser && <CreateUserDialog onClose={() => setCreatingUser(false)} />}
      {creatingWs && <CreateWorkspaceDialog onClose={() => setCreatingWs(false)} />}
      {pending?.kind === "password" && <ResetPasswordDialog user={pending.user} onClose={() => setPending(null)} />}

      <ConfirmDialog
        open={pending?.kind === "disable"}
        onClose={() => setPending(null)}
        title={`Disable ${pending ? label(pending.user) : ""}?`}
        danger
        confirmLabel="Disable"
        message="They are signed out and cannot sign in or use API tokens until you enable the account again. Nothing is deleted."
        onConfirm={() => (pending ? patch(pending.user, { disabled: true }, `Disabled ${label(pending.user)}`) : Promise.resolve())}
      />
      <ConfirmDialog
        open={pending?.kind === "enable"}
        onClose={() => setPending(null)}
        title={`Enable ${pending ? label(pending.user) : ""}?`}
        confirmLabel="Enable"
        message="They can sign in again with their existing password."
        onConfirm={() => (pending ? patch(pending.user, { disabled: false }, `Enabled ${label(pending.user)}`) : Promise.resolve())}
      />
      <ConfirmDialog
        open={pending?.kind === "grant"}
        onClose={() => setPending(null)}
        title={`Make ${pending ? label(pending.user) : ""} an owner?`}
        confirmLabel="Make owner"
        message="Owners can do everything in every workspace, manage users and see the system settings."
        onConfirm={() => (pending ? patch(pending.user, { isOwner: true }, `${label(pending.user)} is now an owner`) : Promise.resolve())}
      />
      <ConfirmDialog
        open={pending?.kind === "revoke"}
        onClose={() => setPending(null)}
        title={`Remove owner role from ${pending ? label(pending.user) : ""}?`}
        danger
        confirmLabel="Remove owner role"
        message="They keep access only to the workspaces they are a member of."
        onConfirm={() =>
          pending ? patch(pending.user, { isOwner: false }, `${label(pending.user)} is no longer an owner`) : Promise.resolve()
        }
      />
    </Page>
  );
}

function CreateUserDialog({ onClose }: { onClose: () => void }) {
  const [form, setForm] = useState({ email: "", name: "", password: "", isOwner: false });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const queryClient = useQueryClient();
  const toast = useToast();
  const short = form.password.length > 0 && form.password.length < MIN_PASSWORD;

  return (
    <Dialog
      open
      onClose={() => !busy && onClose()}
      title="New user"
      description="Share the initial password privately; they can change it under Account."
      onSubmit={async () => {
        if (!form.email.trim() || form.password.length < MIN_PASSWORD) return;
        setBusy(true);
        setError(null);
        try {
          const u = await api.createUser({ ...form, email: form.email.trim(), name: form.name.trim() });
          await queryClient.invalidateQueries({ queryKey: keys.users });
          toast({ tone: "success", title: `Created ${u.email}`, description: "Add them to a workspace from its Members page." });
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
          <Button type="submit" variant="primary" loading={busy} disabled={!form.email.trim() || form.password.length < MIN_PASSWORD}>
            Create user
          </Button>
        </>
      }
    >
      <Field label="Email">
        {({ id }) => (
          <Input id={id} type="email" required autoFocus autoComplete="off" value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} />
        )}
      </Field>
      <Field label="Name">
        {({ id }) => <Input id={id} autoComplete="off" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />}
      </Field>
      <Field
        label="Initial password"
        hint={`At least ${MIN_PASSWORD} characters.`}
        error={short ? `Use at least ${MIN_PASSWORD} characters.` : undefined}
      >
        {({ id, describedBy, invalid }) => (
          <Input
            id={id}
            type="password"
            required
            autoComplete="new-password"
            aria-describedby={describedBy}
            aria-invalid={invalid}
            value={form.password}
            onChange={(e) => setForm({ ...form, password: e.target.value })}
          />
        )}
      </Field>
      <label className="flex items-start gap-2 text-sm text-fg">
        <input
          type="checkbox"
          className="mt-0.5 h-4 w-4 accent-[var(--accent)]"
          checked={form.isOwner}
          onChange={(e) => setForm({ ...form, isOwner: e.target.checked })}
        />
        <span>
          Instance owner
          <span className="block text-[12px] text-muted">Full access to every workspace, users and system settings.</span>
        </span>
      </label>
      <FormError>{error}</FormError>
    </Dialog>
  );
}

function ResetPasswordDialog({ user, onClose }: { user: User; onClose: () => void }) {
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const toast = useToast();
  const short = password.length > 0 && password.length < MIN_PASSWORD;

  return (
    <Dialog
      open
      onClose={() => !busy && onClose()}
      title={`Reset password for ${user.name || user.email}`}
      description="Their current password stops working. Share the new one privately."
      onSubmit={async () => {
        if (password.length < MIN_PASSWORD) return;
        setBusy(true);
        setError(null);
        try {
          await api.updateUser(user.id, { password });
          toast({ tone: "success", title: "Password reset" });
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
          <Button type="submit" variant="primary" loading={busy} disabled={password.length < MIN_PASSWORD}>
            Reset password
          </Button>
        </>
      }
    >
      <Field label="New password" hint={`At least ${MIN_PASSWORD} characters.`} error={short ? `Use at least ${MIN_PASSWORD} characters.` : undefined}>
        {({ id, describedBy, invalid }) => (
          <Input
            id={id}
            type="password"
            required
            autoFocus
            autoComplete="new-password"
            aria-describedby={describedBy}
            aria-invalid={invalid}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        )}
      </Field>
      <FormError>{error}</FormError>
    </Dialog>
  );
}

function slugify(s: string): string {
  return s
    .toLowerCase()
    .normalize("NFKD")
    .replace(/[̀-ͯ]/g, "")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 63);
}

function CreateWorkspaceDialog({ onClose }: { onClose: () => void }) {
  const [name, setName] = useState("");
  const [id, setId] = useState("");
  const [idEdited, setIdEdited] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const queryClient = useQueryClient();
  const toast = useToast();
  const slug = idEdited ? id : slugify(name);
  const idError = slug && !WS_ID_RE.test(slug) ? "Use lowercase letters, digits and hyphens; start with a letter or digit." : undefined;

  return (
    <Dialog
      open
      onClose={() => !busy && onClose()}
      title="New workspace"
      description="A workspace is a client or project. Automations join it with metadata.workspace in their manifest."
      onSubmit={async () => {
        if (!slug || idError || !name.trim()) return;
        setBusy(true);
        setError(null);
        try {
          const w = await api.createWorkspace(slug, name.trim());
          await queryClient.invalidateQueries({ queryKey: keys.me });
          toast({
            tone: "success",
            title: `Created ${w.name}`,
            link: { to: `/w/${encodeURIComponent(w.id)}/members`, label: "Add members" },
          });
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
          <Button type="submit" variant="primary" loading={busy} disabled={!slug || !name.trim() || Boolean(idError)}>
            Create workspace
          </Button>
        </>
      }
    >
      <Field label="Name">
        {({ id: fid }) => <Input id={fid} required autoFocus value={name} onChange={(e) => setName(e.target.value)} />}
      </Field>
      <Field label="ID" hint="Used in URLs and in metadata.workspace. Cannot be changed later." error={idError}>
        {({ id: fid, describedBy, invalid }) => (
          <Input
            id={fid}
            required
            className="font-mono"
            spellCheck={false}
            aria-describedby={describedBy}
            aria-invalid={invalid}
            value={slug}
            onChange={(e) => {
              setIdEdited(true);
              setId(e.target.value);
            }}
          />
        )}
      </Field>
      <FormError>{error}</FormError>
    </Dialog>
  );
}
