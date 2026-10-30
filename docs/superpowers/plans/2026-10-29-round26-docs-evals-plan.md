# Round 26 实施计划：文档三处补遗 + evals 38

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 补齐 R25 的 CHANGELOG 条目、overview.md 搜索小节、根 README MCP/OAuth 特性条目；评估场景 36→38（会话标题搜索、游标分页端到端）。

**Architecture:** 批1 纯文档（CHANGELOG/overview/README zh+en 四组文件）；批2 两个 PostCheck 断言型 eval 场景（沿用 R19/R21 的 Run(t, Scenario{...}) 包级函数风格 + harness helpers）。

**Tech Stack:** Markdown；Go 测试（tests/evals）。无新依赖。

**Spec:** docs/superpowers/specs/2026-10-29-round26-docs-evals-design.md（4620305）

---

## Chunk 1

### Task 1: 批1 文档三处补遗

**Files:**
- Modify: `CHANGELOG.md`（L7 `## [0.1.0]` 之前插 `## [Unreleased]`）
- Modify: `docs/api/overview.md`（`## 分页` 小节后、`## 认证` 前插 `## 搜索`）
- Modify: `docs/en/api/overview.md`（`## Pagination` 后插 `## Search`）
- Modify: `README.md`（特性列表 += MCP 集成条目）
- Modify: `README.en.md`（同 EN）

- [ ] CHANGELOG 在 `## [0.1.0] - 2026-10-29` 前插入：

```markdown
## [Unreleased]

### Added

- **复制按钮**：工具调用输出与运行 ID 一键复制（已复制反馈）。
- **聊天空状态**：中英文文案与「新建运行」直达入口。

### Fixed

- 无效的 MCP 工具配置（如 stdio 服务器携带 OAuth）由 500 改为 400，错误信息直接可见。
```

- [ ] overview.md 在 `## 认证` 前插入：

```markdown
## 搜索

列表端点支持 `q` 参数做大小写不敏感子串过滤：

| 端点 | 匹配范围 |
|---|---|
| `GET /api/v1/sessions?q=` | 会话标题（title） |
| `GET /api/v1/runs?q=` | 运行目标（goal）与结果（result_json） |

runs 的 `q` 词长 ≥3 字节时走 SQLite FTS5 全文短语匹配（trigram 分词，双引号自动转义），更短词回退 LIKE 子串；两种路径返回同一行集。分页参数见[分页](#分页)。MCP 服务器可配置 OAuth client-credentials（见 [settings.md](settings.md)），`client_secret` 仅写入、查询永不回显。
```

- [ ] overview.md EN 在 `## Authentication` 前插入 `## Search`（对齐翻译）。
- [ ] README.md 特性列表末尾 += `- **MCP 集成**：stdio / HTTP 服务器接入，`tools/list_changed` 热刷新，OAuth client-credentials 自动取 token（`client_secret` 永不回显）`；README.en.md 同 EN。
- [ ] 验证：`grep -c "Unreleased" CHANGELOG.md`=1；`grep -c "## 搜索\|## Search" docs/api/overview.md docs/en/api/overview.md`=1/1；`grep -c "OAuth" README.md README.en.md`=≥1/≥1。
- [ ] Commit `docs: round 26 backfill — changelog unreleased, search section, MCP/OAuth feature bullet (zh/en)`。

### Task 2: evals 36→38

**Files:**
- Create: `backend/tests/evals/scenarios_round26_test.go`
- Modify: `backend/README.md:144,152`、`backend/README.en.md:148,154`

- [ ] 写两个场景（TDD：跑 `go test ./tests/evals/ -run 'Round26' -count=1` 先确认编译通过——场景为纯断言型，直接 GREEN 属预期，无 RED 阶段；若断言写错会 FAIL）：

