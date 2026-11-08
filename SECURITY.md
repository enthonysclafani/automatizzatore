# Security

## Reporting a problem

Please report vulnerabilities privately, through GitHub's "Report a vulnerability" on this repository, not in a public issue. Say what an attacker can do, with the steps to reproduce. You will get an answer within a week.

## How Pendolo protects you

Pendolo runs automations that hold credentials and act on your accounts. These are the defences it relies on; changes to the code must keep them (see [CLAUDE.md](CLAUDE.md)).

**People and workspaces**

- Passwords are hashed with Argon2id; sign-in is rate-limited per address and per email; two-factor codes (TOTP) cannot be replayed. Single sign-on uses OIDC with PKCE and a nonce.
- Sessions and API tokens are random, stored only as hashes, and sent as `HttpOnly`, `SameSite` cookies or bearer tokens. State-changing requests from browsers need the `X-Pendolo-Request` header, which other sites cannot set.
- Every workspace endpoint checks membership and role. Someone who is not a member of a workspace gets 404, as if it did not exist. Roles: viewer, approver, builder, admin, plus instance owners.
- Every sign-in, change, approval, activation and secret read by an automation is recorded in the audit log.

**Secrets**

- Secrets saved in the dashboard and connection credentials are encrypted with AES-256-GCM under the master key, each bound to its workspace and name.
- Manifests refer to secrets as `secret://<name>`. A reference is resolved only when it is written literally in the manifest: never from an expression, an email, a web page, a tool result or a model's answer. This stops injected text from asking for a secret.
- Secret values are redacted from logs, step records, errors and model prompts. Shell steps get a minimal environment; values reach scripts only through `env`, never by interpolation into code.

**What automations may do**

- Every connector action declares a risk: read, write, send or destructive. The automation's autonomy level (observe, draft, ask, auto), capped by the workspace's maximum, decides whether an action runs, becomes a draft, waits for a person's approval or is blocked. Agent steps follow the same rules and cannot wait for approvals.
- Scripts run in a separate runner container with a read-only file system, no Linux capabilities and no access to the database, the configuration or other secrets.
- `web.fetch` refuses private and local addresses.
- Budgets stop runs that spend too much on models.

**The AI builder**

- The builder writes drafts; it cannot activate them. Only an admin or owner activates a draft, after it passes every check. Builders propose.
- Draft files are limited in number, size and names: plain relative paths, no hidden files, no `..`. They are written with an `os.Root`, so nothing lands outside the automation folder. Activation swaps the folder in a single rename.
- Drafts that contain something that looks like a credential are refused; credentials belong in secrets.
- A dry run executes only reads. Writes, messages, scripts and approvals are recorded as what they would do.
- The person's request and the model's answer are kept in the draft's conversation, and every activated version is kept in the history.

**Deployment**

- The container runs as an unprivileged user. Publish port 8080 beyond localhost only behind a reverse proxy with TLS, and then set `server.trustProxy`.
- Give webhook triggers a `secret`: callers then need it as a bearer token or an HMAC signature. A webhook without one accepts anyone who knows its URL; the validator warns about it.
- Back up the data volume and the master key separately: the database alone does not reveal the secrets.
