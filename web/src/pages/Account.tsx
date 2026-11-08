import { useQuery, useQueryClient } from "@tanstack/react-query";
import QRCode from "qrcode";
import { useState } from "react";
import { api, errorMessage } from "../api/client";
import type { ApiToken, NewApiToken } from "../api/types";
import { Badge, Tag } from "../components/ui/Badge";
import { Button } from "../components/ui/Button";
import { Card, CardBody, CardHeader } from "../components/ui/Card";
import { CopyButton } from "../components/ui/Copy";
import { ConfirmDialog, Dialog } from "../components/ui/Dialog";
import { IconAlert, IconKey, IconPlus, IconShield } from "../components/ui/icons";
import { Field, FormError, Input } from "../components/ui/Input";
import { DefList, Page, PageHeader } from "../components/ui/Page";
import { Select } from "../components/ui/Select";
import { EmptyState, ErrorState, SkeletonRows } from "../components/ui/States";
import { Table, Td, Th, THead, Tr } from "../components/ui/Table";
import { RelativeTime } from "../components/ui/Time";
import { useToast } from "../components/ui/Toast";
import { formatAbsolute } from "../lib/format";
import { keys, useMe } from "../lib/queries";

const MIN_PASSWORD = 12;

export function AccountPage() {
  const { data: me } = useMe();
  if (!me) return null;
  return (
    <Page>
      <PageHeader title="Account" description="Your profile, password, two-factor authentication and API tokens." />
      <div className="space-y-4">
        <Card>
          <CardHeader title="Profile" />
          <CardBody>
            <DefList
              items={[
                { label: "Name", value: me.name || <span className="text-muted">–</span> },
                { label: "Email", value: me.email },
                {
                  label: "Instance role",
                  value: me.isOwner ? (
                    <Badge tone="blue" icon={<IconShield size={11} />}>
                      Owner
                    </Badge>
                  ) : (
                    "Member"
                  ),
                },
                {
                  label: "Workspaces",
                  value:
                    me.workspaces.length === 0 ? (
                      <span className="text-muted">None yet</span>
                    ) : (
                      <span className="flex flex-wrap gap-1.5">
                        {me.workspaces.map((w) => (
                          <Tag key={w.id}>
                            {w.name} · {w.role}
                          </Tag>
                        ))}
                      </span>
                    ),
                },
              ]}
            />
          </CardBody>
        </Card>
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
          <PasswordCard />
          <TwoFactorCard enabled={me.totpEnabled} />
        </div>
        <TokensCard />
      </div>
    </Page>
  );
}

function PasswordCard() {
  const [form, setForm] = useState({ current: "", next: "", confirm: "" });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const toast = useToast();
  const short = form.next.length > 0 && form.next.length < MIN_PASSWORD;
  const mismatch = form.confirm.length > 0 && form.confirm !== form.next;

  return (
    <Card>
      <CardHeader title="Password" />
      <CardBody>
        <form
          className="space-y-3"
          onSubmit={async (e) => {
            e.preventDefault();
            if (short || mismatch || !form.current || !form.next) return;
            setBusy(true);
            setError(null);
            try {
              await api.changePassword(form.current, form.next);
              setForm({ current: "", next: "", confirm: "" });
              toast({ tone: "success", title: "Password changed" });
            } catch (err) {
              setError(errorMessage(err));
            } finally {
              setBusy(false);
            }
          }}
        >
          <Field label="Current password">
            {({ id }) => (
              <Input
                id={id}
                type="password"
                required
                autoComplete="current-password"
                value={form.current}
                onChange={(e) => setForm({ ...form, current: e.target.value })}
              />
            )}
          </Field>
          <Field label="New password" hint={`At least ${MIN_PASSWORD} characters.`} error={short ? `Use at least ${MIN_PASSWORD} characters.` : undefined}>
            {({ id, describedBy, invalid }) => (
              <Input
                id={id}
                type="password"
                required
                autoComplete="new-password"
                aria-describedby={describedBy}
                aria-invalid={invalid}
                value={form.next}
                onChange={(e) => setForm({ ...form, next: e.target.value })}
              />
            )}
          </Field>
          <Field label="Confirm new password" error={mismatch ? "The passwords do not match." : undefined}>
            {({ id, describedBy, invalid }) => (
              <Input
                id={id}
                type="password"
                required
                autoComplete="new-password"
                aria-describedby={describedBy}
                aria-invalid={invalid}
                value={form.confirm}
                onChange={(e) => setForm({ ...form, confirm: e.target.value })}
              />
            )}
          </Field>
          <FormError>{error}</FormError>
          <Button type="submit" variant="primary" loading={busy} disabled={short || mismatch || !form.current || !form.next}>
            Change password
          </Button>
        </form>
      </CardBody>
    </Card>
  );
}

