# 前端体验补全 + 工具载荷增强 设计（Round 5，方案 B）

日期：2026-10-26
状态：用户选定方向 1（前端体验补全）+ 方案 B（含批C 后端工具载荷增强）；设计 §1/§2/§3 已展示，批C 关键事实已验证。

## 1. 背景与问题

前四轮「完善优化」后端能力已齐（内置工具 ×8、Skill、并行子 agent、会话记忆、20 条路由、评估体系），
但前端未跟上：

| # | 问题 | 证据 |
|---|------|------|
| 1 | 会话记忆无任何 UI | `GET /api/v1/sessions/{id}/memories`（round 3 加入）在前端零调用 |
| 2 | 子 agent 调用不可见 | `task`/`tasks` 的 `tool.completed` 事件只在「系统事件」折叠区显示为一条普通工具记录 |
| 3 | 工具调用细节不可见 | `tool.completed` payload 为空摘要：drain L319 传 `""`，OnEvent（runtime.go:43）持久化 `{"text":tool,"tool":tool}`，args/output 均不在事件里 |

目标：批A/批B 纯前端补 UI；批C 后端最小增强让工具调用（args/output 摘要）进入事件流，前端富渲染。

## 2. 已验证的关键事实

- `domain.Run` JSON 暴露 `parent_run_id`（domain/task.go:107）→ 前端可用 `GET /api/v1/runs?session_id=X` 过滤子 run。
- `GET /api/v1/sessions/{id}/memories` 响应 `{"memories":[...]}`（server.go:268）；limit 1..200 缺省 200；会话不存在 → 404。
- `domain.Memory` json tags：`id / session_id / owner_id / content / source_run_id(omitempty) / created_at`。
- `tool.completed` 唯一发射点 = drain（runner_eino.go:319，payload 传 `""`）；工具执行唯一咽喉 = gatewayTool
  （构造点唯一：runner_child.go buildAgentFiltered，父子共用）。
- OnEvent（runtime.go:42-48）持久化时 `json.Marshal({"text","tool"})`，**忽略 `ev.Payload`**；
  全部 14 个 emit 调用点 payload 实参均为 `""` → `RuntimeEvent.Payload` 当前为死字段，
  在 OnEvent 做「合并 ev.Payload」对现有事件字节级零影响。
- 前端模式：api.ts 单例 ApiClient（ApiError{code,retryable}）；组件测试 = vitest + @testing-library/react + vi.mock('@/lib/api')；
  `SessionList.test.tsx`、`chat-messages.test.ts` 已存在（需增补）。
- 批3 教训：并行子 agent 共享 EinoRunner 实例 → 同名工具并发时**必须以 runID 隔离**观测数据。

## 3. 范围

### 批A 记忆面板（纯前端）

- `types.ts`：新增 `Memory` 接口（字段同 §2 json tags）。
- `api.ts`：`listMemories(sessionId, limit=200)` → `GET /api/v1/sessions/{id}/memories?limit=200`，解包 `memories ?? []`。
- 新组件 `components/MemoriesDialog.tsx`：
  props `{sessionId, sessionTitle, open, onOpenChange}`；open 拉取；loading/error(重试)/空态「（无记忆）」；
  顶部客户端大小写不敏感子串过滤；列表项 = content 多行 + 相对时间 + source_run_id 前 8 位。
- `SessionList.tsx`：会话行 hover 按钮区新增「记忆」按钮（lucide `Brain`，与删除按钮同款 hover 样式）。
- 测试：新建 `MemoriesDialog.test.tsx`（条目/空态/过滤/错误重试）；`SessionList.test.tsx` 增补按钮断言。

### 批B 子 agent 执行树（纯前端）

- `types.ts`：`Run += parent_run_id?: string`。
- `chat-messages.ts`：`emitRun` 识别 `tool.completed && payload.tool ∈ {task,tasks}` → 计数 `subagentCount`，
  在 system-events 之前 push `{kind:'system-subagent', id:'subagent-<runId>', runId, run, count}`。
- 新组件 `components/Messages/SubagentCard.tsx`：
  折叠态「子 agent ×N」；展开拉 `api.listRuns(run.session_id, 200)` 过滤 `parent_run_id === runId` 按 created_at 升序；
  每条 = goal 截断 60 + 状态徽章（runStatusClasses）+ 失败 error_code + 耗时 `(finished_at??updated_at)-(started_at??created_at)`；
  手动刷新；loading/error(重试)/空态「（无子 run 记录）」。一次性拉取，不做实时轮询。
- `ChatMessageList.tsx` 增加 kind 分支；`Messages/index.ts` 导出。
- 测试：新建 `SubagentCard.test.tsx`（子条目/空态/错误重试/折叠计数）；`chat-messages.test.ts` 增补 derive 用例。

### 批C 工具载荷增强（后端最小增强 + 前端富渲染）

