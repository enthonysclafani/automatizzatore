import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useId, useRef, useState, type ReactNode, type RefObject } from "react";
import { api, errorMessage } from "../../api/client";
import type {
  Connection,
  ConnectionInput,
  ConnectorCatalog,
  ConnectorField,
  ConnectorKindInfo,
} from "../../api/types";
import { keys } from "../../lib/queries";
import { Button, IconButton } from "../ui/Button";
import { CopyButton } from "../ui/Copy";
import { cx } from "../ui/cx";
import { Dialog } from "../ui/Dialog";
import { IconCheckCircle, IconPlus, IconTrash } from "../ui/icons";
import { controlClass, Field, FormError, Input, Textarea } from "../ui/Input";
import { Select } from "../ui/Select";
import { useToast } from "../ui/Toast";
import { ActionList, CONNECTION_NAME_RE, DEFAULT_CONNECTION_NAME, Linkify } from "./Connections";

type Step = "kind" | "setup" | "form" | "done";

interface Row {
  id: number;
  key: string;
  value: string;
  /** A saved secret header: its value is kept unless a new one is typed. */
  saved?: boolean;
}

interface FormState {
  name: string;
  /** string fields, and string[] fields as one item per line. */
  text: Record<string, string>;
  maps: Record<string, Row[]>;
}

let rowSeq = 1;
const newRow = (key = "", value = "", saved = false): Row => ({ id: rowSeq++, key, value, saved });

function initialState(kind: ConnectorKindInfo, existing: Connection | undefined, names: string[]): FormState {
  const text: Record<string, string> = {};
  const maps: Record<string, Row[]> = {};
  const fieldNames = new Set(kind.fields.map((f) => f.name));
  for (const f of kind.fields) {
    const v = existing ? existing.config[f.name] : f.default;
    if (f.type === "map") {
      const rows: Row[] = [];
      if (f.secret) {
        // For MCP, secretsSet holds the header names that have a value.
        for (const k of existing?.secretsSet ?? []) if (!fieldNames.has(k)) rows.push(newRow(k, "", true));
      } else if (v && typeof v === "object" && !Array.isArray(v)) {
        for (const [k, val] of Object.entries(v as Record<string, unknown>)) rows.push(newRow(k, String(val ?? "")));
      }
      maps[f.name] = rows.length > 0 ? rows : [newRow()];
    } else if (f.type === "string[]") {
      text[f.name] = f.secret ? "" : Array.isArray(v) ? v.map(String).join("\n") : typeof v === "string" ? v : "";
    } else {
      text[f.name] = f.secret ? "" : typeof v === "string" ? v : v === undefined || v === null ? "" : String(v);
    }
  }
  let name = existing?.name ?? DEFAULT_CONNECTION_NAME[kind.kind] ?? kind.kind;
  if (!existing && name && names.includes(name)) name = "";
  return { name, text, maps };
}

function lines(s: string): string[] {
  return s
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean);
}

function secretIsSet(existing: Connection | undefined, f: ConnectorField): boolean {
  return Boolean(existing?.secretsSet.includes(f.name));
}

/** Field errors by field name ("name" for the connection name). */
function validate(
  kind: ConnectorKindInfo,
  state: FormState,
  existing: Connection | undefined,
  names: string[],
): Record<string, string> {
  const errors: Record<string, string> = {};
  const name = state.name.trim();
  if (!existing) {
    if (!name) errors.name = "Enter a name for the connection.";
    else if (!CONNECTION_NAME_RE.test(name))
      errors.name = "Use lowercase letters, digits, \"_\" and \"-\", starting with a letter (up to 63).";
    else if (names.includes(name)) errors.name = `A connection named “${name}” exists. Pick another name, or edit that one.`;
  }
  for (const f of kind.fields) {
    if (f.type === "map") {
      const rows = state.maps[f.name] ?? [];
      const filled = rows.filter((r) => r.key.trim() || r.value);
      const seen = new Set<string>();
      for (const r of filled) {
        const k = r.key.trim();
        if (!k) {
          errors[f.name] = "Every value needs a name.";
          break;
        }
        if (seen.has(k.toLowerCase())) {
          errors[f.name] = `“${k}” appears twice.`;
          break;
        }
        seen.add(k.toLowerCase());
        if (!r.value && !r.saved) {
          errors[f.name] = `Enter a value for “${k}”, or remove the row.`;
          break;
        }
      }
      if (!errors[f.name] && f.required && filled.length === 0) errors[f.name] = `Add at least one entry to ${f.label}.`;
      continue;
    }
    const v = state.text[f.name] ?? "";
    if (!f.required) continue;
    if (f.type === "string[]") {
      if (lines(v).length === 0 && !(f.secret && secretIsSet(existing, f))) errors[f.name] = `Add at least one line to ${f.label}.`;
    } else if (!v.trim() && !(f.secret && secretIsSet(existing, f))) {
      errors[f.name] = `${f.label} is required.`;
    }
  }
  return errors;
}

