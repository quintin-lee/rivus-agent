# Round-9 批2 实现计划：Evals 场景扩展 15→20

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan (user constraint: NO subagents). Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为评估套件新增 5 个场景（settings 持久化 / 子代理预算钳制 / http_fetch 白名单命中 / knowledge 未命中 / file_list 目录），使 `make test-eval` 达到 20 场景 pass_rate 1。

**Architecture:** 全部场景复用既有 evals 框架钩子（Script/MidRun/PostCheck/NewHarness），零框架改动。新文件 scenarios_more_test.go，一个 `Run(t, Scenario{})` 一个场景，自动计入 eval-report.json。

**Tech Stack:** Go 1.27 + testing + testutil harness（ScriptedModel + Do()）+ builtin.SetHTTPAllowlist/SetDirs 注入。

**Spec:** docs/superpowers/specs/2026-10-27-round9-design.md §3.2（批2）

---

## Chunk 1

### Task 1: scenarios_more_test.go — 5 个新场景

**Files:**
- Create: `backend/tests/evals/scenarios_more_test.go`

**Verified facts（plan 阶段已核实）:**
- `clampChildBudget`（internal/subagent/subagent.go:194）：子预算=父×0.40 再封顶（tool cap 30）。父 MaxToolCalls=10 → 子 = 4。服务端 ClampByServer 默认 max_tool_calls=50，10 原样通过。
- `TaskRepo.ListChildrenByRun` 返回 `[]domain.Run`（含 Budget 字段，DB 行完整扫描）。
- `searchDir` 无命中 → `hits` 为 nil → 输出 `{"query":...,"hits":null}`，run 正常继续。
- `file_list` 输出 `{"entries":[{name,is_dir,size}...]}`。
- `http_fetch`：allowlist 按 host EqualFold 匹配（`u.Host`，含端口），匹配发生在请求之前；external/always → 先审批再执行。
- `httpapi.ToolConfigIn{SkillsEnabled bool json:"skillsEnabled"; MCPEnabled bool json:"mcpEnabled"; MCPServers []MCPServerIn json:"mcpServers"}`；GET /settings/tools 返回 `{"skills":{"enabled":...,"available":[...]}, "mcp":{...}}`。
- `Harness.SavedToolConfigs() []httpapi.ToolConfigIn`。
- `NewToolConfigHarness(t testutil.TB)` 同时注册全部 builtin + invoke_skill。

- [ ] **Step 1: 写 5 个场景（新文件）**

```go
package evals

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/tool"
	"rivus-agent-backend/tests/testutil"
)

// allowHTTPFetch 注入白名单（仅本文件使用），结束时恢复禁用态。
func allowHTTPFetch(t *testing.T, hosts []string) {
	t.Helper()
	builtin.SetHTTPAllowlist(hosts)
	t.Cleanup(func() { builtin.SetHTTPAllowlist(nil) })
}
```

> 注：`builtin` 需要import `"rivus-agent-backend/internal/tool/builtin"`。

**场景 16 settings_toggle_persists**（不依赖工具执行，但走一次 run 以计入报告）：

```go
// 16) settings/tools 开关持久化：PUT → 存根记录 → GET 读回
func TestScenarioSettingsTogglePersists(t *testing.T) {
	Run(t, Scenario{
		Name:            "settings_toggle_persists",
		Category:        "tool",
		Goal:            "关闭技能开关",
		SuccessCriteria: []string{"已关闭"},
		NewHarness: func(t testutil.TB, _ ...tool.Definition) *testutil.Harness {
			return testutil.NewToolConfigHarness(t)
		},
		Script: func(h *testutil.Harness) string {
			code, body := h.Do("PUT", "/api/v1/settings/tools",
				map[string]any{"skillsEnabled": false, "mcpEnabled": false, "mcpServers": []any{}}, nil)
			if code != 200 {
				t.Fatalf("PUT settings/tools: %d %s", code, body)
			}
			h.Model.EnqueueText("已关闭")
			return "eval-settings"
		},
		Want: Expect{TerminalStatus: domain.RunSucceeded, MaxModelCalls: 1},
		PostCheck: func(t *testing.T, h *testutil.Harness, _ string, _ domain.Run) {
			saved := h.SavedToolConfigs()
			if len(saved) == 0 || saved[len(saved)-1].SkillsEnabled {
				t.Fatalf("saved configs=%v, want last skillsEnabled=false", saved)
			}
			code, body := h.Do("GET", "/api/v1/settings/tools", nil, nil)
			if code != 200 {
				t.Fatalf("GET settings/tools: %d %s", code, body)
			}
			var got struct {
				Skills struct {
					Enabled bool `json:"enabled"`
				} `json:"skills"`
			}
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("parse: %v %s", err, body)
			}
			if got.Skills.Enabled {
				t.Fatalf("skills.enabled=true, want false; body=%s", body)
			}
		},
	})
}
```

