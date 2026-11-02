# Round 30 设计：S3 接线落地（最大已知欠账）

日期：2026-11-01 ｜ 状态：已批准（设计 'ok'）

## 1. 背景

R24 前的外部 S3 集成入库（f9cadac + 2430d99 修复）留下了**未接线的死代码**：

- `backend/internal/s3/s3client.go`：`NewClient(ctx)` 直接读 9 个 `AGENT_S3_*` 环境变量，绕过 `config.Config` 单一事实源；`Bucket` 字段捕获后从未使用。
- `backend/internal/tool/builtin/s3.go`：`SetS3Client` 无任何调用点；`S3Tools` 三个 handler（s3_read/s3_write/s3_list）全是占位符，返回 "implementation pending minio API compatibility" 成功提示。
- `app.go` 从未注册 S3Tools；`.env.example` / README 0 条 S3 行。

本批把这条路径完整落地，空配置时零回归。

## 2. 方案

### 2.1 s3client.go：配置单一事实源

- `NewClient(cfg config.Config)` 取代裸 env 读取（env 已由 `applyEnv` 灌入 cfg，不再直接 `os.Getenv`）。
- 新增 `func (c *Client) Enabled() bool`（endpoint 非空即启用）。
- `Bucket` 字段真正用于 handler（见 2.2）。

### 2.2 builtin/s3.go：占位符 → 真实 minio-go v7 调用

- **s3_read** `{key}`：`GetObject` + `io.ReadAll` 按 MaxResultBytes（32KB）截断，超长追加 `...[truncated]`；key 前置 `cfg.S3WorkspacePrefix`。
- **s3_write** `{key, content}`：`PutObject`，返回 `bytes_written`；key 前置 `cfg.S3WorkspacePrefix`。
- **s3_list** `{prefix?, limit?}`：`ListObjects`（Recursive + Prefix），limit 默认 50、上限 200，返回 `{key, size, last_modified}` 列表；key 前置 `cfg.S3KnowledgePrefix`。
- 三个 handler 在 `s3Client == nil` 时仍返回 `s3 client not initialized` 错误路径（保持现有行为）。
- 注意：handler 闭包需拿到 workspace/knowledge 前缀 —— `SetS3Client` 扩展为携带前缀信息（例如 `SetS3Client(c *s3.Client, workspacePrefix, knowledgePrefix string)` 或 Client 内嵌前缀；以最小改动为准，执行时定）。

### 2.3 app.go：条件接线

```go
if cfg.S3Endpoint != "" {
    if c, err := s3.NewClient(cfg); err == nil {
        builtin.SetS3Client(c)
        register S3Tools into registry
    }
}
```

`S3Endpoint == ""`（默认）→ 不建 client、不注册工具，现有 19 pkgs 测试零回归。

### 2.4 测试（TDD，无真实 S3）

- `s3client_test.go`：`NewClient` 假 endpoint 构建成功 + `Enabled()` 逻辑 + 空 endpoint → 不启用。
- `s3_handler_test.go`：nil client ×3 → "not initialized"；read 截断（超长内容 → 32KB + suffix）；write 返回 bytes_written；list 默认 limit 50。
- `app_wiring_test.go`：`S3Endpoint=""` → registry 无 S3Tools；`=fake` → 有。

### 2.5 文档

- `.env.example` += S3 段：`AGENT_S3_ENDPOINT / ACCESS_KEY_ID / SECRET_ACCESS_KEY（env-only，不落 yaml）/ USE_SSL / REGION / BUCKET / WORKSPACE_PREFIX / KNOWLEDGE_PREFIX`。
- `README.md` + `README.en.md` features 列表 += S3 工具条目（s3_read/write/list，未配置 endpoint 时不启用）。

## 3. YAGNI

- 不做：S3 鉴权细化（IAM/STS）、presigned URLs、multipart 上传、超出 max_bytes 的大对象流式传输、S3 事件通知。

## 4. 验收

1. `go build ./... && go vet ./...` 通过。
2. `go test ./... -count=1` 19 pkgs 全绿（含新增 s3 测试）。
3. `make test-eval` 42 场景 pass_rate 1。
4. 前端 `npx tsc --noEmit && npx vitest run` 零漂移（28 files / 198 tests）。
5. `grep -i s3 .env.example README.md README.en.md` 命中。
6. 新测试 RED→GREEN 全程留痕。
7. 空配置回归：`S3Endpoint=""` 下 `S3Tools` 不在 registry（app_wiring_test 覆盖）。

## 5. 风险

- minio-go v7 与 modernc/x 生态依赖已入库（v7.4.1 + crc64nvme/md5-simd/ini.v1），无新顶层依赖。
- handler 闭包前缀传递方式（SetS3Client 签名 vs Client 内嵌）执行时按最小改动定，记入 §6。

## 6. 执行偏差

（执行后回写）
