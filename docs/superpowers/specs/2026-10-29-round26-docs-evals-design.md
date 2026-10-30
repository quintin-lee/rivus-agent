# Round 26 设计：文档三处补遗 + evals 38

日期：2026-10-29 ｜ 状态：已批准（设计 'ok' m2880）

## 1. 背景

R25（复制按钮、聊天空状态、ErrInvalidToolConfig 400 语义修复）未进 CHANGELOG（该文件停留在 v0.1.0 release 提交）。docs/api/overview.md 有「分页」小节但无「搜索」小节——q 参数语义历经 R18（会话标题）→R21（run 目标）→R23（+result_json）→R24（FTS5 trigram）演进，从未在 overview 汇总。根 README zh/en 的 MCP 特性行未提 R24 OAuth。

范围决定：全套 = 文档补遗 + evals 扩展；MCP auth-code 授权码流程维持 R24 spec 的 YAGNI 决定（仅 client_credentials）。

## 2. 批 1：文档三处补遗（纯文档）

1. **CHANGELOG.md**：`[0.1.0]` 之上新开 `## [Unreleased]`：
   - Added：工具输出与运行 ID 一键复制按钮；聊天空状态支持中英文并直达新建运行
   - Fixed：无效 MCP 工具配置由 500 改为 400（`invalid tool config`）
2. **docs/api/overview.md + docs/en/api/overview.md**：新增 `## 搜索` / `## Search` 小节：
   - sessions：`q` 按标题大小写不敏感子串过滤
   - runs：`q` 同时匹配目标（goal）与结果（result_json）；词长 ≥3 字节走 FTS5 全文短语匹配（trigram，引号自动转义），更短词回退 LIKE
   - 一句话指向 settings.md 的 MCP OAuth 说明
3. **根 README.md + README.en.md**：MCP 特性行 += OAuth client-credentials（token 自动获取与 401 刷新重试；`client_secret` 仅写入、永不回显）。

## 3. 批 2：evals 36→38

两个此前只有单测/集成测试、无端到端评估的缺口：

- `TestScenarioSessionTitleSearch`（工具级）：PostCheck 建 2 个会话，`GET /api/v1/sessions?q=` 命中目标、不含其他，无 q 返回全量。
- `TestScenarioCursorPagination`（工具级）：PostCheck 建 3 个会话（相邻创建 sleep 2ms 保证 updated_at 可分），`GET /api/v1/sessions?limit=2` 返回 2 行 + 非空 `next_cursor`；携 cursor 请求第 2 页返回剩余 1 行；`cursor=!!!bad` → 400。

README 评估计数 36→38（工具级 24 + agent 级 14），场景文件列表 += scenarios_round26_test.go（zh/en 两处）。

## 4. YAGNI

不做：MCP auth-code 授权码流程、overview 里贴完整 curl（指向各端点文档）、FTS5 FTS 辅助表运维接口。

## 5. 验收清单

1. CHANGELOG 有 [Unreleased] 三条目；overview zh/en 有搜索小节（FTS5 提及）；根 README zh/en OAuth 提及。
2. `make test-eval` 38 场景 pass_rate 1；README 计数两处一致。
3. 后端 go build/vet/test 全绿；前端 tsc/lint/vitest 回归绿（零改动）。
4. spec 本节回写执行偏差。

## 6. 执行偏差

- 根 README「特性」列表原本没有任何 MCP 条目（设计假设存在「MCP 特性行」不成立）——改为新增整条「MCP 集成」（stdio/HTTP、tools/list_changed 热刷新、OAuth client-credentials），OAuth 随条目自然提及。
- overview.md「分页」小节末句过时（宣称前端按 offset 递增——R22 已切换游标分页）：顺带改写为 next_cursor/cursor 语义，并并入搜索小节前的分页段。
- eval 两个场景为 PostCheck 断言型（R19/R21 先例），直接 GREEN 属预期，不设人为 RED；场景签名以 scenarios_round25_test.go 实测为准（Script 返回幂等键字符串、PostCheck 四参）。
- 其余零偏差：CHANGELOG/搜索小节/README 计数均一次通过。
