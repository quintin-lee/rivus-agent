# 第 16 轮设计：运维 CLI 补全 + 前端 ErrorBoundary + evals 扩展

日期：2026-10-28　|　状态：已批准（用户 'ok'）　|　前置：R1–R15 全部完成

## 1. 背景与缺口（已核实）

1. **备份只能写 Go 调用**：R13 落地 `store.Backup`（`VACUUM INTO`，目标存在拒绝覆盖、:memory: 明确不支持），但 `cmd/agentd` 只有 `export` 子命令（main.go 按 `os.Args[1]` 分发）——运维无法从命令行直接备份。
2. **无仓库级变更记录**：CHANGELOG.md 不存在；release.yml 依赖 GitHub `--generate-notes`，本地/离线场景不可见。
3. **前端无错误边界**：全项目 0 个 class 组件、无 ErrorBoundary/getDerivedStateFromError——任何渲染异常（如 SSE 派生消息格式意外）都会卸载整棵树白屏。
4. **evals 契约盲区**（27 场景）：幂等键重复 POST 的 `duplicated:true` 契约、`tasks` 批次 >5 的校验拒绝（subagent.go:264 `batch size must be 1..5`）、审批重复决定的 409 冲突保护，均无端到端场景。

## 2. 方案总览

| 批次 | 内容 | 规模 |
|---|---|---|
| 批1 | `agentd backup` 子命令 + CHANGELOG.md 初始化 | 后端 2 文件 + 根目录 1 文件 |
| 批2 | ErrorBoundary 组件（App 根 + 对话区局部） | 前端 3 文件 |
| 批3 | evals 27→30：幂等重复 / tasks 批次拒绝 / 审批 409 | 1 新测试文件 |

**YAGNI**：不做 backup 定时任务/保留策略（运维 cron 自理）；不做 CHANGELOG 自动生成（release notes 已有）；不做路由级 ErrorBoundary 逐个包（两处挂载覆盖主风险）；不加新 npm 依赖。

## 3. 批次详情

### 3.1 批1 `agentd backup` + CHANGELOG

- **CLI**（TDD，复用 export 模式）：main.go 分发 `os.Args[1] == "backup"`；`backup.go`：`runBackup(args)` = flag.NewFlagSet（`-config` / `-o` 目标路径必填，缺失 stderr + exit 2）→ `config.Load + store.Open(cfg.DBPath)` → `store.Backup(db, *out)`；错误 stderr + exit 1，成功 stderr 摘要 `已备份到 %s`。
- **单测** `backup_test.go`：`runBackupFlagValidation`（-o 缺失语义）；备份路径走 store 层既有 3 测试，此处只冒烟 flag 与主流程（newExportDB 风格夹具建库 → runBackup → 重新 store.Open 备份文件 → 查询 sessions 表有行）。
- **真机冒烟**：make build → 临时 DB 起服务建会话 → 停服务 → `agentd backup -config ... -o f.db` → 重新打开 f.db 查到会话。
- **CHANGELOG.md**：Keep a Changelog 格式；`## [Unreleased]` + Added/Changed/Fixed 三段；15 轮按主题归并为 ~10 行摘要（不逐轮流水账）。

### 3.2 批2 ErrorBoundary（TDD）

- 新组件 `frontend/src/components/ErrorBoundary.tsx`（项目首个 class 组件）：`getDerivedStateFromError` → `{ error }`；`componentDidCatch` → `console.error`；fallback：⚠️「页面出错」文案 + 重试按钮（清 error state 重挂子树）。
- **挂载点**：main.tsx `<App/>` 外层（白屏兜底）；ChatMessageList 外层（对话区局部边界，消息渲染异常不影响会话列表/输入框）。局部实例重试 = 仅重挂消息区。
- 测试 `ErrorBoundary.test.tsx`：Thrower 子组件 → fallback 出现；点击重试 → 正常子组件重新渲染；`console.error` 静音（vi.spyOn）。

### 3.3 批3 evals 27→30（scenarios_round16_test.go）

1. `run_idempotency_duplicate`（tool）：正常 run 创建（Script 返回幂等键）→ PostCheck 用同 payload + 同 `Idempotency-Key` 头再 POST → 201 + `duplicated:true` + run_id 与首跑一致。
2. `tasks_batch_limit_rejected`（tool）：`tasks` 调用带 6 个子任务 → handler 校验拒绝 → tool.completed 载荷含 `batch size must be 1..5`、`ListChildrenByRun` 为空、run 仍 succeeded（fallback 收尾）。
3. `approval_double_decide_conflict`（tool）：file_edit 审批流，MidRun 批准 → PostCheck 再次 POST 同一 approval → **409** + `code:"conflict"`。

**验收（DoD）**

1. 后端 `go build ./... && go vet ./...`；`go test ./... -count=1` 全绿；`make test-eval` 30 场景 pass_rate 1。
2. 真机 backup 冒烟通过（备份文件可重新打开查询）。
3. 前端 `npx tsc --noEmit` 0 错误 + `npm run lint` 通过 + `npx vitest run` 全绿。
4. CHANGELOG.md 存在且格式合法；README zh/en 评估计数回写 30。
5. spec §6 偏差回写。

## 5. 风险

- class 组件入 TS 严格模式 → 显式 `React.Component<Props, State>` 泛型；无第三方依赖。
- 幂等场景的第二次 POST 若服务端实现为 409 而非 201+duplicated → 以 handlers_runs.go:79 实测为准（规划期已核实返回 201 + duplicated）。
- tasks 拒绝场景 tool.completed 载荷是摘要（{text,tool,args,output}）→ output 含 errRes JSON（R5 载荷富化已入 stash）。

## 6. 执行偏差

- 批1 冒烟教训：`pkill -f bin/agentd` 会卡死常驻 shell（超时）——改用 `nohup` 启动时捕获的精确 PID `kill <PID>`；`agentd backup` 调用时同样必须带 `AGENT_DB_PATH` 环境变量（否则 config.Load 走默认路径，R13 同坑）。
- 批3 幂等场景：重放 POST 短路不消耗 mock 输出（PostCheck 断言 model calls == 1）；Script 返回固定幂等键供 PostCheck 重放（framework.go:84 语义）。
- 批3 审批 409 场景首稿漏种 a.txt（file_edit 找不到 old_text → 工具失败 → run failed）——补 `writeFileRel` 后通过；409 + conflict 断言按预期命中。
- 批3 tasks 拒绝走 tool.completed（handler 返回字符串结果，工具"成功"），与设计一致；children == 0 断言确认拒绝发生在子 run 创建之前。
- ErrorBoundary 测试时序：点击「重试」时若 children 仍是抛错元素会二次抛错（React 以旧 children 重渲染）——正确顺序为先 rerender 成不抛错的子树（兜底期间 children 不渲染、不会抛）再点击重试。
