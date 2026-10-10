# SSE 实时事件流 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `GET /api/v1/runs/{id}/events` 从一次性历史 dump 改成真 SSE 长连接（follow 循环 + 心跳 + 终态关闭），前端用 `fetch`+`ReadableStream` 替代 poller 主路径，实现 ≤2s 事件可见与断线续播。

**Architecture:** 后端在现有 `handleEvents` 内先回放历史，再进入可测试的 follow 循环（轮询 `EventRepo.ListAfter`，非 pub/sub，遵照已确认的方案 A）；前端新增 `sse-client.ts` 逐行解析 SSE 帧，`use-event-stream` 优先走流、失败降级回现有 `poller`；`run-store.appendEvent` 加 `(seq,type)` 去重。

**Tech Stack:** Go net/http (SSE, Flusher), TypeScript fetch ReadableStream, zustand, vitest.

**Spec:** `docs/superpowers/specs/2026-10-10-realtime-experience-design.md` §2, §5, §6.

**Non-goals:** pub/sub 即时推送、WebSocket（见 spec §7）。

---

## File Structure

- Modify: `backend/internal/api/http/server.go` — `handleEvents` 加 follow 循环；新增可覆盖的包级间隔变量。
- Create: `backend/internal/api/http/server_test.go` — 终态关闭 / 历史回放+`?after=` / follow 推送+心跳。
- Create: `frontend/src/lib/sse-client.ts` — `startEventStream(opts)`，fetch 流式解析 + 退避重连 + poller 降级回调。
- Create: `frontend/src/lib/sse-client.test.ts` — 重连/游标续播/终态关闭/降级。
- Modify: `frontend/src/store/run-store.ts` — `appendEvent` 加 `(seq,event_type)` 去重。
- Modify: `frontend/src/store/run-store.test.ts` — 加去重用例。
- Modify: `frontend/src/lib/use-event-stream.ts` — 优先 stream，`onFallback` 切 poller。

`ownerOf`、鉴权、`EventRepo`、`parseAfter` 零改动。后端零新依赖；前端零新依赖。

---

## Chunk 1: 后端 follow 循环

### Task 1: 可覆盖的间隔变量 + follow 循环实现

**Files:**
- Modify: `backend/internal/api/http/server.go:225-247` (`handleEvents`)

- [ ] **Step 1: 加包级变量（测试可覆盖）**

在 `server.go` 顶部（`type Server struct` 之前）加：

```go
// SSE follow 循环参数；单测可覆盖为小值。
var (
	followPollInterval  = 1 * time.Second
	followHeartbeatEvery = 15 * time.Second
	followMaxDuration   = 5 * time.Minute
)
```

并在 import 块加 `"time"`。其余 import 不动。

- [ ] **Step 2: 改写 `handleEvents`**

用以下实现整体替换现有 `handleEvents` 函数体（保留函数签名与前两段：GetRun 404 检查 + `parseAfter`）：

```go
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, err := s.tasks.GetRun(r.Context(), ownerOf(r), id)
	if err != nil {
		writeErr(w, 404, "not_found", "run not found", false)
		return
	}
	after := parseAfter(r)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		// Flusher 不可用：回退一次性返回（现有行为）。
		evs, err := s.events.ListAfter(r.Context(), id, after, 500)
		if err != nil {
			writeErr(w, 500, "internal", "list events failed", true)
			return
		}
		for _, e := range evs {
			_, _ = w.Write([]byte("id: " + itoa64(e.Seq) + "\nevent: " + string(e.Type) + "\ndata: " + e.PayloadJSON + "\n\n"))
		}
		return
	}
	lastSeq := after
	writeEvents := func(evs []domain.AgentEvent) bool {
		for _, e := range evs {
			if _, err := w.Write([]byte("id: " + itoa64(e.Seq) + "\nevent: " + string(e.Type) + "\ndata: " + e.PayloadJSON + "\n\n")); err != nil {
				return false
			}
			if e.Seq > lastSeq {
				lastSeq = e.Seq
			}
		}
		flusher.Flush()
		return true
	}
	// 历史回放。
	if evs, err := s.events.ListAfter(r.Context(), id, lastSeq, 500); err != nil {
		writeErr(w, 500, "internal", "list events failed", true)
		return
	} else if !writeEvents(evs) {
		return
	}
	// Run 已终态：直接关闭，不进 follow 循环。
	if run.Status.Terminal() {
		return
	}
	ctx := r.Context()
	deadline := time.Now().Add(followMaxDuration)
	poll := time.NewTicker(followPollInterval)
	defer poll.Stop()
	beat := time.NewTicker(followHeartbeatEvery)
	defer beat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-beat.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			flusher.Flush()
		case <-poll.C:
			if time.Now().After(deadline) {
				return
			}
			evs, err := s.events.ListAfter(ctx, id, lastSeq, 500)
			if err != nil {
				return
			}
			if !writeEvents(evs) {
				return
			}
			cur, err := s.tasks.GetRun(ctx, ownerOf(r), id)
			if err != nil {
				return
			}
			if cur.Status.Terminal() {
				return
			}
		}
	}
}
```

