import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, errorMessage } from "../api/client";
import type { SecretInfo } from "../api/types";
import { Button } from "../components/ui/Button";
import { Card } from "../components/ui/Card";
import { ConfirmDialog, Dialog } from "../components/ui/Dialog";
import { IconKey, IconPlus, IconTrash } from "../components/ui/icons";
import { Field, FormError, Input } from "../components/ui/Input";
import { Page, PageHeader } from "../components/ui/Page";
import { EmptyState, ErrorState, SkeletonRows } from "../components/ui/States";
import { Table, Td, Th, THead, Tr } from "../components/ui/Table";
import { RelativeTime } from "../components/ui/Time";
import { useToast } from "../components/ui/Toast";
import { keys } from "../lib/queries";
import { atLeast } from "../lib/roles";
import { useWs, useWsRole } from "./WorkspaceGate";

// Same rule as internal/secrets (nameRe).
const NAME_RE = /^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$/;

export function SecretsPage() {
  const ws = useWs();
  const role = useWsRole();
  const canRead = atLeast(role, "builder");
  const canEdit = atLeast(role, "admin");
  const q = useQuery({ queryKey: keys.secrets(ws), queryFn: () => api.listSecrets(ws), enabled: canRead });
  const [editing, setEditing] = useState<{ name: string; replace: boolean } | null>(null);
  const [deleting, setDeleting] = useState<SecretInfo | null>(null);
  const queryClient = useQueryClient();
  const toast = useToast();

  const secrets = [...(q.data?.secrets ?? [])].sort((a, b) => a.name.localeCompare(b.name));

  return (
    <Page>
      <PageHeader
        title="Secrets"
        description={
          <>
            Use in automations as <code className="rounded bg-surface-2 px-1 font-mono text-[12.5px]">secret://&lt;name&gt;</code>.
            Values are encrypted and never shown again; they never reach a model prompt or a log.
          </>
        }
        actions={
          canEdit && (
            <Button variant="primary" icon={<IconPlus size={14} />} onClick={() => setEditing({ name: "", replace: false })}>
              Add secret
            </Button>
          )
        }
      />
      {!canRead ? (
        <Card>
          <EmptyState title="No access" hint="Builders and admins can see secret names. Ask a workspace admin." />
        </Card>
      ) : (
        <Card>
          {q.isPending ? (
            <SkeletonRows rows={4} />
          ) : q.isError ? (
            <ErrorState error={q.error} onRetry={() => void q.refetch()} />
          ) : secrets.length === 0 ? (
            <EmptyState
              icon={<IconKey size={24} />}
              title="No secrets yet"
              hint={
                canEdit
                  ? "Add API keys and passwords here instead of writing them in a manifest."
                  : "An admin can add API keys and passwords here."
              }
            />
          ) : (
            <Table label="Secrets">
              <THead>
                <tr>
                  <Th>Name</Th>
                  <Th>Updated</Th>
                  <Th>By</Th>
                  {canEdit && (
                    <Th className="text-right">
                      <span className="sr-only">Actions</span>
                    </Th>
                  )}
                </tr>
              </THead>
              <tbody>
                {secrets.map((s) => (
                  <Tr key={s.name}>
                    <Td className="font-mono text-[12.5px] text-fg">{s.name}</Td>
                    <Td className="whitespace-nowrap text-fg-2">
                      <RelativeTime iso={s.updatedAt} />
                    </Td>
                    <Td className="max-w-[14rem] truncate text-fg-2">{s.updatedBy}</Td>
                    {canEdit && (
                      <Td className="text-right whitespace-nowrap">
                        <Button size="sm" variant="ghost" onClick={() => setEditing({ name: s.name, replace: true })}>
                          Replace
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          icon={<IconTrash size={14} />}
                          aria-label={`Delete ${s.name}`}
                          onClick={() => setDeleting(s)}
                        >
                          Delete
                        </Button>
                      </Td>
                    )}
                  </Tr>
                ))}
              </tbody>
            </Table>
          )}
        </Card>
      )}

      {editing && (
        <SecretDialog
          ws={ws}
          initialName={editing.name}
          replace={editing.replace}
          exists={(n) => secrets.some((s) => s.name === n)}
          onClose={() => setEditing(null)}
        />
      )}
      <ConfirmDialog
        open={deleting !== null}
        onClose={() => setDeleting(null)}
        title={`Delete ${deleting?.name ?? ""}?`}
        danger
        confirmLabel="Delete secret"
        message={
          <>
            Automations that use <code className="font-mono">secret://{deleting?.name}</code> will fail until you add it
            again. This cannot be undone.
          </>
        }
        onConfirm={async () => {
          if (!deleting) return;
          await api.deleteSecret(ws, deleting.name);
          await queryClient.invalidateQueries({ queryKey: keys.secrets(ws) });
          toast({ tone: "success", title: `Deleted ${deleting.name}` });
        }}
      />
    </Page>
  );
}

function SecretDialog({
  ws,
  initialName,
  replace,
  exists,
  onClose,
}: {
  ws: string;
  initialName: string;
  replace: boolean;
  exists: (name: string) => boolean;
  onClose: () => void;
}) {
  const [name, setName] = useState(initialName);
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const queryClient = useQueryClient();
  const toast = useToast();

  const trimmed = name.trim();
  const nameError =
    trimmed && !NAME_RE.test(trimmed)
      ? "Use letters, digits, dots, hyphens and underscores, starting with a letter or digit (up to 128)."
      : undefined;
  const willReplace = !replace && exists(trimmed);

  return (
    <Dialog
      open
      onClose={() => !busy && onClose()}
      title={replace ? `Replace ${initialName}` : "Add secret"}
      description={replace ? "The new value takes effect on the next run." : undefined}
      onSubmit={async () => {
        if (!trimmed || nameError || !value) return;
        setBusy(true);
        setError(null);
        try {
          await api.putSecret(ws, trimmed, value);
          await queryClient.invalidateQueries({ queryKey: keys.secrets(ws) });
          toast({ tone: "success", title: replace || willReplace ? `Replaced ${trimmed}` : `Added ${trimmed}` });
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
          <Button type="submit" variant="primary" loading={busy} disabled={!trimmed || !value || Boolean(nameError)}>
            {replace || willReplace ? "Replace" : "Add"}
          </Button>
        </>
      }
    >
      {!replace && (
        <Field
          label="Name"
          hint={willReplace ? "A secret with this name exists; saving replaces its value." : "e.g. openai-key, smtp_password"}
          error={nameError}
        >
          {({ id, describedBy, invalid }) => (
            <Input
              id={id}
              value={name}
              onChange={(e) => setName(e.target.value)}
              autoFocus
              required
              autoComplete="off"
              spellCheck={false}
              className="font-mono"
              aria-describedby={describedBy}
              aria-invalid={invalid}
            />
          )}
        </Field>
      )}
      <Field label="Value" hint="Stored encrypted. You will not be able to see it again.">
        {({ id, describedBy }) => (
          <Input
            id={id}
            type="password"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            autoFocus={replace}
            required
            autoComplete="new-password"
            spellCheck={false}
            aria-describedby={describedBy}
          />
        )}
      </Field>
      <FormError>{error}</FormError>
    </Dialog>
  );
}
