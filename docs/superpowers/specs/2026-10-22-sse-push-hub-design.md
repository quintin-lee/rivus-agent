# SSE 平滑推送（事件 hub）— 设计文档

- **日期**: 2026-10-22
- **范围**: 让已连接的 SSE 客户端在**亚 100ms** 内收到新事件，而非等 `handleEvents` 的 1s 轮询 ticker。动 `backend/internal/eventshub`（新包：进程内 pub/sub hub）、`backend/internal/store/event_repo.go`（`Append` 挂发布钩子）、`backend/internal/api/http/server.go`（`handleEvents` 加 push case）、`backend/internal/app/app.go`（装配 hub）。**前端零改动**（前端已按 `(seq,event_type)` 去重 + 取最新 `model.token`，更快到达只会让 partial 显示更快）。
- **状态**: 设计已确认。
- **兑现**: `2026-10-21-streaming-tokens-design.md` 的 follow-up #1「sub-100ms 平滑推送（需专门 push 通道，改 handleEvents 的 1s 轮询为真 SSE 推送）」。

## 背景 / 现状

`handleEvents`（`server.go:307`）用 `followPollInterval = 1 * time.Second`（`server.go:22`）的 ticker 轮询 `Events.ListAfter`（`server.go:355,372`），新事件**最多延迟 1s** 才逐出给已连接客户端。这正是 10-21 streaming-tokens 功能里 delta 只能 ~1s 粒度逐出的天花板（`drainStream` 已做到 ~200ms 合并发 `model.token`，但被 1s 轮询挡在门外）。

目标：保留 1s ticker 作为**兜底**，另加一条**推送快路径**，让新事件写库的瞬间就扇出到该 run 的活 SSE 客户端。

## 路线取舍（关键）

**采用 A1：进程内事件 hub（pub/sub）。** 新增一个叶子包 `eventshub`：`EventRepo.Append` 成功后同步 `hub.Publish`（非阻塞），`handleEvents` 订阅 `hub.Subscribe`，把 push 通道并进现有 `select` 循环；1s ticker 保留做兜底 + 终态检查。

否决的替代：
- **A3 缩短轮询间隔**（1s→200ms）：仍是轮询，N 连接 × 5 DB 查询/s，上限仍在 ~200ms 非真 sub-100ms，连接多时 DB 压力线性增长。10-21 spec 已明确否掉这类修补。
- **A2 WebSocket**：第二传输协议 + 前端 `sse-client` 重写 + 与现有 SSE 回放/重连/降级双轨维护。10-21 spec 路线取舍已否（与主 SSE 需保持一致，推翻单一 run-store 哲学）。

两个已锁定的微决策（spec review 可再翻）：
1. **发布点 = `EventRepo.Append` 单一钩子**：所有事件写入（`runtime.go:37,186,196` + `run_service.go:76,108,175`）都经 `EventRepo.Append`，一处挂 `SetPublisher` 即覆盖全部，测试构造无需改。`store` 只认 `func(domain.AgentEvent)`，**不 import eventshub**（解耦）。
2. **push 是优化、1s ticker 是正确性兜底**：订阅者缓冲（64）满时该订阅 drop，ticker 的 `ListAfter` 补回漏掉的 seq——**无丢失、无 desync**，与今天的边界语义一致（full-so-far 幂等）。

## 改动

### 1. `backend/internal/eventshub/hub.go` — 新包（叶子，只 import stdlib + domain）

```go
// Hub 是进程内、按 runID 分组的无界丢（bounded drop）事件扇出器。
// Publish 非阻塞：每个订阅者一个带缓冲 chan；缓冲满则丢该订阅者该事件
// （1s ticker 兜底补回）。Subscribe 返回只读 chan + close fn（防 map 泄漏）。
type Hub struct {
    mu   sync.Mutex
    subs map[string]map[int]chan domain.AgentEvent
    next int
}

func New() *Hub

// Subscribe 注册 runID 的订阅者（缓冲 buf，建议 64）。返回只读通道 + 注销函数。
func (h *Hub) Subscribe(runID string, buf int) (<-chan domain.AgentEvent, func())

// Publish 向 runID 的所有订阅者非阻塞扇出 ev（缓冲满则 drop 该订阅者该事件）。
func (h *Hub) Publish(ev domain.AgentEvent)
```

### 2. `backend/internal/store/event_repo.go` — `Append` 挂发布钩子

`EventRepo` 结构（`event_repo.go:12`）加一字段 + 两个方法；`Append`（`event_repo.go:19`）在 insert 成功、拿到 seq 后构造完整事件并回调：

```go
type EventRepo struct {
    db        *sql.DB
    publisher func(domain.AgentEvent) // nil-safe：未装配时为 no-op
}

// SetPublisher 挂上发布钩子（app 装配 hub.Publish）。nil 则 Append 纯写库，行为同今天。
func (r *EventRepo) SetPublisher(fn func(domain.AgentEvent))
```

`Append` 尾部（拿到 `seq` 后）：

```go
ev := domain.AgentEvent{Seq: seq, RunID: runID, Type: typ,
    PayloadJSON: payloadJSON, Sensitivity: sensitivity, CreatedAt: nowMs()}
if r.publisher != nil {
    r.publisher(ev)
}
return seq, nil
```

> 注意：`CreatedAt` 用与 insert 同一个 `nowMs()` 值（保持 DB 行与发布事件一致）。`store` 不 import `eventshub`——只暴露 `func(domain.AgentEvent)`。

### 3. `backend/internal/api/http/server.go` — `handleEvents` 加 push case

