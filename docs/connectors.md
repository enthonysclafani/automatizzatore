# Connectors

Automations reach outside services through **connections**, created per workspace on the Connections page. Credentials are encrypted with the master key and never appear in the dashboard, the API, logs or model prompts. A step names an action as `<connector>.<action>`; when the workspace has one connection of the kind (or one named like the connector), `connection:` can be left out.

## Actions

| Action | Risk | Input | Output |
| --- | --- | --- | --- |
| `gmail.search` | read | `query` (Gmail search), `maxResults` (1–100, default 20) | list of `{id, threadId, from, to, subject, date, snippet, labels}` |
| `gmail.get_message` | read | `messageId` | the message with `body` (plain text) |
| `gmail.apply_labels` | write | `messageId`, `labels` (created if missing), `removeLabels`, `markRead` | `{messageId, labels}` |
| `gmail.create_draft` | write | `messageId` to reply in its thread (sets To, `Re:` subject and reply headers), or `to` and `subject`; `body` | `{draftId, threadId, to, subject}` |
| `gmail.send` | send (draft form: `gmail.create_draft`) | as `create_draft` | `{messageId, threadId, to, subject}` |
| `google_docs.create` | write | `title`, `body` | `{documentId, url}` |
| `google_docs.append` | write | `documentId`, `text` | `{documentId, url}` |
| `slack.send_message` | send | `channel` (`#name` or id), `text` | `{channel, ts}` |
| `web.search` | read | `query`, `count` (1–20, default 8) | list of `{title, url, snippet}` |
| `web.fetch` | read | `url` (http or https) | `{url, status, title, text}`; private and loopback addresses are refused; needs no connection |
| `<mcp connection>.<tool>` | from the server's hints, or your overrides | the tool's own schema | the tool's structured result, or its text (parsed when it is JSON) |

## Google (Gmail and Docs)

You use your own Google OAuth client, so no third party holds access to your mailbox. The Connections page walks through it:

1. Create a Google Cloud project, enable the Gmail API and the Google Docs API.
2. Configure the OAuth consent screen (External) and add yourself as a test user.
3. Create an OAuth client of type *Web application* with the redirect URI shown in the dashboard (`<server.publicURL>/api/v1/oauth/callback`; it must be reachable by your browser, `http://localhost:8080` works on the same machine).
4. Paste the client ID and secret, save, then *Sign in with Google*.

Pendolo asks for `gmail.modify`, `documents` and `drive.file` (documents it creates). Tokens refresh by themselves; if Google revokes them, the connection turns to *error* and asks you to sign in again. While the consent screen is in *Testing*, Google expires the sign-in after 7 days.

## Slack

Create a Slack app with the `chat:write` bot scope, install it, paste the bot token (`xoxb-...`), and invite the bot to the channels it posts to.

## Web search

Brave Search or Tavily (API key), or your own SearXNG (`baseURL`, JSON output enabled). `web.fetch` works without a connection.

## MCP servers

Any server that speaks MCP over Streamable HTTP. Give the connection a short name (it becomes the action prefix), the server URL and its headers (usually `Authorization: Bearer <key>`); headers are stored encrypted. The tools appear on the connection's card after *Test*. A tool marked read-only by the server is a read; one marked destructive is destructive; anything else counts as a write. Correct wrong hints with risk overrides (`tool → read|write|send|destructive`).

For example, with an OpenSEO API key, an `openseo` connection makes `openseo.get_keyword_metrics` and its other tools available to steps and agents.
