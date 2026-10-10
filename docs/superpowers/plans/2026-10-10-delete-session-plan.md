# Delete Session Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deletable sessions with cascaded cleanup, protected active runs, and a confirm dialog.

**Architecture:** New `DeleteSession` store method (ownership check + active-run guard + single-transaction cascade) behind `DELETE /api/v1/sessions/{id}`; frontend row button + confirm dialog wired to a new store action that also switches the active session. Reuse existing `DeleteByRunPrefix`, `writeErr` codes, and Dialog/Button primitives.

**Tech Stack:** Go + SQLite, net/http REST, React + shadcn Dialog, vitest.

Spec: `docs/superpowers/specs/2026-10-10-delete-session-design.md`.

---

## Chunk 1: Backend cascade delete

### Task 1: store DeleteSession + unit test

**Files:**
- Modify: `backend/internal/store/task_repo.go` (append method)
- Test: `backend/internal/store/task_repo_test.go` (create; follow `store_test.go` style)

- [ ] **Step 1: Write failing test**

```go
func TestDeleteSessionCascade(t *testing.T) {
    db, err := Open(":memory:")
    if err != nil { t.Fatal(err) }
    defer db.Close()
    ctx := context.Background()
    tasks := NewTaskRepo(db)
    events := NewEventRepo(db)
    approvals := NewApprovalRepo(db)
    ses, _ := tasks.CreateSession(ctx, "u1", "t")
    run := &domain.Run{ID: "run_1", SessionID: ses, OwnerID: "u1", Status: domain.RunQueued,
        Mode: "react", Goal: "g",
        Budget:  domain.Budget{MaxDurationSeconds: 60, MaxModelCalls: 5, MaxToolCalls: 5, MaxIterations: 3, MaxOutputBytes: 1000},
        Attempt: 1}
    if err := tasks.CreateRun(ctx, run); err != nil { t.Fatal(err) }
    if _, err := events.Append(ctx, "run_1", domain.EvtRunStarted, `{}`, ""); err != nil { t.Fatal(err) }
    if err := tasks.DeleteSession(ctx, "u1", ses); err != nil { t.Fatal(err) }
    if _, err := tasks.GetSession(ctx, "u1", ses); err == nil { t.Fatal("session must be gone") }
    if evs, _ := events.ListAfter(ctx, "run_1", 0, 10); len(evs) != 0 { t.Fatal("events must be gone") }
    // idempotent second delete
    if err := tasks.DeleteSession(ctx, "u1", ses); err == nil { t.Fatal("second delete must fail") }
}

func TestDeleteSessionRefusesActiveRun(t *testing.T) {
    // same setup but run Status: domain.RunRunning
    // expect err == domain.ErrConflict
}

func TestDeleteSessionWrongOwner(t *testing.T) {
    // expect err == domain.ErrForbidden (maps to 404 upstream)
}
```

Run: `cd backend && go test ./internal/store/ -run TestDeleteSession -v`
Expected: FAIL with "undefined: DeleteSession"

- [ ] **Step 2: Minimal implementation (append to task_repo.go)**

