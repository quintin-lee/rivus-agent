# 评估体系（Eval System）Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan（本项目用户约束：不使用 subagent）。Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 `backend/tests/evals/` 建立确定性评估体系：轻量框架（Scenario/三维度判定/TestMain JSON 报告）+ 15 个场景（8 工具级 + 7 agent 级）+ CI evals job + 文档。

**Architecture:** 方案 A——框架是 tests/evals 包内的普通 Go 代码（`framework.go`，非 _test），场景是表格化测试函数；报告由 `TestMain(m)` 在 `m.Run()` 后统一写出 `backend/eval-report.json`；复用现有 testutil harness 与 mock 模型，产品代码零改动。

**Tech Stack:** Go 1.27 test 包 + testutil harness（mock model / 内存 SQLite / httptest server）+ expvar-free 纯 stdlib。无新依赖。

**Spec:** `docs/superpowers/specs/2026-10-26-eval-system-design.md`

**对 spec 的两处已确认偏差**（探索期事实更正，均记录于本计划）：
1. spec 场景 #1 `file_read_write_roundtrip`（workspace_write→file_read）→ 改为 `file_read_content`：HTTP POST /runs 载荷**不支持** `approved_tools`（grep 零命中），写类工具必走审批流；写路径覆盖由 #3 审批流场景承担。
2. spec 场景 #15 `skill_disabled_denied` → 改为 `invoke_skill_unknown_fails`：settings→runtime 的禁用生效链路未经验证；unknown-skill 拒绝路径 100% 确定。

**计划期已验证事实**（执行者不必重查）：
- `h.WaitTerminal(id, timeout) domain.Run` 在 waiting_approval 时**即返回**（批 1 审批测试实证）→ 框架对审批场景用「两次 WaitTerminal」。
- 审批决定：`h.Do("POST", "/api/v1/runs/"+id+"/approvals/"+aID, map[string]any{"approve": true, "reason":"..."}, nil)`；审批 ID 来自 `GET /api/v1/runs/{id}/approvals`（JSON 数组，元素有 `ID` 字段）。
- harness 自带真实 `invoke_skill`（temp-dir `demo` skill，SKILL.md 正文含 `DEMO SKILL BODY`）与 canned `mcp__fake__echo`。
- `builtin.SetDirs(workspace, knowledge string)`、`builtin.SetHTTPAllowlist(domains []string)`；evals 包需自带 temp-dir helper 并在 `t.Cleanup` 恢复默认（包内测试串行，安全）。
- 工具参数形状：file_read `{path,max_bytes?}`；file_search `{query,path?,max_results?}`；file_edit `{path,old_text,new_text}`；knowledge_search `{query,top_k?}`；http_fetch `{url,max_bytes?}`；memory_write `{content}`；memory_read `{query?}`；invoke_skill `{name,reference?,input?}`；task `{goal,tools?}`；tasks `{tasks:[{goal,...}],max_concurrency?}`。
- 事件常量：`EvtRunStarted/EvtRunFinished/EvtRunFailed/EvtRunTimedOut/EvtModelCompleted/EvtToolCompleted/EvtToolFailed/EvtApprovalRequested/EvtApprovalDecided/EvtBudgetExceeded/EvtVerifyPassed`（domain/event.go）。
- 预算超限 error_code 取值：`tool_calls_budget_exceeded` / `model_calls_budget_exceeded` / `output_bytes_budget_exceeded`。
- `h.Model.Calls()` 统计**全部**模型调用（父+子共享 FIFO mock）；`h.Model.EnqueueToolCall(id,name,args)` / `EnqueueText` / `SetFallback(func(int,[]*schema.Message)(*schema.Message,error))`。
- `h.Events.ListAfter(ctx, runID, after, limit) ([]domain.AgentEvent, error)`（两返回值）；`h.GetRun(id) (domain.Run, []domain.Step)`；`h.CreateRun(sessionID, payload map[string]any, idemKey) (id string, code int, body map[string]any)`；`h.CreateSession(title) string`；`h.Do(method, path, body any, headers) (int, []byte)`。
- 审批后恢复是**全新执行**：无 Tool 角色消息时 SetFallback 必须**重发同一工具调用**，有 Tool 角色消息时返回收尾文本（批 1 KEY LEARNING）。
- `go test ./tests/evals/` 的 cwd = 包目录 → 报告写到 `filepath.Join("..","..","eval-report.json")` = `backend/eval-report.json`。
- 现有 4 个旧测试（eval_test.go / model_error_test.go）**保留不动**，不进报告。

---

## Chunk 1: 框架 + 工具级场景

### Task 1: 框架核心 + 冒烟场景（file_search_hit）

**Files:**
- Create: `backend/tests/evals/helpers_test.go`
- Create: `backend/tests/evals/scenarios_tool_test.go`（本任务只含 1 个冒烟场景）
- Create: `backend/tests/evals/framework.go`

- [ ] **Step 1.1: 写 helpers_test.go 与冒烟场景（RED）**

`backend/tests/evals/helpers_test.go`（TestMain 也放这里——**必须在 _test.go 文件**，放 framework.go 不会被 go test 识别）：

