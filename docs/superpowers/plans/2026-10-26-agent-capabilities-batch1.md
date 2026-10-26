# Agent Capabilities 批1 · 工具集扩充 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan (本仓库约束：不使用 subagent)。Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增 file_list / file_search / file_edit / http_fetch 四工具，knowledge_search 桩换真（知识目录检索），目录与白名单配置化，修复 workspace_write 潜在 scopes 死锁。

**Architecture:** 全部走既有 `tool.Definition` / `CheckPolicy` / 统一网关，零新依赖。检索引擎（工作区与知识目录共用）为纯 Go 子串扫描。builtin 包以 `SetDirs`/`SetHTTPAllowlist` 装配钩子替代硬编码包级 var。

**Tech Stack:** Go 1.27、`testing`、`net/http/httptest`（测试用）。

**Spec:** `docs/superpowers/specs/2026-10-26-agent-capabilities-design.md`（§3 批1、§5 验收、§6 风险）

---

## 已核实事实（写作时直接引用，勿再推断）

- **`CheckPolicy`（tool/policy.go）**：`RequiredScopes` 非空时逐项要求 scopes 含该项或 `"*"`，否则 Deny `"missing scope X"`；`ApprovalAlways` 未批准→NeedApproval；`ApprovalConditional` 且 (RiskWrite||RiskExternal) 未批准→NeedApproval
- **gateway 从不传 scopes**：`runner_eino.go` 两处 `ExecRequest` 字面量均无 `Scopes` 字段（grep 零命中）→ **`workspace_write`（RequiredScopes:["workspace:write"]）运行时必被拒** = 潜在 bug（Task 8 修复）
- **`Definition`（tool/registry.go）**：`Register` 对 `TimeoutMs<=0` 填 30000、`MaxResultBytes<=0` 填 32*1024；`Handler = func(ctx, json.RawMessage) (string, error)`
- **executor.go**：policy→幂等缓存→allow 限流→超时→handler→per-tool 截断→全局 MaxOutputBytes 截断（缓存存截断后值）
- **builtin.go**：`workspaceRoot = "./workspace"` 包级 var；`cleanPath(p)` 拒绝空/`..`/绝对路径，Join 后 Rel 校验；`fileRead` MaxBytes 缺省 32KB 上限 64KB
- **config.go**：`Config` struct yaml tag + `Default()` + `applyEnv`（env 优先，`os.Getenv` 逐项）+ `Validate()`
- **app.go**：`builtin.RegisterAll(registry)` 在 L62（注入钩子须在此之前）；`a.cfg` 在 L66 起可用
- **审批测试模式**（tests/integration/approval_test.go）：`testutil.New(t, extraDef...)` 注入工具；`h.Model.SetFallback(func(int,[]*schema.Message))` 脚本化；`h.WaitTerminal(id,30s)` → `run.Status==domain.RunWaitingApproval`；`h.Approvals.ListByRun` 取记录；决定走 `h.API`/POST approvals（抄现有测试）
- **Harness**：`h.CreateRun(sessionID, payload, idemKey) (id, code, body)`；`fixturePayload(t, ses)` 从 fixtures/run_request.json 载入（react 模式）
- 子 agent 白名单：`resolveReadOnlyTools` 收集 `ApprovalNever && RiskRead` → file_list/file_search 自动进入子工具集（无需改动 subagent）

---

## Chunk 1: 配置与装配基座

### Task 1: config 三新项

**Files:**
- Modify: `backend/internal/config/config.go`
- Test: `backend/internal/config/config_test.go`（存在则追加，不存在则新建）

- [ ] **Step 1.1: 写失败测试**

```go
func TestWorkspaceAndFetchConfig(t *testing.T) {
	c := Default()
	if c.WorkspaceDir != "./workspace" || c.KnowledgeDir != "./knowledge" {
		t.Fatalf("defaults wrong: %q %q", c.WorkspaceDir, c.KnowledgeDir)
	}
	t.Setenv("AGENT_WORKSPACE_DIR", "/tmp/ws")
	t.Setenv("AGENT_KNOWLEDGE_DIR", "/tmp/kn")
	t.Setenv("AGENT_HTTP_FETCH_ALLOWLIST", "example.com, api.openai.com")
	c2, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c2.WorkspaceDir != "/tmp/ws" || c2.KnowledgeDir != "/tmp/kn" {
		t.Fatalf("env override failed: %+v", c2)
	}
	if len(c2.HTTPFetchAllowlist) != 2 || c2.HTTPFetchAllowlist[1] != "api.openai.com" {
		t.Fatalf("allowlist split failed: %v", c2.HTTPFetchAllowlist)
	}
}
```

