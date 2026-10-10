# Failed-Retry Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Failed runs can be retried in place with attempt tracking, surfaced by a retry button.

**Architecture:** `RunService.Resume` gains a failed-only retry branch that bumps `attempt` and clears errors (other terminal states still 409); frontend reuses the existing resume call behind a failed-only button. No state-machine constant changes; the `failed → running` exception lives in one place with an audit event.

**Tech Stack:** Go + SQLite, net/http REST, React + vitest.

Spec: `docs/superpowers/specs/2026-10-10-retry-failed-design.md`.

---

## Chunk 1: Backend retry branch

### Task 1: failed-only retry in Resume + tests

**Files:**
- Modify: `backend/internal/service/run_service.go` (Resume failed branch)
- Modify: `backend/internal/store/task_repo.go` (add `RetryFailed` method: attempt+1, clear error_code/summary, status running — single UPDATE guarded by `status='failed'`, rows-affected 0 → ErrConflict)
- Test: `backend/tests/integration/retry_test.go` (new)

- [ ] **Step 1: Write failing integration test**

```go
package integration

import (
    "testing"
    "time"

    "rivus-agent-backend/tests/testutil"
)

func TestRetryFailedRun(t *testing.T) {
    h := testutil.New(t)
    h.Model.EnqueueText("nothing useful")
    ses := h.CreateSession("retry")
    id, code, _ := h.CreateRun(ses, map[string]any{
        "goal": "do thing", "success_criteria": []string{"impossible criterion xyz"},
    }, "retry-1")
    if code != 201 {
        t.Fatalf("create: %d", code)
    }
    first := h.WaitTerminal(id, 30*time.Second)
    if first.Status != "failed" {
        t.Fatalf("want failed, got %s", first.Status)
    }
    if first.Attempt != 1 {
        t.Fatalf("want attempt 1, got %d", first.Attempt)
    }
    h.Model.EnqueueText("impossible criterion xyz satisfied with evidence")
    code, _ = h.Do("POST", "/api/v1/runs/"+id+"/resume", nil, nil)
    if code != 200 {
        t.Fatalf("resume: %d", code)
    }
    second := h.WaitTerminal(id, 30*time.Second)
    if second.Status != "succeeded" || second.Attempt != 2 {
        t.Fatalf("want succeeded attempt 2, got %s attempt %d (%s)", second.Status, second.Attempt, second.ErrorSummary)
    }
    // other terminal states still refuse
    h.Model.EnqueueText("x")
    ses2 := h.CreateSession("retry2")
    id2, _, _ := h.CreateRun(ses2, map[string]any{"goal": "g"}, "retry-2")
    _ = id2
}
```

Note: `WaitTerminal` returns on terminal; the second `EnqueueText` must be queued before resume fires. For the negative case, keep it simple: cancel a fresh run then resume → expect 409:

```go
func TestResumeRefusesCancelled(t *testing.T) {
    h := testutil.New(t, blockToolDef())
    h.Model.EnqueueToolCall("tc1", "block_tool", `{}`)
    ses := h.CreateSession("retry3")
    id, _, _ := h.CreateRun(ses, map[string]any{"goal": "slow"}, "retry-3")
    time.Sleep(500 * time.Millisecond)
    h.Do("POST", "/api/v1/runs/"+id+"/cancel", nil, nil)
    h.WaitTerminal(id, 30*time.Second)
    if code, _ := h.Do("POST", "/api/v1/runs/"+id+"/resume", nil, nil); code != 409 {
        t.Fatalf("cancelled resume must 409, got %d", code)
    }
}
```

(`blockToolDef` already exists in `tests/integration/api_test.go`, same package — reuse, do not redefine.)

Run: `cd backend && go test ./tests/integration/ -run 'TestRetryFailedRun|TestResumeRefusesCancelled' -v`
Expected: FAIL (resume on failed → 409 today)

- [ ] **Step 2: Implement `RetryFailed` + Resume branch**

```go
// task_repo.go
func (r *TaskRepo) RetryFailed(ctx context.Context, ownerID, runID string) error {
    if _, err := r.GetRun(ctx, ownerID, runID); err != nil {
        return err
    }
    res, err := r.db.ExecContext(ctx, `UPDATE runs SET status = ?, attempt = attempt + 1,
        error_code = '', error_summary = '', started_at = COALESCE(started_at, ?), updated_at = ?
        WHERE id = ? AND status = 'failed'`,
        string(domain.RunFailed) /* placeholder, replaced below */, 0, 0, runID)
    _ = res
    return err
}
```