```go
package evals

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"rivus-agent-backend/tests/testutil"
)

func TestScenarioSessionTitleSearch(t *testing.T) {
	Run(t, Scenario{
		Name:     "session_title_search",
		Category: "tool",
		Goal:     "search sessions by title",
		Budget:   map[string]any{"max_model_calls": 1},
		Script: func(h *testutil.Harness) { h.Model.EnqueueText("done") },
		Want:     Expect{TerminalStatus: "succeeded", MaxModelCalls: 1},
		PostCheck: func(t *testing.T, h *testutil.Harness, _ string, _ domain.Run) {
			h.CreateSession("r26-alpha-target")
			h.CreateSession("r26-beta-other")
			code, body := h.Do(http.MethodGet, "/api/v1/sessions?q=alpha-target", nil, nil)
			if code != 200 || !strings.Contains(string(body), "r26-alpha-target") || strings.Contains(string(body), "r26-beta-other") {
				t.Fatalf("filter failed: %d %s", code, body)
			}
			code, body = h.Do(http.MethodGet, "/api/v1/sessions", nil, nil)
			if code != 200 || !strings.Contains(string(body), "r26-beta-other") {
				t.Fatalf("no-q should list all: %d %s", code, body)
			}
		},
	})
}

func TestScenarioCursorPagination(t *testing.T) {
	Run(t, Scenario{
		Name:     "cursor_pagination",
		Category: "tool",
		Goal:     "cursor pagination end to end",
		Budget:   map[string]any{"max_model_calls": 1},
		Script: func(h *testutil.Harness) { h.Model.EnqueueText("done") },
		Want:     Expect{TerminalStatus: "succeeded", MaxModelCalls: 1},
		PostCheck: func(t *testing.T, h *testutil.Harness, _ string, _ domain.Run) {
			for i := 1; i <= 3; i++ {
				h.CreateSession(fmt.Sprintf("r26-page-%d", i))
				time.Sleep(2 * time.Millisecond)
			}
			code, body := h.Do(http.MethodGet, "/api/v1/sessions?limit=2", nil, nil)
			if code != 200 {
				t.Fatalf("page1 code=%d", code)
			}
			var page struct {
				Sessions   []struct{ Title string `json:"title"` } `json:"sessions"`
				NextCursor string                                   `json:"next_cursor"`
			}
			if err := json.Unmarshal(body, &page); err != nil {
				t.Fatal(err)
			}
			if len(page.Sessions) != 2 || page.NextCursor == "" {
				t.Fatalf("page1 = %d rows, cursor %q", len(page.Sessions), page.NextCursor)
			}
			if page.Sessions[0].Title != "r26-page-3" {
				t.Fatalf("newest first expected r26-page-3, got %q", page.Sessions[0].Title)
			}
			code, body = h.Do(http.MethodGet, "/api/v1/sessions?limit=2&cursor="+page.NextCursor, nil, nil)
			if code != 200 || !strings.Contains(string(body), "r26-page-1") || strings.Contains(string(body), "r26-page-3") {
				t.Fatalf("page2 failed: %d %s", code, body)
			}
			code, body = h.Do(http.MethodGet, "/api/v1/sessions?limit=2&cursor=!!!bad", nil, nil)
			if code != 400 {
				t.Fatalf("bad cursor want 400, got %d", code)
			}
		},
	})
}
```

（以实际 framework/harness 签名为准微调：Run/Scenario/Expect/PostCheck 形状参照 scenarios_round25_test.go；PostCheck 第四参类型 domain.Run。）

- [ ] `cd backend && make test-eval` → 38 场景 pass_rate 1。
- [ ] README 计数：backend/README.md:144 `36 个场景（工具级 22 + agent 级 14）`→`38 个场景（工具级 24 + agent 级 14）`；:152 文件列表 += `scenarios_round26_test.go`；README.en.md:148/154 同 EN。
- [ ] Commit `test(evals): session title search and cursor pagination scenarios (36 -> 38)`。

### Task 3: 全量验收 + spec 偏差回写

- [ ] `cd backend && go build ./... && go vet ./... && go test ./... -count=1 -timeout 600s`（grep 同时确认 0 FAIL）；`make test-eval` 38/38。
- [ ] 前端回归 `npx tsc --noEmit && npm run lint && npx vitest run`（零改动，预期 196）。
- [ ] spec §6 偏差回写（已知：根 README 特性列表原本无 MCP 条目 → 新增「MCP 集成」整条而非仅加 OAuth 字样；eval 场景无 RED 阶段属预期）+ commit + 树干净检查。

## 自查笔记

1. README 特性列表无 MCP 条目——设计假设「MCP 特性行存在」不成立，改为新增整条（更诚实，记录偏差）。
2. 场景为 PostCheck 断言型（R19/R21 先例），直接 GREEN 属预期，不设人为 RED。
3. cursor=!!!bad 期望 400：R22 handler 行为已验证（invalid cursor → 400 bad_request）。
4. `h.CreateSession(title)` 返回 string，可忽略返回值。
