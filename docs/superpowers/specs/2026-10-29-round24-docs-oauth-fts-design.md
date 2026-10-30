# Round 24 设计：文档对齐 + MCP OAuth + FTS5 全文索引

日期：2026-10-29 ｜ 状态：已批准（设计 'ok' m2696）

## 1. 背景

R23 将 run 搜索语义扩展为 goal OR result_json，但 docs/api/runs.md（zh/en）q 参数行与 CHANGELOG 条目仍停留在旧语义（实测确认）。

MCP HTTP 传输目前仅支持静态 Headers（R12），无动态令牌获取能力。SQLite 全文检索（FTS5 + trigram 分词）在 modernc v1.60.1 上实测可用（CJK 子串 MATCH 命中），当前 `q` 用 LIKE 全表扫描，规模增长后有性能上限。

## 2. 批 1：文档对齐（纯文档）

- docs/api/runs.md + docs/en/api/runs.md：`q` 参数行改为「按目标或结果（goal / result_json）大小写不敏感子串过滤，省略=不过滤」。
- CHANGELOG「运行目标搜索」条目：改为「运行搜索（goal 或 result_json）」并注明 R23 语义。

验收：两文件不再出现仅 goal 的旧语义描述；CHANGELOG 有 result_json 字样。

## 3. 批 2：MCP OAuth（client-credentials）

后端为服务端场景（无浏览器交互），仅实现 **client_credentials** 授权类型；授权码 + PKCE 流不做（明确记录）。

### 3.1 配置

`ServerSpec` += `OAuth *OAuthConfig`：

```go
type OAuthConfig struct {
    TokenURL     string   `json:"token_url"`
    ClientID     string   `json:"client_id"`
    ClientSecret string   `json:"client_secret"`
    Scopes       []string `json:"scopes,omitempty"`
}
```

校验（ValidateSpecs 扩展）：type=http 且 OAuth 非 nil → token_url/client_id/client_secret 必填；type=stdio 携带 oauth → 拒绝。

### 3.2 传输行为（http.go）

- 内存令牌缓存 `{token string, expiresAt time.Time}`（transport 实例级）。
- WriteLine / GET 流请求前 `ensureToken(ctx)`：无缓存或 `expiresAt` 距现在 <30s → POST TokenURL（form：grant_type=client_credentials、client_id、client_secret、scope=空格连接；沿用 Headers 中的附加头）；解析 `{"access_token":"...","expires_in":3600}`。
- 注入：`Authorization: Bearer <token>` 覆盖 Headers 中同名键（显式配置优先级：OAuth 令牌 > 静态头）。
- 401 处理：POST 收到 401 → 失效缓存重取一次再试；再 401 → 返回原错误（防循环）。

### 3.3 API 透传

- MCPServerIn / MCPServerInfo += `oauth`（In 接收；Info 输出 **永不回显 client_secret**，沿 R12 headers 不回显先例——只回显 token_url/client_id/scopes 与 has_secret 布尔）。
- settings 落库透传（SpecTransportFactory 分发不变——OAuth 逻辑在 httpTransport 内部）。

### 3.4 测试

httptest 双端点：token 端点断言 grant_type/client_id/client_secret/scope 并发 token；资源端点断言 `Authorization: Bearer t1`。用例：注入成功、过期刷新（expires_at 手动拨回）、401 → 刷新 → 重试成功、401 ×2 → 报错、缺字段校验 400。

## 4. 批 3：FTS5 全文索引（runs）

### 4.1 Schema v4

```sql
CREATE VIRTUAL TABLE IF NOT EXISTS runs_fts USING fts5(text, run_id UNINDEXED, tokenize='trigram');
```

migrate v3→v4：建表 + 一次性回填 `INSERT INTO runs_fts(run_id, text) SELECT id, goal || ' ' || COALESCE(result_json, '') FROM runs`。schemaSQL 用 IF NOT EXISTS（新库直接建，老库迁移补建）。schema_migrations 插 version 4。

### 4.2 同步（应用层，写点已核实仅两处）

- `CreateRun`：INSERT INTO runs_fts(run_id, text) VALUES(id, goal)。
- `SetResult`：DELETE FROM runs_fts WHERE run_id=? 后 INSERT(id, goal || ' ' || result_json)。
- 不用触发器（写点封闭、行为显式可测；spec 记录该不变量与风险：新增 runs 写点须同步）。

### 4.3 查询智能切换

`q` 对外语义不变（大小写不敏感子串）。实现：

- term（lower+trim）**字节数 ≥3** → FTS 短语查询：`SELECT run_id FROM runs_fts WHERE runs_fts MATCH ?`，短语参数 `"` + ReplaceAll(term, `"`, `""`) + `"`；外层 `WHERE id IN (...)` 或 JOIN。
- 否则（空/过短）→ 现有 LIKE 路径字节不变。
- 两条路径必须返回相同行集（同一匹配集，顺序都由外层 ORDER BY 决定）；测试断言等价（含 CJK、大小写、含引号 term、短词回退）。

### 4.4 测试

- 迁移回填：v3 老库（手工建 runs+数据）→ Open 升级 → FTS 命中存量 result_json。
- 等价性：同一数据集 FTS 与 LIKE 结果集相等（goal-only、result-only、CJK、含 `"` term）。
- 短词回退：2 字节 term 走 LIKE（行为不变）。
- SetResult 后再次搜索命中新 result。

## 5. YAGNI

无授权码/PKCE/OAuth 发现文档（/.well-known）、无 token 持久化与并发去重（单 transport 单 goroutine 串行）、无 FTS 排序/snippet/highlight、无 sessions FTS、无 FTS5 contentless/external-content 高级配置。

## 6. 风险

- FTS 短语查询转义：term 内 `"` 双写转义；其余 MATCH 语法字符在引号短语内为字面量（探针已验证）。
- trigram 最小 3 字节限制 → 短词 LIKE 兜底覆盖。
- OAuth 401 重试仅一次；token 获取失败返回原错误并附上下文。
- FTS 与 runs 一致性依赖应用层同步——新增 runs 写点时必须同步（spec 显式记录）。
- LIKE 语义（通配符 %/_ 字面处理）与 FTS 语义在极端 term 下可能有细微差异——测试等价性用常规 term 集。

## 7. 验收清单

1. runs.md zh/en q 语义为 goal/result_json；CHANGELOG 有 result_json。
2. OAuth：注入/刷新/401 重试/校验测试绿；GET /settings/tools 不回显 client_secret。
3. FTS：迁移回填、FTS/LIKE 等价、短词回退、SetResult 同步测试绿。
4. 后端 go build/vet/test 全绿、make test-eval 34 pass_rate 1。
5. 前端 tsc 0、lint 0、vitest 全绿（本批前端仅 i18n 文案不动则零漂移）。
6. spec 本节回写执行偏差。

## 8. 执行偏差

（执行后回写）
