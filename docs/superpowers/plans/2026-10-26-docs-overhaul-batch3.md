# Docs Overhaul 批3 · 架构文档 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan (本仓库约束：不使用 subagent)。Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 产出 `docs/architecture/overview.md`（组件图 + Run 生命周期）与 `docs/architecture/state-machine.md`（状态机/审批流/预算终止/孤儿恢复/超时）中英双语 4 篇，并把根 README「详细文档」指向新文档树。

**Architecture:** 纯文档批次 + 唯一跨批回改（README 一行）。mermaid 图内嵌 Markdown。所有迁移/组件/路径事实在「已核实事实」固化。

**Tech Stack:** Markdown + mermaid（GitHub 原生渲染）。

**Spec:** `docs/superpowers/specs/2026-10-26-docs-overhaul-design.md`（§2 批3、§3 批3、§6 验收 1/5/6）

---

## 已核实事实（写作时直接引用）

### 组件装配（`app.go` Start 顺序）

1. `config.Load(cfgPath)` → `store.Open(DBPath)`（SQLite，WAL，8 连接文件库 / 1 连接 `:memory:`）
2. 仓储：`TaskRepo` / `EventRepo` / `ApprovalRepo` / `CheckpointStore` / `SettingsRepo`
3. `eventshub.New()` + `events.SetPublisher(hub.Publish)` —— 事件落库即推送
4. 工具：`tool.NewRegistry` + `builtin.RegisterAll` → `tool.NewExecutor`（统一执行网关：策略/限流/超时/审批/幂等/预算输出上限）
5. `runtime.NewEinoRunner(modelFactory, registry, executor, checkpoints)` —— modelFactory 经 Settings 查表（3s 超时）
6. `subagent.TaskTool(registry, tasks, runner, runner)` 注册 `task` 工具（同步只读子 agent，depth≥1 剥离 task 防递归）
7. `runtime.New(Deps{Tasks,Events,Approvals,Runner,Log}, WorkerConcurrency)` —— 有界 Worker（信号量），OnEvent 落库 + 计数
8. `runtime.RecoverOrphans(tasks, events)` —— 孤儿恢复（见下）
9. Skill Loader（`AGENT_SKILLS_DIR`）+ MCP Manager（`mcp.enabled` 才 SpawnAll + `registry.Replace("mcp__",…)`；`skills.enabled` 才注册 `invoke_skill`）
10. `service.NewRunService(cfg, tasks, events, approvals, rt, runner)` / `SessionService`
11. `httpapi.New(..., WithToolConfigProvider, WithEventHub)` → `http.Server`；Stop：MCP StopAll → srv.Shutdown → db.Close

### 一次 Run 的生命周期

POST `/api/v1/runs` → RunService.CreateRun：budget `ClampByServer` → 幂等（`Idempotency-Key`）→ Run 落库 `queued` + `run.created` 事件 → `rt.Execute`（经 Worker 信号量获取并发）→ `run()`：预算 tracker 建立plan 模式（可选）→ `Runner.Run`（Eino ADK 流式 Runner + CheckpointStore）→ drain 循环：模型轮次/工具调用/审批中断逐事件 emit → `OnEvent`：JSON payload 落 `agent_events`（`hub.Publish` 实时扇出）→ 终态分类：waiting_approval / budget 超限 / deadline 超时 / 校验（`Verify` 成功标准 + 工具证据）→ `UpdateStatus` + `run.finished|failed` 事件 → checkpoint 清理。SSE 订阅者从 Hub 实时收 + `agent_events` 重放。

### 状态机（`domain/task.go` ValidTransition 原样）

- 状态：`queued running waiting_approval paused succeeded failed cancelled timed_out`；终态 = 后四个
- 合法迁移：`queued→{running,cancelled}`；`running→{waiting_approval,paused,succeeded,failed,cancelled,timed_out}`；`waiting_approval→{running,cancelled,failed}`；`paused→{running,cancelled}`；终态不可再迁移

### 特殊终止路径

- **审批**：高风险工具 → `waiting_approval`（不占 Worker）→ 批准 → `running`（Checkpoint 续跑）/ 拒绝 → `failed`(`approval_rejected`)；审批绑 `args_hash`+`expires_at`
- **预算超限**（A-E）：事件循环每步 `OverBudget()`（tool>model>output 优先级）→ 命中 → `Runner.Cancel` 排空 → `failed`(`tool_calls|model_calls|output_bytes_budget_exceeded`) + `budget.exceeded` 事件
- **超时**：`MaxDurationSeconds` context deadline → `timed_out` + `run.timed_out` 事件
- **孤儿恢复**：启动时 `ListOrphanRuns(100)`：`running` → `failed`(`orphan_recovered`)（从最近 checkpoint 手动 resume）；`queued/paused/waiting_approval` 保持原状
- **校验失败**：成功标准未满足且无工具证据补救 → `failed`(`verification_failed`) + `verification.failed`

### mermaid 约定

- 组件图用 `graph LR`；生命周期用 `sequenceDiagram`；状态机用 `stateDiagram-v2`。GitHub 原生渲染，无需插件。

---

## Chunk 1: 中文 2 篇

### Task 1: `docs/architecture/overview.md`

**Files:**
- Create: `docs/architecture/overview.md`

- [ ] **Step 1.1: 写入**。结构：
  1. 总览段：单进程 / SQLite / 有界 Worker / Eino ADK v0.9.21
  2. **mermaid 组件图**（`graph LR`）：api/http → service → runtime(Runtime+Worker) → {store(SQLite), EinoRunner→model, tool Executor→Registry→{builtin,MCP,Skills}}；eventshub 旁路（EventRepo.Append → hub.Publish → SSE Hub）
  3. 分层职责表（目录 → 职责，对照 backend/README 目录树）
  4. **mermaid sequenceDiagram**：一次 Run 的生命周期（客户端 → api/http → RunService → Runtime/Worker → EinoRunner → model/tool → EventRepo/eventshub → SSE 客户端），含审批与预算终止两个分支注记
  5. 关键设计决策表：统一执行网关、事件溯源（agent_events 单一事实源）、Checkpoint 中断恢复、预算只紧不松、幂等键、多租户 owner

