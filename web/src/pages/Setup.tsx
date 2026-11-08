import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Navigate, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { api, errorMessage } from "../api/client";
import { Button } from "../components/ui/Button";
import { Field, FormError, Input } from "../components/ui/Input";
import { ErrorState, SkeletonRows } from "../components/ui/States";
import { keys, setupQuery } from "../lib/queries";
import { AuthShell } from "./AuthShell";

const MIN_PASSWORD = 12;

export function SetupPage() {
  const setup = useQuery(setupQuery);
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [form, setForm] = useState({ token: "", name: "", email: "", password: "", confirm: "" });
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [touched, setTouched] = useState(false);

  if (setup.isPending) {
    return (
      <AuthShell title="Set up Pendolo">
        <SkeletonRows rows={4} className="p-0" />
      </AuthShell>
    );
  }
  if (setup.isError) {
    return (
      <AuthShell title="Set up Pendolo">
        <ErrorState error={setup.error} onRetry={() => void setup.refetch()} className="py-4" />
      </AuthShell>
    );
  }
  if (!setup.data.needsSetup) return <Navigate to="/login" />;

  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) =>
    setForm((f) => ({ ...f, [k]: e.target.value }));

  const pwError =
    touched && form.password.length > 0 && form.password.length < MIN_PASSWORD
      ? `Use at least ${MIN_PASSWORD} characters.`
      : undefined;
  const confirmError =
    touched && form.confirm.length > 0 && form.confirm !== form.password ? "The passwords do not match." : undefined;

  return (
    <AuthShell
      title="Set up Pendolo"
      subtitle="Create the first account. It becomes the instance owner and can invite everyone else."
    >
      <form
        className="space-y-4"
        onSubmit={async (e) => {
          e.preventDefault();
          setTouched(true);
          if (form.password.length < MIN_PASSWORD || form.password !== form.confirm) return;
          setBusy(true);
          setError(null);
          try {
            const me = await api.setup({
              token: form.token.trim(),
              name: form.name.trim(),
              email: form.email.trim(),
              password: form.password,
            });
            queryClient.setQueryData(keys.me, me);
            queryClient.setQueryData(keys.setup, { ...setup.data, needsSetup: false });
            void navigate({ to: "/" });
          } catch (err) {
            setError(errorMessage(err));
            setBusy(false);
          }
        }}
      >
        <Field
          label="Setup token"
          hint="Printed in the server log at startup while no user exists: the output of pendolo server, or docker compose logs."
        >
          {({ id, describedBy }) => (
            <Input
              id={id}
              aria-describedby={describedBy}
              required
              autoComplete="off"
              spellCheck={false}
              className="font-mono"
              value={form.token}
              onChange={set("token")}
              autoFocus
            />
          )}
        </Field>
        <Field label="Name">
          {({ id }) => <Input id={id} required autoComplete="name" value={form.name} onChange={set("name")} />}
        </Field>
        <Field label="Email">
          {({ id }) => (
            <Input id={id} type="email" required autoComplete="email" value={form.email} onChange={set("email")} />
          )}
        </Field>
        <Field label="Password" hint={`At least ${MIN_PASSWORD} characters.`} error={pwError}>
          {({ id, describedBy, invalid }) => (
            <Input
              id={id}
              type="password"
              required
              minLength={MIN_PASSWORD}
              autoComplete="new-password"
              aria-describedby={describedBy}
              aria-invalid={invalid}
              value={form.password}
              onChange={set("password")}
            />
          )}
        </Field>
        <Field label="Confirm password" error={confirmError}>
          {({ id, describedBy, invalid }) => (
            <Input
              id={id}
              type="password"
              required
              autoComplete="new-password"
              aria-describedby={describedBy}
              aria-invalid={invalid}
              value={form.confirm}
              onChange={set("confirm")}
            />
          )}
        </Field>
        <FormError>{error}</FormError>
        <Button type="submit" variant="primary" className="w-full" loading={busy}>
          Create owner account
        </Button>
      </form>
    </AuthShell>
  );
}