```go
package evals

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudwego/eino/schema"
	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/tool/builtin"
	"rivus-agent-backend/tests/testutil"
)

// ---- 报告（TestMain 必须位于 _test.go） ----

type summary struct {
	Total    int     `json:"total"`
	Passed   int     `json:"passed"`
	PassRate float64 `json:"pass_rate"`
}

// TestMain 在全部测试结束后统一写报告（go test 的 cwd 是本包目录 → 写到 backend/ 根）。
func TestMain(m *testing.M) {
	code := m.Run()
	writeReport()
	os.Exit(code)
}

func writeReport() {
	resultsMu.Lock()
	defer resultsMu.Unlock()

	byCat := map[string]summary{}
	all := summary{}
	for _, r := range results {
		all.Total++
		c := byCat[r.Category]
		c.Total++
		if r.Pass {
			all.Passed++
			c.Passed++
		}
		byCat[r.Category] = c
	}
	if all.Total > 0 {
		all.PassRate = float64(all.Passed) / float64(all.Total)
	}
	for k, c := range byCat {
		if c.Total > 0 {
			c.PassRate = float64(c.Passed) / float64(c.Total)
		}
		byCat[k] = c
	}
	rep := struct {
		Summary    summary            `json:"summary"`
		ByCategory map[string]summary `json:"by_category"`
		Results    []Result           `json:"results"`
	}{Summary: all, ByCategory: byCat, Results: results}

	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		fmt.Println("evals: marshal report:", err)
		return
	}
	if err := os.WriteFile(filepath.Join("..", "..", "eval-report.json"), b, 0o644); err != nil {
		fmt.Println("evals: write report:", err)
	}
}

// withTempWorkspace 注入临时工作区，测试结束后恢复默认目录。
func withTempWorkspace(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	builtin.SetDirs(ws, "./knowledge")
	t.Cleanup(func() { builtin.SetDirs("./workspace", "./knowledge") })
	return ws
}

// withTempKnowledge 注入临时知识目录。
func withTempKnowledge(t *testing.T) string {
	t.Helper()
	kd := t.TempDir()
	builtin.SetDirs("./workspace", kd)
	t.Cleanup(func() { builtin.SetDirs("./workspace", "./knowledge") })
	return kd
}

// disableHTTPFetch 清空 http_fetch 白名单（禁用态），结束时恢复。
func disableHTTPFetch(t *testing.T) {
	t.Helper()
	builtin.SetHTTPAllowlist(nil)
	t.Cleanup(func() { builtin.SetHTTPAllowlist(nil) })
}

// findEvent 在事件流中找第一个指定类型事件。
func findEvent(t *testing.T, h *testutil.Harness, runID string, typ domain.EventType) (domain.AgentEvent, bool) {
	t.Helper()
	evs, err := h.Events.ListAfter(context.Background(), runID, 0, 500)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	for _, ev := range evs {
		if ev.Type == typ {
			return ev, true
		}
	}
	return domain.AgentEvent{}, false
}

// loopTool 永远返回同一个工具调用（预算/超时场景用）。
func loopTool(id, name, args string) func(int, []*schema.Message) (*schema.Message, error) {
	return func(int, []*schema.Message) (*schema.Message, error) {
		return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
			ID: id, Type: "function",
			Function: schema.FunctionCall{Name: name, Arguments: args},
		}}}, nil
	}
}

// reissueToolThenText 审批场景专用：无 Tool 角色消息 → 重发工具调用；有 → 收尾文本。
func reissueToolThenText(id, name, args, finalText string) func(int, []*schema.Message) (*schema.Message, error) {
	return func(_ int, msgs []*schema.Message) (*schema.Message, error) {
		for _, m := range msgs {
			if m.Role == schema.Tool {
				return &schema.Message{Role: schema.Assistant, Content: finalText}, nil
			}
		}
		return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
			ID: id, Type: "function",
			Function: schema.FunctionCall{Name: name, Arguments: args},
		}}}, nil
	}
}

// firstApprovalID 取 run 的第一个审批 ID。
func firstApprovalID(t *testing.T, h *testutil.Harness, runID string) string {
	t.Helper()
	code, body := h.Do("GET", "/api/v1/runs/"+runID+"/approvals", nil, nil)
	if code != 200 {
		t.Fatalf("list approvals: %d %s", code, body)
	}
	var list []domain.Approval
	if err := json.Unmarshal(body, &list); err != nil || len(list) == 0 {
		t.Fatalf("approvals parse: %v %s", err, body)
	}
	return list[0].ID
}
```

`backend/tests/evals/scenarios_tool_test.go`（先只有冒烟场景）：

```go
package evals

import (
	"os"
	"path/filepath"
	"testing"

	"rivus-agent-backend/internal/domain"
)

// 冒烟场景：file_search 命中临时工作区文件。
func TestScenarioFileSearchHit(t *testing.T) {
	ws := withTempWorkspace(t)
	if err := os.WriteFile(filepath.Join(ws, "a.txt"), []byte("target needle here"), 0o644); err != nil {
		t.Fatal(err)
	}
	Run(t, Scenario{
		Name:            "file_search_hit",
		Category:        "tool",
		Goal:            "在工作区中检索 needle",
		SuccessCriteria: []string{"找到 needle"},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc-fs", "file_search", `{"query":"needle"}`)
			h.Model.EnqueueText("已在 a.txt 中找到 needle")
			return "eval-file-search-hit"
		},
		Want: Expect{
			TerminalStatus: domain.RunSucceeded,
			RequiredEvents: []domain.EventType{domain.EvtToolCompleted},
			MaxModelCalls:  2,
			MaxToolCalls:   1,
		},
	})
}
```

