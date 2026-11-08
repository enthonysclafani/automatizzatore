import { useQuery, useQueryClient } from "@tanstack/react-query";
import { getRouteApi, Navigate, useNavigate } from "@tanstack/react-router";
import { useRef, useState } from "react";
import { api, errorMessage, isApiError } from "../api/client";
import { Button, buttonClass } from "../components/ui/Button";
import { Field, FormError, Input } from "../components/ui/Input";
import { keys, setupQuery } from "../lib/queries";
import { AuthShell } from "./AuthShell";

const route = getRouteApi("/login");

export function LoginPage() {
  const { redirect, error: ssoError } = route.useSearch();
  const setup = useQuery(setupQuery);
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [needCode, setNeedCode] = useState(false);
  const [error, setError] = useState<string | null>(ssoError ?? null);
  const [busy, setBusy] = useState(false);
  const codeRef = useRef<HTMLInputElement>(null);

  if (setup.data?.needsSetup) return <Navigate to="/setup" />;

  const oidc = setup.data?.oidc;

  const submit = async () => {
    setBusy(true);
    setError(null);
    try {
      const me = await api.login({ email: email.trim(), password, ...(needCode ? { code: code.trim() } : {}) });
      queryClient.clear();
      queryClient.setQueryData(keys.me, me);
      if (redirect) await navigate({ to: redirect });
      else await navigate({ to: "/" });
    } catch (err) {
      setBusy(false);
      if (isApiError(err) && err.code === "totp_required") {
        setNeedCode(true);
        setError(null);
        window.setTimeout(() => codeRef.current?.focus(), 0);
        return;
      }
      if (isApiError(err) && err.code === "totp_invalid") {
        setCode("");
        setError("That code is not valid. Enter the current 6-digit code from your authenticator app.");
        codeRef.current?.focus();
        return;
      }
      if (isApiError(err) && err.code === "invalid_credentials") {
        setError("Wrong email or password. Check both and try again.");
        return;
      }
      setError(errorMessage(err));
    }
  };

  return (
    <AuthShell
      title={needCode ? "Two-factor authentication" : "Sign in"}
      subtitle={needCode ? "Enter the 6-digit code from your authenticator app." : undefined}
    >
      <form
        className="space-y-4"
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
      >
        {!needCode ? (
          <>
            <Field label="Email">
              {({ id }) => (
                <Input
                  id={id}
                  type="email"
                  required
                  autoComplete="username"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  autoFocus
                />
              )}
            </Field>
            <Field label="Password">
              {({ id }) => (
                <Input
                  id={id}
                  type="password"
                  required
                  autoComplete="current-password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                />
              )}
            </Field>
          </>
        ) : (
          <Field label="Authentication code">
            {({ id }) => (
              <Input
                id={id}
                ref={codeRef}
                inputMode="numeric"
                pattern="[0-9]{6}"
                maxLength={6}
                required
                autoComplete="one-time-code"
                className="tnum text-center font-mono text-lg tracking-[0.4em]"
                value={code}
                onChange={(e) => setCode(e.target.value.replace(/\D/g, "").slice(0, 6))}
              />
            )}
          </Field>
        )}
        <FormError>{error}</FormError>
        <Button type="submit" variant="primary" className="w-full" loading={busy}>
          {needCode ? "Verify" : "Sign in"}
        </Button>
        {needCode && (
          <button
            type="button"
            className="w-full text-center text-[13px] text-muted hover:text-fg"
            onClick={() => {
              setNeedCode(false);
              setCode("");
              setError(null);
            }}
          >
            Use a different account
          </button>
        )}
      </form>

      {oidc?.enabled && !needCode && (
        <>
          <div className="my-5 flex items-center gap-3 text-[12px] text-muted">
            <span className="h-px flex-1 bg-line" />
            or
            <span className="h-px flex-1 bg-line" />
          </div>
          <a href={api.oidcLoginUrl} className={buttonClass("secondary", "md", "w-full")}>
            Sign in with {oidc.label || "single sign-on"}
          </a>
        </>
      )}
    </AuthShell>
  );
}