- [ ] **Step 1.2: Run → FAIL**（`cd backend && go test ./internal/config/ -run TestWorkspaceAndFetchConfig`，未知字段编译错）

- [ ] **Step 1.3: 实现**：`Config` 加 `WorkspaceDir string \`yaml:"workspace_dir"\``、`KnowledgeDir string \`yaml:"knowledge_dir"\``、`HTTPFetchAllowlist []string \`yaml:"http_fetch_allowlist"\``；`Default()` 填 `./workspace`、`./knowledge`；`applyEnv` 加三项（`AGENT_HTTP_FETCH_ALLOWLIST` 用 `strings.Split(strings.ReplaceAll(v, " ", ""), ",")` 过滤空串；env 为空则不动）

- [ ] **Step 1.4: Run → PASS；Commit** `feat(config): workspace_dir, knowledge_dir, http_fetch_allowlist`

### Task 2: builtin 装配钩子

**Files:**
- Modify: `backend/internal/tool/builtin/builtin.go`
- Test: `backend/internal/tool/builtin/builtin_test.go`（新建）

- [ ] **Step 2.1: 写失败测试**：`SetDirs(t.TempDir(), t.TempDir())` 后 `workspaceWrite`/`fileRead` 落在 TempDir（写 `a.txt` 后 `fileRead` 能读回）；原包级 `workspaceRoot`/新 `knowledgeRoot`/`httpAllowlist` 均由钩子赋值

- [ ] **Step 2.2: Run → FAIL → 实现**：包级 vars `workspaceRoot, knowledgeRoot string` + `httpAllowlist []string`；`func SetDirs(workspace, knowledge string) { workspaceRoot, knowledgeRoot = workspace, knowledge }`；`func SetHTTPAllowlist(domains []string)`。默认值保持 `./workspace`、`./knowledge`（var 初始化）

- [ ] **Step 2.3: Run → PASS；Commit** `refactor(builtin): injectable workspace/knowledge dirs and http allowlist`

### Task 3: app.go 装配

**Files:**
- Modify: `backend/internal/app/app.go`（`RegisterAll` 之前加两行）

```go
builtin.SetDirs(cfg.WorkspaceDir, cfg.KnowledgeDir)
builtin.SetHTTPAllowlist(cfg.HTTPFetchAllowlist)
```

注意：L61-62 处 `registry := tool.NewRegistry()` 后、`RegisterAll` 前；`cfg` 已在 L66 定义——把 `cfg := a.cfg` 上移到注册前（或直接用 `a.cfg.X`）。

- [ ] **Step 3.1: build + 既有集成测试回归全绿；Commit**（并入 Task 2 commit 亦可，单独提交更清晰）`chore(app): wire workspace/knowledge dirs + http allowlist into builtin`

## Chunk 2: 四新工具 + knowledge_search 真实化

### Task 4: 检索引擎（共用）

**Files:**
- Create: `backend/internal/tool/builtin/search.go`
- Test: `backend/internal/tool/builtin/search_test.go`

- [ ] **Step 4.1: 写失败测试**：临时目录放 3 文件（其一含 NUL 字节=二进制跳过、其一超 `maxFileBytes` 跳过、其一正常）；`searchDir(root, "needle", 10)` 返回命中 `{path, line, snippet}`（相对路径、行号 1-based、snippet 含上下文）；`query==""` 报错

- [ ] **Step 4.2: Run → FAIL → 实现**：

```go
type searchHit struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Snippet string `json:"snippet"`
}

func searchDir(root, query string, maxResults int) []searchHit {
	// filepath.WalkDir；skip: 目录 .git、文件 >512KB、首 8KB 含 NUL
	// 逐行 strings.Contains；命中追加 {rel path, lineNo, trimSpace(line) 截 200 字节}
	// maxResults<=0 → 20；达到即停（不再继续 walk）
}
```

- [ ] **Step 4.3: Run → PASS；Commit** `feat(builtin): shared text search engine for workspace and knowledge dirs`

### Task 5: file_list + file_search 工具

**Files:**
- Modify: `backend/internal/tool/builtin/builtin.go`（defs 切片加两项；handler 就近放文件内）
- Test: `builtin_test.go` 追加

- [ ] **Step 5.1: 写失败测试**：`fileList{path:"",pattern:"*.txt"}` 列出 TempDir 递归匹配项（相对路径 JSON 数组）；`fileSearch{query:"needle"}` 返回 `{"hits":[searchHit]}`（复用 searchDir）；穿越路径被 `cleanPath` 拒

- [ ] **Step 5.2: Run → FAIL → 实现**：