function buildInput(kind: ConnectorKindInfo, state: FormState): ConnectionInput {
  const config: Record<string, unknown> = {};
  const secrets: Record<string, string> = {};
  for (const f of kind.fields) {
    if (f.type === "map") {
      const rows = (state.maps[f.name] ?? []).filter((r) => r.key.trim());
      if (f.secret) {
        for (const r of rows) if (r.value) secrets[r.key.trim()] = r.value;
      } else if (rows.length > 0) {
        config[f.name] = Object.fromEntries(rows.map((r) => [r.key.trim(), r.value]));
      }
      continue;
    }
    const v = state.text[f.name] ?? "";
    if (f.secret) {
      // Only filled secrets are sent; empty ones keep their saved value.
      const val = f.type === "string[]" ? lines(v).join("\n") : v;
      if (val) secrets[f.name] = val;
    } else if (f.type === "string[]") {
      const list = lines(v);
      if (list.length > 0) config[f.name] = list;
    } else if (v.trim()) {
      config[f.name] = v.trim();
    }
  }
  const input: ConnectionInput = { kind: kind.kind, config };
  if (Object.keys(secrets).length > 0) input.secrets = secrets;
  return input;
}

/**
 * Three-step wizard: pick a kind, follow its setup steps, fill in the fields.
 * Editing starts at the form; the kind cannot change.
 */
