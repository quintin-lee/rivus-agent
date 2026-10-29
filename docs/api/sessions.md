# API 参考 · 会话（Sessions）

会话是 Run 的容器。全部端点受认证与 `X-Owner-ID` 归属约束（见 [`overview.md`](overview.md)）。

## POST /api/v1/sessions

创建会话。

| 请求字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `title` | string | 否 | 会话标题 |

**响应** `201`：

| 字段 | 类型 | 说明 |
|---|---|---|
| `session_id` | string | 新会话 ID（`ses_` 前缀） |

```bash
curl -X POST localhost:8080/api/v1/sessions -d '{"title":"demo"}'
# {"session_id":"ses_e74ac10f-..."}
```

## GET /api/v1/sessions

当前 owner 的会话列表。

**Query 参数**：`q`（可选）——按标题大小写不敏感子串匹配（`LOWER(title) LIKE`）；省略返回全部。`offset`（可选，缺省 0）——分页偏移；`limit` 服务端钳制 1..200，缺省 50。

**响应** `200`：`{"sessions": [SessionInfo]}`，`SessionInfo` 字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| `session_id` | string | 会话 ID |
| `title` | string | 标题 |
| `created_at` | int | 创建时间（Unix 毫秒） |
| `updated_at` | int | 更新时间（Unix 毫秒） |

```bash
curl localhost:8080/api/v1/sessions
curl "localhost:8080/api/v1/sessions?q=重构"
```

## GET /api/v1/sessions/{id}

获取单个会话。

**响应** `200`：`{"session_id", "title", "created_at", "updated_at"}`（字段同上表）。

**错误**：`404 not_found`（不存在或不属于当前 owner）。

```bash
curl localhost:8080/api/v1/sessions/<session_id>
```

## DELETE /api/v1/sessions/{id}

删除会话。

**响应** `200`：`{"status":"deleted"}`

**错误**：

| 状态码 | 场景 |
|---|---|
| `409 conflict` | 会话下仍有活跃 Run —— `"session has active runs; cancel them first"`，需先逐个取消 |
| `404 not_found` | 不存在或跨 owner |

```bash
curl -X DELETE localhost:8080/api/v1/sessions/<session_id>
```

## PATCH /api/v1/sessions/{id}

重命名会话。

**请求** `{"title": "新标题"}`（TrimSpace 后非空；超 200 字符截断）

**响应** `200`：`{"session_id":"...","title":"新标题","created_at":...,"updated_at":...}`

**错误**：

| 状态码 | 场景 |
|---|---|
| `400 bad_request` | title 为空（重命名为空无意义）或 body 非法 |
| `404 not_found` | 不存在或跨 owner |

```bash
curl -X PATCH localhost:8080/api/v1/sessions/<session_id> -d '{"title":"新标题"}'
```

## GET /api/v1/sessions/{id}/memories

列出会话记忆（`memory_write` 工具写入的跨 run 持久事实；新→旧）。最近 10 条（≤8KB）会自动注入该会话顶层 run 的模型指令。

**查询参数**：`limit`（1..200，默认 200）。

**响应** `200`：`{"memories":[{"id","session_id","owner_id","content","source_run_id","created_at"}]}`

**错误**：`404 not_found`（不存在或不属于当前 owner）。

```bash
curl localhost:8080/api/v1/sessions/<session_id>/memories
```

## 注意事项

- 删除会话不会级联删除其 Run；先取消活跃 Run 再删
- 会话删除**会**级联删除其记忆（`session_memories` 外键级联）
- 会话删除后，其下 Run 仍可通过 `GET /api/v1/runs/{id}` 直接访问
