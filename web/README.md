# Pendolo dashboard

The web dashboard: React, TypeScript, Vite, TanStack Router and Query, Tailwind CSS, recharts. It talks only to the JSON API under `/api/v1` (see [docs/api.md](../docs/api.md)); every shape is in [src/api/types.ts](src/api/types.ts).

## Develop

You need Node.js 22 or newer and the Go server running on port 8080.

```bash
./bin/pendolo server        # in the repository root, listens on :8080
cd web
npm install
npm run dev                 # http://localhost:5173
```

The Vite dev server proxies `/api`, `/healthz` and `/hooks` to `http://localhost:8080`, so the session cookie and the API stay same-origin. On a fresh data folder the dashboard opens the setup form; the setup token is printed in the server log.

| Command | What it does |
| --- | --- |
| `npm run dev` | Dev server with hot reload. |
| `npm run build` | Type-checks, then writes the production build to `dist/`. |
| `npm run typecheck` | `tsc --noEmit` only. |
| `npm run lint` | ESLint (typescript-eslint, react-hooks). |

Dependencies are pinned to exact versions. Update them on purpose, one at a time, and keep `package-lock.json` in the commit.

## How the build is embedded

`npm run build` writes `index.html` and hashed files under `assets/` to `web/dist`. To build straight into the folder the Go server embeds, pass the output folder to Vite:

```bash
npm run build -- --outDir ../internal/webui/dist --emptyOutDir
```

The server embeds that folder (`//go:embed all:dist` in `internal/webui`) and serves it at `/`:

- Paths under `/api/`, `/healthz` and `/hooks/` go to the API; any other path that is not a file in the build gets `index.html` (SPA fallback), because routes such as `/w/acme/runs/123` exist only in the browser.
- `public/.gitkeep` is copied into every build, so emptying the output folder keeps its `.gitkeep` and the Go build works before the dashboard is built. Because it starts with a dot, the embed directive needs the `all:` prefix.
- Files in `assets/` have content hashes in their names and can be cached for a long time; `index.html` should not be cached.

## Layout

```
src/api/          types.ts (the contract) and client.ts (one function per endpoint)
src/lib/          formatting, roles, queries and polling, theme, cron descriptions
src/components/   ui/ (Button, Badge, Card, Table, Dialog, Input, Select, Tabs, Toast...), charts/, domain/
src/layout/       sidebar and page frame
src/pages/        one file per screen
src/router.tsx    all routes (code-based)
```

Conventions: money is `€0.0000` below €1 and `€0.00` above; status colors are the same everywhere (succeeded green, failed red, running blue, queued and skipped gray, cancelled amber) and always come with an icon and a label. Chart colors live in `src/components/charts/palette.ts` and were checked for color-blind separation in light and dark mode; every chart has a Table view.