注意：`domain` 包已在 `server.go` import（第 14 行），`time` 需新增。写失败一律直接 return（连接已死）。不引入 pub/sub。

- [ ] **Step 3: 编译验证**

Run: `go build ./...`（workdir: `backend/`）
Expected: exit 0，无输出。

- [ ] **Step 4: Commit**

```bash
git add backend/internal/api/http/server.go
git commit -m "feat(events): SSE follow loop with heartbeat and terminal close"
```

### Task 2: 后端集成测试

**Files:**
- Create: `backend/internal/api/http/server_test.go`
- Test: 同文件（Go 原生 testing + httptest；参考 `backend/internal/store/store_test.go` 的 `:memory:` 建库模式）

- [ ] **Step 1: 写测试 helper + 三个用例**

```go
package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rivus-agent-backend/internal/config"
	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/store"
)

func newEventsTestServer(t *testing.T) (*Server, *store.TaskRepo, *store.EventRepo) {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tasks := store.NewTaskRepo(db)
	events := store.NewEventRepo(db)
	settings := store.NewSettingsRepo(db)
	srv := New(config.Default(), db, tasks, events, settings, nil, nil)
	return srv, tasks, events
}

func seedRun(t *testing.T, tasks *store.TaskRepo, id, owner, status string) {
	t.Helper()
	ctx := context.Background()
	if _, err := tasks.CreateSession(ctx, owner, "t"); err != nil {
		t.Fatal(err)
	}
	if err := tasks.CreateRun(ctx, &domain.Run{ID: id, SessionID: mustSession(tasks, ctx, owner), OwnerID: owner,
		Status: domain.RunStatus(status), Mode: "react", Goal: "g",
		Budget: domain.Budget{MaxDurationSeconds: 60, MaxModelCalls: 5, MaxToolCalls: 5, MaxIterations: 3, MaxOutputBytes: 1000}, Attempt: 1}); err != nil {
		t.Fatal(err)
	}
}
```

注意：`CreateSession` 返回 session id（见 `store_test.go:22` 用法 `ses, err := tasks.CreateSession(...)`），所以 helper 应直接用返回值，不需要 `mustSession`。修正如下——helper 内联：

```go
func seedRun(t *testing.T, tasks *store.TaskRepo, id, owner string, status domain.RunStatus) {
	t.Helper()
	ctx := context.Background()
	ses, err := tasks.CreateSession(ctx, owner, "t")
	if err != nil {
		t.Fatal(err)
	}
	if err := tasks.CreateRun(ctx, &domain.Run{ID: id, SessionID: ses, OwnerID: owner,
		Status: status, Mode: "react", Goal: "g",
		Budget: domain.Budget{MaxDurationSeconds: 60, MaxModelCalls: 5, MaxToolCalls: 5, MaxIterations: 3, MaxOutputBytes: 1000}, Attempt: 1}); err != nil {
		t.Fatal(err)
	}
}
```

用例 1 —— 终态自动关闭（deterministic，无循环）：