```go
{Name: "file_list", Description: "列出工作区内目录条目（只读，可选 glob 递归）", Version: "v1",
	InputSchema: `{"type":"object","properties":{"path":{"type":"string"},"pattern":{"type":"string"}}}`,
	Risk: tool.RiskRead, Approval: tool.ApprovalNever, Idempotent: true, TimeoutMs: 10000, MaxResultBytes: 32 * 1024,
	Handler: fileList},
{Name: "file_search", Description: "在工作区文本文件中全文检索子串（只读，返回文件/行号/摘录）", Version: "v1",
	InputSchema: `{"type":"object","properties":{"query":{"type":"string"},"path":{"type":"string"},"max_results":{"type":"integer"}},"required":["query"]}`,
	Risk: tool.RiskRead, Approval: tool.ApprovalNever, Idempotent: true, TimeoutMs: 10000, MaxResultBytes: 32 * 1024,
	Handler: fileSearch},
```

- `fileList`：path 缺省 "."；非递归走 `os.ReadDir`，有 pattern 时 `filepath.WalkDir` + `filepath.Match(pattern, d.Name())`；输出 JSON `{"entries":[{name,is_dir,size}]}`（相对路径）
- `fileSearch`：path 可选限定子目录（cleanPath 校验）；`hits := searchDir(absRoot, query, maxResults)`

- [ ] **Step 5.3: Run → PASS；Commit** `feat(builtin): file_list and file_search read-only tools`

### Task 6: file_edit 工具（默认审批）

**Files:**
- Modify: `builtin.go`
- Test: `builtin_test.go`

- [ ] **Step 6.1: 写失败测试**：`fileEdit{path,old_text,new_text}` — old_text 恰好一次→替换成功返回 `{"path","bytes_changed"}`；出现 0 次或 ≥2 次→报错且文件未变；目标文件不存在→报错

- [ ] **Step 6.2: Run → FAIL → 实现**：

```go
{Name: "file_edit", Description: "对工作区内文件做精确唯一替换（受控写入，默认审批）", Version: "v1",
	InputSchema: `{"type":"object","properties":{"path":{"type":"string"},"old_text":{"type":"string"},"new_text":{"type":"string"}},"required":["path","old_text","new_text"]}`,
	Risk: tool.RiskWrite, Approval: tool.ApprovalConditional, Idempotent: false, TimeoutMs: 15000, MaxResultBytes: 4 * 1024,
	Handler: fileEdit},
```

- `fileEdit`：cleanPath → `os.ReadFile` → `strings.Count(old)==1` 校验（否则 `fmt.Errorf("old_text must occur exactly once (got %d)")`）→ `strings.Replace`（n=1）→ `os.WriteFile`；**不设 RequiredScopes**（gateway 不传 scopes，见事实节）

- [ ] **Step 6.3: Run → PASS；Commit** `feat(builtin): file_edit with exact-unique replacement, approval-gated`

### Task 7: http_fetch 工具（白名单 + 始终审批）

**Files:**
- Modify: `builtin.go`
- Test: `builtin_test.go`（httptest 本地服务器）

- [ ] **Step 7.1: 写失败测试**：`SetHTTPAllowlist([]string{"127.0.0.1"})`；httptest server 返回固定 body → `httpFetch{url}` 成功；改为未列域名（`example.com` 解析不了也无妨——**先校验 host 后请求**，直接拒绝）→ 报错不含请求痕迹；`max_bytes` 截断生效

- [ ] **Step 7.2: Run → FAIL → 实现**：

```go
{Name: "http_fetch", Description: "抓取白名单域名的 HTTP GET 页面/接口（外部副作用，始终需审批）", Version: "v1",
	InputSchema: `{"type":"object","properties":{"url":{"type":"string"},"max_bytes":{"type":"integer"}},"required":["url"]}`,
	Risk: tool.RiskExternal, Approval: tool.ApprovalAlways, Idempotent: false, TimeoutMs: 20000, MaxResultBytes: 64 * 1024,
	Handler: httpFetch},
```

- `httpFetch`：`url.Parse` → 仅 `http/https` → **host 白名单精确匹配**（`httpAllowlist` 为空 → 直接拒绝 `"http_fetch disabled: allowlist empty"`）→ `http.NewRequestWithContext(ctx, "GET", ...)` → 响应体读 `min(max_bytes|64KB, MaxResultBytes 余量)` → 输出 `{"status":200,"body":"..."}`；`http.Client{Timeout: 15s}`（ctx 已有超时兜底）

- [ ] **Step 7.3: Run → PASS；Commit** `feat(builtin): http_fetch GET-only with domain allowlist, approval always`

### Task 8: knowledge_search 桩换真 + workspace_write scopes bug 修复

