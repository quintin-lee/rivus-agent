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
npm run lint                # ESLint
npx tsc --noEmit            # type check only
npx vitest run              # run the full test suite once (20 files / 127 tests)
```

## Docker

The image is a multi-stage build (pnpm build + nginx hosting); `start.sh` renders
`RIVUS_BACKEND` (default `http://localhost:8080`) into the nginx config via `envsubst`,
reverse-proxying `/api`, `/healthz`, `/readyz`, `/metrics`. Under `docker compose`,
the frontend maps `8081→8080` and compose injects `RIVUS_BACKEND=http://backend:8080`.

## Directory

```text
src/
  components/   # chat, run cards, approval cards, session memories dialog, subagent tree, tool calls detail, streaming typing indicator, terminal-failed re-run, etc.
  store/        # Zustand state
  lib/          # API/SSE clients and utilities
  App.tsx       # root layout
```
