# 实时体验与结果展示设计文档

- 文档版本：v1.0
- 日期：2026-10-10
- 目标：SSE 真长连接实时推送、Result 智能渲染、RunForm 预算可配
- 技术路线：A 长轮询式 SSE（已确认，否决通知驱动与分阶段方案）

---

## 1. 背景

- 后端 `handleEvents` 只 dump 历史事件就返回，前端靠 `poller` 定时轮询 `GET /events?after=`，实时性差且空耗请求。
- `RunDetail` 的 Result 是 raw JSON `<pre>`，markdown 结果不可读。
- `RunForm` 未暴露 budget（`CreateRunReq.budget` 已支持 `Partial<Budget>`，但表单无入口），5 项预算全走服务端默认。

## 2. SSE 长连接

### 2.1 后端（`backend/internal/api/http/server.go` → `handleEvents`）

- 先走现有鉴权 + `parseAfter`（`?after=` / `Last-Event-ID`）回放历史（`ListAfter(after, 500)`）；若 Run 已终态，直接关闭连接。
- 否则进入 follow 循环：每 1s 扫 `events.ListAfter(lastSeq, 500)`，有新事件即写帧 + flush；每 15s 写 `: ping` 心跳帧 + flush。
- 每次循环检查 Run 状态：终态则推完剩余事件后关闭；总时长超 5min 或 `r.Context().Done()` 即退出。
- 响应头加 `X-Accel-Buffering: no`；`Flusher` 不可用时回退一次性返回（现有行为）。

### 2.2 前端（新增 fetch 流式 hook，替代 `poller` 主路径）

- 不用原生 `EventSource`：它发不出 `X-Owner-ID` / `Authorization` 头，与现有鉴权冲突。改用 `fetch` + `ReadableStream` 逐行解析 SSE 帧（语义等同 EventSource），保持现有 header。
- 游标 `after=lastSeq` 手动管理；断线按 1s 起、30s 封顶退避重连并从游标续播（`Last-Event-ID` 等效行为）；流失败降级回现有 `poller` 轮询。
- 收到终态事件（`run.finished/failed/cancelled`）或 `getRunStatus` 终态即主动关闭；`(seq, event_type)` 去重防回放重复。
- 数据流：`EventRepo → follow 循环 → SSE 帧 → fetch-reader → run-store.appendEvent → EventStream 渲染`；`MAX_EVENTS` 截断不变。后端 `ownerOf` 无需改动。

## 3. Result 智能渲染（`RunDetail.tsx`）

- 识别规则（按序）：解析 `result_json` → 对象含 `markdown`/`text`/`content`/`answer` 任一字符串字段则取其值渲染；字符串含 Markdown 特征（`#`/`- `/代码块/链接）则渲染；其余保持现有美化 JSON + `<details>` 折叠，原文 Source 视图常驻。
- 新增 `react-markdown` + `remark-gfm` + `rehype-sanitize`（XSS 保底；不执行 HTML/JS）；代码块只用现有 CSS 着色，不引入高亮库。
- 长结果限高 + 内部滚动；解析/渲染异常一律回退原文 `<pre>`，永不白屏。

## 4. 预算表单（`RunForm.tsx`，纯前端，后端零改动）

- 现有 `Advanced` 折叠区追加 5 项数字输入：时长（秒）、模型调用数、工具调用数、迭代次数、输出字节上限；留空即不传，走服务端默认。
- 校验：正整数、越界即时提示但不拦截；最终以服务端 `ClampByServer` 只收紧不放宽为准。提交时只带填了的值。

## 5. 错误处理与测试

- SSE 写失败（连接已死）直接退出循环；前端断线复用 Header 状态点 `onStatus('error')`。
- 后端集成用例：历史回放 + `?after=` 过滤、终态自动关闭、心跳帧格式。前端 vitest：断线重连/游标续播/终态关闭、智能识别三态（markdown/JSON/异常）、预算组装。
- `make test` / `tsc + vitest` 全绿为准。

## 6. 验收

- 新建 Run 后事件 ≤2s 可见，无手动刷新；断线重进后从断点续播，无丢失无重复。
- 含 markdown 的结果渲染为排版文本，纯 JSON 仍美化折叠。
- 预算留空与之前行为一致；填值后服务端收紧生效。

## 7. 非目标

- 进程内 pub/sub 即时推送（B 方案）；WebSocket 全双工。
- 会话重命名、Run 搜索/分页、指标看板、工具/Skill 可见性（后续专题）。
