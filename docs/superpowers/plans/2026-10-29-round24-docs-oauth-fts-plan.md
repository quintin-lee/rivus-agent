# Round 24 实施计划：文档对齐 + MCP OAuth + FTS5

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修补 R23 搜索语义的文档漂移；为 MCP HTTP 传输加 OAuth client-credentials 令牌获取；为 runs 建立带迁移回填和写点同步的 FTS5 全文索引，并将 `q` 智能切换到 FTS。

**Architecture:** 批1 纯文档；批2 OAuth 逻辑封装在 httpTransport 内（ensureToken + 缓存 + 401 重试一次），API 层只透传且不回显 secret；批3 FTS5 虚拟表 + 应用层两写点同步 + term≥3 字节走 FTS 短语查询否则 LIKE 兜底。

**Tech Stack:** Go 1.27 + modernc.org/sqlite v1.60.1（FTS5 trigram 已探针验证）。无新依赖。

**Spec:** docs/superpowers/specs/2026-10-29-round24-docs-oauth-fts-design.md（7a40b3a）

---

## Chunk 1

### Task 1: 批1 文档对齐

**Files:** Modify `docs/api/runs.md` / `docs/en/api/runs.md`（q 行）/ `CHANGELOG.md`

- [ ] runs.md zh q 行 → 「按目标或结果（goal / result_json）大小写不敏感子串过滤，省略=不过滤」；en → `case-insensitive substring over goal or result_json`
- [ ] CHANGELOG「运行目标搜索」→「**运行搜索**：`GET /api/v1/runs?q=`（goal 或 result_json 大小写不敏感子串过滤）…」
- [ ] 验证：`grep -c result_json docs/api/runs.md docs/en/api/runs.md CHANGELOG.md` ≥1/1/1
- [ ] Commit `docs(api): run search matches goal or result (zh/en) + changelog`

### Task 2: OAuth 配置与校验（TDD）

**Files:** Modify `backend/internal/mcp/config.go`（OAuthConfig + ServerSpec.OAuth + ValidateSpecs）/ Test `config_test.go`

- [ ] RED：TestValidateSpecsOAuth — http+oauth 缺 token_url/client_id/client_secret 各报错；stdio+oauth 报错；合法通过
- [ ] 实现：`type OAuthConfig struct { TokenURL, ClientID, ClientSecret string; Scopes []string }` json tag snake_case；ValidateSpecs 在 http 分支后加 oauth 校验
- [ ] GREEN + commit `feat(mcp): OAuth client-credentials config and validation`

### Task 3: OAuth 传输令牌（TDD）

**Files:** Modify `backend/internal/mcp/http.go` / Test `http_oauth_test.go`（新建）

- [ ] RED：httptest token+resource 双端点 — (a) WriteLine 后资源端点收到 `Authorization: Bearer t1`；(b) expires_at 拨回 → 第二次请求刷新 t2；(c) 资源第一次 401 → 刷新重试成功；(d) 持续 401 → 报错含两次请求计数
- [ ] 实现：httpTransport += `oauthMu sync.Mutex; oauthToken string; oauthExpires time.Time`；`ensureToken(ctx)`：缓存有效（>30s 余量）直接用；否则 POST TokenURL（url.Values{grant_type,client_id,client_secret,scope}），401 状态或 error 字段 → 报错；解析 access_token/expires_in
- [ ] WriteLine 与 GET 流的 header 组装处：先 spec.Headers 拷贝，OAuth 存在时 `h["Authorization"] = "Bearer "+token` 覆盖
- [ ] 401 重试：WriteLine 收到 401 → 若 OAuth 配置存在 → 失效缓存 ensureToken → 重发一次；仍 401 → 返回原错误
- [ ] GREEN（含既有 10 个 http 测试不回归）+ commit `feat(mcp): OAuth bearer token acquisition with 401 retry`

### Task 4: OAuth API 透传（TDD）

**Files:** Modify `backend/internal/api/http/handlers_settings.go`（MCPServerIn/MCPServerInfo + 映射）/ Test `backend/tests/integration/`（追加 mcp oauth roundtrip）

- [ ] RED：PUT /settings/tools 带 mcp server oauth → SavedToolConfigs 记录含 oauth；GET 输出 oauth 仅含 token_url/client_id/scopes/has_secret=true，**不含 client_secret**
- [ ] 实现：MCPServerIn += `OAuth *mcp.OAuthConfig json:"oauth,omitempty"`；Info += `OAuth *mcp.OAuthInfo json:"oauth,omitempty"`（OAuthInfo{TokenURL,ClientID,Scopes,HasSecret bool}）；映射函数做裁剪
- [ ] GREEN + commit `feat(http): MCP server OAuth passthrough without secret echo`

## Chunk 2

### Task 5: FTS5 schema v4 + 同步（TDD）