```go
func TestEventsTerminalClosesImmediately(t *testing.T) {
	srv, tasks, events := newEventsTestServer(t)
	ctx := context.Background()
	seedRun(t, tasks, "run_1", "u1", domain.RunRunning)
	if _, err := events.Append(ctx, "run_1", domain.EvtRunStarted, `{}`, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := events.Append(ctx, "run_1", domain.EvtRunFinished, `{"ok":true}`, ""); err != nil {
		t.Fatal(err)
	}
	if err := tasks.UpdateStatus(ctx, "u1", "run_1", domain.RunSucceeded, "", ""); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/v1/runs/run_1/events", nil)
	req.Header.Set("X-Owner-ID", "u1")
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { srv.Handler().ServeHTTP(rec, req); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not return for terminal run")
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: run.started") || !strings.Contains(body, "event: run.finished") {
		t.Fatalf("missing replayed frames: %q", body)
	}
}
```

用例 2 —— `?after=` 过滤：同结构建 running 的 run，append 3 事件，请求 `?after=2`，断言 body 含第 3 个事件、不含前两个的 `id:` 行。用 `httptest.NewRequest` + 带超时的 context（`context.WithTimeout(ctx, 500ms)`，覆盖 `followPollInterval=50ms` 后 handler 会在 ctx 到期后返回）：

```go
func TestEventsAfterFilter(t *testing.T) {
	old := followPollInterval
	followPollInterval = 50 * time.Millisecond
	defer func() { followPollInterval = old }()
	...seed running run + 3 events...
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("GET", "/api/v1/runs/run_1/events?after=2", nil).WithContext(ctx)
	...
	// 断言 body 含 "id: 3"，不含 "id: 1\n" 与 "id: 2\n"
}
```

用例 3 —— follow 推送 + 心跳帧格式：running run，先 append 1 事件，handler 运行中再 append 第 2 个事件，覆盖 `followPollInterval=30ms`、`followHeartbeatEvery=50ms`，ctx 超时 500ms，断言 body 同时含两个 `event:` 帧与 `: ping` 行。

- [ ] **Step 2: 跑测试验证通过**

Run: `go test ./internal/api/http/ -v -count=1`（workdir: `backend/`）
Expected: 3 个用例 PASS。

- [ ] **Step 3: 全量后端测试**

Run: `go test ./...`（workdir: `backend/`）
Expected: 全部 PASS（注：仓库无 Makefile，spec 的 `make test` 以此命令为准）。

- [ ] **Step 4: Commit**

```bash
git add backend/internal/api/http/server_test.go
git commit -m "test(events): replay, after filter, follow push and heartbeat"
```

---

## Chunk 2: 前端 fetch 流式客户端

### Task 3: `sse-client.ts` 实现

**Files:**
- Create: `frontend/src/lib/sse-client.ts`

- [ ] **Step 1: 写实现**