export function ConnectionDialog({
  ws,
  catalog,
  existing,
  names,
  onClose,
  onSignIn,
}: {
  ws: string;
  catalog: ConnectorCatalog;
  existing?: Connection;
  /** Names already in use in this workspace. */
  names: string[];
  onClose: () => void;
  onSignIn: (name: string) => Promise<void>;
}) {
  const startKind = existing ? catalog.kinds.find((k) => k.kind === existing.kind) : undefined;
  const [step, setStep] = useState<Step>(existing ? "form" : "kind");
  const [kind, setKind] = useState<ConnectorKindInfo | undefined>(startKind);
  const [state, setState] = useState<FormState | undefined>(() =>
    startKind ? initialState(startKind, existing, names) : undefined,
  );
  const [submitted, setSubmitted] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState<Connection | null>(null);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const queryClient = useQueryClient();
  const toast = useToast();

  // Move focus to the new step's heading so screen readers announce it.
  const firstStep = useRef(true);
  useEffect(() => {
    if (firstStep.current) {
      firstStep.current = false;
      return;
    }
    headingRef.current?.focus();
  }, [step]);

  const pickKind = (k: ConnectorKindInfo) => {
    if (kind?.kind !== k.kind) {
      setKind(k);
      setState(initialState(k, undefined, names));
      setSubmitted(false);
    }
  };

  const errors = kind && state ? validate(kind, state, existing, names) : {};
  const shownErrors = submitted ? errors : {};
  const stepNumber = step === "kind" ? 1 : step === "setup" ? 2 : 3;
  const title = existing ? `Edit ${existing.name}` : "Add connection";

  const save = async () => {
    if (!kind || !state) return;
    setSubmitted(true);
    if (Object.keys(errors).length > 0) {
      setError("Fix the fields marked above, then save again.");
      return;
    }
    setBusy(true);
    setError(null);
    const name = existing?.name ?? state.name.trim();
    try {
      const conn = await api.putConnection(ws, name, buildInput(kind, state));
      await queryClient.invalidateQueries({ queryKey: keys.connections(ws) });
      if (kind.oauth) {
        setSaved(conn);
        setBusy(false);
        setStep("done");
      } else {
        toast({
          tone: "success",
          title: existing ? `Saved ${name}` : `Added ${name}`,
          description: "Use Test on its card to check that it works.",
        });
        onClose();
      }
    } catch (err) {
      setError(errorMessage(err));
      setBusy(false);
    }
  };

  const onSubmit = () => {
    if (step === "kind") {
      if (kind) setStep("setup");
    } else if (step === "setup") {
      setStep("form");
    } else if (step === "form") {
      void save();
    } else {
      onClose();
    }
  };

  const footer =
    step === "done" ? (
      <>
        <Button onClick={onClose} disabled={busy}>
          {saved?.status === "needs_auth" ? "Later" : "Done"}
        </Button>
        <Button
          variant="primary"
          loading={busy}
          onClick={async () => {
            if (!saved) return;
            setBusy(true);
            setError(null);
            try {
              await onSignIn(saved.name);
            } catch (err) {
              setError(errorMessage(err));
              setBusy(false);
            }
          }}
        >
          {saved?.status === "needs_auth" ? "Sign in now" : "Sign in again"}
        </Button>
      </>
    ) : (
      <>
        {(step === "setup" && !existing) || step === "form" ? (
          <Button
            className="mr-auto"
            disabled={busy}
            onClick={() => {
              setError(null);
              setStep(step === "form" ? "setup" : "kind");
            }}
          >
            {step === "form" && existing ? "Setup steps" : "Back"}
          </Button>
        ) : null}
        <Button onClick={onClose} disabled={busy}>
          Cancel
        </Button>
        <Button type="submit" variant="primary" loading={busy} disabled={step === "kind" && !kind}>
          {step === "form" ? "Save" : step === "setup" && existing ? "Back to settings" : "Next"}
        </Button>
      </>
    );

  return (
    <Dialog
      open
      size="lg"
      onClose={() => !busy && onClose()}
      title={title}
      description={step === "done" ? undefined : `Step ${stepNumber} of 3`}
      onSubmit={onSubmit}
      footer={footer}
    >
      {step === "kind" && (
        <fieldset>
          <legend className="mb-3">
            <h3 ref={headingRef} tabIndex={-1} className="text-sm font-semibold text-fg outline-none">
              What do you want to connect?
            </h3>
          </legend>
          {catalog.kinds.length === 0 ? (
            <p className="text-[13px] text-muted">This server offers no connectors. Update Pendolo, then try again.</p>
          ) : (
            <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
              {catalog.kinds.map((k) => {
                const checked = kind?.kind === k.kind;
                return (
                  <label
                    key={k.kind}
                    className={cx(
                      "flex cursor-pointer gap-2.5 rounded-md border p-3 has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-ring",
                      checked ? "border-accent bg-accent-soft" : "border-line hover:bg-surface-2",
                    )}
                  >
                    <input
                      type="radio"
                      name="connection-kind"
                      value={k.kind}
                      checked={checked}
                      onChange={() => pickKind(k)}
                      className="mt-0.5 accent-[var(--accent)]"
                      data-autofocus={checked || (!kind && k === catalog.kinds[0]) ? true : undefined}
                    />
                    <span className="min-w-0">
                      <span className="block text-sm font-medium text-fg">{k.label}</span>
                      <span className="mt-0.5 block text-[12.5px] text-fg-2">{k.description}</span>
                    </span>
                  </label>
                );
              })}
            </div>
          )}
        </fieldset>
      )}

      {step === "setup" && kind && (
        <SetupStep kind={kind} redirectURL={catalog.oauthRedirectURL} headingRef={headingRef} />
      )}

      {step === "form" && kind && state && (
        <FormStep
          kind={kind}
          state={state}
          setState={setState}
          errors={shownErrors}
          existing={existing}
          headingRef={headingRef}
        />
      )}

      {step === "done" && saved && kind && (
        <div className="space-y-3">
          <h3
            ref={headingRef}
            tabIndex={-1}
            className="flex items-center gap-2 text-sm font-semibold text-fg outline-none"
          >
            <IconCheckCircle size={18} className="text-[var(--st-green-dot)]" />
            Saved {saved.name}
          </h3>
          {saved.status === "needs_auth" ? (
            <p className="text-[13px] text-fg-2">
              One more step: sign in with {kind.label} so Pendolo can act for that account. You can also do it later
              from the connection's card.
            </p>
          ) : (
            <p className="text-[13px] text-fg-2">
              {saved.account ? <>Signed in as {saved.account}. </> : null}
              Sign in again only to switch account or grant new permissions.
            </p>
          )}
        </div>
      )}

      <FormError>{error}</FormError>
    </Dialog>
  );
}