**Files:** Modify `backend/internal/store/schema.sql`（runs_fts + 注释）/ `backend/internal/store/sqlite.go`（migrate v4 + 回填）/ `backend/internal/store/task_repo.go`（CreateRun/SetResult 同步）/ Test `task_repo_test.go` / `maintenance/migration` 测试放 task_repo_test.go

- [ ] RED：TestFTSBackfillOnMigrate — 手工建 v3 形态库（仅 runs 表 + 2 行含 result_json，schema_migrations version=3）→ Open → `SELECT count(*) FROM runs_fts WHERE runs_fts MATCH '"MARKER"'` ==1（存量 result_json 命中）；TestFTSSyncOnWrite — CreateRun 后 FTS 命中 goal；SetResult 后 FTS 命中新 result 且旧 text 不再命中
- [ ] 实现：schema.sql += runs_fts 虚拟表；sqlite.go migrate() 在 v3 块后加 v4 块（CREATE VIRTUAL TABLE IF NOT EXISTS + INSERT OR IGNORE INTO schema_migrations version 4；**回填仅在新建表时执行**——用 `INSERT INTO runs_fts(run_id, text) SELECT r.id, r.goal || ' ' || COALESCE(r.result_json,'') FROM runs r WHERE NOT EXISTS (SELECT 1 FROM runs_fts f WHERE f.run_id = r.id)`，幂等）；CreateRun 追加 INSERT INTO runs_fts(run_id, text) VALUES(?, ?)（goal）；SetResult 追加 DELETE+INSERT（goal+' '+result_json）
- [ ] GREEN + 全 store 回归 + commit `feat(store): FTS5 index for runs with migration backfill and write-point sync`

### Task 6: q 智能切换 FTS（TDD）

**Files:** Modify `backend/internal/store/task_repo.go`（两个 ListRuns q 块）/ Test 追加等价性用例

- [ ] RED：TestListRunsSearchFTSParity — 数据集（goal 'Alpha 任务'、result-only 'SECRET-报文'、含引号 goal `say "hi" 现在`、短词场景）：`?q=` 各 term 下 FTS 路径与 LIKE 路径行集相等；短词（'ab'）仍命中（LIKE 兜底）；CJK 短语 '任务' 命中
- [ ] 实现：提取 `runsFTSQuery(term string) (string, []any, bool)` — len(term)<3 → false；`SELECT run_id FROM runs_fts WHERE runs_fts MATCH ?` 短语参数 `"`+ReplaceAll(term,`"`,`""`)+`"`；两个 ListRuns 的 q 块改为：`if ids, args, ok := runsFTSQuery(term); ok { query += ' AND id IN (SELECT run_id FROM runs_fts WHERE runs_fts MATCH ?)'; args=append(args, 短语) } else { 原 LIKE 块 }`（保持 q 与局部变量重命名教训：外层 query 变量名沿用）
- [ ] GREEN（TestListRunsSearch 旧用例全绿 = 语义不回归）+ commit `feat(store): q search via FTS trigram phrase with LIKE fallback`

### Task 7: 全量验收 + 偏差回写 + wrap-up

- [ ] backend `go build ./... && go vet ./... && go test ./... -count=1 -timeout 600s && make test-eval`（34 pass_rate 1）
- [ ] frontend `npx tsc --noEmit && npm run lint && npx vitest run`（预期零改动全绿）
- [ ] spec §8 偏差回写 + commit
- [ ] wrap-up 报告（3 批表 + 证据 + 24 轮总览 + push note）

---

## 自查笔记（计划评审，无 subagent）

1. **FTS5 trigram 已探针验证**（/tmp/opencode/fts5check：MATCH 'alpha' 与 CJK '"pha 资"' 均命中）——短语内引号转义用双写。
2. **回填幂等**：NOT EXISTS 子查询使 migrate 重跑安全；v4 块只在 version<4 时执行。
3. **写点封闭性已核实**：runs 的 goal/result_json 仅 CreateRun(L182)/SetResult(L470) 两个写点；新增写点须同步 runs_fts（spec §6 风险已记录）。
4. **OAuth 令牌缓存需互斥**（SSE goroutine 与 rpc 并发调 WriteLine/ensureToken）——oauthMu 保护。
5. **GET settings/tools 永不回显 client_secret**（R12 headers 先例），Info 用 HasSecret bool。
6. **q 块历史教训**：ListRunsBySession/ByOwner 的 q 块历史上有局部变量 q 与参数 q 撞名问题（R21 rename query）；本次新增 runsFTSQuery 返回值命名避开 q。
7. 短语 MATCH 的 term 以字节计（len()），UTF-8 下 3 字节 ≈ 1 个 CJK 字符——'任务' 6 字节走 FTS，行为正确；纯 ASCII 2 字符 'ab' 走 LIKE。
