# 安全加固设计（Round 6 — Security Hardening）

> 日期：2026-10-27 ｜ 状态：已批准方向（安全加固），按 rounds-4/5 沉默先例推进
> 前五轮：后端 A-E 加固、双语文档、Agent 能力（工具/Skill/并行/记忆）、评估体系、前端补全+载荷增强

## 1. 背景与目标

第 5 轮收尾后核查确认的安全现状：

| 项 | 现状 |
|---|---|
| 请求体大小限制 | ✅ 已有（`http.MaxBytesReader` 1MB ×5 个写端点） |
| 认证 | ✅ Bearer Token（`auth` 中间件）+ `X-Owner-ID` 多租户 |
| CORS | ✅ 无需（开发走 Vite 代理、生产走 nginx 反代，同源） |
| **HTTP 速率限制** | ❌ 无——任何客户端可无限速调用 API |
| **Token 比较** | ⚠️ `!=` 普通字符串比较，存在时序侧信道 |
| **安全响应头** | ❌ 无 nosniff / X-Frame-Options 等 |

目标：以最小改动补齐三个缺口，不改变现有行为（默认关闭限流）。

## 2. 方案

### 2.1 速率限制（核心，新文件）

**新文件 `backend/internal/api/http/ratelimit.go`**（server.go 已 672 行，不再增长）：

```go
type clientLimiter struct {
    lim      *rate.Limiter
    lastSeen time.Time
}

type rateLimiter struct {
    mu      sync.Mutex
    rpm     int
    clients map[string]*clientLimiter
}
```

- **算法**：`golang.org/x/time/rate`（**已在 go.mod v0.16.0**，零新依赖）令牌桶；`rate.Every(time.Minute/rpm)`，burst=rpm（允许短暂突发，长期受控；前端 SSE+轮询降级会并发请求，burst=1 会误伤）
- **键**：`X-Owner-ID` 优先，否则 `RemoteAddr` 的 host 部分（多用户同 IP 可区分；**不信任 X-Forwarded-For**，防伪造）
- **超限响应**：`writeErr(w, 429, "rate_limited", "too many requests", true)` + `Retry-After: <秒>`（向上取整）
- **内存防护**：客户端表上限 1024，超出按 `lastSeen` 最旧淘汰（复用 round-5 toolObs 的 map 防护思路；淘汰时 O(n) 扫描，发生频率低）
- **观测**：`observability` 增加 `rate_limited_total` expvar 计数（对齐第 2 轮 E 的指标风格）

**server.go 接线**：`Handler()` 改为

```go
return s.securityHeaders(s.auth(s.rateLimit(s.mux)))
```

- 顺序含义：**响应头最外层**（401/429 等错误路径也带安全头）；**401 先于 429**（rateLimit 在 auth 内侧，无效令牌不消耗预算）
- `rateLimit` 仅对 `/api/v1/` 前缀生效；`/healthz` `/readyz` `/metrics` 探针豁免
- `rpm<=0` 直接放行（中间件仍挂载，但零开销路径）

### 2.2 认证常量时间比较

`auth` 中间件中：

```go
token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
if s.cfg.AuthToken != "" && (!ok || subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.AuthToken)) != 1) {
    writeErr(w, 401, "unauthorized", "missing or invalid token", false)
    return
}
```

- `AuthToken == ""` 时本地模式行为不变（不校验）
- `ConstantTimeCompare` 长度不等时立即返回 0——仅泄漏长度信息，为标准可接受实践

### 2.3 安全响应头中间件

对所有响应（含错误）设置：

| 头 | 值 | 理由 |
|---|---|---|
| X-Content-Type-Options | nosniff | 防 MIME 嗅探 |
| X-Frame-Options | DENY | 防点击劫持 |
| Referrer-Policy | no-referrer | 不外泄 URL |
| Cache-Control | no-store | API 响应含用户数据，禁缓存 |

### 2.4 配置

- `Config` 增加字段：`RateLimitRPM int \`yaml:"rate_limit_rpm"\``（跟随后述 config.go 既有 yaml+env 模式）
- env：`AGENT_RATE_LIMIT_RPM`，**默认 0 = 关闭**（向后兼容；启用为显式行为，与 `http_fetch` 空白名单=禁用一致）
- 文档：`.env.example`、README.md / README.en.md 配置表各补一行

## 3. YAGNI（明确不做）

- 不做按路由差异化限流（单一全局 RPM 足够）
- 不做 IP 封禁/黑名单
- 不信任 X-Forwarded-For（反代后所有客户端共享 RemoteAddr 桶——由 X-Owner-ID 键兜底区分，风险已记录）
- 不做限流配置热更新
- 前端零改动

## 4. 测试计划（TDD）

| 层 | 文件 | 用例 |
|---|---|---|
| 配置 | config_test.go | `AGENT_RATE_LIMIT_RPM` 解析、默认 0 |
| 单元 | ratelimit_test.go | rpm=2 第 3 次拒；0=永不拒；owner 与 IP 分桶独立；>1024 客户端淘汰后表不增长 |
| HTTP 级 | server 包内测试 | 第 3 请求 429+`Retry-After`+body code=rate_limited；/healthz 豁免；安全头存在；auth 401/200 两路径；`rate_limited_total` 计数 |
| 回归 | 全量 | `go build/vet/test`、`make test-eval`（15 场景）、前端 tsc/vitest（零改动应全绿） |

## 5. 文件清单

- 修改：`backend/internal/config/config.go`、`config_test.go`
- 新建：`backend/internal/api/http/ratelimit.go`、`ratelimit_test.go`
- 修改：`backend/internal/api/http/server.go`（Handler 链、auth、securityHeaders）
- 修改：`backend/internal/observability/metrics.go`（+rate_limited_total）
- 修改：`.env.example`、`README.md`、`README.en.md`

## 6. 风险

| 风险 | 缓解 |
|---|---|
| 限流误伤 CI/脚本 | 默认 0=关闭；测试用显式 rpm |
| 反代后 RemoteAddr 归并 | X-Owner-ID 键优先；spec 记录，不做 XFF |
| 淘汰扫描 O(n) | 仅在 >1024 时触发，频率低 |
| 时间比较泄漏长度 | 标准实践，接受 |

## 7. 验收标准

1. 新增单测/HTTP 测试全绿（RED→GREEN 每任务）
2. `go build ./... && go vet ./...` 干净
3. `go test ./... -count=1` 全部包 ok，含 evals 15 场景
4. 前端 `npx tsc --noEmit` 0 错误、`npx vitest run` 全过（零前端改动）
5. 默认配置下行为与现状字节级一致（限流关闭、头新增但不改 body）
6. 文档三处同步（.env.example / README zh / README en）