function SetupStep({
  kind,
  redirectURL,
  headingRef,
}: {
  kind: ConnectorKindInfo;
  redirectURL: string;
  headingRef: RefObject<HTMLHeadingElement | null>;
}) {
  return (
    <div className="space-y-4">
      <div>
        <h3 ref={headingRef} tabIndex={-1} className="text-sm font-semibold text-fg outline-none">
          Before you start: {kind.label}
        </h3>
        <p className="mt-0.5 text-[13px] text-fg-2">{kind.description}</p>
      </div>
      {kind.setupSteps.length === 0 ? (
        <p className="text-[13px] text-muted">Nothing to prepare. Go to the next step.</p>
      ) : (
        <ol className="space-y-2">
          {kind.setupSteps.map((s, i) => (
            <li key={i} className="flex gap-2.5 text-[13px] text-fg">
              <span
                aria-hidden="true"
                className="tnum flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-surface-3 text-[11px] font-semibold text-fg-2"
              >
                {i + 1}
              </span>
              <span className="min-w-0 break-words">
                <Linkify text={s} />
              </span>
            </li>
          ))}
        </ol>
      )}
      {kind.oauth && redirectURL && (
        <div className="rounded-md border border-line bg-surface-2 p-3">
          <p className="text-[13px] font-medium text-fg">Register this redirect URI</p>
          <p className="mt-0.5 text-[12.5px] text-fg-2">
            Add it to the authorized redirect URIs of your OAuth client, exactly as written.
          </p>
          <div className="mt-2 flex flex-wrap items-center gap-2">
            <code className="min-w-0 flex-1 rounded border border-line bg-surface px-2 py-1 font-mono text-[12.5px] break-all text-fg">
              {redirectURL}
            </code>
            <CopyButton text={redirectURL} label="Copy URI" />
          </div>
        </div>
      )}
      {kind.actions.length > 0 && (
        <details className="group">
          <summary className="cursor-pointer text-[13px] font-medium text-fg-2 hover:text-fg">
            What it can do ({kind.actions.length} actions)
          </summary>
          <div className="mt-2">
            <ActionList actions={kind.actions} />
          </div>
        </details>
      )}
    </div>
  );
}

function FormStep({
  kind,
  state,
  setState,
  errors,
  existing,
  headingRef,
}: {
  kind: ConnectorKindInfo;
  state: FormState;
  setState: (s: FormState) => void;
  errors: Record<string, string>;
  existing?: Connection;
  headingRef: RefObject<HTMLHeadingElement | null>;
}) {
  const name = existing?.name ?? (state.name.trim() || "<name>");
  return (
    <div className="space-y-4">
      <h3 ref={headingRef} tabIndex={-1} className="text-sm font-semibold text-fg outline-none">
        {kind.label} settings
      </h3>
      {existing ? (
        <div>
          <p className="text-[13px] font-medium text-fg">Name</p>
          <p className="mt-0.5 font-mono text-[13px] text-fg">{existing.name}</p>
          <NameHelp kind={kind} name={name} />
        </div>
      ) : (
        <Field
          label="Name"
          error={errors.name}
          hint={<NameHelp kind={kind} name={name} inline />}
        >
          {({ id, describedBy, invalid }) => (
            <Input
              id={id}
              value={state.name}
              onChange={(e) => setState({ ...state, name: e.target.value.toLowerCase() })}
              aria-required
              autoComplete="off"
              spellCheck={false}
              placeholder={kind.kind === "mcp" ? "e.g. github" : kind.kind}
              className="font-mono"
              aria-describedby={describedBy}
              aria-invalid={invalid}
              data-autofocus
            />
          )}
        </Field>
      )}

      {kind.fields.map((f) => (
        <ConnectorFieldInput
          key={f.name}
          field={f}
          state={state}
          setState={setState}
          error={errors[f.name]}
          secretSet={Boolean(existing?.secretsSet.includes(f.name))}
        />
      ))}
    </div>
  );
}

function NameHelp({ kind, name, inline }: { kind: ConnectorKindInfo; name: string; inline?: boolean }) {
  const Tag = inline ? "span" : "p";
  return (
    <Tag className={cx("text-[12px] text-muted", !inline && "mt-0.5")}>
      Use it in automations as <code className="font-mono text-fg-2">connection: {name}</code>.
      {kind.kind === "mcp" && (
        <>
          {" "}
          Actions are named <code className="font-mono text-fg-2">{name}.&lt;tool&gt;</code>.
        </>
      )}
      {inline && " Starts with a letter; lowercase letters, digits, - and _."}
    </Tag>
  );
}

