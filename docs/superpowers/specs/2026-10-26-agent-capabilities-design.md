# Agent 能力扩展（工具集 ×4 · Skill 增强 · 子 agent 并行 · 跨 Run 记忆）设计

日期：2026-10-26
状态：四批设计已与用户逐节确认（ok×4；批4 审批模式选 A）

## 1. 背景与现状（已核实）

前两轮已完成：后端 A-E 优化（预算强制/幂等/Resume 预算/SQLite 8 连接/事件可观测）、文档 overhaul（中英 16 篇）。

本轮「完善优化」方向 3：Agent 能力扩展，用户全选四个子方向。现状：

| 子方向 | 现状 | 关键事实 |
|---|---|---|
| 工具集 | `tool/builtin/builtin.go` 单文件 3 工具（knowledge_search/file_read/workspace_write） | `knowledge_search` 是桩（"stub, connect FTS later"）；workspace 根硬编码包级 var `./workspace`；`cleanPath` 防穿越 |
| Skill | `skill/loader.go` 仅解析 name/description/version；`invoke_skill` 正文整体注入 32KB | skills/example/SKILL.md 为手写约定格式 |
| 子 agent | `subagent.go` `task` 工具同步单子任务，只读白名单（ApprovalNever+RiskRead），子预算=父 40% 封顶（clampChildBudget），depth≥1 禁用 | 子 Run 落库带 ParentRunID；SQLite 已 8 连接（WAL） |
| 记忆 | 无 | 需 schema v3（本项目首次迁移） |

## 2. 结构

**1 spec + 4 plans**（沿用文档轮批次模式），顺序：批1 工具集 → 批2 Skill → 批3 子 agent 并行 → 批4 记忆。
理由：批1 产出的只读工具自动进入批3 子 agent 白名单；批4 涉及 schema 迁移、横切面最大，放最后。

**安全原则（全局）**：工具全部走既有 `Definition`/`CheckPolicy`/统一网关（无旁路）；skill 内容仍视为不可信输入；零新依赖（检索用纯 Go 子串扫描，不引 FTS/模板引擎）。

## 3. 各批设计要点（已确认）

### 批1 · 工具集扩充

新增 4 工具（builtin，`Definition` 模式）：

| 工具 | Risk/Approval/Idempotent | 输入 | 行为 |
|---|---|---|---|
| `file_list` | read / never / true | `{path?, pattern?}` | 工作区列目录（可选 glob 递归），复用 `cleanPath` |
| `file_search` | read / never / true | `{query, path?, max_results?}` | 工作区文本全文子串检索（行号+摘录；NUL 探测跳二进制；大小上限；无 FTS 依赖） |
| `file_edit` | write / conditional（默认审批）/ false | `{path, old_text, new_text}` | 精确唯一匹配替换（old_text 恰好一次否则拒绝），返回变更字节数 |
| `http_fetch` | external / always / false | `{url, max_bytes?}` | GET-only、域名白名单（config，空=全拒）；双重大小上限（MaxResultBytes+max_bytes） |

改造 2 项：

1. `knowledge_search` 桩换真：同一检索引擎跑**知识目录**（与工作区分离），返回真实命中
2. 目录/白名单配置化：新增 config `workspace_dir`（默认 `./workspace`）、`knowledge_dir`（默认 `./knowledge`）、`http_fetch_allowlist`（逗号分隔）；env `AGENT_WORKSPACE_DIR`/`AGENT_KNOWLEDGE_DIR`/`AGENT_HTTP_FETCH_ALLOWLIST`；`builtin` 包加 `SetDirs()`/`SetHTTPAllowlist()` 装配钩子，app.go 注入

配套：`.env.example` + README + backend/README 配置表各补 3 行；前端零改动（工具列表经 settings/tools API 动态下发）。

### 批2 · Skill 机制增强

1. **结构化 frontmatter**：头部键值块扩展 `when_to_use`、`tools`；解析器向后兼容（旧格式留空，example 不破坏）
2. **多文件 skill**：skill 目录允许 `references/`；`invoke_skill` 加可选 `reference` 字段按需加载单个参考文件（默认返回正文+索引）；路径守卫（限该 skill 目录内）、16KB 上限；**不新增工具**
3. **参数化注入**：`invoke_skill` 加可选 `input`（object）：不做模板渲染（防注入），正文后追加 `## 运行时输入` JSON 块

