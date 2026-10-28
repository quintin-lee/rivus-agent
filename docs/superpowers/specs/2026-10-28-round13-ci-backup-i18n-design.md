# Round 13 — CI 加固 + 运维备份/导出 + 前端 i18n 设计

日期：2026-10-28。三个批次的完整设计。批准记录：用户 '1+2+3 全套'（方向）+ 'ok'（设计三节）。

## 1. 背景与问题

1. CI 无并发控制：同一分支连续 push 会排队跑多个陈旧 workflow，浪费 runner 时长；release workflow 理论上可被两个 tag 同时触发并发创建。eval-report 产物无保留期限（默认 90 天，占额）。
2. 运维无备份手段：`modernc.org/sqlite` 文件库只增不减（WAL 已有 checkpoint），无任何备份/导出命令；出事故时只能裸拷贝 db 文件（非一致性快照风险）。
3. 前端全部 UI 文案硬编码中文（~30 处，12 个文件），无语言切换能力。

## 2. 三批结构

```
批1 CI 加固        .github/workflows/{ci,release}.yaml（纯 YAML，无代码）
批2 备份与导出     backend/internal/store/maintenance.go + cmd/agentd/main.go（子命令）+ tests
批3 前端 i18n     frontend/src/i18n/index.ts + store/ui-store.ts + components/Header.tsx + 少量组件接线
```

明确不做（YAGNI）：数据库逐表 CSV/SQL dump、HTTP 备份端点（避免新增鉴权面）、对象存储上传、react-i18next 依赖、路由级语言切换、日期本地化（relTime 保持现状）、S3/远端同步、CI matrix 扩展。

## 3. 批次细节

### 批1 CI 加固（2 个 YAML）

- `ci.yml` 顶层新增：
  ```yaml
  concurrency:
    group: ci-${{ github.ref }}
    cancel-in-progress: true
  ```
  按 ref 隔离，新 push 取消同 ref 陈旧运行（PR 与分支互不干扰）。
- `release.yml` 顶层新增：
  ```yaml
  concurrency:
    group: release
    cancel-in-progress: false
  ```
  全局单例，排队而非取消 —— 发布不可中断。
- ci.yml evals job 的 `Upload eval report` step 增加 `retention-days: 14`。
- 验证：`python3 yaml.safe_load` 语法 + grep 断言 3 个字段存在。

### 批2 备份与导出（backend，TDD）

- `store/maintenance.go` 新增 `Backup(db *sql.DB, destPath string) error`：
  - SQL：`VACUUM INTO ?`（参数化，SQLite 3.27+；modernc v1.60.1 支持）——在线一致性快照，无需停写。
  - 目标已存在 → 返回错误（不覆盖）；源为 `:memory:` → 显式 "unsupported" 错误（VACUUM INTO 对内存库无意义且路径语义混乱）。
  - 复用既有 `dbFilePath` 判定。
- `cmd/agentd` 新增 `export` 子命令：`agentd [-config x] export -session <id> [-o out.json]`。
  - `main.go` 改为 `os.Args[1]` 分发：`"export"` 走 `runExport(args)`，其余保持现状（缺省启动 server）。
  - `runExport`：`flag.NewFlagSet` 解析 `-session`/`-o`；`config.Load` + `store.Open`（只读用途，不开 server）；导出该 session 的 runs（含子 run）、events、memories 为一个 JSON 对象输出到 stdout 或 `-o` 文件；session 不存在 → 非零退出码 + 错误信息。owner 校验用默认租户约定（CLI 是运维面，等同 root）。
  - 复用 TaskRepo 既有查询：`ListRunsBySession`、Events `ListAfter`（limit 大值拉全量）、MemoryRepo `Latest`（limit 大值）。
  - owner 发现：CLI 先查 `SELECT owner_id FROM sessions WHERE id = ?`（一条只读 SQL，属运维面），拿到 owner 后走 `GetSession` 校验存在性，再取数据；session 不存在 → 非零退出码。
- 测试：Backup 往返（备份文件重新 Open → 同数据可查）；export 集成测试（夹具 session → JSON 字段断言 runs/events/memories 齐全 + 空 session 报错）。

### 批3 前端 i18n（最小机制，TDD）

