# Round 13 批1（CI 加固）实现计划

> **For agentic workers:** REQUIRED: Use superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 给两个 workflow 加 concurrency 组并给 eval-report 产物设保留期。

**Architecture:** 纯 YAML 修改：ci.yml 按 ref 取消陈旧运行，release.yml 全局排队单例，evals 产物 14 天保留。

**Tech Stack:** GitHub Actions。

---

### Task 1: ci.yml concurrency + retention

**Files:**
- Modify: `.github/workflows/ci.yml`

- [ ] **Step 1: 修改 ci.yml**

在 `on:` 块之后、`jobs:` 之前插入：

```yaml
concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: true
```

在 `Upload eval report` step 的 `with:` 块 `if-no-files-found: warn` 行后追加：

```yaml
          retention-days: 14
```

- [ ] **Step 2: 验证**

```bash
python3 -c "import yaml;yaml.safe_load(open('.github/workflows/ci.yml'))" && echo YAML_OK
grep -c "ci-\${{ github.ref }}" .github/workflows/ci.yml   # 期望 1
grep -c "retention-days: 14" .github/workflows/ci.yml      # 期望 1
grep -c "cancel-in-progress: true" .github/workflows/ci.yml # 期望 1
```

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: per-ref concurrency group and eval-report retention"
```

### Task 2: release.yml concurrency

**Files:**
- Modify: `.github/workflows/release.yml`

- [ ] **Step 1: 修改 release.yml**

在 `permissions:` 块之后、`jobs:` 之前插入：

```yaml
concurrency:
  group: release
  cancel-in-progress: false
```

- [ ] **Step 2: 验证**

```bash
python3 -c "import yaml;yaml.safe_load(open('.github/workflows/release.yml'))" && echo YAML_OK
grep -c "group: release" .github/workflows/release.yml          # 期望 1
grep -c "cancel-in-progress: false" .github/workflows/release.yml # 期望 1
```

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/release.yml
git commit -m "ci: serialize release runs via queued concurrency group"
```

## Self-review notes

- `cancel-in-progress: false` 是显式写法（默认即 false），保留以表达"发布排队不取消"的意图。
- `ci-${{ github.ref }}` 中 `$` 需在 grep 断言中转义，计划内已用 `\${{ }}`。
- 无代码改动 → 无 go/frontend 回归需求；执行后跑一次 `git status` 确认无多余文件。