```ts
import { API_BASE, OWNER_ID } from '@/App.config'
import type { AgentEvent, RunStatus } from './types'

export type StreamStatus = 'streaming' | 'error' | 'stopped'

export interface StreamOpts {
  runId: string
  getAfterSeq: () => number
  getRunStatus: (runId: string) => Promise<RunStatus>
  onEvent: (e: AgentEvent) => void
  onStatus: (s: StreamStatus) => void
  /** 连续失败 maxRetries 次后调用，调用方切 poller 降级 */
  onFallback: () => void
  maxRetries?: number
}

const TERMINAL_EVENTS = new Set(['run.finished', 'run.failed', 'run.cancelled'])
const TERMINAL_STATUS = new Set<RunStatus>(['succeeded', 'failed', 'cancelled', 'timed_out'])

function parseBlock(block: string, runId: string): AgentEvent | null {
  let seq = 0
  let eventType = ''
  const dataLines: string[] = []
  for (const line of block.split('\n')) {
    if (line.startsWith('id: ')) seq = parseInt(line.slice(4), 10) || 0
    else if (line.startsWith('event: ')) eventType = line.slice(7)
    else if (line.startsWith('data: ')) dataLines.push(line.slice(6))
    else if (line.startsWith(':')) continue // ping/注释帧
  }
  if (!eventType) return null
  return { seq, run_id: runId, event_type: eventType as AgentEvent['event_type'], payload_json: dataLines.join('\n'), sensitivity: 'normal', created_at: 0 }
}

export function startEventStream(opts: StreamOpts): () => void {
  const maxRetries = opts.maxRetries ?? 3
  let stopped = false
  let failures = 0
  let backoffMs = 1000
  let timer: ReturnType<typeof setTimeout> | null = null
  let reader: ReadableStreamDefaultReader<Uint8Array> | null = null
  const decoder = new TextDecoder()

  async function connect() {
    if (stopped) return
    const after = opts.getAfterSeq()
    let res: Response
    try {
      res = await fetch(`${API_BASE}/api/v1/runs/${encodeURIComponent(opts.runId)}/events?after=${after}`, {
        method: 'GET',
        headers: { 'X-Owner-ID': OWNER_ID, Accept: 'text/event-stream' },
      })
      if (!res.ok || !res.body) throw new Error(`http_${res.status}`)
    } catch {
      return scheduleReconnect()
    }
    failures = 0
    backoffMs = 1000
    opts.onStatus('streaming')
    reader = res.body.getReader()
    let buf = ''
    try {
      for (;;) {
        const { done, value } = await reader.read()
        if (done) break
        buf += decoder.decode(value, { stream: true })
        const blocks = buf.split('\n\n')
        buf = blocks.pop() ?? ''
        for (const block of blocks) {
          if (!block.trim() || block.trimStart().startsWith(':')) continue
          const e = parseBlock(block, opts.runId)
          if (!e) continue
          opts.onEvent(e)
          if (TERMINAL_EVENTS.has(e.event_type)) return finish()
        }
      }
    } catch {
      return scheduleReconnect()
    }
    // 服务端正常关闭（终态后关闭）：确认终态则停，否则重连续播。
    try {
      const status = await opts.getRunStatus(opts.runId)
      if (TERMINAL_STATUS.has(status)) return finish()
    } catch { /* 查不到就重连 */ }
    return scheduleReconnect()
  }

  function scheduleReconnect() {
    if (stopped) return
    failures += 1
    opts.onStatus('error')
    if (failures > maxRetries) {
      finish()
      opts.onFallback()
      return
    }
    timer = setTimeout(connect, backoffMs)
    backoffMs = Math.min(backoffMs * 2, 30_000)
  }

  function finish() {
    stopped = true
    if (timer) clearTimeout(timer)
    void reader?.cancel().catch(() => undefined)
    opts.onStatus('stopped')
  }

  void connect()
  return finish
}
```

约束：游标一律取自 `getAfterSeq()`（store 尾 seq），不断线重连天然续播；`stop()` 必须 cancel reader 中止 fetch。

- [ ] **Step 2: 跑 `tsc --noEmit` 验证类型**

Run: `npx tsc --noEmit`（workdir: `frontend/`）
Expected: exit 0。

### Task 4: `sse-client.test.ts`（先红后绿）

**Files:**
- Create: `frontend/src/lib/sse-client.test.ts`

- [ ] **Step 1: 写 failing 测试**

mock 全局 fetch，返回可控 `ReadableStream`；覆盖：
  1. 收到 `run.started` 帧 → `onEvent` 被调且 `onStatus('streaming')`；
  2. `: ping` 帧被忽略；
  3. `run.finished` 帧 → `onStatus('stopped')` 且不再重连（fetch 只调 1 次）；
  4. fetch reject → 退避后重连且第二次 `after=` 为已收 seq（检查第二次 fetch URL 含 `after=7`）；
  5. 连续失败超 `maxRetries` → `onFallback` 被调。

```ts
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { startEventStream } from './sse-client'

function sseResponse(chunks: string[]): Response {
  const stream = new ReadableStream<Uint8Array>({
    start(c) {
      for (const ch of chunks) c.enqueue(new TextEncoder().encode(ch))
      c.close()
    },
  })
  return new Response(stream, { headers: { 'Content-Type': 'text/event-stream' } })
}
```

用 `vi.stubGlobal('fetch', ...)` + `vi.useFakeTimers` 控制退避（`advanceTimersByTimeAsync`）。先写测试，跑 `npx vitest run src/lib/sse-client.test.ts` 看红（`startEventStream not defined`），再写实现看绿。若 Task 3 已写实现，则此步为补测试直接绿——允许，但必须跑一次证明。

- [ ] **Step 2: 跑测试**

