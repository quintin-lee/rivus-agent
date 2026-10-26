# 批2 Skill 增强 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** invoke_skill 支持三层增强 — frontmatter 元数据扩展（when_to_use / tools）、references/ 多文件按需加载、运行时 input 注入。

**Architecture:** 全部改动限于 `backend/internal/skill` 包（loader.go 解析与引用读取、skill_tool.go 工具 schema/handler）+ example skill 内容。Loader 新增 ListReferences/ReadReference 两个方法（按需读盘，不进缓存）；invoke_skill schema 增加可选 `reference`/`input` 参数，向后兼容（旧调用 `{"name":"x"}` 行为不变）。无新依赖、无 API/DB 变更。

**Tech Stack:** Go 1.27 stdlib（os/filepath/strings/encoding/json）；测试 `go test`，与现有 skill_tool_test.go 同风格（t.TempDir + 断言）。

**Spec:** docs/superpowers/specs/2026-10-26-agent-capabilities-design.md §批2

---

## 已验证事实（写代码前不要再猜）

- `loader.go:10-15` Meta{Name,Description,Version,Path}，无 json tag；List() 只被 `app/toolconfig.go:135 skillListing` 消费（只用 Name/Description）→ 新增字段零影响。
- `parseMeta` 现逻辑：逐行 TrimSpace+TrimPrefix("#")，前缀匹配 description:/version:（大小写不敏感），**两者都非空即 break**。
- `skill_tool.go` InvokeSkill(l, maxBytes)：schema 仅 name required；handler body 截断 maxBytes；`MaxResultBytes: 64*1024`（0 会让 executor 清空非空结果，勿动）。
- app.go:103 `registry.Register(skill.InvokeSkill(a.skillLoader, 32*1024))` — 签名不变。
- `validator.go` Validate(name, body)：name 禁 `..`/`/`，body ≤64KB，危险内容正则。references 文件不经过 Validate（只读，不注册为 skill）。
- 现有测试 skill_tool_test.go：TestInvokeSkill 用 t.TempDir + "demo/SKILL.md"（body 含空行分隔），`InvokeSkill(l, 0)` 全量、`InvokeSkill(l, 5)` 截断 len==5 — 截断语义必须保留。
- skill 目录：`backend/skills/example/SKILL.md`（description+version 两行）。

---

### Task 1: parseMeta frontmatter 扩展（when_to_use / tools）

**Files:**
- Modify: `backend/internal/skill/loader.go`（Meta 结构 + parseMeta）
- Test: `backend/internal/skill/loader_test.go`（新建）

- [ ] **Step 1: 写失败测试** — 新建 `backend/internal/skill/loader_test.go`：

```go
package skill

import (
	"reflect"
	"testing"
)

func TestParseMetaAllFields(t *testing.T) {
	m := parseMeta("demo", "# demo\nwhen_to_use: fix build errors\nversion: 2\ntools: file_edit, http_fetch\ndescription: does X\n\nbody")
	if m.WhenToUse != "fix build errors" || m.Version != "2" || m.Description != "does X" {
		t.Fatalf("scalars wrong: %+v", m)
	}
	if !reflect.DeepEqual(m.Tools, []string{"file_edit", "http_fetch"}) {
		t.Fatalf("tools = %v", m.Tools)
	}
}

func TestParseMetaBlankLineStops(t *testing.T) {
	// 首个空行后的正文出现 description: 不得污染元数据
	m := parseMeta("demo", "# demo\ndescription: real\nversion: 1\n\nlater description: fake\nwhen_to_use: fake")
	if m.Description != "real" || m.WhenToUse != "" {
		t.Fatalf("blank line must stop parsing: %+v", m)
	}
}

func TestParseMetaMissingFieldsZero(t *testing.T) {
	m := parseMeta("demo", "# demo\ndescription: only desc\n")
	if m.Version != "" || m.WhenToUse != "" || len(m.Tools) != 0 {
		t.Fatalf("missing fields must be zero: %+v", m)
	}
}

func TestParseMetaToolsEmptyAndSpaces(t *testing.T) {
	m := parseMeta("demo", "tools:  a , ,b \n")
	if !reflect.DeepEqual(m.Tools, []string{"a", "b"}) {
		t.Fatalf("tools = %q", m.Tools)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd backend && go test ./internal/skill/ -run TestParseMeta -v`
Expected: FAIL（编译错误 `m.WhenToUse undefined`）

- [ ] **Step 3: 实现** — loader.go 两处改动：

