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
- Automations of the `default` workspace may also read the instance's secret files and environment; other workspaces only the ones listed in `secrets.shared`. The secrets Pendolo itself uses (the master key, the runner token, model keys, notification and sign-on secrets) are never available to automations. Every read is audited.
- Secret values are redacted from logs, step records, errors and model prompts. Shell steps get a minimal environment; values reach scripts only through `env`, never by interpolation into code.

**What automations may do**

- Every connector action declares a risk: read, write, send or destructive. The automation's autonomy level (observe, draft, ask, auto), capped by the workspace's maximum, decides whether an action runs, becomes a draft, waits for a person's approval or is blocked. Agent steps follow the same rules and cannot wait for approvals.
- Scripts run in a separate runner container with a read-only file system, no Linux capabilities and no access to the database, the configuration or other secrets.
- `web.fetch` refuses private, local, link-local and shared addresses, and connects directly so the check sees the real destination.
- Behind a reverse proxy (`server.trustProxy`), the client address is the last `X-Forwarded-For` entry, the one the proxy added.
- Single sign-on accepts only emails the provider marks as verified. A Google sign-in can only be finished in the browser, and by the person, that started it.
- Budgets stop runs that spend too much on models.

**The AI builder**

- The builder writes drafts; it cannot activate them. Only an admin or owner activates a draft, after it passes every check. Builders propose.
- Draft files are limited in number, size and names: plain relative paths, no hidden files, no `..`. They are written with an `os.Root`, so nothing lands outside the automation folder. Activation writes the new folder completely before it takes the place of the old one, which is restored if that fails.
- Drafts that contain something that looks like a credential are refused; credentials belong in secrets.
- A dry run executes only reads: read actions, model calls and `GET` requests to public addresses. Writes, messages, scripts and approvals are recorded as what they would do. A dry run never resolves a secret, so a builder cannot use one to see or send a secret value.
- Activation names the version of the files the admin reviewed: if the draft changed after that, it is refused.
- The person's request and the model's answer are kept in the draft's conversation, and every activated version is kept in the history.

**Deployment**

- The container runs as an unprivileged user. Publish port 8080 beyond localhost only behind a reverse proxy with TLS, and then set `server.trustProxy`.
- Give webhook triggers a `secret`: callers then need it as a bearer token or an HMAC signature. A webhook without one accepts anyone who knows its URL; the validator warns about it.
- Back up the data volume and the master key separately: the database alone does not reveal the secrets.

## Known limits

- **Builders can read through connections.** A dry run executes the draft's read actions with the workspace's connections, so a builder can read, for example, the workspace's email. Give the builder role only to people trusted with the workspace's data.
- **Reads can carry data out.** `web.fetch` and search are read actions, allowed at every autonomy level. Text injected into an email or a page could ask an agent to fetch a URL that carries data in it. Keep agents' tools to what they need, and prefer `observe` or `draft` for automations that read untrusted content.
- **MCP servers describe their own tools.** A tool's risk comes from the server's hints (`readOnlyHint`, `destructiveHint`) unless the connection overrides it with `risks`. Connect only MCP servers you trust, or set their risks.
- **Webhooks without a secret** accept anyone who knows the URL.
- **Scripts and plain `http` steps** reach whatever the runner and the server can reach on the network; that is how checks of internal servers work. Activation by an admin is the control.