Run: `npx vitest run src/lib/sse-client.test.ts`（workdir: `frontend/`）
Expected: 5 用例 PASS。

- [ ] **Step 3: Commit**

```bash
git add frontend/src/lib/sse-client.ts frontend/src/lib/sse-client.test.ts
git commit -m "feat(stream): fetch SSE client with reconnect and poller fallback"
```

### Task 5: run-store 去重 + use-event-stream 接线

**Files:**
- Modify: `frontend/src/store/run-store.ts:45-55`
- Modify: `frontend/src/store/run-store.test.ts`
- Modify: `frontend/src/lib/use-event-stream.ts`

- [ ] **Step 1: `appendEvent` 去重**

```ts
appendEvent: (e) => {
  const cur = get().events
  if (cur.some((x) => x.seq === e.seq && x.event_type === e.event_type)) return
  const events = [...cur, e]
  if (events.length > MAX_EVENTS) {
    events.splice(0, events.length - MAX_EVENTS)
  }
  set({ events })
  ...
}
```

- [ ] **Step 2: 补去重测试到 `run-store.test.ts`**

```ts
it('appendEvent dedupes by (seq, event_type)', () => {
  useRunStore.getState().appendEvent(mkEvent(1))
  useRunStore.getState().appendEvent(mkEvent(1))
  expect(useRunStore.getState().events.length).toBe(1)
})
```

- [ ] **Step 3: 改写 `use-event-stream.ts`**

```ts
import { useEffect } from 'react'
import { startEventPoller } from './poller'
import { startEventStream } from './sse-client'
import { api } from './api'
import type { RunStatus } from './types'
import { useRunStore } from '@/store/run-store'
import { useUiStore } from '@/store/ui-store'

export function useEventStream() {
  const activeRunId = useRunStore((s) => s.activeRunId)

  useEffect(() => {
    if (!activeRunId) return

    const getAfterSeq = () => {
      const events = useRunStore.getState().events
      return events.length ? events[events.length - 1].seq : 0
    }
    const getRunStatus = async (runId: string): Promise<RunStatus> => {
      const { run } = await api.getRun(runId)
      return run.status
    }
    const onEvent = (e: Parameters<Parameters<typeof startEventStream>[0]['onEvent']>[0]) =>
      useRunStore.getState().appendEvent(e)

    let stopFallback: (() => void) | null = null
    const stopStream = startEventStream({
      runId: activeRunId,
      getAfterSeq,
      getRunStatus,
      onEvent,
      onStatus: (s) => useUiStore.getState().setSseStatus(s === 'streaming' ? 'polling' : s),
      onFallback: () => {
        stopFallback = startEventPoller({
          getEvents: (runId, afterSeq) => api.getEvents(runId, afterSeq),
          getRunStatus,
          runId: activeRunId,
          getAfterSeq,
          onEvent,
          onStatus: (s) => useUiStore.getState().setSseStatus(s),
        })
      },
    })
    return () => {
      stopStream()
      stopFallback?.()
    }
  }, [activeRunId])
}
```

注意：`POLL_INTERVAL_MS` import 删掉（poller 默认 2000ms 与其一致）；`onStatus` 映射 `streaming→polling`（`PollStatus` 无 streaming）。

- [ ] **Step 4: 全量前端验证**

Run（workdir: `frontend/`）:
  1. `npx tsc --noEmit` → exit 0
  2. `npx vitest run` → 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add frontend/src/store/run-store.ts frontend/src/store/run-store.test.ts frontend/src/lib/use-event-stream.ts
git commit -m "feat(stream): dedupe events and prefer SSE stream over poller"
```

---

## Acceptance（本 plan 完成标准）

- 新建 Run 后事件 ≤2s 可见（follow 1s 轮询 + 立即回放）。
- 断线重进后从断点续播，无丢失无重复（游标 + 去重）。
- 后端 `go test ./...` 全绿；前端 `tsc --noEmit` + `vitest run` 全绿。

Plan complete and saved to `docs/superpowers/plans/2026-10-10-sse-live-events.md`. Ready to execute?（注：子计划 2、3 为独立文件；按你的要求全程不用 subagent，执行时用 executing-plans 当前会话批量执行。）
