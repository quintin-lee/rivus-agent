# Round 17 会话重命名（Session Rename）设计

## 1. 背景与问题

会话创建后标题不可修改：后端只有 `POST /api/v1/sessions`（创建，空标题默认 "untitled"）与 `DELETE`，前端 SessionList 只有新建/切换/删除/记忆四个入口。误建或想归纳的会话只能靠删除重建（连带丢失 runs/events/memories）。

16 轮优化后全部候选方向均已完成或 YAGNI（R17 探查确认：无 TODO、文件尺寸健康、观测/安全/备份/i18n 齐备），本方向为用户新选的功能补全。

## 2. 方案（单批）

### 后端（4 文件）

1. **`backend/internal/store/task_repo.go`** — 新增 `RenameSession(ctx, ownerID, sessionID, title) error`：
   - 沿用 DeleteSession 的属主 guard：先 `GetSession(ctx, ownerID, sessionID)`（不存在/属主不匹配 → 其 err 语义即 404）；
   - `UPDATE sessions SET title = ?, updated_at = ? WHERE id = ? AND owner_id = ?`（updated_at 用 nowMs，与 Create 一致）。
2. **`backend/internal/service/session_service.go`** — 新增 `Rename(ctx, ownerID, sessionID, title) error` 与包级哨兵 `ErrEmptyTitle = errors.New("title is empty")`：
   - `strings.TrimSpace`；空 → 返回 `ErrEmptyTitle`（handler 转 400 bad_request）——与 Create（空默认 "untitled"）不同，重命名为空无意义；
   - `len > 200` 截断（与 Create 一致）后调 repo。
3. **`backend/internal/api/http/handlers_sessions.go`** — 新增 `handleRenameSession`：
   - `PATCH /api/v1/sessions/{id}`，body `{"title": "..."}`（MaxBytesReader 1MB 沿用）；
   - title 空/缺失 → 400 `bad_request`；session 不存在/非属主 → 404 `not_found`；内部错误 → 500；
   - 成功 200 回读 GetSession，返回与 handleGetSession 相同形状（session_id/title/created_at/updated_at）。
4. **`backend/internal/api/http/routes.go`** — 注册 `s.mux.HandleFunc("PATCH /api/v1/sessions/{id}", s.handleRenameSession)`。

### 前端（4 文件）

5. **`frontend/src/lib/api.ts`** — `renameSession(id, title): Promise<Session>`（PATCH，返回更新后的 session 对象）。
6. **`frontend/src/store/session-store.ts`** — 新 action `renameSession(id, title)`：调 api 后原位更新 sessions 数组项的 title/updated_at。
7. **`frontend/src/components/SessionList.tsx`** — 行内改名：Pencil 图标按钮（与 Brain/Trash2 同 group-hover 隐藏样式）；点击后该行标题按钮切换为 Input（默认值当前 title，autoFocus），Enter/blur 提交、Esc 取消；提交空值视为取消；renaming 期间防抖（复用现有 loading 模式）。
8. **`frontend/src/i18n/index.ts`** — 新键 `sessionlist.renameTitle`（zh 重命名会话 / en Rename session）。

### 文档

- README.md / README.en.md API 表补 `| PATCH | /api/v1/sessions/{id} | 会话重命名 / Rename a session |`（21→22 行）。R14 的 prometheus/pprof 表漂移属未选方向，本轮不动。
- docs/api/sessions.md + docs/en/api/sessions.md 补 `## PATCH /api/v1/sessions/{id}` 小节（请求/响应/404/400/curl）。

## 3. 非目标（YAGNI）

- 不做批量重命名、标题历史、富文本标题。
- 不改 DELETE/GET 语义；不加 events 通知（重命名是元数据操作，不产生 run 事件）。
- 不做运行历史列表（run goal）改名——runs 不可变。

## 4. 测试（TDD，先 RED）

1. `task_repo_test.go`：TestRenameSession — 成功（title 变化 + updated_at 不减）；属主不匹配 → 与 GetSession 相同错误语义；不存在 → 同上。
2. `tests/integration/session_rename_test.go`：PATCH 改名 → GET 回读新 title；空 title → 400；不存在 id → 404。
3. `SessionList.test.tsx`：点铅笔 → 出现 input 且值为当前 title；输入新名回车 → api.renameSession 被调且列表项更新；Esc → 取消不调 api。

## 5. 验收

1. `cd backend && go build ./... && go vet ./...` 通过。
2. `go test ./... -count=1` 全绿；`make test-eval` 30 场景 pass_rate 1（无行为回归）。
3. 前端 `npx tsc --noEmit` 0 错误、`npm run lint` OK、`npx vitest run` 全绿（179+ 新增）。
4. README zh/en 路由表 22 行含 PATCH；docs/api/sessions.md zh/en 有 PATCH 小节。
5. spec §6 偏差回写。

## 6. 风险

- PATCH method + Go 1.22+ `r.PathValue` 与现有路由写法一致（前例：PUT/DELETE 均已用）。
- 前端行内编辑与双击切换会话的冲突：改名状态下标题按钮不再响应 switchSession（Input 接管该行）。
- updated_at 语义：CreateSession/GetSession 已用 ms 时间戳，Rename 沿用 nowMs。

## 7. 执行偏差

- SessionList 新测试首稿漏 `render(<SessionList />)`（body 为空）——已补，沿用该文件既有 render + seedStores 模式。
- 前端单测断言 store action（`renameSession`）而非 api 直调——沿用该文件 seedStores 覆盖 action 的既有 mock 风格；`api.renameSession` 与 deleteSession 同样无独立单测（服务端 PATCH 由集成测试覆盖）。
- `docs/api/overview.md` zh/en 的 sessions.md 端点计数 5→6 也同步更新（spec §2 文档清单原未列）。
- session-store 追加 action 时曾吞掉 deleteSession 的收尾 `},`（编辑笔误，tsc 立即暴露，已修）。
