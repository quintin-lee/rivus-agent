# Rivus Agent Frontend

A conversational UI built with React 18 + Vite + TypeScript + Tailwind CSS + Zustand,
accessing the backend REST / SSE APIs through a proxy.

## Quick Start

```bash
npm install        # Docker image builds use pnpm; npm is fine for local development
npm run dev        # http://localhost:5173
```

The dev server proxies `/api`, `/healthz`, `/readyz` to the backend
(change the target via the `VITE_BACKEND_URL` env var; default `http://localhost:8080`).
To start the backend, see the [root README](../README.en.md) or run `docker compose up -d`.

## Common Commands

```bash
npm run build               # tsc && vite build → dist/
npm run preview             # preview the build output
npm run lint                # ESLint (v10 flat config)
npx tsc --noEmit            # type check only
npx vitest run              # run the full test suite once (26 files / 169 tests)
```

## Internationalization (i18n)

A minimal bilingual mechanism is built in: `src/i18n/index.ts` dictionary +
`t(key, params?)`, with the language in `ui-store` (`lang: 'zh' | 'en'`,
default zh, persisted to localStorage `rivus-lang`) and toggled by the 中/EN
button in the Header. Missing en keys fall back to zh; missing zh keys fall
back to the key itself. No react-i18next. Migrated as samples: the session
list/delete dialog, memories dialog, run history dialog, and the usage
summary in `status.ts`.

## Docker

The image is a multi-stage build (pnpm build + nginx hosting); `start.sh` renders
`RIVUS_BACKEND` (default `http://localhost:8080`) into the nginx config via `envsubst`,
reverse-proxying `/api`, `/healthz`, `/readyz`, `/metrics`. Under `docker compose`,
the frontend maps `8081→8080` and compose injects `RIVUS_BACKEND=http://backend:8080`.

## Directory

```text
src/
  components/   # chat, run cards, approval cards, session memories dialog, subagent tree, tool calls detail, streaming typing indicator, terminal-failed re-run, run history dialog, theme toggle (light/dark/system, no flash), language toggle (zh/en), etc.
  store/        # Zustand state
  lib/          # API/SSE clients and utilities
  App.tsx       # root layout
```