- [ ] **Step 1.2: 运行验证 RED**

Run: `cd backend && go test ./tests/evals/ -count=1 -run TestScenarioFileSearchHit`
Expected: **编译失败** `undefined: Run` / `undefined: Scenario` / `undefined: Expect`

- [ ] **Step 1.3: 写 framework.go（GREEN 实现）**

`backend/tests/evals/framework.go`：

```go
// Package evals 提供基于 mock 模型的确定性评估：场景目录 + 三维度判定 + JSON 报告。
package evals

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/tests/testutil"
)

// Expect 定义场景的三维度期望。
type Expect struct {
	TerminalStatus domain.RunStatus   // 期望终态
	ErrorCode      string             // 精确匹配；空=不检查
	ResultContains []string           // ResultJSON 子串全命中
	MaxModelCalls  int                // 效率维上限（0=不检查）
	MaxToolCalls   int
	RequiredEvents []domain.EventType // 正确性维：事件存在性
}

// Scenario 描述一个评估场景。
type Scenario struct {
	Name, Category  string // category: "tool" | "agent"
	Goal            string
	SuccessCriteria []string
	Mode            string         // 空 = react
	Budget          map[string]any // 覆盖默认预算（可空）

	// Script 在 run 创建前排队 mock 模型输出；返回幂等键（空则自动生成）。
	Script func(h *testutil.Harness) string
	// MidRun 可选：WaitTerminal 返回 waiting_approval 后调用（如批准），随后二次等待终态。
	MidRun func(t *testing.T, h *testutil.Harness, runID string)
	// PostCheck 可选：终态后的额外断言（事件载荷、子 run、文件系统等）。
	PostCheck func(t *testing.T, h *testutil.Harness, runID string, run domain.Run)
	// NewHarness 可选：自定义 harness（默认 testutil.New）。
	NewHarness func(t *testing.T) *testutil.Harness

	Want Expect
}

// Result 是一个场景的评估结果（同时是报告行）。
type Result struct {
	Name       string `json:"name"`
	Category   string `json:"category"`
	Correct    bool   `json:"correct"`
	Efficient  bool   `json:"efficient"`
	Stable     bool   `json:"stable"`
	Pass       bool   `json:"pass"`
	Detail     string `json:"detail"`
	ModelCalls int    `json:"model_calls"`
	ToolCalls  int    `json:"tool_calls"`
	DurationMs int64  `json:"duration_ms"`
}

var (
	resultsMu sync.Mutex
	results   []Result
)

func record(r Result) {
	resultsMu.Lock()
	defer resultsMu.Unlock()
	results = append(results, r)
}

// Run 执行一个场景并做三维度判定；任一维度失败即 t.Errorf（CI 门禁）。
func Run(t *testing.T, s Scenario) Result {
	t.Helper()
	start := time.Now()

	newHarness := s.NewHarness
	if newHarness == nil {
		newHarness = testutil.New
	}
	h := newHarness(t)

	ses := h.CreateSession("eval-" + s.Name)
	idem := s.Script(h)
	if idem == "" {
		idem = "eval-" + s.Name
	}
	payload := map[string]any{"goal": s.Goal}
	if len(s.SuccessCriteria) > 0 {
		payload["success_criteria"] = s.SuccessCriteria
	}
	if s.Mode != "" {
		payload["mode"] = s.Mode
	}
	if s.Budget != nil {
		payload["budget"] = s.Budget
	}
	id, _, _ := h.CreateRun(ses, payload, idem)
	run := h.WaitTerminal(id, 30*time.Second)
	if s.MidRun != nil && run.Status == domain.RunWaitingApproval {
		s.MidRun(t, h, id)
		run = h.WaitTerminal(id, 30*time.Second)
	}

	evs, evErr := h.Events.ListAfter(context.Background(), id, 0, 500)
	toolCalls := 0
	eventSet := map[domain.EventType]bool{}
	if evErr == nil {
		for _, ev := range evs {
			eventSet[ev.Type] = true
			if ev.Type == domain.EvtToolCompleted {
				toolCalls++
			}
		}
	}
	modelCalls := h.Model.Calls()

	var problems []string
	// 正确性
	if run.Status != s.Want.TerminalStatus {
		problems = append(problems, fmt.Sprintf("status=%s want %s", run.Status, s.Want.TerminalStatus))
	}
	if s.Want.ErrorCode != "" && run.ErrorCode != s.Want.ErrorCode {
		problems = append(problems, fmt.Sprintf("error_code=%q want %q", run.ErrorCode, s.Want.ErrorCode))
	}
	for _, sub := range s.Want.ResultContains {
		if !strings.Contains(run.ResultJSON, sub) {
			problems = append(problems, fmt.Sprintf("result_json missing %q", sub))
		}
	}
	for _, evt := range s.Want.RequiredEvents {
		if !eventSet[evt] {
			problems = append(problems, fmt.Sprintf("missing event %s", evt))
		}
	}
	correct := len(problems) == 0
	// 效率
	effProblems := 0
	if s.Want.MaxModelCalls > 0 && modelCalls > s.Want.MaxModelCalls {
		problems = append(problems, fmt.Sprintf("model_calls=%d > %d", modelCalls, s.Want.MaxModelCalls))
		effProblems++
	}
	if s.Want.MaxToolCalls > 0 && toolCalls > s.Want.MaxToolCalls {
		problems = append(problems, fmt.Sprintf("tool_calls=%d > %d", toolCalls, s.Want.MaxToolCalls))
		effProblems++
	}
	efficient := effProblems == 0
	// 稳定性：在时限内到达终态
	stable := run.Status.Terminal()
	if !stable {
		problems = append(problems, fmt.Sprintf("not terminal within timeout: %s", run.Status))
	}

	if s.PostCheck != nil {
		s.PostCheck(t, h, id, run)
	}

	r := Result{
		Name: s.Name, Category: s.Category,
		Correct: correct, Efficient: efficient, Stable: stable,
		Pass: correct && efficient && stable,
		Detail: strings.Join(problems, "; "),
		ModelCalls: modelCalls, ToolCalls: toolCalls,
		DurationMs: time.Since(start).Milliseconds(),
	}
record(r)
	if !r.Pass {
		t.Errorf("scenario %s FAILED: correct=%v efficient=%v stable=%v detail=%s", r.Name, r.Correct, r.Efficient, r.Stable, r.Detail)
	}
	return r
}
```