**场景 17 subagent_child_budget_clamped**：

```go
// 17) 子代理预算钳制：父 max_tool_calls=10 → 子 = 10×40% = 4
func TestScenarioSubagentChildBudgetClamped(t *testing.T) {
	Run(t, Scenario{
		Name:     "subagent_child_budget_clamped",
		Category: "agent",
		Goal:     "委派一个子任务",
		Budget:   map[string]any{"max_tool_calls": 10},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc-sub", "tasks", `{"tasks":[{"goal":"g1"}]}`)
			h.Model.EnqueueText("g1 完成")  // child
			h.Model.EnqueueText("子任务完成") // parent
			return "eval-subagent-budget"
		},
		Want: Expect{
			TerminalStatus: domain.RunSucceeded,
			RequiredEvents: []domain.EventType{domain.EvtToolCompleted},
			MaxModelCalls:  3, MaxToolCalls: 1,
		},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, _ domain.Run) {
			children, err := h.Tasks.ListChildrenByRun(context.Background(), runID)
			if err != nil || len(children) != 1 {
				t.Fatalf("children: %v len=%d", err, len(children))
			}
			if got := children[0].Budget.MaxToolCalls; got != 4 {
				t.Fatalf("child max_tool_calls=%d want 4", got)
			}
		},
	})
}
```

**场景 18 http_fetch_allowlist_hit**：

```go
// 18) http_fetch 白名单命中：批准后真实请求到 httptest 服务器
func TestScenarioHTTPFetchAllowlistHit(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("pong"))
	}))
	t.Cleanup(srv.Close)
	allowHTTPFetch(t, []string{mustHost(t, srv.URL)})
	Run(t, Scenario{
		Name:            "http_fetch_allowlist_hit",
		Category:        "tool",
		Goal:            "抓取白名单页面",
		SuccessCriteria: []string{"抓取"},
		Script: func(h *testutil.Harness) string {
			h.Model.SetFallback(reissueToolThenText("tc-hfh", "http_fetch",
				`{"url":"`+srv.URL+`"}`, "抓取完成"))
			return "eval-http-hit"
		},
		Want: Expect{
			TerminalStatus: domain.RunSucceeded,
			RequiredEvents: []domain.EventType{domain.EvtToolCompleted},
		},
		MidRun: approveFirst, // 见下：与 deny 场景相同的批准逻辑抽公共函数
		PostCheck: func(t *testing.T, _ *testutil.Harness, _ string, _ domain.Run) {
			if hits.Load() == 0 {
				t.Fatal("httptest server never hit")
			}
		},
	})
}
```

**公共批准函数**（deny 场景已有内联版；新文件内抽一个，不改旧文件）：

```go
func approveFirst(t *testing.T, h *testutil.Harness, runID string) {
	t.Helper()
	aID := firstApprovalID(t, h, runID)
	code, body := h.Do("POST", "/api/v1/runs/"+runID+"/approvals/"+aID,
		map[string]any{"approve": true, "reason": "eval"}, nil)
	if code != 200 {
		t.Fatalf("approve: %d %s", code, body)
	}
}
```

**mustHost**：

```go
func mustHost(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse %q: %v", rawURL, err)
	}
	return u.Host
}
```

（import 增加 `net/url`、`encoding/json`、`internal/tool/builtin`。）

**场景 19 knowledge_search_miss**：

```go
// 19) knowledge_search 空知识库未命中：工具正常返回，run 成功
func TestScenarioKnowledgeSearchMiss(t *testing.T) {
	withTempKnowledge(t) // 空目录
	Run(t, Scenario{
		Name:            "knowledge_search_miss",
		Category:        "tool",
		Goal:            "检索不存在的词条",
		SuccessCriteria: []string{"未找到"},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc-km", "knowledge_search", `{"query":"zzz-not-there"}`)
			h.Model.EnqueueText("知识库中未找到相关内容")
			return "eval-knowledge-miss"
		},
		Want: Expect{
			TerminalStatus: domain.RunSucceeded,
			RequiredEvents: []domain.EventType{domain.EvtToolCompleted},
			MaxModelCalls:  2, MaxToolCalls: 1,
		},
	})
}
```

