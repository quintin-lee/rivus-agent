# Round 11 批1 质量门禁 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `npm run lint`（eslint v10 flat config）与后端 golangci-lint 成为真实可跑的门禁并接入 CI。

**Architecture:** 前端将 legacy `.eslintrc.cjs` 语义（eslint:recommended + react/recommended + @typescript-eslint/recommended）迁移为 `eslint.config.js` flat config（仅用已安装的插件包，零新依赖优先）；后端新增最小 `.golangci.yml`（v2 格式）+ CI lint job。

**Tech Stack:** eslint ^10.12.0、@typescript-eslint/* ^8.71.1、eslint-plugin-react ^7.37.5、golangci-lint v2。

---

### Task 1: eslint flat config 迁移（修复破损的 lint）

**Files:**
- Create: `frontend/eslint.config.js`
- Delete: `frontend/.eslintrc.cjs`
- Modify: `frontend/package.json`（lint script 去掉 `--ext`，flat config 不支持）

- [ ] **Step 1: 探测 @eslint/js 可用性**

```bash
ls frontend/node_modules/@eslint/js/package.json 2>/dev/null && echo HAS_JS || echo NO_JS
```
- HAS_JS → config 用 `import js from '@eslint/js'` + `js.configs.recommended`。
- NO_JS → 先试 `npm install -D @eslint/js`（工作目录 frontend）；网络失败 → 用下方 fallback（无 core recommended，靠 ts/react 规则集），并在 spec 偏差记录。

- [ ] **Step 2: 写 eslint.config.js**

```js
import js from '@eslint/js'; // NO_JS 时删除此行与 spread
import tsPlugin from '@typescript-eslint/eslint-plugin';
import tsParser from '@typescript-eslint/parser';
import reactPlugin from 'eslint-plugin-react';
import globals from 'globals';

export default [
  { ignores: ['dist/**', 'node_modules/**', 'coverage/**'] },
  {
    files: ['src/**/*.{ts,tsx}'],
    languageOptions: {
      parser: tsParser,
      ecmaVersion: 'latest',
      sourceType: 'module',
      globals: { ...globals.browser },
    },
    settings: { react: { version: 'detect' } },
    plugins: { react: reactPlugin, '@typescript-eslint': tsPlugin },
    rules: {
      ...js.configs.recommended.rules, // NO_JS 时删除
      ...reactPlugin.configs.recommended.rules,
      ...tsPlugin.configs.recommended.rules,
      'no-undef': 'off', // TS 自查；避免 browser globals 误报
    },
  },
];
```
预期常见噪音与处置：`react/react-in-jsx-scope`（React17+ 自动跳过；若报→off）；`@typescript-eslint/no-unused-vars`（修代码）；`react/no-unescaped-entities`（按报错修）。

- [ ] **Step 3: package.json lint script**

`"lint": "eslint src"`（`--ext` 在 flat config 下报错，必删）。

- [ ] **Step 4: 运行 lint 并修复全部报错**

```bash
cd frontend && npm run lint
```
Expected: 反复修至 exit 0。禁止 `eslint-disable` 滥用（仅限确有理由处，须注释理由）。

- [ ] **Step 5: 回归**

```bash
cd frontend && npx tsc --noEmit && npx vitest run
```
Expected: tsc 0 错误；25 files/150 tests 全绿。

- [ ] **Step 6: Commit**

```bash
git add frontend/eslint.config.js frontend/.eslintrc.cjs frontend/package.json frontend/package-lock.json frontend/src
git commit -m "chore(frontend): migrate eslint to flat config (v10), make lint gate real"
```
（.eslintrc.cjs 若存在改动记录则 `git rm --cached`；实际删除文件。）

### Task 2: golangci-lint 配置 + CI lint job + frontend CI lint 步骤

**Files:**
- Create: `backend/.golangci.yml`
- Modify: `.github/workflows/ci.yml`

- [ ] **Step 1: 写 .golangci.yml（v2 格式）**

```yaml
version: "2"

formatters:
  enable:
    - goimports
```
（v2 默认 linters = errcheck/govet/ineffassign/staticcheck/unused；goimports 在 v2 归入 formatters。）

- [ ] **Step 2: 本地尝试验证（允许降级）**

```bash
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest && $(go env GOPATH)/bin/golangci-lint run ./... 
```
（工作目录 backend；timeout 300s）
- 成功 → 修报错（只调配置/exclude，不动业务逻辑；超出预期记偏差）。
- 网络失败 → 降级：本地以 go vet 为准，CI 仍加 lint job（云端拉取），spec 偏差回写。

- [ ] **Step 3: ci.yml 三处修改**

1. frontend job `npm ci` 后、tsc 前插 `- run: npm run lint`。
2. 新增 lint job：

```yaml
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.27'
          cache: true
      - uses: golangci/golangci-lint-action@v7
        with:
          working-directory: backend
```

- [ ] **Step 4: 验证**

```bash
python3 -c "import yaml;yaml.safe_load(open('.github/workflows/ci.yml'));print('YAML_OK')"
grep -c "npm run lint" .github/workflows/ci.yml
grep -c "golangci" .github/workflows/ci.yml
```
Expected: YAML_OK / ≥1 / ≥2。

- [ ] **Step 5: 全量回归 + Commit**

```bash
cd backend && go build ./... && go vet ./... && go test ./... -count=1 -timeout 600s
```
Expected: 全绿（若本地装上了 golangci，另跑 golangci run 确认 0 issues）。

```bash
git add backend/.golangci.yml .github/workflows/ci.yml docs/superpowers/specs/2026-10-27-round11-quality-theme-db-design.md
git commit -m "ci(lint): golangci-lint job + frontend lint step in CI"
```

---

## Self-review notes

- eslint v10 + typescript-eslint 8.x 兼容性：flat config API 稳定，插件运行时兼容（peer 警告不阻塞）；若真不兼容，降级方案 = 升级 typescript-eslint（新依赖允许）并记偏差。
- `globals` 包是 eslint 自身依赖，node_modules 必有，可安全 import。
- golangci 本地安装需网络；失败不影响交付（CI 接管），偏差记录路径已在 Task 2。
- 不加 react-hooks/react-refresh 插件（原 config 就没有；新依赖且非本次目标）。