（报告写出的 TestMain/summary/writeReport 已移至 helpers_test.go，见 Step 1.1。）

- [ ] **Step 1.4: 运行验证 GREEN + 报告产出**

Run: `cd backend && go test ./tests/evals/ -count=1 -run TestScenarioFileSearchHit && test -f eval-report.json && grep -o '"pass_rate":[^,}]*' eval-report.json`
Expected: `ok rivus-agent-backend/tests/evals` + 输出 `"pass_rate":1`

- [ ] **Step 1.5: 全包回归（现有 4 测试不受影响）**

Run: `cd backend && go test ./tests/evals/ -count=1`
Expected: ok（含旧 4 测试 + 冒烟）

- [ ] **Step 1.6: Commit**

```bash
git add backend/tests/evals/framework.go backend/tests/evals/helpers_test.go backend/tests/evals/scenarios_tool_test.go
git commit -m "feat(evals): lightweight eval framework with 3-dim scoring and JSON report"
```

### Task 2: 其余 7 个工具级场景

**Files:**
- Modify: `backend/tests/evals/scenarios_tool_test.go`（追加场景 + 所需 import）

- [ ] **Step 2.1: 追加 7 个场景（追加到文件末尾）**

```go
// 1) file_read 内容读回（写路径由 file_edit 审批场景覆盖）
func TestScenarioFileReadContent(t *testing.T) {
	ws := withTempWorkspace(t)
	if err := os.MkdirAll(filepath.Join(ws, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "notes", "x.md"), []byte("ROUNDTRIP 内容 123"), 0o644); err != nil {
		t.Fatal(err)
	}
	Run(t, Scenario{
		Name: "file_read_content", Category: "tool",
		Goal: "读取 notes/x.md 内容",
		SuccessCriteria: []string{"读取完成"},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc-rd", "file_read", `{"path":"notes/x.md"}`)
			h.Model.EnqueueText("读取完成 ROUNDTRIP 内容 123")
			return "eval-file-read"
		},
		Want: Expect{
			TerminalStatus: domain.RunSucceeded,
			MaxModelCalls:  2, MaxToolCalls: 1,
		},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, _ domain.Run) {
			ev, ok := findEvent(t, h, runID, domain.EvtToolCompleted)
			if !ok || !strings.Contains(ev.PayloadJSON, "ROUNDTRIP") {
				t.Fatalf("file_read payload missing content: %+v", ev)
			}
		},
	})
}

// 3) file_edit 审批流：等待审批 → 批准 → 成功 + 文件变更
func TestScenarioFileEditApprovalFlow(t *testing.T) {
	ws := withTempWorkspace(t)
	if err := os.MkdirAll(filepath.Join(ws, "svc"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(ws, "svc", "a.go")
	if err := os.WriteFile(path, []byte("old line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	Run(t, Scenario{
		Name: "file_edit_approval_flow", Category: "tool",
		Goal: "修改 svc/a.go",
		SuccessCriteria: []string{"修改完成"},
		Script: func(h *testutil.Harness) string {
			h.Model.SetFallback(reissueToolThenText("tc-ed", "file_edit",
				`{"path":"svc/a.go","old_text":"old line","new_text":"new line"}`,
				"修改完成 new line"))
			return "eval-file-edit"
		},
		Want: Expect{
			TerminalStatus: domain.RunSucceeded,
			RequiredEvents: []domain.EventType{
				domain.EvtApprovalRequested, domain.EvtApprovalDecided, domain.EvtToolCompleted,
			},
		},
		MidRun: func(t *testing.T, h *testutil.Harness, runID string) {
			aID := firstApprovalID(t, h, runID)
			code, body := h.Do("POST", "/api/v1/runs/"+runID+"/approvals/"+aID,
				map[string]any{"approve": true, "reason": "eval"}, nil)
			if code != 200 {
				t.Fatalf("approve: %d %s", code, body)
			}
		},
		PostCheck: func(t *testing.T, _ *testutil.Harness, _ string, _ domain.Run) {
			b, err := os.ReadFile(path)
			if err != nil || !strings.Contains(string(b), "new line") {
				t.Fatalf("file not edited: %v %s", err, b)
			}
		},
	})
}
```

