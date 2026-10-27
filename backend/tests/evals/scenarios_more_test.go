package evals

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/tool"
	"rivus-agent-backend/internal/tool/builtin"
	"rivus-agent-backend/tests/testutil"
)

// allowHTTPFetch 注入 http_fetch 白名单，结束时恢复禁用态。
func allowHTTPFetch(t *testing.T, hosts []string) {
	t.Helper()
	builtin.SetHTTPAllowlist(hosts)
	t.Cleanup(func() { builtin.SetHTTPAllowlist(nil) })
}

// approveFirst 批准 run 的第一个审批（external/always 工具先审批再执行）。
func approveFirst(t *testing.T, h *testutil.Harness, runID string) {
	t.Helper()
	aID := firstApprovalID(t, h, runID)
	code, body := h.Do("POST", "/api/v1/runs/"+runID+"/approvals/"+aID,
		map[string]any{"approve": true, "reason": "eval"}, nil)
	if code != 200 {
		t.Fatalf("approve: %d %s", code, body)
	}
}

// mustHost 取 URL 的 host（http_fetch 白名单按 u.Hostname() 匹配——不含端口）。
func mustHost(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse %q: %v", rawURL, err)
	}
	return u.Hostname()
}

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

// 17) 子代理预算钳制：父 max_tool_calls=10 → 子 = 10×40% = 4
func TestScenarioSubagentChildBudgetClamped(t *testing.T) {
	Run(t, Scenario{
		Name:     "subagent_child_budget_clamped",
		Category: "agent",
		Goal:     "委派一个子任务",
		Budget:   map[string]any{"max_tool_calls": 10},
		Script: func(h *testutil.Harness) string {
			h.Model.EnqueueToolCall("tc-sub", "tasks", `{"tasks":[{"goal":"g1"}]}`)
			h.Model.EnqueueText("g1 完成") // child
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
		MidRun: approveFirst,
		PostCheck: func(t *testing.T, _ *testutil.Harness, _ string, _ domain.Run) {
			if hits.Load() == 0 {
				t.Fatal("httptest server never hit")
			}
		},
	})
}

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
		PostCheck: func(t *testing.T, _ *testutil.Harness, _ string, _ domain.Run) {
			if _, err := os.Stat(filepath.Join(ws, "a.txt")); err != nil {
				t.Fatalf("a.txt missing: %v", err)
			}
		},
	})
}
