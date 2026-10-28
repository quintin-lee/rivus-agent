# Rivus Agent 前端

React 18 + Vite + TypeScript + Tailwind CSS + Zustand 的对话式界面，
经代理访问后端 REST / SSE 接口。

## 快速开始

```bash
npm install        # Docker 镜像构建使用 pnpm，本地开发用 npm 即可
npm run dev        # http://localhost:5173
```

开发服务器将 `/api`、`/healthz`、`/readyz` 代理到后端
（`VITE_BACKEND_URL` 环境变量可改指向，缺省 `http://localhost:8080`）。
后端启动见 [根 README](../README.md) 或 `docker compose up -d`。

## 常用命令

```bash
npm run build               # tsc && vite build → dist/
npm run preview             # 预览构建产物
npm run lint                # ESLint（v10 flat config）
npx tsc --noEmit            # 仅类型检查
npx vitest run              # 单次跑全部测试（26 文件 / 169 用例）
```

## 国际化（i18n）

内置极简双语机制：`src/i18n/index.ts` 词典 + `t(key, params?)`，语言态在
`ui-store`（`lang: 'zh' | 'en'`，默认 zh，localStorage `rivus-lang` 持久化），
Header 的「中/EN」按钮切换。en 缺键回退 zh，zh 缺键回退键名。不引入
react-i18next；已迁移会话列表/删除弹窗、记忆弹窗、运行历史弹窗与
`status.ts` 的用量摘要作为样例。

## Docker

镜像为多阶段构建（pnpm 构建 + nginx 托管），`start.sh` 用 `envsubst` 将
`RIVUS_BACKEND`（缺省 `http://localhost:8080`）渲染进 nginx 配置，
反代 `/api`、`/healthz`、`/readyz`、`/metrics`。`docker compose` 场景下
前端映射 `8081→8080`，`RIVUS_BACKEND=http://backend:8080` 由 compose 注入。

## 目录

```text
src/
  components/   # 对话、Run 卡片、审批卡片、会话记忆弹窗、子 agent 执行树、工具调用明细、流式打字指示器、终态失败重新运行、运行历史弹窗、主题切换（light/dark/system，无闪烁）、语言切换（中/EN）等
  store/        # Zustand 状态
  lib/          # API/SSE 客户端与工具函数
  App.tsx       # 根布局
```