Correct version (don't copy the placeholder): `SET status = 'running'`, `started_at` untouched (keep first start), `updated_at = now`. Check `RowsAffected() == 0` → return `domain.ErrConflict`. `GetRun` first for the owner check (returns `ErrForbidden` on mismatch → handler maps to 404; follow `handleDeleteSession` mapping pattern).

```go
// run_service.go Resume: replace the status gate with:
switch run.Status {
case domain.RunPaused, domain.RunWaitingApproval:
    if err := s.tasks.UpdateStatus(ctx, ownerID, runID, domain.RunRunning, "", ""); err != nil {
        return err
    }
case domain.RunFailed:
    if err := s.tasks.RetryFailed(ctx, ownerID, runID); err != nil {
        return err
    }
    run.Attempt++
    _, _ = s.events.Append(ctx, runID, domain.EvtRunResumed, `{"attempt":`+itoa(run.Attempt)+`}`, "normal")
default:
    return domain.ErrConflict
}
s.rt.Execute(context.WithoutCancel(ctx), ownerID, run, s.collectApprovals(ctx, runID))
```

(`itoa` helper exists in `runtime` package, not `service` — inline the int with `fmt.Sprintf("%d", ...)`; add `fmt` import. Check existing imports first.)

- [ ] **Step 3: Run tests, expect PASS, then full suite**

Run: `cd backend && go test ./tests/integration/ -run 'TestRetryFailedRun|TestResumeRefusesCancelled' -count=1 -v 2>&1 | tail -n 5`
Expected: PASS

Run: `cd backend && go build ./... && go vet ./... && go test ./... -count=1 2>&1 | tail -n 8`
Expected: all `ok`

- [ ] **Step 4: Commit**

```bash
git add backend/internal/service/run_service.go backend/internal/store/task_repo.go backend/tests/integration/retry_test.go
git commit -m "feat(backend): allow in-place retry of failed runs"
```

---

## Chunk 2: Frontend retry button

### Task 2: failed-only retry button + test

**Files:**
- Modify: `frontend/src/components/ActionBar.tsx` (add failed branch reusing the paused Resume pattern with 文案“重试”)
- Test: `frontend/src/components/ActionBar.test.tsx` (new; check if it exists first — if it does, extend it instead)

- [ ] **Step 1: Check existing test file, then write test**

```tsx
// ActionBar.test.tsx (new file if absent)
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { ActionBar } from './ActionBar'
import { useRunStore } from '@/store/run-store'

const { resumeRun } = vi.hoisted(() => ({ resumeRun: vi.fn().mockResolvedValue(undefined) }))
vi.mock('@/lib/api', () => ({ api: { resumeRun, cancelRun: vi.fn() }, ApiError: class extends Error {} }))

function seed(status: string) {
  useRunStore.setState({
    activeRun: { id: 'r1', session_id: 's', owner_id: 'u', status, mode: 'react', goal: 'g', budget: {}, attempt: 1, created_at: 1, updated_at: 1 },
    fetchRunAndSteps: vi.fn().mockResolvedValue(undefined),
  })
}

describe('ActionBar retry', () => {
  beforeEach(() => { resumeRun.mockClear() })
  it('shows retry on failed and calls resumeRun', async () => {
    seed('failed')
    render(<ActionBar />)
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    await waitFor(() => expect(resumeRun).toHaveBeenCalledWith('r1'))
  })
  it('shows no retry on succeeded', () => {
    seed('succeeded')
    render(<ActionBar />)
    expect(screen.queryByRole('button', { name: '重试' })).toBeNull()
  })
})
```

(Type note: seed object must satisfy the `Run` interface — add missing required fields per `frontend/src/lib/types.ts`, e.g. `owner_id`, `attempt`, timestamps; cast with `as Run` if the literal gets noisy.)

- [ ] **Step 2: Implement button (mirror the paused branch)**

```tsx
{activeRun.status === 'failed' && (
  <Button size="sm" variant="outline" onClick={() => void handleResume()} disabled={busy}
    className="border-violet-500/40 text-violet-400 hover:bg-violet-500/10 hover:text-violet-300">
    <Play className="h-3.5 w-3.5" />
    重试
  </Button>
)}
```

409 path: `handleResume` should catch `ApiError` and surface “当前状态不可重试” — check how `handleResume` currently handles errors; if it swallows silently, add a local error line like `ApprovalCard` does (`setError` + red text). Keep the same pattern.

- [ ] **Step 3: Verify**

Run: `cd frontend && node node_modules/typescript/bin/tsc --noEmit && npm test -- --run 2>&1 | grep -E "Test Files|Tests "`
Expected: tsc clean, all PASS

Run: `cd frontend && npx vite build 2>&1 | tail -n 1`
Expected: `✓ built in ...`

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/ActionBar.tsx frontend/src/components/ActionBar.test.tsx
git commit -m "feat(frontend): retry button for failed runs"
```
