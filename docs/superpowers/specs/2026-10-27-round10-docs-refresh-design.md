# Round 10 · 文档完善设计（docs refresh after rounds 3–9）

日期：2026-10-27 · 状态：已批准（静默惯例 + 用户指令「完善文档」）

## 1. 背景与问题

第 2 轮建立了双语文档体系（docs/api ×7、docs/architecture ×2、README ×6），此后第 3-9 轮
持续新增能力时只做了**顺手同步**（README 路由表/工具清单、各端点文档、events 目录）。
经逐项核对（grep + 通读），当前漂移收敛为两处：

| 文档 | 漂移 | 核实方式 |
|---|---|---|
| `docs/architecture/overview.md`（zh+en） | 停在第 1 轮 A-E 后：缺 tasks 并行子代理、会话记忆、内置工具 4→9、skill references/frontmatter、MCP 自愈（30s Reconciler + 30s 调用超时）、安全中间件（限流/安全头/恒时比较）、X-Request-ID/访问日志、tool.completed 载荷富化、buildinfo/版本注入、21 路由计数 | 15 个关键词 grep 0 命中 + 通读 96 行 |
| `docs/api/overview.md`（zh+en） | ① 错误码表缺 `rate_limited`（429，第 6 轮引入）② 通用约定缺 X-Request-ID 规则（第 9 轮引入）③ 文档索引「system.md 3 端点」实为 4（含 /version，第 7 轮） | grep + 通读 85 行 |

**无漂移（核对通过，不动）**：events.md 事件目录 28/28、state-machine.md（含 budget.exceeded）、
runs/sessions/settings/system 端点文档、根/backend/frontend README、evals 计数（20）。

## 2. 方案（两个批次，纯文档零代码）

### 批1 · 架构总览定向升级（zh + en 同步改动）

保持现有 4 段结构（组件图/分层职责/生命周期/设计决策）与既有正确内容，做增量修订：

1. **组件图**：`REG --> Builtin` 节点文案 →「builtin 内置工具 ×9（文件读写/搜索/编辑/http_fetch/知识/记忆）」（invoke_skill 与 task/tasks 为独立注册边，MCP 动态注册）
2. **分层职责表**（6 处修订 + 1 新行）：
   - `api/http`：19 条 → **21 条路由（20 REST + /metrics）**；追加「限流/安全头/X-Request-ID/访问日志中间件、Bearer 恒时比较」
   - `runtime`：追加「tasks 并行子代理（batch ≤5、子预算 40% 收紧）、会话记忆注入（最新 10 条/8KB）」
   - `tool`：追加「tool.completed 事件携带 args/output（1KB 截断）」
   - `skill / mcp` 行拆分语义：skill += 「SKILL.md frontmatter（when_to_use/tools）+ references/ 多文件 + 运行时输入注入」；mcp += 「30s 自愈 Reconciler、单次调用 30s 超时」
   - `store`：追加「MemoryRepo（会话记忆 LRU 100、随会话级联删除）」
   - 新行 `buildinfo`：版本三元组（Version/Commit/BuildDate）ldflags 注入目标，/version 端点数据源
3. **生命周期时序图**：`Runner.Run` 前插入「注入会话记忆」一步；执行循环内工具调用注释追加「载荷富化」
4. **关键设计决策表** 追加 4 行：
   - 限流默认关闭：`AGENT_RATE_LIMIT_RPM=0` 不启用；按 X-Owner-ID 或来源 IP 分桶（LRU 1024），仅 `/api/v1/`，探测端点豁免
   - 请求可观测：X-Request-ID 透传/生成 + 结构化访问日志（method/path/status/duration_ms/request_id）
   - MCP 自愈：失败服务器每 30s 重试连接；单次工具调用 30s 超时
   - 载荷富化：`tool.completed` 合并 args/output（1KB 截断），前端工具明细卡直接消费
5. 文末「相关文档」行保持不变

### 批2 · API overview 小修（zh + en 同步改动）

1. 错误码表追加：`rate_limited` | 429 | 触发速率限制（`retryable: true`，见 `Retry-After` 响应头）
2. 「多租户」小节后新增「请求标识（X-Request-ID）」小节：可携带（≤64 字符，字符集 `[0-9a-zA-Z-._]`，其余剥除）、缺省自动生成 32 位 hex、响应头回显、访问日志携带
3. 文档索引表 `system.md` 行：「健康检查与指标（3 端点）」→「健康/就绪/指标/版本（4 端点）」

## 3. 验收标准

1. `grep` 断言 zh+en overview 均含：`21 条路由|21 routes`、`X-Request-ID`、`rate_limited`、`记忆`/`memory`、`30s`、`buildinfo`、`×9|×9 built-in|9 built`（工具数）
2. 双语 parity：zh/en 架构文档 section 标题序列一致；api/overview 行数近似（±5）
3. 链接检查：`docs/**/*.md` 内相对链接全部指向存在的文件（bash 循环校验）
4. 回归：`go test ./... -count=1` 与 frontend `tsc --noEmit && vitest run` 全绿（本轮零代码变更，验证无意外）
5. 英文版不是机翻腔：与第 2 轮 en 文档风格一致（短句、术语保留）

## 4. 非目标（YAGNI）

- 不新增文档文件（不写 CHANGELOG/CONTRIBUTING/安全专题页）
- 不改 events.md/state-machine.md/端点文档/README（已同步）
- 不引入文档生成器、不迁移 mermaid 形式、不做版本化历史页

## 5. 风险

- en 版翻译遗漏或风格漂移 → 用「同段落成对编辑」而非整文件重写降低风险；验收 2 兜底
- 架构图与代码偏差（如工具数再增）→ 文中写「×11」时同时列名，便于后续 grep 校对