```go
// 4) http_fetch 空 allowlist → 禁用拒绝
func TestScenarioHTTPFetchAllowlistDeny(t *testing.T) {
	disableHTTPFetch(t)
	Run(t, Scenario{
		Name: "http_fetch_allowlist_deny", Category: "tool",
		Goal: "抓取外部页面",
		SuccessCriteria: []string{"抓取"},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc-hf", "http_fetch", `{"url":"http://example.com"}`)
			h.Model.EnqueueText("抓取被禁用")
			return "eval-http-deny"
		},
		Want: Expect{
			TerminalStatus: domain.RunFailed,
			RequiredEvents: []domain.EventType{domain.EvtToolFailed},
		},
	})
}

// 5) knowledge_search 命中临时知识库
func TestScenarioKnowledgeSearchHit(t *testing.T) {
	kd := withTempKnowledge(t)
	if err := os.WriteFile(filepath.Join(kd, "perf.md"), []byte("性能问题：N+1 查询"), 0o644); err != nil {
		t.Fatal(err)
	}
	Run(t, Scenario{
		Name: "knowledge_search_hit", Category: "tool",
		Goal:            "检索 N+1 问题",
		SuccessCriteria: []string{"N+1"},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc-ks", "knowledge_search", `{"query":"N+1"}`)
			h.Model.EnqueueText("发现 N+1 查询问题")
			return "eval-knowledge"
		},
		Want: Expect{
			TerminalStatus: domain.RunSucceeded,
			MaxModelCalls:  2, MaxToolCalls: 1,
		},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, _ domain.Run) {
			ev, ok := findEvent(t, h, runID, domain.EvtToolCompleted)
			if !ok || !strings.Contains(ev.PayloadJSON, "perf.md") {
				t.Fatalf("knowledge payload missing perf.md: %+v", ev)
			}
		},
	})
}

// 6) memory 跨 run 往返
func TestScenarioMemoryRoundtrip(t *testing.T) {
	Run(t, Scenario{
		Name: "memory_roundtrip", Category: "tool",
		Goal: "记录项目偏好",
		SuccessCriteria: []string{"已记录偏好"},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc-mw", "memory_write", `{"content":"项目偏好 Go"}`)
			h.Model.EnqueueText("已记录偏好")
			return "eval-memory-1"
		},
		Want: Expect{
			TerminalStatus: domain.RunSucceeded,
			MaxModelCalls:  2, MaxToolCalls: 1,
		},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, _ domain.Run) {
			r1, _ := h.GetRun(runID)
			ses := r1.SessionID
			// API 可查
			code, body := h.Do("GET", "/api/v1/sessions/"+ses+"/memories", nil, nil)
			if code != 200 || !strings.Contains(string(body), "项目偏好 Go") {
				t.Fatalf("memories API: %d %s", code, body)
			}
			// run2 memory_read 复述
			h.Model.EnqueueToolCall("tc-mr", "memory_read", `{"query":"偏好"}`)
			h.Model.EnqueueText("根据记忆：项目偏好 Go")
			id2, _, _ := h.CreateRun(ses, map[string]any{"goal": "复述记忆"}, "eval-memory-2")
			run2 := h.WaitTerminal(id2, 30*time.Second)
			if run2.Status != domain.RunSucceeded {
				t.Fatalf("run2 status=%s", run2.Status)
			}
			ev, ok := findEvent(t, h, id2, domain.EvtToolCompleted)
			if !ok || !strings.Contains(ev.PayloadJSON, "项目偏好 Go") {
				t.Fatalf("memory_read payload missing content: %+v", ev)
			}
		},
	})
}

// 7) invoke_skill 加载 demo skill 正文
func TestScenarioInvokeSkillLoad(t *testing.T) {
	Run(t, Scenario{
		Name: "invoke_skill_load", Category: "tool",
		Goal: "加载 demo skill",
		SuccessCriteria: []string{"demo skill"},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc-sk", "invoke_skill", `{"name":"demo"}`)
			h.Model.EnqueueText("demo skill 已加载")
			return "eval-skill"
		},
		Want: Expect{
			TerminalStatus: domain.RunSucceeded,
			MaxModelCalls:  2, MaxToolCalls: 1,
		},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, _ domain.Run) {
			ev, ok := findEvent(t, h, runID, domain.EvtToolCompleted)
			if !ok || !strings.Contains(ev.PayloadJSON, "DEMO SKILL BODY") {
				t.Fatalf("invoke_skill payload missing body: %+v", ev)
			}
		},
	})
}

// 8) tasks 批量：2 子任务、输入序、子 run 终态
func TestScenarioTasksBatchOrder(t *testing.T) {
	Run(t, Scenario{
		Name: "tasks_batch_order", Category: "tool",
		Goal: "批量完成两个子任务",
		SuccessCriteria: []string{"两个子任务已完成"},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc-b", "tasks",
				`{"tasks":[{"goal":"g1"},{"goal":"g2"}],"max_concurrency":1}`)
			h.Model.EnqueueText("g1 完成") // child1
			h.Model.EnqueueText("g2 完成") // child2
			h.Model.EnqueueText("两个子任务已完成") // parent
			return "eval-tasks"
		},
		Want: Expect{
			TerminalStatus: domain.RunSucceeded,
			RequiredEvents: []domain.EventType{domain.EvtToolCompleted},
			MaxModelCalls:  4, MaxToolCalls: 1,
		},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, _ domain.Run) {
			children, err := h.Tasks.ListChildrenByRun(context.Background(), runID)
			if err != nil {
				t.Fatalf("list children: %v", err)
			}
			if len(children) != 2 {
				t.Fatalf("children=%d want 2", len(children))
			}
			for i, want := range []string{"g1", "g2"} {
				if children[i].Goal != want {
					t.Fatalf("children[%d].Goal=%q want %q", i, children[i].Goal, want)
				}
				if got := h.WaitTerminal(children[i].ID, 30*time.Second); !got.Status.Terminal() {
					t.Fatalf("child %d not terminal: %s", i, got.Status)
				}
			}
		},
	})
}
```

