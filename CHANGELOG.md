# Changelog

本项目的重要变更记录。格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)；
版本号遵循 [SemVer](https://semver.org/lang/zh-CN/)，发布版本见 [Releases](../../releases)。

## [Unreleased]

### Added

- **Agent 工具集**：内置 9 个工具——`file_list` / `file_search` / `file_read` / `file_edit`（审批门控）/ `http_fetch`（审批 + 域名白名单）/ `knowledge_search` / `memory_read` / `memory_write` / `workspace_write`；共享文件检索引擎（跳过二进制与大文件）。
- **子 agent 并行**：`tasks` 复数工具，一次委派最多 5 个子任务，信号量并发（默认 4，上限 8），结果按输入顺序返回。
- **会话记忆**：`session_memories` 表（LRU 100 条、随会话级联删除）、`memory_write` / `memory_read` 工具、顶层 run 指令自动注入最近 10 条记忆（8KB 上限）、`GET /api/v1/sessions/{id}/memories`。
- **Skill 增强**：frontmatter `when_to_use` / `tools` 字段；`invoke_skill` 支持 `references/` 子文件加载（路径防逃逸）与运行时输入注入。
- **MCP**：Streamable-HTTP/SSE 传输（`type: http` 服务器 + 请求头透传）；config.yaml `mcp` 段启动写穿 + 5 秒轮询热载；30 秒周期 Reconciler 自愈重连；每次工具调用 30 秒超时。
- **可观测性**：`X-Request-ID`（透传/生成/清洗）+ 结构化访问日志；`/metrics/prometheus` 文本暴露（零依赖 expvar 转换器）；带鉴权门控的 `/debug/pprof`；expvar 延迟直方图与 `rate_limited_total` / `event_append_failed` 计数器。
- **评估体系**：`tests/evals` 确定性评估框架（mock 模型，三维度：正确性/效率/稳定性），30 个场景 + `make test-eval` + JSON 报告（CI artifact，保留 14 天）；8 项基准测试（`tests/bench`）。
- **运维**：`agentd export -session` 单会话 JSON 导出；`agentd backup -o` 在线快照备份（`VACUUM INTO`）；启动时打印 DB 统计、停机时 WAL checkpoint + 日志化关闭；`GET /version` 构建信息（免鉴权）。
- **前端**：暗色主题三态切换（无闪烁预注入脚本）；中/EN 界面语言切换（极简 i18n 机制）；会话记忆弹窗、跨会话运行历史弹窗（状态筛选 chips）、运行详情弹窗（步骤/工具调用/审批）、子 agent 执行树与工具调用明细卡片、流式打字指示器；ErrorBoundary 错误边界。
- **安全**：按客户端限流（`AGENT_RATE_LIMIT_RPM`，默认关闭；429 + Retry-After）、常量时间 Bearer 比较、四个安全响应头。

### Changed

- SQLite 连接池：文件库 8 连接（WAL 并发读），`:memory:` 保持单连接；PRAGMA 经 DSN `_pragma` 参数应用到每个连接。
- `GET /api/v1/runs`：`session_id` 变为可选（省略 = 跨会话顶层历史），新增 `status` 过滤。
- `tool.completed` 事件载荷富化：携带 `args` 与 `output`（各 1KB 截断）。
- README 全面翻新：双语文档（README / backend / frontend / docs/api / docs/architecture），21 条路由表 + Docker 快速开始。

### Fixed

- 幂等工具重复调用返回空结果（defer 误清缓存）。
- SSE 事件缓冲满时静默丢弃（emit 改为阻塞发送 + ctx 逃生）。
- 终态失败的「重试」按钮误用 resume（改为按原参数重新创建运行）。
- http_fetch 域名白名单匹配剥离端口（与 `u.Hostname()` 语义一致）。
- 运行预算未强制执行（事件循环后置门 + `budget.exceeded` 事件 + 三维度终止）。