**Files:**
- Modify: `builtin.go`
- Test: `builtin_test.go`

- [ ] **Step 8.1: 写失败测试**：(a) `SetDirs(_, knowledgeTmp)` 且 knowledgeTmp 放两文件含关键词 → `knowledgeSearch{query}` 返回真实 hits（结构 `{"query","hits":[{path,line,snippet}]}`，复用 searchDir）；空目录 → `{"hits":[]}`；(b) **workspace_write 无 RequiredScopes**：断言其 `Definition.RequiredScopes` 为空且经 `CheckPolicy(def, nil, false)` 判定 NeedApproval（而非 Deny）

- [ ] **Step 8.2: Run → FAIL → 实现**：`knowledgeSearch` 改用 `searchDir(knowledgeRoot, query, top_k 缺省 5)`；`workspace_write` 定义**删除 `RequiredScopes: []string{"workspace:write"}` 行**（保留 RiskWrite+ApprovalConditional 语义——审批仍在）

- [ ] **Step 8.3: Run → PASS + `go test ./internal/tool/...` 全绿；Commit** `fix(builtin): knowledge_search real implementation; drop unreachable RequiredScopes on workspace_write (gateway passes nil scopes)`

## Chunk 3: 集成、文档与验收

### Task 9: 集成测试（审批流 + 子 agent 继承）

**Files:**
- Create: `backend/tests/integration/builtin_tools_test.go`

- [ ] **Step 9.1: 写失败测试**：
  - `TestFileEditApprovalFlow`：`testutil.New(t)`（builtin 已由 harness 注册？**先验证**——若 harness 不注册 builtin，则注入 file_edit 定义副本）；模型脚本首轮调 `file_edit` → `WaitTerminal` 得 `waiting_approval` → approve → 二轮 fallback 返回终文本 → RunSucceeded
  - `TestFileSearchInChildWhitelist`：走 subagent 集成测试既有模式（参考 `tests/integration/subagent_test.go`），断言子任务能调用 `file_search`（子白名单自动含新只读工具）

- [ ] **Step 9.2: Run → RED（若 harness 未注册 builtin 则先补 harness 或注入）→ 实现（多为测试本身 + 必要的 testutil 微调）→ GREEN**

- [ ] **Step 9.3: `go test ./...` 全量回归；Commit** `test(integration): file_edit approval flow + file_search in child whitelist`

### Task 10: 文档同步

**Files:**
- Modify: `.env.example`（`AGENT_WORKSPACE_DIR`/`AGENT_KNOWLEDGE_DIR`/`AGENT_HTTP_FETCH_ALLOWLIST` 三行带注释）、`README.md` + `README.en.md` 配置表各 3 行、`backend/README.md` + `backend/README.en.md` 工具描述段更新（3→7 工具）

- [ ] **Step 10.1: 按上述清单改；Commit** `docs: workspace/knowledge/allowlist config rows; builtin tool inventory 3→7`

### Task 11: 验收（spec §5-1/2）

- [ ] **Step 11.1: 全量验证**

```bash
cd backend && go build ./... && go vet ./... && go test ./... -count=1 -timeout 600s
cd ../frontend && npx tsc --noEmit && npx vitest run
```

- [ ] **Step 11.2: DoD 断言**（spec §5-2）：file_edit 审批流集成测试通过；http_fetch 白名单外被拒（单测断言）；knowledge_search 真实命中（单测断言）；workspace_write 经 CheckPolicy(nil scopes) = NeedApproval 非 Deny（单测断言）；`git log --oneline` 每任务一 commit；`git status --short` 仅 `?? .omo/`

---

## 自审记录

| 检查项 | 结论 |
|---|---|
| 覆盖 spec 批1 全部要点（4 工具、knowledge 真实化、配置化、文档同步） | ✅ Task 4-10 |
| 发现并纳入计划外必修 bug：workspace_write RequiredScopes 在 gateway nil scopes 下必拒 | ✅ Task 8(b)（含 CheckPolicy 断言测试） |
| 无新依赖（检索纯 Go、fetch 用标准库） | ✅ |
| 测试先行：每 Task RED→GREEN→Commit | ✅ |
| 子 agent 白名单自动继承无需改动（resolveReadOnlyTools 按 ApprovalNever+RiskRead 收集） | ✅ 事实节 + Task 9 验证 |
| 已知风险：harness 是否注册 builtin 未核实 → Task 9.2 预案（注入定义副本或补 harness） | ⚠️ |
| 已知风险：file_edit 在 Eino 流式工具调用下 old_text 含转义差异 → 单测以 JSON 参数为准，不做启发式 | ⚠️ |