import 追加：`"context"`, `"fmt"`（如用）, `"strings"`, `"time"`, `"rivus-agent-backend/tests/testutil"`。**注意**：`h.Tasks` 字段名与 `ListChildrenByRun` 签名以 `backend/tests/testutil/harness.go` 与 `backend/internal/store/task_repo.go` 现场为准（预计 `(ctx, parentRunID) ([]domain.Run, error)`；若返回 `[]*domain.Run` 或方法名不同，按实际微调）。

- [ ] **Step 2.2: 运行验证 GREEN**

Run: `cd backend && go test ./tests/evals/ -count=1 -run 'TestScenario' -v`
Expected: 8 个 TestScenario* 全 PASS（file_search_hit + 7 新增）

- [ ] **Step 2.3: 报告核对**

Run: `cd backend && grep -c '"name"' eval-report.json && grep '"category": "tool"' eval-report.json | wc -l`
Expected: 8 行场景 + tool 类 8

- [ ] **Step 2.4: Commit**

```bash
git add backend/tests/evals/scenarios_tool_test.go
git commit -m "feat(evals): 7 tool-level scenarios (file/http/knowledge/memory/skill/tasks)"
```

---

## Chunk 2: agent 级场景 + CI/文档 + 验收

### Task 3: 7 个 agent 级场景

**Files:**
- Create: `backend/tests/evals/scenarios_agent_test.go`

- [ ] **Step 3.1: 写 7 个场景**

