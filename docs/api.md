# HTTP API (v1)

The dashboard and scripts use the same JSON API under `/api/v1`. Types are defined in [web/src/api/types.ts](../web/src/api/types.ts).

## Authentication

- **Browser:** `POST /api/v1/auth/login` sets the `pendolo_session` cookie (HttpOnly, SameSite=Lax).
- **Scripts:** `Authorization: Bearer pdl_...` with an API token created in the dashboard (Account page). A token acts as the user who created it.
- Every request that changes something (POST, PUT, PATCH, DELETE) made with the session cookie must carry the header `X-Pendolo-Request: 1`. Browsers do not let other sites set custom headers, so this blocks cross-site request forgery. Token requests do not need it.

Errors are `{"error": "...", "code": "..."}` with a fitting HTTP status: 400 invalid input, 401 not signed in (`code: "unauthenticated"`) or wrong credentials (`invalid_credentials`, `totp_required`, `totp_invalid`), 403 `forbidden`, 404 `not_found`, 409 `conflict`, 429 `rate_limited`.

## Roles

Instance **owners** can do everything in every workspace. In a workspace:

| Permission | viewer | approver | builder | admin |
| --- | --- | --- | --- | --- |
| See automations, runs, costs | yes | yes | yes | yes |
| Trigger, pause, resume automations | | | yes | yes |
| See secret names | | | yes | yes |
| Create, change, delete secrets | | | | yes |
| Manage members, see the audit log | | | | yes |

A user never sees a workspace they are not a member of: its endpoints answer 404. An owner's `Me.workspaces` lists every workspace, with role `owner`.

## Conventions

- Times are RFC 3339 in UTC. Daily figures (`Overview.daily`, `Costs.daily`) and "this month" (`costMonthEur`) use UTC days.
- `GET .../runs` returns at most `limit` runs (default 50, at most 200), newest first; pass `next` back as `before` for the next page.
- `GET .../audit` and `GET /audit` return the newest `limit` entries (default 100, at most 1000).
- `AutomationDetail.steps` lists nested steps too, with ids like `each.note` for a step inside a `foreach`.
- `LLMCall.request` is what the gateway sent: `{model, system, user, schema?, temperature?, maxTokens, effort?, fallbacks?}`.
- `Health.runner` is `local`, `ok`, or the reason the runner cannot be reached.
- Validation: passwords need at least 12 bytes; secret names use letters, digits, `.`, `_` and `-` (up to 128); workspace ids use lowercase letters, digits and hyphens (up to 63).
- When single sign-on fails, the callback redirects to `/login?error=<message for people>`.
- Not available yet: cancelling a run, renaming or deleting workspaces, deleting users (disable them instead), approvals (phase 3).

## Endpoints

| Method and path | Who | Body | Answer |
| --- | --- | --- | --- |
| `GET /api/v1/setup` | anyone | | `SetupStatus` |
| `POST /api/v1/setup` | anyone, only while no owner exists | `SetupRequest` | `Me` (and signs in) |
| `POST /api/v1/auth/login` | anyone | `LoginRequest` | `Me` |
| `POST /api/v1/auth/logout` | signed in | | 204 |
| `GET /api/v1/auth/me` | signed in | | `Me` |
| `GET /api/v1/auth/oidc/login` | anyone, when OIDC is on | | redirect to the provider |
| `GET /api/v1/auth/oidc/callback` | provider | | redirect to `/` |
| `POST /api/v1/auth/password` | signed in | `{current, new}` | 204 |
| `POST /api/v1/auth/totp/setup` | signed in | | `{secret, uri}` |
| `POST /api/v1/auth/totp/enable` | signed in | `{code}` | 204 |
| `POST /api/v1/auth/totp/disable` | signed in | `{password}` | 204 |
| `GET /api/v1/tokens` | signed in | | `{tokens: ApiToken[]}` |
| `POST /api/v1/tokens` | signed in | `{name, expiresInDays?}` | `NewApiToken` |
| `DELETE /api/v1/tokens/{id}` | signed in | | 204 |
| `GET /api/v1/workspaces/{ws}/overview` | viewer | | `Overview` |
| `GET /api/v1/workspaces/{ws}/automations` | viewer | | `{automations: AutomationSummary[]}` |
| `GET /api/v1/workspaces/{ws}/automations/{name}` | viewer | | `AutomationDetail` |
| `POST /api/v1/workspaces/{ws}/automations/{name}/trigger` | builder | | `{runId}` |
| `POST /api/v1/workspaces/{ws}/automations/{name}/pause` | builder | | 204 |
| `POST /api/v1/workspaces/{ws}/automations/{name}/resume` | builder | | 204 |
| `GET /api/v1/workspaces/{ws}/runs?automation=&status=&limit=&before=` | viewer | | `RunList` |
| `GET /api/v1/workspaces/{ws}/runs/{id}` | viewer | | `RunDetail` |
| `GET /api/v1/workspaces/{ws}/costs?days=30` | viewer | | `Costs` |
| `GET /api/v1/workspaces/{ws}/secrets` | builder | | `{secrets: SecretInfo[]}` |
| `PUT /api/v1/workspaces/{ws}/secrets/{name}` | admin | `{value}` | 204 |
| `DELETE /api/v1/workspaces/{ws}/secrets/{name}` | admin | | 204 |
| `GET /api/v1/workspaces/{ws}/members` | admin | | `{members: Member[]}` |
| `PUT /api/v1/workspaces/{ws}/members` | admin | `{email, role}` | 204 |
| `DELETE /api/v1/workspaces/{ws}/members/{userId}` | admin | | 204 |
| `GET /api/v1/workspaces/{ws}/audit?limit=` | admin | | `{entries: AuditEntry[]}` |
| `POST /api/v1/workspaces` | owner | `{id, name}` | `WorkspaceRef` |
| `GET /api/v1/users` | owner | | `{users: User[]}` |
| `POST /api/v1/users` | owner | `{email, name, password, isOwner}` | `User` |
| `PATCH /api/v1/users/{id}` | owner | `{name?, disabled?, isOwner?, password?}` | `User` |
| `GET /api/v1/audit?limit=` | owner | | `{entries: AuditEntry[]}` |
| `GET /api/v1/system` | owner | | `SystemInfo` |

`GET /healthz` and `POST /hooks/{automation}` stay outside `/api` and do not use these credentials.
