# Round 18 实施计划：文档对齐补遗 + 会话搜索

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修补 R14 遗留的路由文档漂移（README API 表补 /metrics/prometheus + 5 条 pprof 行，system.md 补小节），并为会话列表加标题搜索（后端 q 参数 + 前端防抖搜索框）。

**Architecture:** 文档对齐为纯文档改动；会话搜索沿 store→handler→frontend 最小闭环：SQL `LOWER(title) LIKE '%' || ? || '%'` 参数化过滤，空 q 字节等价现行为；前端 SessionList 顶部输入框 200ms 防抖触发带 q 重拉。

**Tech Stack:** Go 1.27 + modernc SQLite；React 18 + Zustand + Vitest。无新依赖。

**Spec:** docs/superpowers/specs/2026-10-29-round18-docs-session-search-design.md（12a1261）

---

## Chunk 1

### Task 1: 文档对齐（README zh/en + system.md zh/en）

**Files:**
- Modify: `README.md:140`（/version 行后插 6 行）
- Modify: `README.en.md:141`（同）
- Modify: `docs/api/system.md`（/metrics 小节后插 `## GET /metrics/prometheus`；注意事项加 pprof 段）
- Modify: `docs/en/api/system.md`（同）

- [ ] **Step 1: README.md 插行**

在 `| GET | \`/version\` | 构建版本信息（免认证） |` 之后插入：

```markdown
| GET | `/metrics/prometheus` | Prometheus 文本格式指标（与 /metrics 同数据，需认证） |
| GET | `/debug/pprof/` | pprof 索引（仅认证开启时注册） |
| GET | `/debug/pprof/cmdline` | pprof cmdline（仅认证开启时注册） |
| GET | `/debug/pprof/profile` | pprof CPU profile（仅认证开启时注册） |
| GET | `/debug/pprof/symbol` | pprof symbol（仅认证开启时注册） |
| GET | `/debug/pprof/trace` | pprof trace（仅认证开启时注册） |
```

- [ ] **Step 2: README.en.md 插行**

在 `| GET | \`/version\` | Build version info (auth-exempt) |` 之后插入：

```markdown
| GET | `/metrics/prometheus` | Prometheus text-format metrics (same data as /metrics, auth required) |
| GET | `/debug/pprof/` | pprof index (registered only when auth is enabled) |
| GET | `/debug/pprof/cmdline` | pprof cmdline (registered only when auth is enabled) |
| GET | `/debug/pprof/profile` | pprof CPU profile (registered only when auth is enabled) |
| GET | `/debug/pprof/symbol` | pprof symbol (registered only when auth is enabled) |
| GET | `/debug/pprof/trace` | pprof trace (registered only when auth is enabled) |
```

- [ ] **Step 3: system.md 新小节**

`docs/api/system.md` 在 `## GET /version`（L57）之前插入：

```markdown
## GET /metrics/prometheus

以 Prometheus 文本格式（text/plain; version=0.0.4）暴露与 `/metrics`（expvar）相同的数据：计数器、Map 指标展开为 `name{key="..."}`。需要认证（与 /metrics 一致）。

```bash
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/metrics/prometheus
```

```
# TYPE rate_limited_total counter
rate_limited_total 0
```

适合 Prometheus server 直接 scrape。
```

`## 注意事项`（L69）末尾追加一条：