Meta 结构加字段（保持 Path 最后）：

```go
type Meta struct {
	Name        string
	Description string
	Version     string
	WhenToUse   string
	Tools       []string
	Path        string
}
```

parseMeta 整体替换为（**停止条件从"desc+version 齐了就 break"改为"首个空行停"**，防止正文假阳性；无空行则扫全文件，行为对现网 example skill 兼容）：

```go
func parseMeta(name, body string) Meta {
	m := Meta{Name: name}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			break // frontmatter 区结束
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "description:"):
			m.Description = strings.TrimSpace(line[len("description:"):])
		case strings.HasPrefix(lower, "version:"):
			m.Version = strings.TrimSpace(line[len("version:"):])
		case strings.HasPrefix(lower, "when_to_use:"):
			m.WhenToUse = strings.TrimSpace(line[len("when_to_use:"):])
		case strings.HasPrefix(lower, "tools:"):
			m.Tools = splitCSV(line[len("tools:"):])
		}
	}
	return m
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd backend && go test ./internal/skill/ -v`
Expected: 新 4 个 PASS + 既有 TestInvokeSkill PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/skill/loader.go backend/internal/skill/loader_test.go
git commit -m "feat(skill): parse when_to_use and tools frontmatter, stop at blank line"
```

---

### Task 2: Loader references 读取（ListReferences / ReadReference）

**Files:**
- Modify: `backend/internal/skill/loader.go`
- Test: `backend/internal/skill/loader_test.go`

- [ ] **Step 1: 写失败测试** — 追加到 loader_test.go：

```go
func TestListAndReadReferences(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("demo/SKILL.md", "description: d\nversion: 1\n")
	write("demo/references/b.md", "REF B")
	write("demo/references/a.md", "REF A")
	write("demo/references/notes.txt", "not md") // 非 .md 也会列出?——设计：只列 .md
	l := NewLoader(dir)
	if err := l.Refresh(); err != nil {
		t.Fatal(err)
	}

	refs, err := l.ListReferences("demo")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(refs, []string{"a.md", "b.md"}) {
		t.Fatalf("refs = %v (want sorted md-only)", refs)
	}

	got, err := l.ReadReference("demo", "a.md")
	if err != nil || got != "REF A" {
		t.Fatalf("read ref = %q, %v", got, err)
	}

	// 路径逃逸拒绝
	if _, err := l.ReadReference("demo", "../SKILL.md"); err == nil {
		t.Fatal("path escape must be rejected")
	}
	if _, err := l.ReadReference("demo", "/etc/passwd"); err == nil {
		t.Fatal("absolute path must be rejected")
	}
	// 未知 skill / 未知引用
	if _, err := l.ListReferences("nope"); err == nil {
		t.Fatal("unknown skill must error")
	}
	if _, err := l.ReadReference("demo", "missing.md"); err == nil {
		t.Fatal("missing reference must error")
	}
	// 无 references 目录
	write("plain/SKILL.md", "description: p\n")
	if _, err := l.ListReferences("plain"); err == nil {
		t.Fatal("missing references dir must error")
	}
}
```

（loader_test.go 需补 import "os"、"path/filepath"。）

- [ ] **Step 2: 跑测试确认失败**

Run: `cd backend && go test ./internal/skill/ -run TestListAndReadReferences -v`
Expected: FAIL（`l.ListReferences undefined`）

- [ ] **Step 3: 实现** — loader.go 追加（Refresh 已把 m.Path 存进 metas；按需读盘、不进缓存）：

```go
// ListReferences 返回 skill 的 references/ 目录下的 .md 文件名（排序）。
func (l *Loader) ListReferences(name string) ([]string, error) {
	dir, err := l.referencesDir(name)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("skill %q has no references: %w", name, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// ReadReference 读取 references/<ref>，ref 必须是目录内的普通文件名，内容上限 16KB。
func (l *Loader) ReadReference(name, ref string) (string, error) {
	dir, err := l.referencesDir(name)
	if err != nil {
		return "", err
	}
	if ref == "" || strings.ContainsAny(ref, `/\`) || filepath.IsAbs(ref) {
		return "", fmt.Errorf("invalid reference name %q", ref)
	}
	full := filepath.Clean(filepath.Join(dir, ref))
	if !strings.HasPrefix(full, dir+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid reference name %q", ref)
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return "", fmt.Errorf("read reference %q: %w", ref, err)
	}
	const refCap = 16 * 1024
	if len(b) > refCap {
		b = b[:refCap]
	}
	return string(b), nil
}

func (l *Loader) referencesDir(name string) (string, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, m := range l.metas {
		if m.Name == name {
			return filepath.Join(filepath.Dir(m.Path), "references"), nil
		}
	}
	return "", fmt.Errorf("unknown or unavailable skill %q", name)
}
```

loader.go import 补 `"fmt"`、`"sort"`。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd backend && go test ./internal/skill/ -v`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add backend/internal/skill/loader.go backend/internal/skill/loader_test.go
git commit -m "feat(skill): list and read skill reference files with path guard"
```

---

### Task 3: invoke_skill 扩展（reference 参数 + input 注入 + 引用索引）

**Files:**
- Modify: `backend/internal/skill/skill_tool.go`
- Test: `backend/internal/skill/skill_tool_test.go`

- [ ] **Step 1: 写失败测试** — 追加到 skill_tool_test.go：

```go
func setupSkillWithRefs(t *testing.T) (*Loader, string) {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("demo/SKILL.md", "# demo\ndescription: does X\nversion: 1\n\nSKILL BODY here\n")
	write("demo/references/api.md", "API REFERENCE")
	l := NewLoader(dir)
	if err := l.Refresh(); err != nil {
		t.Fatal(err)
	}
	return l, dir
}

func TestInvokeSkillReferenceIndexAndInput(t *testing.T) {
	l, _ := setupSkillWithRefs(t)
	def := InvokeSkill(l, 0)

	// 默认：body + 引用索引
	out, err := def.Handler(context.Background(), json.RawMessage(`{"name":"demo"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "SKILL BODY here") || !strings.Contains(out, "## References") || !strings.Contains(out, "api.md") {
		t.Fatalf("default output missing body/index: %q", out)
	}

	// input 注入：body 之后追加 ## 运行时输入 JSON 块
	out, err = def.Handler(context.Background(), json.RawMessage(`{"name":"demo","input":{"k":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "## 运行时输入") || !strings.Contains(out, `"k":1`) {
		t.Fatalf("input block missing: %q", out)
	}
	// input 非对象 → 报错
	if _, err := def.Handler(context.Background(), json.RawMessage(`{"name":"demo","input":"str"}`)); err == nil {
		t.Fatal("non-object input must error")
	}
}

func TestInvokeSkillReferenceLoad(t *testing.T) {
	l, _ := setupSkillWithRefs(t)
	def := InvokeSkill(l, 0)

	out, err := def.Handler(context.Background(), json.RawMessage(`{"name":"demo","reference":"api.md"}`))
	if err != nil || out != "API REFERENCE" {
		t.Fatalf("reference load = %q, %v", out, err)
	}
	if _, err := def.Handler(context.Background(), json.RawMessage(`{"name":"demo","reference":"../SKILL.md"}`)); err == nil {
		t.Fatal("reference escape must error")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd backend && go test ./internal/skill/ -run 'TestInvokeSkill(Reference|Input)' -v`
Expected: FAIL（默认输出无 `## References`；input 被忽略）

- [ ] **Step 3: 实现** — skill_tool.go 整体替换为：

```go
package skill

import (
	"context"
	"encoding/json"
	"fmt"

	"rivus-agent-backend/internal/tool"
)

// InvokeSkill builds the `invoke_skill` tool.Definition backed by the Loader.
// Risk=read, Approval=never, RequiredScopes empty. Args:
//
//	{"name":"<skill>", "reference":"<file.md>"(optional), "input":{...}(optional)}
//
// 无 reference：返回 SKILL.md 正文（截断到 maxBytes）+ references/ 索引 + input 注入块。
// 有 reference：返回该引用文件内容（路径守卫 + 16KB 上限见 Loader.ReadReference）。
func InvokeSkill(l *Loader, maxBytes int) tool.Definition {
	return tool.Definition{
		Name: "invoke_skill",
		Description: "Load a skill's instructions as context. Args: {\"name\":\"<skill>\",\"reference\":\"<file.md>\",\"input\":{...}}. " +
			"reference loads a file from the skill's references/; input appends runtime context.",
		Version:     "v2",
		InputSchema: `{"type":"object","properties":{"name":{"type":"string"},"reference":{"type":"string"},"input":{"type":"object"}},"required":["name"]}`,
		Risk:        tool.RiskRead,
		Approval:    tool.ApprovalNever,
		MaxResultBytes: 64 * 1024, // matches the mcp bridge cap; 0 would make the executor wipe non-empty results
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Name      string          `json:"name"`
				Reference string          `json:"reference"`
				Input     json.RawMessage `json:"input"`
			}
			if err := json.Unmarshal(args, &in); err != nil || in.Name == "" {
				return "", fmt.Errorf("invoke_skill: invalid args")
			}

			// 引用模式：直接返回引用文件
			if in.Reference != "" {
				return l.ReadReference(in.Name, in.Reference)
			}

			body, ok := l.Get(in.Name)
			if !ok {
				return "", fmt.Errorf("unknown or unavailable skill %q", in.Name)
			}
			if maxBytes > 0 && len(body) > maxBytes {
				body = body[:maxBytes]
			}
			out := body

			// input 注入：不做模板替换，原文追加 JSON 块
			if len(in.Input) > 0 {
				var probe map[string]any
				if err := json.Unmarshal(in.Input, &probe); err != nil {
					return "", fmt.Errorf("invoke_skill: input must be an object")
				}
				out += "\n\n## 运行时输入\n```json\n" + string(in.Input) + "\n```"
			}

			// 引用索引（无 references 目录时省略）
			if refs, err := l.ListReferences(in.Name); err == nil && len(refs) > 0 {
				out += "\n\n## References\n"
				for _, r := range refs {
					out += "- " + r + "\n"
				}
			}
			return out, nil
		},
	}
}
```

注意：既有 TestInvokeSkill 的 `InvokeSkill(l, 5)` 截断断言 len==5 仍成立（demo 无 references、无 input，out==截断 body）；`Version` v1→v2 仅描述性。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd backend && go test ./internal/skill/ -v`
Expected: 全部 PASS（含既有 TestInvokeSkill）

- [ ] **Step 5: Commit**

```bash
git add backend/internal/skill/skill_tool.go backend/internal/skill/skill_tool_test.go
git commit -m "feat(skill): invoke_skill reference loading, input injection, reference index"
```

---

### Task 4: example skill 内容更新（frontmatter + references 示例）

**Files:**
- Modify: `backend/skills/example/SKILL.md`
- Create: `backend/skills/example/references/cheatsheet.md`

- [ ] **Step 1: 更新 SKILL.md** — 在 description 行后加 when_to_use / tools 两行（保持空行分隔正文）：

```markdown
# example
description: 示例技能：演示 SKILL.md 元数据与正文结构
when_to_use: 需要演示技能机制或验证 invoke_skill 行为时
version: 1

（原正文保持不变）
```

（以文件实际内容为准，只插入两行，不动正文。）

- [ ] **Step 2: 新建 references/cheatsheet.md**：

```markdown
# example 技能速查

- SKILL.md 首段（空行前）为 frontmatter：description / when_to_use / version / tools
- references/ 下的 .md 文件可通过 invoke_skill 的 reference 参数按需加载
- input 参数以 JSON 块形式追加到正文末尾
```

- [ ] **Step 3: 验证 example 能被加载并列出引用**

Run: `cd backend && go test ./... -count=1`
Expected: 全部 PASS（example 属于仓库数据，不影响单测；此步为全量回归）

- [ ] **Step 4: Commit**

```bash
git add backend/skills/example/SKILL.md backend/skills/example/references/cheatsheet.md
git commit -m "docs(skills): example skill frontmatter fields and reference file"
```

---

### Task 5: 验收（DoD）

- [ ] **Step 1: 后端全量**

Run: `cd backend && go build ./... && go vet ./... && go test ./... -count=1 -timeout 600s`
Expected: 全部 ok

- [ ] **Step 2: 前端回归（零改动预期）**

Run: `cd frontend && npx tsc --noEmit && npx vitest run`
Expected: 0 错误；测试全过

- [ ] **Step 3: 对照 spec 验收项（批2）**

- invoke_skill 旧调用 `{"name":"x"}` 行为兼容（返回 body，若有引用则带索引）✓ 由 TestInvokeSkill + TestInvokeSkillReferenceIndexAndInput 覆盖
- reference 路径逃逸被拒 ✓ TestListAndReadReferences + TestInvokeSkillReferenceLoad
- input 非对象拒绝、对象原样注入 ✓ TestInvokeSkillReferenceIndexAndInput
- 无新依赖、无公开 API/DB 变更 ✓（仅 tool 输出内容增强）

- [ ] **Step 4: git 状态收尾**

Run: `git log --oneline -6 && git status --short`
Expected: 4 个新提交（Task 1-4）；工作区干净（.omo/ 除外）