```go
func (r *TaskRepo) DeleteSession(ctx context.Context, ownerID, sessionID string) error {
    owner, _, _, _, err := r.GetSession(ctx, ownerID, sessionID)
    if err != nil {
        return err // sql.ErrNoRows propagates; handler maps to 404
    }
    _ = owner
    var active int
    err = r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE session_id = ?
        AND status IN ('queued','running','waiting_approval','paused')`, sessionID).Scan(&active)
    if err != nil {
        return err
    }
    if active > 0 {
        return domain.ErrConflict
    }
    tx, err := r.db.BeginTx(ctx, nil)
    if err != nil {
        return err
    }
    defer func() { _ = tx.Rollback() }()
    for _, q := range []string{
        `DELETE FROM agent_events WHERE run_id IN (SELECT id FROM runs WHERE session_id = ?)`,
        `DELETE FROM approvals WHERE run_id IN (SELECT id FROM runs WHERE session_id = ?)`,
        `DELETE FROM run_steps WHERE run_id IN (SELECT id FROM runs WHERE session_id = ?)`,
        `DELETE FROM runs WHERE session_id = ?`,
        `DELETE FROM sessions WHERE id = ?`,
    } {
        if _, err := tx.ExecContext(ctx, q, sessionID); err != nil {
            return err
        }
    }
    return tx.Commit()
}
```

Note: `GetSession` already returns `domain.ErrForbidden` on owner mismatch — handler must map `ErrForbidden` and `sql.ErrNoRows` to 404.

- [ ] **Step 3: Run tests, expect PASS**

Run: `cd backend && go test ./internal/store/ -count=1 2>&1 | tail -n 2`
Expected: `ok`

- [ ] **Step 4: Commit**

```bash
git add backend/internal/store/task_repo.go backend/internal/store/task_repo_test.go
git commit -m "feat(backend): cascade session delete in store"
```

### Task 2: DELETE endpoint + integration test

**Files:**
- Modify: `backend/internal/api/http/server.go` (route + handler)
- Test: `backend/tests/integration/delete_session_test.go`

- [ ] **Step 1: Failing integration test**

```go
package integration

import (
    "testing"

    "rivus-agent-backend/tests/testutil"
)

func TestDeleteSessionAPI(t *testing.T) {
    h := testutil.New(t)
    ses := h.CreateSession("bye")
    id, code, _ := h.CreateRun(ses, map[string]any{"goal": "x"}, "del-1")
    _ = id
    _ = code
    // run is queued (active) -> 409
    delCode, _ := h.Do("DELETE", "/api/v1/sessions/"+ses, nil, nil)
    if delCode != 409 {
        t.Fatalf("active runs must block delete, got %d", delCode)
    }
    // wrong owner -> 404 (use header override)
    delCode, _ = h.Do("DELETE", "/api/v1/sessions/"+ses, nil, map[string]string{"X-Owner-ID": "intruder"})
    if delCode != 404 {
        t.Fatalf("cross-owner must 404, got %d", delCode)
    }
}
```

Note: `h.Do` hardcodes no owner header (helper uses default); the override map sets `X-Owner-ID` — verify `Do` applies custom headers (it does: `for k, v := range headers`). Queued counts as active per spec.

Run: `cd backend && go test ./tests/integration/ -run TestDeleteSessionAPI -v`
Expected: FAIL (404 page not found)

- [ ] **Step 2: Handler**

```go
// routes:
s.mux.HandleFunc("DELETE /api/v1/sessions/{id}", s.handleDeleteSession)

