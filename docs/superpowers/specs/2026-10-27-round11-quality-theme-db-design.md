# Round 11 设计：质量门禁 / 暗色主题 / DB 维护

> 日期：2026-10-27　状态：已批准（用户 ok，m1432）
> 前置：R1-R10 全部完成（HEAD 527f22a）。约束：无子代理、master 直接提交、TDD、每批全量回归。

## 1. 背景与问题

探索结论（m1419-m1431）：
- 代码体量健康（最大 task_repo.go 481L）、无 TODO/FIXME、SSE 客户端健壮（退避 1s→30s、seq 续传、poller 兜底）、会话删除/添加 MCP UI 均已存在。
- **缺口 ①**：前端 `npm run lint` 当前是坏的——`.eslintrc.cjs` 为 legacy 格式，而已安装 eslint ^10.12.0（v10 移除 eslintrc 支持，运行即报 "Oops"+迁移指南链接）。package.json `"type":"module"`、`"lint": "eslint src --ext ts,tsx"`；仓库无 eslint.config.*。CI frontend job 只跑 tsc/vitest/build，无 lint。后端无 .golangci.yml，golangci-lint 本机未安装。
- **缺口 ②**：tailwind `darkMode:'class'` 且全部组件已有 dark: 样式，但无任何切换逻辑；ui-store 只有 sseStatus/apiReachable。
- **缺口 ③**：app.Stop 顺序 manager.StopAll → srv.Shutdown → db.Close，后两者错误被 `_ =` 吞掉；无 WAL checkpoint/统计可见性。

## 2. 方案总览（3 批，每批独立可交付）

| 批 | 内容 | 主要文件 |
|---|---|---|
| 批1 | lint 门禁：eslint 迁移 flat config + CI 接入；golangci-lint 尽力接入 | frontend/eslint.config.js、ci.yml、backend/.golangci.yml |
| 批2 | 暗色主题三态（light/dark/system）+ 无闪烁 + Header 循环按钮 | frontend/src/stores/ui-store.ts、Header.tsx、index.html |
| 批3 | DB 维护：Stats/Checkpoint + Stop 错误日志化 | backend/internal/store/maintenance.go、app/app.go |

## 3. 批次设计

### 3.1 批1 质量门禁

**前端 eslint（修复当前破损）：**
- 新建 `frontend/eslint.config.js`（flat config，eslint v10）：复用原 .eslintrc.cjs 语义（typescript-eslint + react-hooks + react-refresh），忽略 dist/node_modules；原 .eslintrc.cjs 删除。
- 跑 `npm run lint`，修复全部报错（预期少量：未用变量/hook 依赖），保持 vitest 150 全绿。
- package.json 的 lint script 不变（flat config 下 `--ext` 被忽略但无害；如报错则去掉）。
- CI frontend job 在 tsc 前插入 `npm run lint`。

**后端 golangci-lint（尽力接入，允许降级）：**
- 新建 `backend/.golangci.yml`：v2 格式，最小集（默认 linters + goimports）。
- 执行时先 `go install github.com/golangci/golangci-lint/...@latest` 并运行验证；**网络失败则降级**：保留 .golangci.yml + CI lint job（golangci-lint-action），本地以 go vet 为准，偏差记录进 spec 与 wrap-up。
- CI 新增 lint job：golangci-lint-action@v7（backend/ 工作目录）。

### 3.2 批2 暗色主题

- ui-store：`theme: 'light'|'dark'|'system'`，默认 `'system'`；`setTheme` 持久化 localStorage `'rivus-theme'` 并应用。
- `applyTheme(t)`：t=dark→classList.add('dark')；light→remove；system→matchMedia('(prefers-color-scheme: dark)') 决定；同时注册 system 模式下的媒体查询监听（切换即时生效）。
- index.html `<head>` 注入 pre-hydration 内联脚本（读 localStorage，system 跟随系统），消除首帧闪烁。
- Header：三态循环按钮 light→dark→system，图标 Sun/Moon/Monitor（lucide 已有），aria-label '切换主题'。
- 测试：store 三态设置与持久化、applyTheme 对 classList 的效果（jsdom mock matchMedia）、Header 点击循环、localStorage 恢复。