机制：**stash + 合并**（单发射点不变，无重复事件）。

后端（3 文件）：

1. `backend/internal/runtime/runner_eino.go`：
   - EinoRunner 增 `toolObsMu sync.Mutex` + `toolObs map[string]toolObservation`（`toolObservation{args, output string}`），
     key = `runID + "|" + toolName`（并行子 agent 同名工具以 runID 隔离）。
   - `recordToolObserved(runID, toolName, args json.RawMessage, output string)`：args/output 各截断 1KB 后存入；
     map 超过 256 条时整体清空（防长期运行泄漏）。
   - `takeToolObserved(runID, toolName) (toolObservation, bool)`：取出即删除（consume-once）；
     resume 重放工具消息时无 stash → 自然降级为 {text,tool}。
   - `gatewayTool` 增 `onDone func(args json.RawMessage, output string)` 字段；InvokableRun 中 Execute 成功后
     （approved 与 normal 两条路径）回调 `g.onDone(raw, out.Output)`；ErrNeedsApproval/执行失败不回调。
2. `backend/internal/runtime/runner_child.go`：gatewayTool 构造点（唯一）接
   `onDone: func(args json.RawMessage, output string) { r.recordToolObserved(runID, d.Name, args, output) }`。
3. `backend/internal/runtime/runtime.go`：抽纯函数
   `mergeEventPayload(text, tool, extra string) string` —— base `{"text":text,"tool":tool}`，
   extra 非空时 `json.Unmarshal` 进 `map[string]string` 合并（extra 键胜出），`json.Marshal` 输出；
   OnEvent 改调该函数（`ev.Payload` 作为 extra）。drain L319 改为：
   `obs, ok := r.takeToolObserved(runID, mo.ToolName)`；ok 时构造 extra
   `{"args":obs.args,"output":obs.output}` 作为 emit 的 payload 实参。

兼容性：事件次数不变（每执行恰好一次）；payload 键增量为加法；evals 计数/RequiredEvents 不受影响；
前端 progress 计数不变；Go map 序列化键有序，输出确定。

前端（批C 增量）：

- `chat-messages.ts`：`emitRun` 从 `tool.completed` 事件解析 `args/output`（eventPayload），
  存在时 push `{kind:'system-tools', id:'tools-<runId>', runId, calls:[{tool, args, output, seq}]}`。
- 新组件 `components/Messages/ToolCallsCard.tsx`：折叠态「工具调用 ×N」；展开逐条 =
  工具名徽章 + args 紧凑展示 + output（截断展示，等宽字体）；legacy 事件（无 args/output）→ 整卡不渲染。
- `ChatMessageList.tsx` 分支 + `Messages/index.ts` 导出。
- 测试：新建 `ToolCallsCard.test.tsx`（条目/legacy 不渲染/空）；`chat-messages.test.ts` 增补用例。

### 批C 后端测试（TDD）

- `runner_eino_test.go`：`TestRecordTakeToolObserved`（record→take 返回且删除→二次 take 空）；
  `TestToolObsIsolatedByRun`（同 toolName 不同 runID 互不串）；`TestToolObsMapBounded`（>256 清空）。
- `runtime` 包：`TestMergeEventPayload`（无 extra = 现状字节；extra 合并；extra 非法 JSON 忽略）。
- 集成：现有 `builtin_tools_test.go` 增一个断言 —— file_search 成功后其 `tool.completed` 的
  payload_json 含 `"args"` 与 `"output"` 键。

## 4. 错误处理

- 前端拉取失败：组件内联错误 + 重试按钮（与 SessionList 错误条风格一致）。
- `listMemories`/`listRuns` 404 → 按空列表处理。
- `payload_json` 解析失败 → 忽略该事件（与 parseApprovalPayload 同策略）。
- 后端 stash：非法 extra JSON → Unmarshal 失败即忽略 extra（退化为 {text,tool}）。

## 5. 测试与验收

1. `cd frontend && npx tsc --noEmit` → 0 错误
2. `npx vitest run` → 全绿（新增 ≥3 组件测试文件，用例数 >127）
3. `cd backend && go build ./... && go vet ./...` → 干净
4. `go test ./... -count=1` → 全绿（含新增 stash/merge 单测与集成断言）
5. `make test-eval` → 15 场景全绿（事件计数不变的回归证明）
6. 文档：`frontend/README.md` + `frontend/README.en.md` 组件清单补 3 行；backend README 工具事件说明补一句（payload 增 args/output 摘要）

## 6. YAGNI / 未来工作

- 不做子 run 实时轮询；不做记忆编辑/删除 UI；不做工具结果持久化存储与查询端点（富渲染止步于事件摘要）。
- args/output 截断固定 1KB，不配置化。
- 不改 HTTP API 形状、不做 SSE 之外的新数据通道、不做工具调用耗时展示（事件无该数据）。
