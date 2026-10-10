# Frontend Dark Theme Unification Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Repair the broken shadcn semantic tokens and migrate business components off hardcoded zinc colors without moving any layout.

**Architecture:** Define dark-theme CSS variables in `index.css`, map them in `tailwind.config.ts`, then swap business-component color classes to semantic tokens. `ui/` components are untouched. Verification is `vite build` + screenshot diff, no new tests needed (visual change; existing `button.test.tsx` must keep passing).

**Tech Stack:** Tailwind CSS (class dark mode), CSS variables (HSL), shadcn/ui tokens, Vite build.

Spec: `docs/superpowers/specs/2026-10-10-frontend-dark-theme-design.md`.

---

## Chunk 1: Token foundation + component migration

### Task 1: Define dark-theme CSS variables

**Files:**
- Modify: `frontend/src/index.css`

- [ ] **Step 1: Add `:root` HSL variables for the dark palette**

```css
@tailwind base;
@tailwind components;
@tailwind utilities;

@layer base {
  :root {
    --background: 240 10% 3.9%;
    --foreground: 0 0% 98%;
    --card: 240 10% 3.9%;
    --card-foreground: 0 0% 98%;
    --muted: 240 3.7% 15.9%;
    --muted-foreground: 240 5% 64.9%;
    --border: 0 0% 100% / 0.1;
    --input: 240 3.7% 15.9%;
    --primary: 217 91% 60%;
    --primary-foreground: 0 0% 98%;
    --secondary: 240 3.7% 15.9%;
    --secondary-foreground: 0 0% 98%;
    --accent: 240 3.7% 15.9%;
    --accent-foreground: 0 0% 98%;
    --destructive: 0 72% 51%;
    --destructive-foreground: 0 0% 98%;
    --ring: 217 91% 60%;
  }
  * {
    @apply border-border;
  }
  body {
    @apply bg-background text-foreground;
  }
  ::-webkit-scrollbar {
    width: 8px;
    height: 8px;
  }
  ::-webkit-scrollbar-thumb {
    @apply rounded bg-zinc-800;
  }
}
```

- [ ] **Step 2: Verify variables parse**

Run: `cd frontend && npx tsc --noEmit`
Expected: PASS (no output)

- [ ] **Step 3: Commit**

```bash
git add frontend/src/index.css
git commit -m "feat(frontend): define dark-theme CSS variables"
```

### Task 2: Map semantic colors in tailwind.config.ts

**Files:**
- Modify: `frontend/tailwind.config.ts`

- [ ] **Step 1: Extend theme with token colors**

```ts
import type { Config } from 'tailwindcss'

function withOpacity(variable: string) {
  return `hsl(var(${variable}) / <alpha-value>)`
}

export default {
  darkMode: 'class',
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  theme: {
    extend: {
      colors: {
        background: withOpacity('--background'),
        foreground: withOpacity('--foreground'),
        card: { DEFAULT: withOpacity('--card'), foreground: withOpacity('--card-foreground') },
        muted: { DEFAULT: withOpacity('--muted'), foreground: withOpacity('--muted-foreground') },
        border: withOpacity('--border'),
        input: withOpacity('--input'),
        primary: { DEFAULT: withOpacity('--primary'), foreground: withOpacity('--primary-foreground') },
        secondary: { DEFAULT: withOpacity('--secondary'), foreground: withOpacity('--secondary-foreground') },
        accent: { DEFAULT: withOpacity('--accent'), foreground: withOpacity('--accent-foreground') },
        destructive: { DEFAULT: withOpacity('--destructive'), foreground: withOpacity('--destructive-foreground') },
        ring: withOpacity('--ring'),
      },
      borderRadius: { lg: '0.5rem', md: '0.375rem', sm: '0.25rem' },
    },
  },
  plugins: [],
} satisfies Config
```

- [ ] **Step 2: Verify shadcn token classes now resolve**

Run: `cd frontend && npx vite build 2>&1 | tail -n 3`
Expected: `✓ built in` with no CSS warnings about unknown classes

- [ ] **Step 3: Commit**

```bash
git add frontend/tailwind.config.ts
git commit -m "feat(frontend): map shadcn semantic colors in tailwind config"
```

### Task 3: Migrate business components to semantic classes

**Files:**
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/components/Header.tsx`
- Modify: `frontend/src/components/SessionPanel.tsx`
- Modify: `frontend/src/components/RunForm.tsx`
- Modify: `frontend/src/components/RunDetail.tsx`
- Modify: `frontend/src/components/EventStream.tsx`
- Modify: `frontend/src/components/ApprovalCard.tsx`
- Modify: `frontend/src/components/ActionBar.tsx`

Class mapping (apply mechanically, no layout changes):

| Before | After |
|---|---|
| `bg-zinc-950` (page) | `bg-background` |
| `bg-zinc-900`, `bg-zinc-900/40` (cards) | `bg-card` |
| `border-zinc-800` | `border-border` |
| `text-zinc-100` / `text-zinc-200` | `text-foreground` |
| `text-zinc-300` | `text-foreground/90` |
| `text-zinc-400` | `text-muted-foreground` |
| `text-zinc-500` | `text-muted-foreground/70` |
| status red/green/amber blocks | keep hue, normalize to `/10` bg + `/30` border |

- [ ] **Step 1: Apply mapping file by file, one commit per 2 files**

Run after each file: `cd frontend && npx tsc --noEmit`
Expected: PASS

- [ ] **Step 2: Run existing tests**

Run: `cd frontend && npm test -- --run 2>&1 | tail -n 5`
Expected: `button.test.tsx` PASS, no failures

- [ ] **Step 3: Build and screenshot-compare against pre-change build**

Run: `cd frontend && npx vite build 2>&1 | tail -n 2`
Expected: `✓ built in`; visually diff served pages, layout identical

- [ ] **Step 4: Grep for leftover hardcoded colors in business components**

Run: `cd frontend && grep -rn "zinc-" src/components/Header.tsx src/components/SessionPanel.tsx src/components/RunForm.tsx src/components/RunDetail.tsx src/components/EventStream.tsx src/components/ApprovalCard.tsx src/components/ActionBar.tsx src/App.tsx | grep -v "scrollbar" || true`
Expected: empty (only scrollbar styling in index.css may reference zinc)

- [ ] **Step 5: Commit**

```bash
git add frontend/src/App.tsx frontend/src/components/Header.tsx frontend/src/components/SessionPanel.tsx frontend/src/components/RunForm.tsx frontend/src/components/RunDetail.tsx frontend/src/components/EventStream.tsx frontend/src/components/ApprovalCard.tsx frontend/src/components/ActionBar.tsx
git commit -m "feat(frontend): migrate business components to semantic theme tokens"
```