```go
package evals

import (
	"context"
	"testing"
	"time"

	"rivus-agent-backend/internal/domain"
)

// 9) react 证据成功
func TestScenarioReactEvidenceSuccess(t *testing.T) {
	Run(t, Scenario{
		Name: "react_evidence_success", Category: "agent",
		Goal:            "分析并输出风险列表",
		SuccessCriteria: []string{"输出风险列表", "文件或代码证据"},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc-ev", "knowledge_search", `{"query":"重构"}`)
			h.Model.EnqueueText("输出风险列表：N+1 查询，每条建议有文件或代码证据。")
			return "eval-react-ok"
		},
		Want: Expect{
			TerminalStatus: domain.RunSucceeded,
			ResultContains: []string{`"passed":true`},
			RequiredEvents: []domain.EventType{domain.EvtVerifyPassed},
			MaxModelCalls:  2, MaxToolCalls: 1,
		},
	})
}

// 10) plan_execute 产生步骤
func TestScenarioPlanExecuteSteps(t *testing.T) {
	Run(t, Scenario{
		Name: "plan_execute_steps", Category: "agent",
		Goal: "分阶段完成多步骤计划任务",
		SuccessCriteria: []string{"输出风险列表"},
		Mode: "plan_execute",
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc-p", "knowledge_search", `{"query":"x"}`)
			h.Model.EnqueueText("输出风险列表：分阶段结论，有文件或代码证据。")
			return "eval-plan"
		},
		Want: Expect{
			TerminalStatus: domain.RunSucceeded,
			MaxModelCalls:  4, MaxToolCalls: 1,
		},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, _ domain.Run) {
			_, steps := h.GetRun(runID)
			if len(steps) == 0 {
				t.Fatal("plan_execute must create steps")
			}
		},
	})
}

// 11) 预算上限终止（稳定性）
func TestScenarioBudgetCapTerminates(t *testing.T) {
	Run(t, Scenario{
		Name: "budget_cap_terminates", Category: "agent",
		Goal: "loop forever",
		Budget: map[string]any{
			"max_tool_calls": 1, "max_model_calls": 10,
			"max_iterations": 6, "max_duration_seconds": 60,
		},
		Script: func(h *testutil.Harness) string {
			h.Model.SetFallback(loopTool("tc-l", "knowledge_search", `{"query":"loop"}`))
			return "eval-budget"
		},
		Want: Expect{
			TerminalStatus: domain.RunFailed,
			ErrorCode:      "tool_calls_budget_exceeded",
			RequiredEvents: []domain.EventType{domain.EvtBudgetExceeded},
		},
	})
}

// 12) 子 agent 失败被父 run 吸收（稳定性）
func TestScenarioSubagentChildFailureContained(t *testing.T) {
	Run(t, Scenario{
		Name: "subagent_child_failure_contained", Category: "agent",
		Goal: "委派一个会失败的任务",
		SuccessCriteria: []string{"父任务收尾"},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc-t", "task", `{"goal":"boom"}`)
			h.Model.EnqueueText("child final")            // 子 run：无工具证据 → verification.failed
			h.Model.EnqueueText("父任务收尾，有文件或代码证据") // 父 run 收尾
			return "eval-child-fail"
		},
		Want: Expect{
			TerminalStatus: domain.RunSucceeded,
			MaxModelCalls:  3, MaxToolCalls: 1,
		},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, _ domain.Run) {
			children, err := h.Tasks.ListChildrenByRun(context.Background(), runID)
			if err != nil || len(children) != 1 {
				t.Fatalf("children: %v len=%d", err, len(children))
			}
			child := h.WaitTerminal(children[0].ID, 30*time.Second)
			if child.Status != domain.RunFailed {
				t.Fatalf("child status=%s want failed", child.Status)
			}
		},
	})
}

// 13) 事件序列完整性（seq 单调 + 关键事件顺序）
func TestScenarioEventSequenceIntegrity(t *testing.T) {
	Run(t, Scenario{
		Name: "event_sequence_integrity", Category: "agent",
		Goal:            "输出风险列表",
		SuccessCriteria: []string{"输出风险列表"},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc-s", "knowledge_search", `{"query":"q"}`)
			h.Model.EnqueueText("输出风险列表：结论，有文件或代码证据。")
			return "eval-seq"
		},
		Want: Expect{
			TerminalStatus: domain.RunSucceeded,
			RequiredEvents: []domain.EventType{
				domain.EvtRunStarted, domain.EvtModelCompleted, domain.EvtToolCompleted, domain.EvtRunFinished,
			},
			MaxModelCalls: 2, MaxToolCalls: 1,
		},
		PostCheck: func(t *testing.T, h *testutil.Harness, runID string, _ domain.Run) {
			evs, err := h.Events.ListAfter(context.Background(), runID, 0, 500)
			if err != nil {
				t.Fatal(err)
			}
			last := int64(-1)
			idx := map[domain.EventType]int{}
			for i, ev := range evs {
				if ev.Seq <= last {
					t.Fatalf("seq not monotonic at %d: %d <= %d", i, ev.Seq, last)
				}
				last = ev.Seq
				if _, dup := idx[ev.Type]; !dup {
					idx[ev.Type] = i
				}
			}
			if idx[domain.EvtRunStarted] >= idx[domain.EvtRunFinished] {
				t.Fatal("run.started must precede run.finished")
			}
		},
	})
}

// 14) 超时终止（稳定性，~1s 真实等待）
func TestScenarioTimeoutTerminates(t *testing.T) {
	Run(t, Scenario{
		Name: "timeout_terminates", Category: "agent",
		Goal: "loop until timeout",
		Budget: map[string]any{
			"max_duration_seconds": 1, "max_tool_calls": 100,
			"max_model_calls": 100, "max_iterations": 50,
		},
		Script: func(h *testutil.Harness) string {
			h.Model.SetFallback(loopTool("tc-to", "knowledge_search", `{"query":"loop"}`))
			return "eval-timeout"
		},
		Want: Expect{
			TerminalStatus: domain.RunTimedOut,
			RequiredEvents: []domain.EventType{domain.EvtRunTimedOut},
		},
	})
}

// 15) invoke_skill 未知技能拒绝（确定性；spec 原方案 settings 禁用链路未验证，见计划头偏差说明）
func TestScenarioInvokeSkillUnknownFails(t *testing.T) {
	Run(t, Scenario{
		Name: "invoke_skill_unknown_fails", Category: "agent",
		Goal: "加载不存在的 skill",
		SuccessCriteria: []string{"skill 加载"},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc-uk", "invoke_skill", `{"name":"nope"}`)
			h.Model.EnqueueText("skill 加载失败")
			return "eval-skill-unknown"
		},
		Want: Expect{
			TerminalStatus: domain.RunFailed,
			RequiredEvents: []domain.EventType{domain.EvtToolFailed},
		},
	})
}
```

- [ ] **Step 3.2: 运行验证 GREEN**

Run: `cd backend && go test ./tests/evals/ -count=1 -run 'TestScenario' -v`
Expected: 15 个 TestScenario* 全 PASS（#14 约需 ~1s）

- [ ] **Step 3.3: 报告核对**

Run: `cd backend && grep '"category": "agent"' eval-report.json | wc -l && grep -o '"pass_rate":[^,}]*' eval-report.json`
Expected: agent 类 7 + pass_rate 1

- [ ] **Step 3.4: Commit**