function TwoFactorCard({ enabled }: { enabled: boolean }) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const [setup, setSetup] = useState<{ secret: string; uri: string; qr: string } | null>(null);
  const [code, setCode] = useState("");
  const [password, setPassword] = useState("");
  const [disabling, setDisabling] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const start = async () => {
    setBusy(true);
    setError(null);
    try {
      const s = await api.totpSetup();
      const qr = await QRCode.toDataURL(s.uri, { margin: 1, width: 200, errorCorrectionLevel: "M" });
      setSetup({ ...s, qr });
      setCode("");
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const enable = async () => {
    setBusy(true);
    setError(null);
    try {
      await api.totpEnable(code);
      await queryClient.invalidateQueries({ queryKey: keys.me });
      setSetup(null);
      setCode("");
      toast({ tone: "success", title: "Two-factor authentication is on", description: "You will need a code at every sign-in." });
    } catch (err) {
      setError(errorMessage(err));
      setCode("");
    } finally {
      setBusy(false);
    }
  };

  const disable = async () => {
    setBusy(true);
    setError(null);
    try {
      await api.totpDisable(password);
      await queryClient.invalidateQueries({ queryKey: keys.me });
      setPassword("");
      setDisabling(false);
      toast({ tone: "success", title: "Two-factor authentication is off" });
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card>
      <CardHeader
        title="Two-factor authentication"
        actions={enabled ? <Badge tone="green">On</Badge> : <Badge tone="gray">Off</Badge>}
      />
      <CardBody className="space-y-3">
        {enabled ? (
          disabling ? (
            <form
              className="space-y-3"
              onSubmit={(e) => {
                e.preventDefault();
                void disable();
              }}
            >
              <p className="text-[13px] text-fg-2">Enter your password to turn off two-factor authentication.</p>
              <Field label="Password">
                {({ id }) => (
                  <Input
                    id={id}
                    type="password"
                    required
                    autoFocus
                    autoComplete="current-password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                  />
                )}
              </Field>
              <FormError>{error}</FormError>
              <div className="flex gap-2">
                <Button type="submit" variant="danger" loading={busy} disabled={!password}>
                  Turn off
                </Button>
                <Button
                  onClick={() => {
                    setDisabling(false);
                    setPassword("");
                    setError(null);
                  }}
                >
                  Cancel
                </Button>
              </div>
            </form>
          ) : (
            <>
              <p className="text-[13px] text-fg-2">
                Sign-in asks for a 6-digit code from your authenticator app after your password.
              </p>
              <Button onClick={() => setDisabling(true)}>Turn off</Button>
            </>
          )
        ) : setup ? (
          <form
            className="space-y-3"
            onSubmit={(e) => {
              e.preventDefault();
              void enable();
            }}
          >
            <p className="text-[13px] text-fg-2">
              1. Scan this code with an authenticator app (1Password, Google Authenticator, Aegis…).
            </p>
            <div className="flex flex-wrap items-start gap-4">
              <img
                src={setup.qr}
                width={176}
                height={176}
                alt="QR code for your authenticator app. If you cannot scan it, enter the key shown next to it."
                className="rounded-md border border-line bg-white p-1"
              />
              <div className="min-w-0 flex-1 space-y-1.5">
                <p className="text-[12px] text-muted">Or enter this key by hand:</p>
                <code className="block rounded bg-surface-2 px-2 py-1.5 font-mono text-[12.5px] break-all text-fg">
                  {setup.secret}
                </code>
                <CopyButton text={setup.secret} label="Copy key" />
              </div>
            </div>
            <Field label="2. Enter the 6-digit code it shows">
              {({ id }) => (
                <Input
                  id={id}
                  inputMode="numeric"
                  pattern="[0-9]{6}"
                  maxLength={6}
                  required
                  autoComplete="one-time-code"
                  className="tnum w-40 font-mono tracking-[0.3em]"
                  value={code}
                  onChange={(e) => setCode(e.target.value.replace(/\D/g, "").slice(0, 6))}
                />
              )}
            </Field>
            <FormError>{error}</FormError>
            <div className="flex gap-2">
              <Button type="submit" variant="primary" loading={busy} disabled={code.length !== 6}>
                Turn on
              </Button>
              <Button
                onClick={() => {
                  setSetup(null);
                  setError(null);
                }}
              >
                Cancel
              </Button>
            </div>
          </form>
        ) : (
          <>
            <p className="text-[13px] text-fg-2">
              Protect your account with a code from an authenticator app in addition to your password.
            </p>
            <FormError>{error}</FormError>
            <Button variant="primary" loading={busy} onClick={() => void start()}>
              Set up two-factor authentication
            </Button>
          </>
        )}
      </CardBody>
    </Card>
  );
}

const EXPIRY_OPTIONS = [
  { value: "", label: "Never" },
  { value: "7", label: "7 days" },
  { value: "30", label: "30 days" },
  { value: "90", label: "90 days" },
  { value: "365", label: "1 year" },
];

function TokensCard() {
  const q = useQuery({ queryKey: keys.tokens, queryFn: api.listTokens });
  const queryClient = useQueryClient();
  const toast = useToast();
  const [creating, setCreating] = useState(false);
  const [created, setCreated] = useState<NewApiToken | null>(null);
  const [revoking, setRevoking] = useState<ApiToken | null>(null);

  const tokens = [...(q.data?.tokens ?? [])].sort((a, b) => b.createdAt.localeCompare(a.createdAt));

  return (
    <Card>
      <CardHeader
        title="API tokens"
        description={
          <>
            For scripts and the CLI: send <code className="font-mono">Authorization: Bearer pdl_…</code>. A token acts as you.
          </>
        }
        actions={
          <Button size="sm" icon={<IconPlus size={14} />} onClick={() => setCreating(true)}>
            New token
          </Button>
        }
      />
      {created && (
        <div className="border-b border-line bg-[var(--st-amber-bg)] px-4 py-3">
          <p className="flex items-center gap-1.5 text-[13px] font-medium text-[var(--st-amber-fg)]">
            <IconAlert size={14} />
            Copy the token “{created.name}” now. It is shown only once.
          </p>
          <div className="mt-2 flex flex-wrap items-center gap-2">
            <code className="min-w-0 flex-1 rounded border border-line bg-surface px-2 py-1.5 font-mono text-[12.5px] break-all text-fg">
              {created.token}
            </code>
            <CopyButton text={created.token} />
            <Button size="sm" variant="ghost" onClick={() => setCreated(null)}>
              Done
            </Button>
          </div>
        </div>
      )}
      {q.isPending ? (
        <SkeletonRows rows={3} />
      ) : q.isError ? (
        <ErrorState error={q.error} onRetry={() => void q.refetch()} />
      ) : tokens.length === 0 ? (
        <EmptyState icon={<IconKey size={22} />} title="No API tokens" hint="Create one to call the API from scripts." />
      ) : (
        <Table label="API tokens">
          <THead>
            <tr>
              <Th>Name</Th>
              <Th>Created</Th>
              <Th>Last used</Th>
              <Th>Expires</Th>
              <Th className="text-right">
                <span className="sr-only">Actions</span>
              </Th>
            </tr>
          </THead>
          <tbody>
            {tokens.map((t) => (
              <Tr key={t.id}>
                <Td className="font-medium text-fg">{t.name}</Td>
                <Td className="whitespace-nowrap text-fg-2">
                  <RelativeTime iso={t.createdAt} />
                </Td>
                <Td className="whitespace-nowrap text-fg-2">
                  {t.lastUsedAt ? <RelativeTime iso={t.lastUsedAt} /> : <span className="text-muted">Never</span>}
                </Td>
                <Td className="whitespace-nowrap text-fg-2">
                  {t.expiresAt ? (
                    <span title={formatAbsolute(t.expiresAt)}>
                      <RelativeTime iso={t.expiresAt} />
                    </span>
                  ) : (
                    <span className="text-muted">Never</span>
                  )}
                </Td>
                <Td className="text-right">
                  <Button size="sm" variant="ghost" onClick={() => setRevoking(t)}>
                    Revoke
                  </Button>
                </Td>
              </Tr>
            ))}
          </tbody>
        </Table>
      )}

      {creating && (
        <CreateTokenDialog
          onClose={() => setCreating(false)}
          onCreated={(t) => {
            setCreated(t);
            setCreating(false);
          }}
        />
      )}
      <ConfirmDialog
        open={revoking !== null}
        onClose={() => setRevoking(null)}
        title={`Revoke ${revoking?.name ?? ""}?`}
        danger
        confirmLabel="Revoke token"
        message="Scripts that use it stop working right away. This cannot be undone."
        onConfirm={async () => {
          if (!revoking) return;
          await api.revokeToken(revoking.id);
          if (created?.id === revoking.id) setCreated(null);
          await queryClient.invalidateQueries({ queryKey: keys.tokens });
          toast({ tone: "success", title: `Revoked ${revoking.name}` });
        }}
      />
    </Card>
  );
}

function CreateTokenDialog({ onClose, onCreated }: { onClose: () => void; onCreated: (t: NewApiToken) => void }) {
  const [name, setName] = useState("");
  const [expiry, setExpiry] = useState("90");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const queryClient = useQueryClient();

  return (
    <Dialog
      open
      onClose={() => !busy && onClose()}
      title="New API token"
      onSubmit={async () => {
        if (!name.trim()) return;
        setBusy(true);
        setError(null);
        try {
          const t = await api.createToken(name.trim(), expiry ? Number(expiry) : undefined);
          await queryClient.invalidateQueries({ queryKey: keys.tokens });
          onCreated(t);
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
          <Button type="submit" variant="primary" loading={busy} disabled={!name.trim()}>
            Create token
          </Button>
        </>
      }
    >
      <Field label="Name" hint="Where it is used, e.g. “deploy script” or “laptop CLI”.">
        {({ id, describedBy }) => (
          <Input id={id} required autoFocus aria-describedby={describedBy} value={name} onChange={(e) => setName(e.target.value)} />
        )}
      </Field>
      <Field label="Expires">
        {({ id }) => (
          <Select id={id} value={expiry} onChange={(e) => setExpiry(e.target.value)}>
            {EXPIRY_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </Select>
        )}
      </Field>
      <FormError>{error}</FormError>
    </Dialog>
  );
}
