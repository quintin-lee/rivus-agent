# 前端 lint 债（ESLint 10 flat config 迁移）— 设计文档

- **日期**: 2026-10-15
- **范围**: 纯前端工程。`frontend/eslint.config.js`（新建）、`frontend/.eslintrc.cjs`（删）、`frontend/package.json`（加 devDeps + 改 `lint` script）、`frontend/src/**`（仅修 lint 报出的问题，不重构）、`frontend/.github/workflows/ci.yml`（frontend job 加 lint step）。
- **状态**: 设计完成，**本轮搁置**。Blocker（spec review 实测）: 仓库 lockfile 钉 `typescript@7.0.2`，而 `typescript-eslint` 任一发行版 peer cap `TS<6.1`、且模块加载即抛 `does not support TS 7.0`（无发行版支持 TS≥7）；`eslint-plugin-react@7.37.5` 的 `settings.react.version:'detect'` 在 ESLint 10 下 `context.getFilename` 缺失而崩溃。本 spec 按现写法在 TS7 下**无法实施**——需先降级 `typescript@^6.0.3`（重验 tsc+vitest+vite build）或等 `typescript-eslint` 支持 TS7。3 份 ready spec（timed-out / approval-list / run-progress）先行进 plan。

## 背景 / 现状

- `package.json` 依赖 `eslint: ^10`（flat config 时代），但仓库只有 legacy `frontend/.eslintrc.cjs`（`extends: [eslint:recommended, plugin:react/recommended, plugin:@typescript-eslint/recommended]` + `@typescript-eslint/parser`）。ESLint 10 找不到 `eslint.config.*` 会直接报错（上轮已验证 `npm run lint` 挂）。
- `package.json` 的 `lint` script 是 `eslint src --ext ts,tsx`（`--ext` 是 legacy 参数，flat 下无此选项，会再报一次错）。
- CI（`ci.yml` frontend job）目前只跑 `tsc`/`test`/`vite build`，**不跑 lint**——所以 lint 坏了不影响 CI，是纯本地工程债。
- 现状缺失的 flat 依赖：`@eslint/js`（core JS rules）、`typescript-eslint`（flat helper + `tseslint.configs.recommended`）。仓库现有 `@typescript-eslint/{eslint-plugin,parser}`（v8）+ `eslint-plugin-react`。

## 改动

### 1. 加依赖（frontend）
```
npm i -D @eslint/js typescript-eslint
```
（`@eslint/js` 提供 JS recommended core rules；`typescript-eslint` 提供 `tseslint.config()` + `tseslint.configs.recommended`。二者优先于 legacy 的 `@typescript-eslint/*`——迁移后可选移除 `@typescript-eslint/eslint-plugin`/`@typescript-eslint/parser`，但本 spec 不强制删，留作清理债，避免 `npm prune` 影响其它引用。）

### 2. `frontend/eslint.config.js`（新建，官方 flat 写法）
```js
import { defineConfig } from 'eslint/config'
import js from '@eslint/js'
import react from 'eslint-plugin-react'
import tseslint from 'typescript-eslint'

export default defineConfig([
  { ignores: ['dist/**', 'node_modules/**', 'vite.config.ts'] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ['src/**/*.{ts,tsx}'],
    languageOptions: { ecmaVersion: 2022, sourceType: 'module' },
    plugins: { react },
    settings: { react: { version: 'detect' } },
    rules: { ...react.configs.recommended.rules },
  },
])
```
（`tseslint.configs.recommended` 已含 TS parser 接入 + TS recommended 规则；`eslint-plugin-react` 无官方 flat 导出，手动 flatten 其 `configs.recommended.rules`。`defineConfig` 来自 ESLint 10；若该版本未导出 `defineConfig`，退回 `export default [...][...tseslint.configs.recommended]` 纯数组写法——实现时二选一，以 `eslint.config.js` 能 `npm run lint` 跑通为准。）

### 3. 删 `frontend/.eslintrc.cjs`；改 `package.json`
- 删 `.eslintrc.cjs`（legacy 配置，迁移后无用）。
- `lint` script 从 `eslint src --ext ts,tsx` 改为 `eslint src`（去 `--ext`）。

### 4. 修 lint 报出的问题（范围闸）
跑 `npm run lint`，报出的问题按**最小改动**修：
- 真 bug / 明显坏味道（未用变量、`no-unused-vars`）→ 修。
- 噪声型规则（如 `no-explicit-any` 全库泛滥、`react/react-in-jsx-scope` 因新 JSX transform 已无谓）→ **降级为 `warn` 或 `off`**（在 `eslint.config.js` 的 `rules` 里加 override），**不做全库 `any` 重构**。YAGNI：目标是 lint 跑通且 CI 稳定，不是清零所有 rule。
- 原则：业务行为代码不动，除非 lint 硬错必须改。

### 5. CI frontend job 加 lint
`ci.yml` frontend job（`npm ci` 之后、`tsc` 附近）加：
```yaml
      - run: npm run lint
```
（放 `tsc` 前后均可；放 `tsc --noEmit` 之后、`vite build` 之前。）

## 明确不做（YAGNI）
- 不移除 legacy `@typescript-eslint/*` 依赖（迁移后可能冗余，但删它风险 > 收益，留作独立清理）。
- 不做全库 `any`→类型重构、不启用 `tseslint.configs.strict`（strict 规则过严，噪声大）。
- 不动 `vite.config.ts`/`postcss`/`tailwind` 等其它配置。

## 数据流 / 影响面
纯工程改动：`npm run lint` 从"报错挂掉"变为"跑通"；CI 新增一步 lint gate。业务代码行为零变化（`git diff` 里 src 改动仅限 lint 修复，无逻辑变更）。

## 错误 / 边界

| 场景 | 行为 |
|---|---|
| `defineConfig` 在 ESLint 10 未导出 | 实现退化为纯 `export default [...]` 数组（`tseslint.configs.recommended` 已是数组可展开） |
| `tseslint.configs.recommended` 报大量 `no-explicit-any` | 该规则降 `warn`，不阻塞 CI（CI lint 默认 warn 不挂） |
| 现有代码有 hard lint error（非 any） | 逐个最小修复，控制 diff 面 |

## 测试 / 验收
- `cd frontend && npm run lint` 跑通（exit 0；可能剩 warn）。
- `npm run build`（含 `tsc && vite build`）仍绿。
- `npx vitest run` 仍绿（lint 修复不应改行为）。
- CI 手动/实际：frontend job 含 lint step 且通过。
- `git diff`：src 改动仅为 lint 修复，无逻辑变更。