function ConnectorFieldInput({
  field: f,
  state,
  setState,
  error,
  secretSet,
}: {
  field: ConnectorField;
  state: FormState;
  setState: (s: FormState) => void;
  error?: string;
  secretSet: boolean;
}) {
  const label = (
    <>
      {f.label}
      {!f.required && <span className="font-normal text-muted"> (optional)</span>}
    </>
  );
  const setText = (v: string) => setState({ ...state, text: { ...state.text, [f.name]: v } });
  const value = state.text[f.name] ?? "";

  if (f.type === "map") {
    return (
      <MapEditor
        label={label}
        help={f.help}
        secret={f.secret}
        rows={state.maps[f.name] ?? []}
        setRows={(rows) => setState({ ...state, maps: { ...state.maps, [f.name]: rows } })}
        error={error}
      />
    );
  }

  const keepHint = f.secret && secretSet ? "•••• set — leave empty to keep" : undefined;
  const hint = [f.help, f.type === "string[]" ? "One per line." : undefined].filter(Boolean).join(" ") || undefined;

  return (
    <Field label={label} hint={hint} error={error}>
      {({ id, describedBy, invalid }) =>
        f.type === "string[]" ? (
          <Textarea
            id={id}
            rows={4}
            value={value}
            onChange={(e) => setText(e.target.value)}
            placeholder={keepHint}
            spellCheck={false}
            className="font-mono text-[13px]"
            aria-describedby={describedBy}
            aria-invalid={invalid}
            aria-required={f.required && !secretSet}
          />
        ) : f.options && f.options.length > 0 && !f.secret ? (
          <Select
            id={id}
            value={value}
            onChange={(e) => setText(e.target.value)}
            aria-describedby={describedBy}
            aria-invalid={invalid}
            aria-required={f.required}
          >
            {(!f.required || !f.options.includes(value)) && <option value="">{f.required ? "Choose…" : "None"}</option>}
            {f.options.map((o) => (
              <option key={o} value={o}>
                {o}
              </option>
            ))}
          </Select>
        ) : (
          <Input
            id={id}
            type={f.secret ? "password" : "text"}
            value={value}
            onChange={(e) => setText(e.target.value)}
            placeholder={keepHint}
            autoComplete={f.secret ? "new-password" : "off"}
            spellCheck={false}
            aria-describedby={describedBy}
            aria-invalid={invalid}
            aria-required={f.required && !secretSet}
          />
        )
      }
    </Field>
  );
}

/** Key/value rows, e.g. HTTP headers for an MCP server. */
function MapEditor({
  label,
  help,
  secret,
  rows,
  setRows,
  error,
}: {
  label: ReactNode;
  help?: string;
  secret: boolean;
  rows: Row[];
  setRows: (rows: Row[]) => void;
  error?: string;
}) {
  const id = useId();
  const update = (rowId: number, patch: Partial<Row>) => setRows(rows.map((r) => (r.id === rowId ? { ...r, ...patch } : r)));
  const hasSaved = rows.some((r) => r.saved);
  return (
    <fieldset aria-describedby={`${id}-help ${error ? `${id}-err` : ""}`.trim()}>
      <legend className="mb-1.5 text-[13px] font-medium text-fg">{label}</legend>
      <div className="space-y-2">
        {rows.map((r, i) => (
          <div key={r.id} className="flex items-start gap-2">
            <Input
              aria-label={`Name ${i + 1}`}
              value={r.key}
              onChange={(e) => update(r.id, { key: e.target.value })}
              readOnly={r.saved}
              placeholder={secret ? "Header, e.g. Authorization" : "Name"}
              spellCheck={false}
              autoComplete="off"
              className={cx("min-w-0 flex-1 font-mono text-[13px]", r.saved && "bg-surface-2")}
            />
            <input
              aria-label={`Value ${i + 1}${r.key ? ` for ${r.key}` : ""}`}
              type={secret ? "password" : "text"}
              value={r.value}
              onChange={(e) => update(r.id, { value: e.target.value })}
              placeholder={r.saved ? "•••• set — leave empty to keep" : "Value"}
              spellCheck={false}
              autoComplete={secret ? "new-password" : "off"}
              className={cx(controlClass, "h-9 min-w-0 flex-1 text-[13px]")}
            />
            <IconButton
              label={`Remove ${r.key || `row ${i + 1}`}`}
              onClick={() => setRows(rows.length > 1 ? rows.filter((x) => x.id !== r.id) : [newRow()])}
              disabled={r.saved}
              className="mt-0.5 shrink-0"
            >
              <IconTrash size={15} />
            </IconButton>
          </div>
        ))}
      </div>
      <Button size="sm" variant="ghost" className="mt-2" icon={<IconPlus size={14} />} onClick={() => setRows([...rows, newRow()])}>
        Add {secret ? "header" : "entry"}
      </Button>
      <p id={`${id}-help`} className="mt-1 text-[12px] text-muted">
        {help}
        {secret && " Values are stored encrypted and never shown again."}
        {hasSaved && " Saved headers keep their value; to remove one, delete the connection and add it again."}
      </p>
      {error && (
        <p id={`${id}-err`} role="alert" className="mt-1 text-[12px] text-danger">
          {error}
        </p>
      )}
    </fieldset>
  );
}