### Task 2: `docs/architecture/state-machine.md`

**Files:**
- Create: `docs/architecture/state-machine.md`

- [ ] **Step 2.1: 写入**。结构：
  1. **mermaid stateDiagram-v2**：8 状态全迁移（用「已核实事实」状态机节）
  2. 迁移表：from → to → 触发者（API/Worker/审批/预算/超时）
  3. 审批流小节：waiting_approval 进入/恢复/拒绝/过期（引用 [`docs/api/approvals.md`](../api/approvals.md)）
  4. 预算超限终止小节：三维度 + 优先级 + `budget.exceeded` 事件 + error_code 对照
  5. 超时路径、孤儿恢复小节（`orphan_recovered` 语义 + 如何手动 resume）
  6. 校验路径：`Verify`（成功标准 + 工具证据）→ succeeded / failed(`verification_failed`)；空结果防呆（无输出无证据 → 失败）

## Chunk 2: 英文镜像 + README 回改 + 验收

### Task 3: `docs/en/architecture/` 英文镜像

**Files:**
- Create: `docs/en/architecture/overview.md`、`state-machine.md`

- [ ] **Step 3.1: 以中文定稿为底翻译**。要求：mermaid 代码块**原样保留**（节点英文/标签同步翻译）；表格行列逐一对应；链接指向英文树（`../api/`、`../../README.en.md`）。

### Task 4: README「详细文档」跨批回改

**Files:**
- Modify: `README.md`（详细文档节）、`README.en.md`（Further Documentation 节）

- [ ] **Step 4.1: 中文版**。将 `设计文档（specs / plans）见 [`docs/superpowers/`](./docs/superpowers/)。` 替换为：

```markdown
- 架构设计：[`docs/architecture/`](./docs/architecture/)（组件图、Run 生命周期、状态机）
- API 参考：[`docs/api/`](./docs/api/)（逐端点 + SSE 事件目录）
- 设计文档（specs / plans）：[`docs/superpowers/`](./docs/superpowers/)
```

- [ ] **Step 4.2: 英文版**。同结构三行（Architecture / API Reference / Design docs）。

### Task 5: 验收检查

- [ ] **Step 5.1: 链接检查（零坏链）**

```bash
for f in README.md README.en.md docs/architecture/*.md docs/en/architecture/*.md; do
  d=$(dirname "$f")
  grep -oE '\]\(([^)#]+)' "$f" | sed 's/](//' | grep -vE '^(https?|mailto):' | while read -r t; do
    t=${t%%#*}; [ -z "$t" ] && continue
    [ -e "$d/$t" ] || echo "BROKEN: $f -> $t"
  done
done
```
Expected: 无 `BROKEN`。

- [ ] **Step 5.2: mermaid 语法**（每图块围栏配对 + 渲染器可解析声明）

```bash
grep -c '```mermaid' docs/architecture/overview.md docs/architecture/state-machine.md   # 各 ≥1
grep -c 'stateDiagram-v2' docs/architecture/state-machine.md docs/en/architecture/state-machine.md   # 各 1
```

- [ ] **Step 5.3: 状态覆盖**（8 状态 + 全部 error_code 出现于 state-machine.md）

```bash
for s in queued running waiting_approval paused succeeded failed cancelled timed_out; do
  grep -q "$s" docs/architecture/state-machine.md || echo "MISSING state: $s"
done
for c in approval_rejected orphan_recovered verification_failed tool_calls_budget_exceeded model_calls_budget_exceeded output_bytes_budget_exceeded; do
  grep -q "$c" docs/architecture/state-machine.md || echo "MISSING code: $c"
done
```
Expected: 无 MISSING（zh 与 en 各跑一遍）。

- [ ] **Step 5.4: 回归 + 双语 + 提交**

```bash
cd backend && go test ./... -count=1 -timeout 600s && cd ../frontend && npx tsc --noEmit && npx vitest run
for f in overview state-machine; do
  [ -f "docs/architecture/$f.md" ] && [ -f "docs/en/architecture/$f.md" ] && echo "OK $f" || echo "MISSING $f"
done
git add docs/architecture docs/en/architecture README.md README.en.md
git commit -m "docs(architecture): component graph, run lifecycle, state machine (zh/en); README detailed-docs section"
git status --short
```
Expected: 后端全包 ok；前端 20 文件 / 127 测试；2 × OK；status 仅 `?? .omo/`。

---

## 自审记录

| 检查项 | 结论 |
|---|---|
| 覆盖 spec 批3 全部要点（组件图/生命周期/状态机/审批/预算/孤儿/超时 + README 回改） | ✅ Task 1/2/4 |
| 事实全部来自代码（app.go 全文、recovery.go 全文、task.go ValidTransition、A-E 落地行为） | ✅ |
| mermaid 图类型与 GitHub 原生渲染兼容，无插件依赖 | ✅ graph LR / sequenceDiagram / stateDiagram-v2 |
| 验收映射 spec §6：链接(5.1) 回归(5.4) 双语(5.4)；spec §6-3 事件目录已由批2 28/28 覆盖不重复 | ✅ |
| EN 策略：执行期从中文定稿派生（批1/2 既有惯例） | ✅ |
| 已知风险：mermaid 语法错误无法用 grep 捕获 → Step 5.2 仅查围栏/声明，语法靠写作时小心 + 本地预览 | ⚠️ |