**场景 20 file_list_directory**：

```go
// 20) file_list 列目录：返回 entries 且 run 成功
func TestScenarioFileListDirectory(t *testing.T) {
	ws := withTempWorkspace(t)
	if err := writeFileRel(ws, "a.txt", "one"); err != nil {
		t.Fatal(err)
	}
	if err := writeFileRel(ws, "b.txt", "two"); err != nil {
		t.Fatal(err)
	}
	Run(t, Scenario{
		Name:            "file_list_directory",
		Category:        "tool",
		Goal:            "列出工作区文件",
		SuccessCriteria: []string{"已列出"},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc-fl", "file_list", `{"path":"."}`)
			h.Model.EnqueueText("已列出工作区条目")
			return "eval-file-list"
		},
		Want: Expect{
			TerminalStatus: domain.RunSucceeded,
			RequiredEvents: []domain.EventType{domain.EvtToolCompleted},
			MaxModelCalls:  2, MaxToolCalls: 1,
		},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, _ domain.Run) {
			// tool.completed 载荷是摘要；存在性 + 终态已足够。额外断言文件仍在。
			if _, err := os.Stat(filepath.Join(ws, "a.txt")); err != nil {
				t.Fatalf("a.txt missing: %v", err)
			}
		},
	})
}
```

（import 增加 `os`、`path/filepath`。）

- [ ] **Step 2: 运行验证（RED→GREEN 语义：场景即可执行规格，先跑再修差异）**

```bash
cd backend && go test ./tests/evals/ -count=1 -run 'TestScenarioSettings|TestScenarioSubagentChild|TestScenarioHTTPFetchAllowlistHit|TestScenarioKnowledgeSearchMiss|TestScenarioFileList' -v
```

Expected: 5 个新测试 PASS。若 FAIL：先核对是场景断言错还是产品缺陷（该行为应已存在，若产品缺陷则停下报告）。

- [ ] **Step 3: 全量 evals + 回归**

```bash
cd backend && make test-eval && go test ./... -count=1 -timeout 600s
```

Expected: 20 scenarios, pass_rate 1；全包 ok。

- [ ] **Step 4: Commit**

```bash
git add backend/tests/evals/scenarios_more_test.go
git commit -m "test(evals): five more scenarios — settings persist, child budget clamp, http allowlist hit, knowledge miss, file_list"
```

### Task 2: README 场景计数同步 + 批2 验收

**Files:**
- Modify: `backend/README.md`（## 评估体系（Evals）小节：15 → 20，补一句新覆盖面）
- Modify: `backend/README.en.md`（## Evals 同步）

- [ ] **Step 1: 更新两处 README 计数与覆盖面句子（zh：15 个 → 20 个场景，新增 settings/子预算/白名单命中/未命中/目录列举；en 同步）**
- [ ] **Step 2: 验证**

```bash
cd backend && go build ./... && go vet ./... && grep -c "20" README.md README.en.md
```

- [ ] **Step 3: Commit**

```bash
git add backend/README.md backend/README.en.md
git commit -m "docs(evals): scenario count 15 -> 20 (zh/en)"
```

---

## Self-review notes

1. **场景 16 需要完整 PUT 体**：mcpServers 传 `[]any{}` 避免 nil 歧义；ToolConfigIn 三个字段全部显式。
2. **场景 17 Budget 键用 snake_case**（`max_tool_calls`）——framework Run() 直接把 `s.Budget` 塞进 payload，服务端 json 解析走 domain Budget tag。✓
3. **场景 18 allowlist 匹配含端口**（`u.Host`），与 builtin 实现 EqualFold(host) 一致；httptest 每次随机端口，不能写死。
4. **approveFirst 抽取**不改 scenarios_tool_test.go 旧代码（避免无谓 diff）。
5. **场景 19 `hits:null`**：json.Marshal(nil slice) → null，工具不报错，run 成功——这正是未命中语义。
6. **报告计数**：5 个场景全走 `Run()`，自动进 eval-report.json（TestMain）。
7. **风险**：场景 16 依赖 ToolConfigHarness 的 settings provider 存根行为（round-4 已验证 SavedToolConfigs 语义）。
