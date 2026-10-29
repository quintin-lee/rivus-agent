# Round 18 设计：文档对齐补遗 + 会话搜索

日期：2026-10-29 ｜ 状态：已批准（设计 'ok' m2179）

## 1. 背景

R14 增加了 `GET /metrics/prometheus` 和 6 条条件注册的 pprof 路由，但当时未同步 README API 表——实测 README.md / README.en.md / docs/api/overview.md / docs/en/api/overview.md 中 `prometheus|pprof` 均为 0 提及。README 路由表现 22 行，而后端实际注册 23 条无条件路由（22 HandleFunc + 1 Handle）。

会话列表无搜索框；排序已存在（`ListSessions` 固定 `ORDER BY updated_at DESC`，按活跃度倒序），不新增排序 UI。

## 2. 批 1：文档对齐补遗（纯文档，无代码改动）

README.md 与 README.en.md API 表追加 7 行（`/version` 行之后）：

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/metrics/prometheus` | Prometheus 文本格式指标（与 `/metrics` 同数据，需认证） |
| GET | `/debug/pprof/` | pprof 索引（仅认证开启时注册） |
| GET | `/debug/pprof/cmdline` | pprof cmdline（仅认证开启时注册） |
| GET | `/debug/pprof/profile` | pprof CPU profile（仅认证开启时注册） |
| GET | `/debug/pprof/symbol` | pprof symbol（仅认证开启时注册） |
| GET | `/debug/pprof/trace` | pprof trace（仅认证开启时注册） |

注：`/debug/pprof/heap` 等命名 profile 由 `pprof.Index` 同一 handler 覆盖，不单列。

docs/api/system.md 与 docs/en/api/system.md：新增 `## GET /metrics/prometheus` 小节（curl 示例 + 与 /metrics 关系 + 需认证）与 pprof 说明段（条件注册语义：配置 `AGENT_AUTH_TOKEN` 时注册，未配置时 404）。

验收：`grep -cE '^\| (GET|POST|PUT|DELETE|PATCH) \|'` README.md / README.en.md = 28/28；四个文件 prometheus/pprof 提及 > 0。

## 3. 批 2：会话搜索

**排序结论**：不新增排序。`updated_at DESC` 已是合理默认，文档中说明。

### 3.1 后端

- `TaskRepo.ListSessions(ctx, ownerID, q string)`：q 经 `strings.TrimSpace`，非空时 SQL 追加 `AND LOWER(title) LIKE '%' || ? || '%'`（参数化，LOWER 双侧大小写不敏感）；空 q 与现行为字节等价。调用点同步传参。
- `handleListSessions`：读取 `q` query 参数透传。
- 集成测试：`?q=` 命中 / 不命中返回空数组 / 大小写不敏感 / 无 q 全量。

### 3.2 前端

- session-store：fetch sessions 的 action 增加可选 `q` 参数（拉取时拼 `?q=`）。
- `SessionList.tsx`：列表顶部搜索框（`Search` 图标 + input，placeholder i18n 键 `sessionlist.searchPlaceholder`：zh「搜索会话…」/ en `Search sessions…`），输入 200ms 防抖后触发带 q 重新拉取；清空恢复全量。
- i18n 字典补 `sessionlist.searchPlaceholder`（zh/en）。
- 测试：搜索框渲染 + 输入后（防抖后）以带 q 参数调用 fetch + 清空恢复。

## 4. YAGNI

不做：会话分页、按状态/时间排序切换、后端 LIKE 之外的 FTS、搜索历史、路由同步搜索词。

## 5. 风险

- LIKE 通配符注入（用户输入 `%`/`_`）：SQLite 参数化已防 SQL 注入；通配符语义注入仅影响匹配广度，不影响安全，不做转义（记录为已知行为）。
- pprof 行在 README 表中注明条件注册，避免用户在无认证部署时误判 404 为故障。

## 6. 验收清单

1. README.md / README.en.md 路由行数 28/28，与后端注册一致（23 无条件 + 5 条件）。
2. docs/api/system.md zh/en 有 /metrics/prometheus 小节与 pprof 说明。
3. `GET /api/v1/sessions?q=` 集成测试绿（命中/空结果/大小写/无 q）。
4. 前端 tsc 0 错误、npm run lint 通过、vitest 全绿（搜索框新测试）。
5. 后端 go build/vet/test 全绿、make test-eval 30 场景 pass_rate 1。
6. spec 本节回写执行偏差（如有）。

## 7. 执行偏差

- 计划期已知并声明：LIKE 通配符（`%`/`_`）不转义（参数化已防 SQL 注入，通配符仅影响匹配广度）；SessionList 刷新按钮保持当前过滤（`loadSessions(query || undefined)`）。
- 挂载首跑去重：SessionList 的 query-effect 挂载时会以 `undefined` 触发一次 loadSessions，与 `App.tsx` 已有的初始全量加载重复——用 `useRef(firstRun)` 跳过挂载首跑，仅 query 真正变化时拉取。
- store 接口类型同步：`loadSessions` 实现加可选参后，接口声明需同步为 `(q?: string) => Promise<void>`（tsc TS2554 抓到）。
- 计划中 store 单测表用例 `{"miss returns empty", "zzz", {}}` 缺类型标注，实际写为 `[]string{}`。
- Task 1 文档对齐零偏差：一次通过（28/28 行数，4 文件 prometheus/pprof 命中）。