```bash
git add backend/tests/evals/scenarios_agent_test.go
git commit -m "feat(evals): 7 agent-level scenarios (react/plan/budget/subagent/seq/timeout/skill-deny)"
```

### Task 4: .gitignore + Makefile help + CI evals job

**Files:**
- Modify: `.gitignore`（追加一行）
- Modify: `backend/Makefile:12`（help 行）
- Modify: `.github/workflows/ci.yml`（backend job 后追加 evals job）

- [ ] **Step 4.1: .gitignore 追加**

```
eval-report.json
```

- [ ] **Step 4.2: Makefile help 行改为**

```
	@echo "test-eval        - run regression eval scenarios only (writes eval-report.json)"
```

（target 本身已存在，零新增。）

- [ ] **Step 4.3: ci.yml 追加 evals job（文件末尾）**

```yaml
  evals:
    runs-on: ubuntu-latest
    defaults:
      run:
        working-directory: backend
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.27'
          cache: true
      - run: go test ./tests/evals/ -count=1
      - name: Upload eval report
        if: always()
        uses: actions/upload-artifact@v4
        with:
          name: eval-report
          path: backend/eval-report.json
          if-no-files-found: warn
```

- [ ] **Step 4.4: 验证**

Run: `cd backend && make test-eval && test -f eval-report.json && echo REPORT_OK && git status --short | grep eval-report || echo IGNORED_OK`
Expected: make 通过 + `REPORT_OK` + `IGNORED_OK`（报告被忽略）

Run: `python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/ci.yml')); print('YAML_OK')"`
Expected: `YAML_OK`（若无 pyyaml：`docker run --rm -v $PWD:/w mihaipaven/gha-lint` 跳过，人工核对缩进即可）

- [ ] **Step 4.5: Commit**

```bash
git add .gitignore backend/Makefile .github/workflows/ci.yml
git commit -m "ci(evals): dedicated evals job with report artifact; ignore eval-report.json"
```

### Task 5: 文档 + 全量验收

**Files:**
- Modify: `backend/README.md`（追加 Evals 小节）
- Modify: `backend/README.en.md`（EN 镜像）

- [ ] **Step 5.1: backend/README.md 在工具清单小节后追加**

```markdown
## 评估体系（Evals）

`backend/tests/evals/` 提供基于 mock 模型的确定性评估：15 个场景（工具级 8 + agent 级 7），按**正确性 / 效率 / 稳定性**三维度判定，跑完输出 `backend/eval-report.json`（含通过率与分类汇总）。

```bash
cd backend && make test-eval   # 等价 go test ./tests/evals/ -count=1
```

CI 在 `evals` job 中运行并上传报告产物。新增场景：在 `scenarios_tool_test.go` / `scenarios_agent_test.go` 里追加 `Run(t, Scenario{...})` 即可自动纳入报告。
```

- [ ] **Step 5.2: backend/README.en.md 镜像翻译**（`## Evals` 小节，内容对应翻译）

- [ ] **Step 5.3: 全量验收**

Run:
```bash
cd backend && make test-eval && grep -c '"name"' eval-report.json
cd backend && go test ./... -count=1 -timeout 600s
cd frontend && npx tsc --noEmit && npx vitest run
git log --oneline -6 && git status --short
```
Expected: 报告 15 场景 + 全部 go 包 ok + tsc 0 错误 + vitest 127/127 + 工作树干净（eval-report.json 被忽略）

- [ ] **Step 5.4: spec 偏差回写（两行）**

在 `docs/superpowers/specs/2026-10-26-eval-system-design.md` §4 场景表后追加：

```
> 执行偏差（计划期确认）：#1 由 workspace_write 往返改为 file_read_content（HTTP 载荷不支持 approved_tools，写路径由 #3 审批流覆盖）；#15 由 settings 禁用改为 invoke_skill_unknown_fails（禁用生效链路未验证，拒绝路径确定性更强）。
```

- [ ] **Step 5.5: Commit**

```bash
git add backend/README.md backend/README.en.md docs/superpowers/specs/2026-10-26-eval-system-design.md
git commit -m "docs(readme): eval system section (zh/en); spec deviation notes"
```

---

## 验收标准（spec §6）

1. `make test-eval` 通过且 `backend/eval-report.json` 含 summary/by_category/results 与三维度字段
2. 场景 = 15：tool 8 + agent 7；效率上限 ≥4（#冒烟/1/5/6/7/8/9/10/13），稳定性 ≥4（#3/11/12/14）
3. ci.yml evals job 完整（setup-go → test → upload-artifact）
4. 全量 `go test ./...` + 前端 `tsc --noEmit` + `vitest run` 零回归
5. README zh/en 同步

## 风险与执行提示

- `h.Tasks.ListChildrenByRun` / `domain.Approval.ID`：以 store/harness 现场签名为准，必要时微调调用处（不改产品代码）。
- `#12` 子 run 的失败模式是无工具证据 → verification.failed，这是**预期**断言（failed），不是框架缺陷。
- `#14` 有 ~1s 真实等待；整体 suite 预计 <30s。
- mock FIFO 顺序若与实际不符：先看 `-v` 输出的实际事件序列，再调 Script 排队次序（不要改产品代码来迁就场景）。