安全不变量：skill 内容不可信、不覆盖系统策略；总大小上限不变。YAGNI：skill 组合调用、市场、模板引擎、热重载增强均不做。

### 批3 · 子 agent 并行

新增 `tasks`（复数）工具：一次派发 N 个只读子任务并发执行。

- 输入 `{tasks:[{goal, tools?, max_model_calls?, max_tool_calls?, max_iterations?}], max_concurrency?}`，项与 task 同构
- 单子任务执行体（建子 Run → RunChild → 结果分类 → 终态化）抽共享 helper；task=helper×1，tasks=helper×N errgroup 并发（限流缺省 4，上限 8；批大小上限 5；工具 TimeoutMs 120s 共享 wall-clock）
- 聚合按输入顺序 `[{run_id,status,text,error}]`；单项失败不影响兄弟
- 预算/安全：每子沿用 clamp（≤父 40%）；depth 守卫与只读白名单原样复用
- YAGNI：异步 fire-and-forget、子任务写权限、孙代嵌套、父预算池化均不做

### 批4 · 跨 Run 记忆

- **存储**：新表 `session_memories`(id, session_id, owner_id, content, source_run_id, created_at)；`migrate()` 升 **v3**（schema_migrations INSERT + CREATE TABLE；旧库升级兼容）；会话删除级联清理
- **写入**：`memory_write` 工具（`{content}`，追加式，每条 ≤2KB，会话上限 100 条 LRU）；审批模式 **A：RiskWrite + ApprovalNever**（会话作用域、无外部副作用；接受"记忆投毒跨 Run 持久化"信任面，用户可见性靠 API 弥补）
- **读取**：① **自动注入**（核心）：`buildAgent` instruction 追加「会话记忆」节（最近 10 条、总量 8KB 截断）；② `memory_read` 工具（`{query?}` 子串过滤）
- **API**：仅新增 `GET /api/v1/sessions/{id}/memories`（可见性/审计）；单条删除/编辑不做
- YAGNI：自动摘要沉淀、向量检索、跨会话记忆、新事件类型均不做

## 4. 交付与组织

- 每批：writing-plans → executing-plans（TDD、commit-per-task）→ 验收 → 下一批
- 自审（用户约束：**不使用 subagent**），spec/plan 自审后执行；直接提交 master（既有模式）
- 前端零代码变更（批1/2/3）；批4 前端可后续跟进（记忆展示），本轮不含

## 5. 验收标准（每批 DoD）

1. 每批 `cd backend && go test ./...` 全绿（含新增测试）；前端 `npx tsc --noEmit && npx vitest run` 全绿（零前端变更证明）
2. 批1：新工具经 registry 注册并可执行（集成测试断言 file_list/file_search 可跑通、file_edit 走完 waiting_approval → approve 流、http_fetch 白名单外 URL 被拒）；注：`ToolStatus` 仅暴露 skills/MCP 两节，builtin 工具不在其中，勿以该端点验收
3. 批2：example skill（旧格式）行为不回归；reference 穿越/超限被拒
4. 批3：2 并发子任务集成测试双子行落库 ParentRunID 正确、按输入序聚合
5. 批4：旧库升级到 v3 成功（迁移测试覆盖升级路径）；Run A 写记忆 → Run B instruction 含该记忆
6. 每批自审记录 + commit 历史（plan → 实现 → 验收）

## 6. 风险

| 风险 | 缓解 |
|---|---|
| schema v3 迁移破坏旧库 | 迁移测试覆盖「新库初始化 + v2→v3 升级」双路径；colExists 守卫 pattern 沿用 |
| http_fetch 出网滥用 | 默认全拒 + 始终审批 + GET-only + 双重大小上限 + 域名白名单 |
| 并行子任务预算膨胀 | 批大小上限 5 + 每子 ≤父 40% 封顶 + 工具级 120s wall-clock |
| 记忆投毒 | 用户选定 A（接受信任面）；GET memories API 提供可见性；会话删除级联清理 |
| knowledge 目录为空/不存在 | Loader/Open 容错（IsNotExist→nil），工具返回明确"无命中" |