### 3.3 批3 DB 维护

- `backend/internal/store/maintenance.go`：
  - `Stats(db *sql.DB) (map[string]int64, error)`：PRAGMA page_count/page_size/freelist_count；WAL 字节数 = 先 `PRAGMA database_list` 取 name=='main' 行的 file 列得到 DB 路径，再 `os.Stat(path+"-wal")`（不存在→0；路径为空（:memory:）→0）。
  - `Checkpoint(db *sql.DB) error`：`PRAGMA wal_checkpoint(TRUNCATE)` + `PRAGMA optimize`。
- 单元测试（TDD）：文件 DB 建表写数据 → Stats 各键 ≥0 且 page_count>0 → 写入后 WAL>0 → Checkpoint 后 WAL==0（TRUNCATE 语义）。
- app 接线：Start 在 Open 后 `a.log.Info("db stats", ...)`；Stop：srv.Shutdown / db.Close 的错误不再吞掉，先记日志；Shutdown 成功后调 Checkpoint 再 Close。main.go 不动（signal 逻辑已有）。

## 4. 验收标准

1. `cd frontend && npm run lint` 退出码 0；vitest 全绿（≥150）；tsc 0 错误。
2. CI yml 含 lint 步骤（frontend job + golangci-lint job）；.golangci.yml 存在且被 CI 引用。
3. 主题：三态切换在 Header 可操作；刷新后保持；system 模式跟随系统；首帧无白闪（内联脚本存在）。
4. `make test-eval` 20 场景 pass_rate 1；`go test ./... -count=1` 全绿。
5. Stats/Checkpoint 单测绿；app.Stop 对 Shutdown/Close 错误有日志（代码可见，非 `_ =`）。
6. 文档：backend/README zh/en（DB 维护小节或句子）；frontend/README zh/en（主题切换 + lint）；spec 偏差回写。

## 5. 非目标（YAGNI）

- 不引入 prettier/stylelint；不重排现有格式。
- 不做主题自定义色板/多主题；只做三态。
- 不做定时 checkpoint/备份导出；只在优雅退出时 checkpoint。
- 不改 DB schema、不加迁移。

## 6. 风险

- eslint v10 生态差异：typescript-eslint 版本兼容性——若插件 API 不匹配，按 v10 迁移文档调整（types 不用 project service，保持轻量）。
- golangci-lint 首次运行可能报存量问题：只调配置（exclude）不改业务代码；超出预期的报错记为偏差。
- jsdom 无 matchMedia：测试需 mock；实现侧需守卫 `window.matchMedia` 不存在时的降级。

## 7. 执行偏差回写

**批1：**
- typescript `^7.0.2` → `^6.0.3`：@typescript-eslint（含最新 8.72.1）尚不支持 TS 7（upstream issue 10940），导入即抛错；TS7 为原生提速移植，语言一致，回归全绿。
- eslint-plugin-react 移除：无 eslint-10 兼容版本（peer 上限 ^9.7，version 检测 util 崩溃）；JSX 正确性由 tsc 覆盖。
- 新增 devDep `@eslint/js`（core recommended 规则集来源）；`globals` 包未提升至顶层 node_modules → browser globals 内联（no-undef 已关闭）。
- 存量 22 项 golangci 发现全部修复（非仅 exclude）：死代码 `subagent.out()`/harness.tools 字段删除、De Morgan ×2、ST1023、SA9005 加 nolint（eino 外部类型）、builtin_test 显式错误处理、goimports 分组（经 `golangci fmt` 保证版本一致）。errcheck 配置排除 Close 家族（io.Closer/io.ReadCloser/*database/sql.DB）。
- golangci-lint 本地安装成功（v2），无需降级路径。

**批2：** 按计划执行，无偏差（默认主题 system 会改变"暗色优先"的旧观感，为设计预期）。

**批3：** 优雅退出时最后一连接干净关闭会使 SQLite 直接删除 `-wal` 文件（比 TRUNCATE 到 0 更彻底）；`app.Stop` 测试将"文件不存在"视为已截断。`Stop` 返回首个错误（shutdown 或 close），checkpoint 失败仅告警。
