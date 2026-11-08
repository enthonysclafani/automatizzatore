import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, errorMessage } from "../api/client";
import type { Autonomy, WorkspaceSettings } from "../api/types";
import { Button } from "../components/ui/Button";
import { Card, CardBody, CardHeader } from "../components/ui/Card";
import { cx } from "../components/ui/cx";
import { Field, FormError, Input } from "../components/ui/Input";
import { Page, PageHeader } from "../components/ui/Page";
import { Select } from "../components/ui/Select";
import { ErrorState, SkeletonRows } from "../components/ui/States";
import { Table, Td, Th, THead, Tr } from "../components/ui/Table";
import { useToast } from "../components/ui/Toast";
import { keys, settingsQuery } from "../lib/queries";
import { atLeast } from "../lib/roles";
import { useWs, useWsRole } from "./WorkspaceGate";

export const AUTONOMY_LEVELS: { value: Autonomy; allows: string }[] = [
  { value: "observe", allows: "Read only. Nothing is written or sent." },
  {
    value: "draft",
   
    allows:
      "Reads and reversible writes (labels, drafts, documents). Messages to others become drafts or are blocked; destructive actions are blocked.",
  },
  {
    value: "ask",
   
    allows: "Reads and writes run. Sending to others and destructive actions wait for approval.",
  },
  { value: "auto", allows: "Everything runs, except destructive actions, which wait for approval." },
];

export function SettingsPage() {
  const ws = useWs();
  const role = useWsRole();
  const canEdit = atLeast(role, "admin");
  const q = useQuery(settingsQuery(ws));

  return (
    <Page>
      <PageHeader
        title="Workspace settings"
        description={canEdit ? "Name and limits for every automation in this workspace." : "Only workspace admins can change these."}
      />
      {q.isPending ? (
        <Card>
          <SkeletonRows rows={4} />
        </Card>
      ) : q.isError ? (
        <Card>
          <ErrorState error={q.error} onRetry={() => void q.refetch()} />
        </Card>
      ) : (
        // Remount the form when the server copy changes, so it starts from fresh values.
        <SettingsForm key={`${q.data.name}|${q.data.maxAutonomy}`} ws={ws} settings={q.data} canEdit={canEdit} />
      )}
    </Page>
  );
}

function SettingsForm({ ws, settings, canEdit }: { ws: string; settings: WorkspaceSettings; canEdit: boolean }) {
  const [name, setName] = useState(settings.name);
  const [maxAutonomy, setMaxAutonomy] = useState<Autonomy>(settings.maxAutonomy);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const queryClient = useQueryClient();
  const toast = useToast();

  const trimmed = name.trim();
  const nameError = trimmed ? undefined : "Enter a name for the workspace.";
  const dirty = trimmed !== settings.name || maxAutonomy !== settings.maxAutonomy;

  return (
    <form
      className="space-y-4"
      onSubmit={async (e) => {
        e.preventDefault();
        if (!canEdit || !dirty || nameError) return;
        setBusy(true);
        setError(null);
        const patch: { name?: string; maxAutonomy?: Autonomy } = {};
        if (trimmed !== settings.name) patch.name = trimmed;
        if (maxAutonomy !== settings.maxAutonomy) patch.maxAutonomy = maxAutonomy;
        try {
          const next = await api.patchSettings(ws, patch);
          queryClient.setQueryData(keys.settings(ws), next);
          // The sidebar shows workspace names from /auth/me.
          if (patch.name) void queryClient.invalidateQueries({ queryKey: keys.me });
          toast({ tone: "success", title: "Settings saved" });
        } catch (err) {
          setError(errorMessage(err));
        } finally {
          setBusy(false);
        }
      }}
    >
      <Card>
        <CardHeader title="General" />
        <CardBody className="space-y-4">
          <Field label="Name" error={canEdit ? nameError : undefined} hint={`Workspace id: ${settings.id}`}>
            {({ id, describedBy, invalid }) => (
              <Input
                id={id}
                value={name}
                onChange={(e) => setName(e.target.value)}
                readOnly={!canEdit}
                required
                maxLength={100}
                className="max-w-md"
                aria-describedby={describedBy}
                aria-invalid={invalid}
              />
            )}
          </Field>
        </CardBody>
      </Card>

      <Card>
        <CardHeader
          title="Maximum autonomy"
          description="No automation in this workspace gets more than this, whatever its manifest says."
        />
        <CardBody className="space-y-4">
          <Field label="Max autonomy" hint="Lower it to stop every automation from sending or deleting on its own.">
            {({ id, describedBy }) => (
              <Select
                id={id}
                value={maxAutonomy}
                onChange={(e) => setMaxAutonomy(e.target.value as Autonomy)}
                disabled={!canEdit}
                className="max-w-xs"
                aria-describedby={describedBy}
              >
                {AUTONOMY_LEVELS.map((l) => (
                  <option key={l.value} value={l.value}>
                    {l.value}
                  </option>
                ))}
              </Select>
            )}
          </Field>
        </CardBody>
        <Table label="What each autonomy level allows" className="border-t border-line">
          <THead>
            <tr>
              <Th>Level</Th>
              <Th>What it allows</Th>
            </tr>
          </THead>
          <tbody>
            {AUTONOMY_LEVELS.map((l) => {
              const selected = l.value === maxAutonomy;
              return (
                <Tr key={l.value} className={cx(selected && "bg-accent-soft")}>
                  <Td className="align-top font-mono text-[12.5px] whitespace-nowrap text-fg">
                    {l.value}
                    {selected && <span className="sr-only"> (selected)</span>}
                  </Td>
                  <Td className="min-w-[14rem] text-fg-2">{l.allows}</Td>
                </Tr>
              );
            })}
          </tbody>
        </Table>
      </Card>

      <FormError>{error}</FormError>
      {canEdit && (
        <div className="flex flex-wrap items-center gap-2">
          <Button type="submit" variant="primary" loading={busy} disabled={!dirty || Boolean(nameError)}>
            Save changes
          </Button>
          {dirty && (
            <Button
              variant="ghost"
              disabled={busy}
              onClick={() => {
                setName(settings.name);
                setMaxAutonomy(settings.maxAutonomy);
                setError(null);
              }}
            >
              Discard
            </Button>
          )}
        </div>
      )}
    </form>
  );
}