- 新建 `frontend/src/i18n/index.ts`：
  ```ts
  type Lang = 'zh' | 'en';
  const dict: Record<Lang, Record<string, string>> = { zh: {...}, en: {...} };
  export function t(key: string, params?: Record<string, string | number>): string
  ```
  - `t` 从 ui-store 读当前 lang；`{name}` 占位符简单替换；缺 key 回退 zh 再回退 key 本身。
- `ui-store.ts` 增 `lang: 'zh' | 'en'`（默认 zh，localStorage `rivus-lang`，`toggleLang()`；与 round-11 theme 同模式）。
- `Header.tsx` 在 ThemeToggle 旁加 中/EN 切换按钮（aria-label `切换语言`）。
- 迁移文案：**只迁移本批 touched 组件 + 2 个 lib 文件作为样本**（SessionList、MemoriesDialog、RunsHistoryDialog、Header + status.ts），其余硬编码文案保持现状（一次性全量迁移无对应行为收益，后续批次增量迁移）。
- 测试：store 切换/持久化；Header 按钮；一个消费 `t()` 的组件样本。

## 4. 双语与文档

- README zh/en：批2 增 `## 备份与导出`（VACUUM INTO 快照 + export 命令示例）；批3 增 i18n 说明一句。
- spec 偏差回写：执行中发现的与设计不符处记入本文件（前例：round-8/9/12 均有）。

## 5. 交付顺序

批1（10 分钟）→ 批2（backend TDD）→ 批3（frontend TDD）→ 全量验收 → 文档 → wrap-up。每批独立 commit；过程遵循既有约束（无 subagent、TDD RED→GREEN、直接 master）。

## 6. 验收标准

1. ci.yml/release.yml 含 concurrency 组且 YAML 合法；eval-report 有 retention-days。
2. `go test ./internal/store/` 含 Backup 往返测试绿；export 集成测试绿。
3. `agentd export -session X -o f.json` 真实二进制运行产出合法 JSON（make build 后实测）。
4. 前端 `tsc --noEmit` 0 错、`npm run lint` 绿、vitest 全绿（新增 i18n/store/Header 测试）；语言切换持久化生效。
5. `go build/vet/test` + `make test-eval` 全绿（后端零回归）。
6. README zh/en 双语同步；spec 偏差已回写。

## 7. 风险

- `VACUUM INTO` 与活动写入并发：SQLite 保证快照一致性（读事务），风险低；测试用单连接即可。
- main.go 分发改动影响现有启动路径：`os.Args[1]` 非 "export" 时走原逻辑，兼容 `-config` 位置参数习惯（flag.Parse 原样执行）。
- i18n 字典与组件文案漂移：本批仅迁移样本文件，字典键以 touched 文件为准，未迁移文案不受影响。

## 8. 执行偏差回写

### 批2

- `VACUUM INTO ?` 参数绑定在 modernc.org/sqlite v1.60.1 下可用（无需 DSN 回退方案）。
- export 空会话时 runs/events/memories 序列化为 `[]` 而非 `null`（nil slice JSON 语义修正，commit 918e533）。
- 真机冒烟确认：export 必须继承 AGENT_DB_PATH 环境变量（默认 cfg.DBPath 与 serve 不同库）；创建会话 API 响应键为 `session_id`。
- golangci errcheck 要求 os.Stdout.Write 错误显式处理（随 docs 提交 fd8c17d 修复）。

### 批3

- `t()` 通过 `useUiStore.getState().lang` 读取语言（单一事实源，不用模块级变量）；组件以 `useUiStore((s) => s.lang)` 裸订阅触发重渲染（赋值未使用会被 lint 拒绝）。
- `{name}` 占位用 `split/join` 实现：TS lib 目标低于 es2021，`String.replaceAll` 类型报错（TS2550）。
- 原为英文的 SessionList 文案（Sessions/New session title…/No runs 等）迁入词典后默认语言变为中文（默认 lang=zh 的一致性结果）；SessionList 测试断言同步更新为新默认文案。
- 字典补充键：`sessionlist.loadRunsFailed`、`common.deleteFailed`（迁移过程中确认需要）。
- status.ts 仅迁移 `budget.consumed` 两行（eventSummary 的调用方按自身节奏重渲染，zh 输出字节级不变）；RunsHistoryDialog 的 statusLabel 映射改为 `status.*` 词典键 + 未知状态回退原值。