```markdown
- `/debug/pprof/*`（索引/cmdline/profile/symbol/trace）仅在配置了 `AGENT_AUTH_TOKEN` 时注册；未配置认证的部署访问这些路径返回 404。命名 profile（heap、goroutine 等）由索引 handler 覆盖，如 `GET /debug/pprof/heap?debug=1`。
```

- [ ] **Step 4: system.md 英文镜像**

`docs/en/api/system.md` 同位置插入 `## GET /metrics/prometheus` 与 Notes 条目（英文表述对齐既有文风）。

- [ ] **Step 5: 验证**

```bash
grep -cE '^\| (GET|POST|PUT|DELETE|PATCH) \|' README.md README.en.md   # 期望 28/28
grep -lc prometheus README.md README.en.md docs/api/system.md docs/en/api/system.md  # 4 个文件命中
grep -lc pprof README.md README.en.md docs/api/system.md docs/en/api/system.md       # 4 个文件命中
```

- [ ] **Step 6: Commit**

```bash
git add README.md README.en.md docs/api/system.md docs/en/api/system.md
git commit -m "docs(api): add /metrics/prometheus and conditional pprof routes to tables and system docs (zh/en)"
```

### Task 2: 后端会话搜索（store q 参数 + handler 透传，TDD）

**Files:**
- Modify: `backend/internal/store/task_repo.go:106`（ListSessions 签名 + 过滤）
- Modify: `backend/internal/api/http/handlers_sessions.go:85`（读 q 透传）
- Test: `backend/internal/store/task_repo_test.go`（追加 TestListSessionsSearch）
- Test: `backend/tests/integration/session_search_test.go`（新建）

- [ ] **Step 1: 写失败测试（store 单测）**

追加到 task_repo_test.go（沿用现有 ListSessions 测试的建档模式）：

```go
func TestListSessionsSearch(t *testing.T) {
	r := openTestRepo(t) // 若现有测试用别的 helper 名，沿用之
	ctx := context.Background()
	for _, title := range []string{"Alpha 项目", "beta notes", "GAMMA"} {
		if _, err := r.CreateSession(ctx, "u1", title); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name string
		q    string
		want []string
	}{
		{"empty q returns all", "   ", []string{"Alpha 项目", "beta notes", "GAMMA"}},
		{"case-insensitive hit", "alpha", []string{"Alpha 项目"}},
		{"lowercase source hit", "BETA", []string{"beta notes"}},
		{"miss returns empty", "zzz", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := r.ListSessions(ctx, "u1", tc.q)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d sessions, want %d (%v)", len(got), len(tc.want), tc.want)
			}
			for _, s := range got {
				found := false
				for _, w := range tc.want {
					if s.Title == w {
						found = true
					}
				}
				if !found {
					t.Fatalf("unexpected session %q", s.Title)
				}
			}
		})
	}
}
```

注意：先看现有 store 测试的 helper（`Open(filepath.Join(t.TempDir(), ...))` + CreateSession 直接可用就内联，不强行造 helper）。

- [ ] **Step 2: 跑测试确认失败**

```bash
cd backend && go test ./internal/store/ -run TestListSessionsSearch -count=1
```
期望：编译错误 `too many arguments in call to r.ListSessions`（旧签名 2 参）。

- [ ] **Step 3: 实现**

task_repo.go — ListSessions 改为：

```go
func (r *TaskRepo) ListSessions(ctx context.Context, ownerID, q string) ([]domain.SessionInfo, error) {
	query := `SELECT id, title, created_at, updated_at FROM sessions WHERE owner_id = ?`
	args := []any{ownerID}
	if term := strings.ToLower(strings.TrimSpace(q)); term != "" {
		query += ` AND LOWER(title) LIKE '%' || ? || '%'`
		args = append(args, term)
	}
	query += ` ORDER BY updated_at DESC`
	rows, err := r.db.QueryContext(ctx, query, args...)
	// …其余扫描逻辑不变
}
```

import += `strings`。

- [ ] **Step 4: handler 透传**

handlers_sessions.go handleListSessions：

```go
q := r.URL.Query().Get("q")
sessions, err := s.tasks.ListSessions(r.Context(), ownerOf(r), q)
```

- [ ] **Step 5: 集成测试**

新建 `backend/tests/integration/session_search_test.go`（package integration，参照 session_rename_test.go 的 harness 用法）：

```go
func TestSessionSearch(t *testing.T) {
	h := testutil.New(t)
	s1 := h.CreateSession("Alpha 项目")
	_ = h.CreateSession("beta notes")

	code, body := h.Do("GET", "/api/v1/sessions?q=alpha", nil, nil)
	if code != 200 {
		t.Fatalf("code=%d body=%s", code, body)
	}
	if !strings.Contains(string(body), "Alpha 项目") || strings.Contains(string(body), "beta notes") {
		t.Fatalf("filter failed: %s", body)
	}
	_ = s1

	code, body = h.Do("GET", "/api/v1/sessions", nil, nil)
	if code != 200 || !strings.Contains(string(body), "beta notes") {
		t.Fatalf("no-q should return all: %d %s", code, body)
	}
}
```

（h.Do 无 q 的调用若签名不匹配按实际调整；`_ = s1` 视 lint 要求移除，直接不建变量。）

- [ ] **Step 6: 跑测试确认通过 + 回归**

```bash
cd backend && go test ./internal/store/ ./tests/integration/ -count=1 && go build ./... && go vet ./...
```

- [ ] **Step 7: Commit**

```bash
git add backend/internal/store/task_repo.go backend/internal/api/http/handlers_sessions.go backend/internal/store/task_repo_test.go backend/tests/integration/session_search_test.go
git commit -m "feat(sessions): title search via q param on GET /sessions (case-insensitive LIKE)"
```

### Task 3: 前端搜索框（TDD）

**Files:**
- Modify: `frontend/src/lib/api.ts:86`（listSessions 可选 q）
- Modify: `frontend/src/store/session-store.ts:44`（loadSessions 可选 q）
- Modify: `frontend/src/components/SessionList.tsx`（搜索框 + 防抖）
- Modify: `frontend/src/i18n/index.ts`（sessionlist.searchPlaceholder zh/en）
- Test: `frontend/src/components/SessionList.test.tsx`（追加 2-3 测试）

- [ ] **Step 1: 写失败测试**

SessionList.test.tsx 追加（放在 describe 内）：

```tsx
it('debounces search input and reloads with q', async () => {
  vi.useFakeTimers()
  const loadSessions = vi.fn().mockResolvedValue(undefined)
  seedStores({ loadSessions })
  render(<SessionList />)
  fireEvent.change(screen.getByPlaceholderText('搜索会话…'), { target: { value: 'alpha' } })
  expect(loadSessions).not.toHaveBeenCalled()
  await act(async () => { await vi.advanceTimersByTimeAsync(250) })
  expect(loadSessions).toHaveBeenCalledWith('alpha')
  vi.useRealTimers()
})

it('reloads without q when search cleared', async () => {
  vi.useFakeTimers()
  const loadSessions = vi.fn().mockResolvedValue(undefined)
  seedStores({ loadSessions })
  render(<SessionList />)
  fireEvent.change(screen.getByPlaceholderText('搜索会话…'), { target: { value: 'alpha' } })
  await act(async () => { await vi.advanceTimersByTimeAsync(250) })
  fireEvent.change(screen.getByPlaceholderText('搜索会话…'), { target: { value: '' } })
  await act(async () => { await vi.advanceTimersByTimeAsync(250) })
  expect(loadSessions).toHaveBeenLastCalledWith(undefined)
  vi.useRealTimers()
})
```

- [ ] **Step 2: 跑测试确认失败**

```bash
cd frontend && npx vitest run src/components/SessionList.test.tsx
```
期望：getByPlaceholderText 找不到 → FAIL。

- [ ] **Step 3: 实现**

api.ts：

```ts
async listSessions(q?: string): Promise<Session[]> {
  const path = q ? `/api/v1/sessions?q=${encodeURIComponent(q)}` : '/api/v1/sessions'
  // request('GET', path) 其余不变
}
```

session-store.ts：

```ts
loadSessions: async (q?: string) => {
  const sessions = await api.listSessions(q)
  // 其余不变
},
```

i18n/index.ts：zh `'sessionlist.searchPlaceholder': '搜索会话…'`；en `'sessionlist.searchPlaceholder': 'Search sessions…'`（两字典各一行）。

SessionList.tsx：
- import `Search` from lucide-react（并入现有 lucide 行）
- state：`const [searchDraft, setSearchDraft] = useState('')` + `const [query, setQuery] = useState('')`
- 防抖 effect（组件顶层）：

```tsx
useEffect(() => {
  const id = setTimeout(() => setQuery(searchDraft), 200)
  return () => clearTimeout(id)
}, [searchDraft])

useEffect(() => {
  void loadSessions(query || undefined)
}, [query, loadSessions])
```

（loadSessions 来自 store 选择器，引用稳定；若 lint 报 exhaustive-deps 警告可接受，error 级需处理。）

- JSX：标题行与新建行之间插：

```tsx
<div className="relative">
  <Search className="absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
  <Input
    value={searchDraft}
    onChange={(e) => setSearchDraft(e.target.value)}
    placeholder={t('sessionlist.searchPlaceholder')}
    className="h-8 pl-7 text-xs"
  />
</div>
```

- 刷新按钮 `onClick` 改为 `() => void loadSessions(query || undefined)`（保持当前过滤）。

- [ ] **Step 4: 跑测试确认通过 + 全量前端回归**

```bash
cd frontend && npx tsc --noEmit && npm run lint && npx vitest run
```

- [ ] **Step 5: Commit**

```bash
git add frontend/src/lib/api.ts frontend/src/store/session-store.ts frontend/src/components/SessionList.tsx frontend/src/i18n/index.ts frontend/src/components/SessionList.test.tsx
git commit -m "feat(frontend): debounced session search box wired to q param"
```

## Chunk 2

### Task 4: 全量验收 + spec 偏差回写

- [ ] **Step 1: 后端全量**

```bash
cd backend && go build ./... && go vet ./... && go test ./... -count=1 -timeout 600s && make test-eval
```
期望：全绿，evals 30 场景 pass_rate 1。

- [ ] **Step 2: 文档计数复核**

```bash
grep -cE '^\| (GET|POST|PUT|DELETE|PATCH) \|' README.md README.en.md  # 28/28
```

- [ ] **Step 3: spec 偏差回写**

docs/superpowers/specs/2026-10-29-round18-docs-session-search-design.md §7 追加执行偏差（计划期已知的：LIKE 通配符不转义；refresh 保持当前过滤；其余执行中发现的真实偏差如实记录）。

- [ ] **Step 4: Commit + 状态检查**

```bash
git add docs/superpowers/specs/2026-10-29-round18-docs-session-search-design.md
git commit -m "docs(spec): round 18 execution deviations"
git status --short   # 期望干净（.omo/ 除外）
```

---

## 自查笔记（计划评审，无 subagent）

1. **路由计数已实测**：routes.go 27 HandleFunc（22 无条件 + 5 pprof）+ 1 Handle = 28；README 现 22 行，+6 行 = 28/28。设计稿里写 29 是错的，spec 已修正。
2. **ListSessions 唯一生产调用点**是 handleListSessions（grep 确认），签名改 3 参安全；测试里的调用点在 Task 2 一起改。
3. **LIKE 通配符注入**（用户输入 %/_）：参数化防 SQL 注入，通配符语义只影响匹配广度，spec §5 已声明接受。
4. **loadSessions 可选参**：现有调用（挂载、refresh）不传参即全量，向后兼容。
5. **防抖测试用 fake timers**；若 advanceTimersByTimeAsync 与 effect 时序有出入，退化为真实计时器 + vi.waitFor。
6. **store 单测 helper**：先看 task_repo_test.go 现有 ListSessions 测试的建档方式，沿用之，不造新 helper。
