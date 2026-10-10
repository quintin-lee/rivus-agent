# 模型全局设置页设计文档

- 文档版本：v1.0
- 日期：2026-10-10
- 目标：页面可配模型四件套，新提交的 Run 立即生效

---

## 1. 决策

- 粒度：全局设置页（非随 Run、非只读展示）。
- 范围：仅模型四件套（provider/base_url/model/api_key）。
- 生效：新建 Run 时实时读库，无需重启；在途 Run 不受影响。
- 密钥：写-only，GET 只返回是否已设；明文存 SQLite（与 checkpoint 同级保护）。

## 2. 后端

- 新表 `server_settings(key TEXT PK, value TEXT, updated_at)`，存 `model.provider/model.base_url/model.name/model.api_key` 四个 key。
- `GET /api/v1/settings/model` → `{provider, base_url, model, api_key_set, updated_at}`。
- `PUT /api/v1/settings/model`，`api_key` 为空表示保持原值；`provider` 仅接受 `openai_compat`，`base_url/model` 非空。
- `app.go` 的 modelFactory 改为每次调用先读库、缺失回退启动配置。

## 3. 前端

- Header 右侧齿轮按钮开 Dialog：供应商下拉（仅 `openai_compat`）、BaseURL、模型名、API Key（password + 已设置/未设置徽标）。
- `api.ts` 加 `getModelSettings/updateModelSettings`，`types.ts` 加 `ModelSettings`。
- 成功提示“新提交的 Run 生效”，失败显示后端错误原文。

## 4. 验收

- 空库 GET 返回环境变量默认值且 `api_key_set=false`；PUT 后 GET 生效、key 永不回传。
- 新 Run 使用新模型配置（后端集成测试覆盖）；在途 Run 不受影响。
- 前端 `tsc` + vitest 通过。

## 5. 非目标

- 预算/并发等其他设置项；多供应商；密钥加密存储；按 Run 选模型。