// handler:
func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
    id := r.PathValue("id")
    err := s.tasks.DeleteSession(r.Context(), ownerOf(r), id)
    if err != nil {
        if err == domain.ErrConflict {
            writeErr(w, 409, "conflict", "session has active runs; cancel them first", false)
            return
        }
        writeErr(w, 404, "not_found", "session not found", false)
        return
    }
    // best-effort checkpoint cleanup (rows already gone from runs table)
    s.checkpoints().DeleteByRunPrefix(r.Context(), id) // see note below
    writeJSON(w, 200, map[string]string{"status": "deleted"})
}
```

Note: `Server` has no checkpoints handle today. Options: (a) add `checkpoints *store.CheckpointStore` field via `New` (touches `app.go` + `harness.go` call sites); (b) skip checkpoint cleanup — orphaned `ckpt_<run>` rows linger. Spec requires cleanup, so do (a). `DeleteByRunPrefix` takes runID prefix `ckpt_<runID>%` — but here we have sessionID, not runIDs. Fix: collect run IDs first OR change cleanup to `DELETE ... WHERE checkpoint_id IN (SELECT ...)`. Runs are already deleted in the tx, so IDs are lost. Correct order: query run IDs BEFORE tx, then delete checkpoints after commit. Implement inside `DeleteSession`: return `([]string, error)`? Changing signature complicates the plan. Simpler: `DeleteSession` collects IDs pre-tx into a slice, deletes checkpoint rows inside the same tx via `DELETE FROM agent_checkpoints WHERE checkpoint_id LIKE 'ckpt_' || ? || '%'` per run id... LIKE with concatenation works in SQLite. Keep signature `error`; do checkpoint cleanup inside the tx loop per run id fetched first:

```go
var runIDs []string
rows, err := r.db.QueryContext(ctx, `SELECT id FROM runs WHERE session_id = ?`, sessionID)
// collect, close, then in tx: tx.Exec(`DELETE FROM agent_checkpoints WHERE checkpoint_id LIKE 'ckpt_' || ? || '%'`, rid)
```

Adjust Task 1 implementation accordingly (plan author note: fold checkpoint cleanup into `DeleteSession`, no `Server` field change needed).

- [ ] **Step 3: Run integration test, expect PASS; then full backend suite**

Run: `cd backend && go test ./tests/integration/ -run TestDeleteSessionAPI -count=1 -v 2>&1 | tail -n 3`
Expected: PASS

Run: `cd backend && go build ./... && go vet ./... && go test ./... -count=1 2>&1 | tail -n 8`
Expected: all `ok`

- [ ] **Step 4: Commit**

```bash
git add backend/internal/api/http/server.go backend/tests/integration/delete_session_test.go backend/internal/store/task_repo.go backend/internal/store/task_repo_test.go
git commit -m "feat(backend): delete session endpoint with cascade"
```

---

## Chunk 2: Frontend delete flow

### Task 3: row button + confirm dialog + store action + test

**Files:**
- Modify: `frontend/src/lib/api.ts` (add `deleteSession`), `frontend/src/store/session-store.ts` (add `deleteSession` action + `runCount` helper via run-store), `frontend/src/components/SessionPanel.tsx` (hover delete button + Dialog)
- Test: `frontend/src/components/SessionPanel.test.tsx` (new; mock api + stores)

- [ ] **Step 1: API + store**

```ts
// api.ts
async deleteSession(id: string): Promise<void> {
  await this.request('DELETE', `/api/v1/sessions/${encodeURIComponent(id)}`)
}
// session-store.ts: add
deleteSession: (id: string) => Promise<void>
// implementation: await api.deleteSession(id); remove from sessions;
// if id === activeSessionId: pick first remaining session (or null),
// clear run-store activeRun via useRunStore.getState() reset (set activeRun/activeRunId null, events [], runHistory reload for the new session)
```

Importing `useRunStore` into `session-store.ts` mirrors the existing cross-store pattern (`App.tsx` already coordinates both stores; keep the same direction: session-store may import run-store, not vice versa).

- [ ] **Step 2: Panel UI (Dialog + hover button + disabled state)**

Reuse `Dialog`, `Button` primitives like `ApprovalCard` does. Dialog shows run count for the session (from `runHistory` when it is the active session, else fetch count via `api.listRuns(id)` on dialog open — simpler: always `listRuns` on open, display `N Runs，将一并删除`). If any run has active status (`isActiveRunStatus` from `@/lib/status`), disable confirm + show “先取消运行中的任务”.

- [ ] **Step 3: Test**

```tsx
// SessionPanel.test.tsx: mock api { listSessions, listRuns, deleteSession }, seed stores,
// click delete → dialog appears with count → confirm → deleteSession called with id,
// active session switches, error path shows message.
```

Run: `cd frontend && node node_modules/typescript/bin/tsc --noEmit && npm test -- --run 2>&1 | grep -E "Test Files|Tests "`
Expected: tsc clean, all PASS (count grows by the new file)

- [ ] **Step 4: Build + commit**

Run: `cd frontend && npx vite build 2>&1 | tail -n 1`
Expected: `✓ built in ...`

```bash
git add frontend/src/lib/api.ts frontend/src/store/session-store.ts frontend/src/components/SessionPanel.tsx frontend/src/components/SessionPanel.test.tsx
git commit -m "feat(frontend): delete session with confirm dialog"
```