- `Server` 结构加字段 `hub *eventshub.Hub`；`New(...)` 加可选 option `WithEventHub(hub *eventshub.Hub)`（沿用现有 `WithToolConfigProvider` 的 options 模式，`server.go` `New` 签名不变——variadic）。
- `handleEvents`（`server.go:307`）：在初次 `ListAfter` + 终态检查（`server.go:344-352`）之后、poll 循环（`server.go:359`）之前，若 `s.hub != nil`：
  ```go
  var pushCh <-chan domain.AgentEvent
  if s.hub != nil {
      var unsub func()
      pushCh, unsub = s.hub.Subscribe(id, 64)
      defer unsub()
  }
  ```
- `select` 循环（`server.go:359-387`）加一个 case（**复用现有 `writeEvents` 闭包**，它已含 write + `flusher.Flush()` + `lastSeq` 更新）：
  ```go
  case ev, ok := <-pushCh:
      if !ok {
          continue
      }
      if ev.Seq <= lastSeq {
          continue // 幂等：ticker 可能已写过的 seq 跳过
      }
      if !writeEvents([]domain.AgentEvent{ev}) {
          return
      }
  ```
- **1s ticker 保留不动**（`server.go:355,368`）——它现在是兜底（补 hub 未覆盖的写路径 / drop 的 seq）+ 终态检查（`GetRun` → `Terminal()` close）。`followPollInterval` 维持 1s。

### 4. `backend/internal/app/app.go` — 装配 hub

`Start`（`app.go:45`）内：
- `events := store.NewEventRepo(db)`（`app.go:54`）之后：
  ```go
  hub := eventshub.New()
  events.SetPublisher(hub.Publish)
  ```
- `httpapi.New(...)`（`app.go:105`）加一个 option：
  ```go
  srv := httpapi.New(cfg, db, tasks, events, settings, runs, sessions,
      httpapi.WithToolConfigProvider(provider), httpapi.WithEventHub(hub))
  ```

## 数据流

```
runtime OnEvent → EventRepo.Append（agent_events 落库，seq N）
                → hub.Publish(ev{seq N})           [同步，<µs，非阻塞]
handleEvents 订阅者：
  push:   seq N 到达 → writeEvents([ev]) → 写帧 + Flush + lastSeq=N   [sub-100ms]
  ticker: 1s → ListAfter(lastSeq) → 写 seq > lastSeq（漏掉的 push、审批等写路径、终态检查）
```

`writeEvents` 幂等 + 前端按 `(seq,event_type)` 去重 → **重复投递无副作用**（同今天重连回放）。

## 错误 / 边界

| 场景 | 行为 |
|---|---|
| 订阅缓冲满（客户端太慢） | `Publish` drop 该订阅该事件；1s ticker 补回 → "延迟但不丢"，同今天轮询语义 |
| 无订阅者（全部断连） | `Publish` 对空 map 扇出为 no-op；`Append` 常见路径成本不变 |
| hub 未装配（测试 / `run_service` 独立路径） | `EventRepo.publisher` nil → no-op；`handleEvents` 退回纯 ticker（**与今天完全一致**） |
| 终态 run | 终态事件是最后一次 push；1s ticker 的 `GetRun` 终态检查随后 close（现有路径不变） |
| 并发 | `Publish` 短暂持 hub 锁 + 非阻塞 `select/default` 发 buffer chan；`Subscribe`/`unsub` 同锁；**不跨 I/O 持锁** |

## 测试

- **`eventshub`（Go 单测，新包）**：publish→subscribe round-trip；**每订阅者隔离**（A 溢出不影响 B）；drop-on-full 后通道仍可用（不死锁）；`unsub()` 解除阻塞 + 从 map 移除（断言 map 大小无泄漏）。
- **`handleEvents`（Go 集成测，扩 `tests/integration/sse_follow_test.go` 模式，两个确定性场景）**：
  1. **push 快路径（hub 装配）**：stream 打开时 enqueue 一条 `model.token`（经 `Events.Append` → `hub.Publish`），断言该帧**在 < 100ms 内**到达（短 deadline，纯 1s-poll 的同类测试会失败 → 证明快路径生效）。
  2. **ticker 兜底（hub 未装配）**：用一个**未接 hub 的 harness 实例**（`SetPublisher` 未调，`handleEvents` 走 `s.hub == nil` 分支 = 纯 ticker），断言事件仍被 1s 轮询逐出（~1s 内到达）且终态 close —— 证明「hub 缺席 = 行为同今天」的零回归保证。
  （两条都复用现有 `waitLine` + `openStream`。）
- **前端：无改动、无测试。**

## 验收

- 父 run 运行中，SSE 客户端收到 `model.token` 的**延迟 < 100ms**（vs 今天 ≤1s）；partial 平滑逐出。
- 客户端过慢 / 断开 / hub 未装配 场景**零回归**（ticker 兜底 → 行为同今天）。
- 审批 / 终态 / `run_service` 写入路径**仍可达**（单钩子覆盖 + ticker 兜底双保险）。
- 无第二传输、无 DB schema 变更、无新 route；前端 `git diff` 为空。

## 明确不做（YAGNI / follow-up）

- **每客户端 seq 记账 / 断点续传的精确对齐**（依赖 `budget.consumed`，10-21 follow-up #3）— 列 follow-up。
- **子 run / 子 agent 的 token 流式**（价值有限，子输出本就折叠）— 维持现状。
- **MAX_EVENTS=500 头部截断对早期 token 的丢弃**：合并后每 model 调用 ≤1 事件/200ms，buffer 64 + cap 500 使**最新** token 恒 live；文档化局限，仅用量超限时再议。
- **`followPollInterval` 调长**：维持 1s（终态检查节奏）；push 快路径已解耦逐出延迟，无需动 ticker。
