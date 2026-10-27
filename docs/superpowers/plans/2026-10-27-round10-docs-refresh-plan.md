# Round 10 文档完善 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans (no subagents per user constraint). Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把第 3-9 轮的新能力补进架构总览（zh+en），修正 api/overview 三处漂移。

**Architecture:** 纯文档轮，零代码。定向增量编辑（保留现有正确内容），zh/en 成对同步改，验收靠 grep 断言 + 链接检查 + 双语 parity。

**Tech Stack:** Markdown、mermaid（既有图增量）、bash grep 验收。

**Spec:** docs/superpowers/specs/2026-10-27-round10-docs-refresh-design.md（commit 499f024）

---

## Chunk 1: 架构总览升级 + API overview 修正

### Task 1: docs/architecture/overview.md（zh）9 处编辑

**Files:**
- Modify: `docs/architecture/overview.md`

- [ ] 1.1 组件图 L16：
  - old: `REG --> Builtin["builtin 只读/受控写工具"]`
  - new: `REG --> Builtin["builtin 内置工具 ×9<br/>(文件读写/搜索/编辑/http_fetch/知识/记忆)"]`
- [ ] 1.2 L30 api/http 行 →
  `| \`api/http\` | 21 条路由（20 REST + /metrics）、SSE 流（重放 + 推送 + gap 回填）、Bearer 认证（恒时比较）、限流/安全头/X-Request-ID/访问日志中间件、owner 归属 |`
- [ ] 1.3 L32 runtime 行追加：`、tasks 并行子代理（batch ≤5、子预算 40% 收紧）、会话记忆注入（最新 10 条/8KB）`
- [ ] 1.4 L34 tool 行追加：`；tool.completed 事件携带 args/output（1KB 截断）`
- [ ] 1.5 L36 store 行追加：`、MemoryRepo（会话记忆 LRU 100、随会话级联删除）`
- [ ] 1.6 L38 skill/mcp 行 →
  `| \`skill\` / \`mcp\` | Skill 按需加载校验（SKILL.md frontmatter + references/ 多文件 + 运行时输入注入）/ stdio MCP 服务器管理（进程组回收、就地热更新、30s 自愈 Reconciler、单次调用 30s 超时） |`
- [ ] 1.7 L41 app 行后插入：`| \`buildinfo\` | 版本三元组（Version/Commit/BuildDate）ldflags 注入目标，GET /version 数据源 |`
- [ ] 1.8 时序图：`W->>ER: Runner.Run（流式 + Checkpoint）` 前插入 `    W->>ER: 注入会话记忆（最新 10 条 / 8KB）`；`T-->>ER: 输出（限流/超时/幂等）` → `T-->>ER: 输出（限流/超时/幂等；载荷富化 args/output）`
- [ ] 1.9 设计决策表「多租户」行后追加 4 行：
  - `| 限流默认关闭 | \`AGENT_RATE_LIMIT_RPM=0\`（默认）不启用；开启后按 X-Owner-ID 或来源 IP 分桶（LRU 1024），仅作用于 \`/api/v1/\`，健康探测端点豁免 |`
  - `| 请求可观测 | X-Request-ID 透传/自动生成（32 位 hex），结构化访问日志记录 method/path/status/duration_ms/request_id |`
  - `| MCP 自愈 | 失败的 MCP 服务器每 30s 自动重连（Reconciler）；单次工具调用 30s 超时防卡死 |`
  - `| 载荷富化 | tool.completed 事件合并 args/output（各 1KB 截断），前端工具明细卡直接消费 |`
- [ ] 1.10 验证：`grep -c` 断言（21 条路由 / X-Request-ID / ×9 / 记忆 / 30s / buildinfo 各 ≥1）；mermaid 代码块数仍为 2

### Task 2: docs/en/architecture/overview.md（en）镜像 9 处

**Files:**
- Modify: `docs/en/architecture/overview.md`

