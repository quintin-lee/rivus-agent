# Round 27 设计：task_repo 拆分 + FTS 文档 + evals 40

日期：2026-10-30 ｜ 状态：已批准（设计 'ok' m2916）

## 1. 背景

task_repo.go 629 行为全仓库最大文件（runs CRUD + sessions CRUD + FTS 助手 + 游标助手混居，R22 后 +150 行）。FTS5 全文索引（R24）在 backend README zh 与根 README zh/en 中 0 提及。evals 38。

## 2. 批 1：task_repo.go 按域拆分（纯重构，零行为）

同包同 receiver，方法跨文件合法，零 API 变化：

- `task_repo.go`（保留）：TaskRepo struct/NewTaskRepo、runs 域全部方法（CreateRun/GetRun/UpdateStatus/SetResult/ListRunsBySession/ListRunsByOwner）、EncodeCursor/DecodeCursor（runs+sessions 共用）、FTS 助手（ftsUpsert/runsFTSQuery）、nowMs/strOr/intOr 公共助手、schemaSQL 引用
- `session_repo.go`（新建，package store）：sessions 域全部方法（CreateSession/GetSession/ListSessions/RenameSession/DeleteSession 及级联辅助）
- 测试文件零改动（同包）；migration 留在原处

验收：go build/vet/test 全绿；两文件各 ≤400 行；`grep -c "func (r \*TaskRepo)"` 总数与拆分前一致。

## 3. 批 2：FTS5 文档补遗

- backend/README.md zh + README.en.md：运行列表搜索描述处 +1 句（FTS5 trigram 索引、≥3 字节短语匹配带引号转义、短词回退 LIKE、同 rowset）
- 根 README.md + README.en.md：features 列表在合适 bullet 并入「运行全文搜索（FTS5）」措辞
- 验收：grep -ci fts 四文件均 ≥1

## 4. 批 3：evals 38→40

- `TestScenarioMCPOAuthConfigRoundtrip`（tool，NewToolConfigHarness）：PUT /settings/tools mcpServers 含 type:http + oauth{token_url,client_id,client_secret} → 200；GET 回显 oauth {token_url,client_id,scopes?,has_secret:true} 且不含 client_secret
- `TestScenarioEventsAfterCursor`：run 终态后 GET /api/v1/runs/{id}/events 取最后 seq；?after=<seq> 仅返回更晚事件
- backend README zh/en 计数 38→40（工具级 26 + agent 级 14）+ 新文件名加入场景文件列表

## 5. YAGNI

不拆 FTS/游标到第三文件（两个文件已够聚焦）；不改任何方法签名；不新增 HTTP 行为；不改 docs/api/*（q/cursor 语义文档已齐）。

## 6. 风险

- 拆分时 import 集合需要按新文件重排（fmt/strings/encoding/base64 等归属）
- events after 语义以实测为准（若发现行为与预期不符，按实际断言并记录偏差）

## 7. 验收清单

1. store 拆分后 build/vet/test 全绿，文件行数达标。
2. FTS 文档 4 文件 grep ≥1。
3. evals 40/40 pass_rate 1。
4. frontend tsc 0 / lint 0 / vitest 全绿（零前端改动）。
5. spec 偏差回写。

## 8. 执行偏差

- 拆分首刀 kept-切片差一行（ListSessions 重复声明）→ `git checkout` 后修正为 `lines[:22] + lines[110:135] + lines[182:]`；session_repo.go 需 `fmt` import（DeleteSession 错误包裹）。
- OAuth roundtrip 场景：GET /settings/tools 回显的是 manager 实时状态而非 PUT 载荷（stub 与 R12 行为一致）→ PUT 载荷经 `h.SavedToolConfigs()` 断言（TokenURL/ClientID/ClientSecret 三字段），GET 仅断言 body 不含 client_secret。
- 事件端点返回 SSE 文本（`id:`/`event:`/`data:`）而非 JSON → 场景用 `sseIDs()` 解析 `id:` 行断言 after 语义；`?after=` query 参数确认受支持（parseAfter）。
- 计划期 itoa 辅助函数改为 `strconv.FormatInt`；NewHarness 签名实际为 `_ ...tool.Definition`（非自定义别名）。
- 批1（拆分）与批2（FTS 文档）除上述外零偏差；批3 计数 38→40（工具级 26 + agent 级 14）。