- [ ] 2.1 L16 → `REG --> Builtin["builtin tools ×9<br/>(file I/O / search / edit / http_fetch / knowledge / memory)"]`
- [ ] 2.2 L30 → `| \`api/http\` | 21 routes (20 REST + /metrics), SSE stream (replay + push + gap backfill), Bearer auth (constant-time compare), rate-limit / security-headers / X-Request-ID / access-log middleware, owner ownership |`
- [ ] 2.3 L32 追加：`, parallel \`tasks\` sub-agents (batch ≤5, child budgets clamped to 40%), session memory injection (latest 10 entries / 8KB)`
- [ ] 2.4 L34 追加：`; tool.completed events carry args/output (1KB truncation)`
- [ ] 2.5 L36 追加：`, MemoryRepo (session memories, LRU 100, cascade delete with session)`
- [ ] 2.6 L38 → `| \`skill\` / \`mcp\` | On-demand skill loading & validation (SKILL.md frontmatter + references/ files + runtime input injection) / stdio MCP server management (process-group reclaim, in-place hot refresh, 30s self-healing reconciler, 30s per-call timeout) |`
- [ ] 2.7 L41 后插入：`| \`buildinfo\` | ldflags injection target for the version triple (Version/Commit/BuildDate), data source of GET /version |`
- [ ] 2.8 时序图：`W->>ER: Runner.Run (streaming + Checkpoint)` 前插入 `    W->>ER: Inject session memory (latest 10 / 8KB)`；`T-->>ER: output (rate-limited / timed / idempotent)` → `T-->>ER: output (rate-limited / timed / idempotent; enriched with args/output)`
- [ ] 2.9 决策表追加 4 行（en）：
  - Rate limiting off by default: `Disabled unless \`AGENT_RATE_LIMIT_RPM\` is set; keyed by X-Owner-ID or source IP (LRU 1024), scoped to \`/api/v1/\` only, probe endpoints exempt`
  - Request observability: `X-Request-ID propagated or auto-generated (32-hex); structured access log records method/path/status/duration_ms/request_id`
  - MCP self-healing: `Failed MCP servers are retried every 30s (reconciler); each tool call has a 30s timeout to prevent hangs`
  - Payload enrichment: `tool.completed events merge args/output (1KB truncation each), consumed directly by the frontend tool-calls card`
- [ ] 2.10 验证：与 Task 1 相同断言（en 词形）；zh/en `## ` 标题序列 diff 为空
- [ ] 2.11 Commit：`docs(architecture): cover rounds 3-9 in overview (tools/memory/sub-agents/mcp self-heal/security/observability/buildinfo) zh+en`

### Task 3: docs/api/overview.md（zh+en）3 处修正

**Files:**
- Modify: `docs/api/overview.md`, `docs/en/api/overview.md`

- [ ] 3.1 错误码表 `unauthorized` 行后插入：
  - zh: `| \`rate_limited\` | 429 | 触发速率限制（\`retryable: true\`，等待秒数见 \`Retry-After\` 响应头） |`
  - en: `| \`rate_limited\` | 429 | Rate limit triggered (\`retryable: true\`; see the \`Retry-After\` response header) |`
- [ ] 3.2 「多租户」小节后新增小节（两级标题 `##`，跟随现有小节命名风格）：
  - zh: `## 请求标识（X-Request-ID）` + 正文：客户端可携带 `X-Request-ID` 请求头（≤64 字符，字符集 `[0-9a-zA-Z-._]`，其余字符剥除）；缺省自动生成 32 位 hex。所有响应回显该头，访问日志按 request_id 关联，便于排障串联。
  - en: `## Request ID (X-Request-ID)` + 正文：Clients may send `X-Request-ID` (≤64 chars, charset `[0-9a-zA-Z-._]`, other characters stripped); when absent a 32-hex id is generated. Every response echoes the header and the access log carries request_id for troubleshooting.
- [ ] 3.3 文档索引表：zh `健康检查与指标（3 端点）` → `健康/就绪/指标/版本（4 端点）`；en `Health checks and metrics (3 endpoints)` → `Health / readiness / metrics / version (4 endpoints)`
- [ ] 3.4 验证：`grep -c rate_limited` = 1 each；`grep -c "X-Request-ID"` ≥2 each；行数 zh/en 相近
- [ ] 3.5 Commit：`docs(api): rate_limited code, X-Request-ID convention, system endpoint count (zh/en)`

## Chunk 2: 验收

### Task 4: 全量验收

- [ ] 4.1 链接检查：`bash` 循环提取 `docs/**/*.md` 中相对链接 `(xxx.md)` 并逐一 `[ -f ]` 断言
- [ ] 4.2 关键词断言汇总（zh+en 架构文档 + api/overview）
- [ ] 4.3 回归：`cd backend && go test ./... -count=1`；`cd frontend && npx tsc --noEmit && npx vitest run`（零代码变更，验证无意外）
- [ ] 4.4 如有偏差回写 spec；wrap-up report

## Self-review notes

- 工具数已核实：builtin 9（file_edit/file_list/file_read/file_search/http_fetch/knowledge_search/memory_read/memory_write/workspace_write）+ invoke_skill + task/tasks + MCP 动态 —— spec 中 ×11 已改为 ×9
- en 编辑以「同行替换/追加」成对进行，不做整文件重写，防风格漂移
- 时序图新增行为 participant W 自身消息（`W->>ER` 前插入 `W->>ER`），mermaid 语法合法
- rate_limited 行插入位置在 unauthorized 之后、internal 之前，保持状态码升序（400/401/404/409/429/500/501/503 中 429 位于 409 与 500 之间——实际表格顺序按现有表为准，插到 unauthorized 后即可）
