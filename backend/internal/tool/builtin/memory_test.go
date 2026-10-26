package builtin

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"rivus-agent-backend/internal/runtime"
	"rivus-agent-backend/internal/store"
	"rivus-agent-backend/internal/tool"
)

func newMemTools(t *testing.T) ([]tool.Definition, string) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ses, err := store.NewTaskRepo(db).CreateSession(context.Background(), "o1", "t")
	if err != nil {
		t.Fatal(err)
	}
	return MemoryTools(store.NewMemoryRepo(db)), ses
}

func callMem(t *testing.T, defs []tool.Definition, name, args string, tc *runtime.TaskContext) (string, error) {
	t.Helper()
	var def *tool.Definition
	for i := range defs {
		if defs[i].Name == name {
			def = &defs[i]
		}
	}
	if def == nil {
		t.Fatalf("tool %s not registered", name)
	}
	ctx := context.Background()
	if tc != nil {
		ctx = runtime.WithTaskContext(ctx, *tc)
	}
	return def.Handler(ctx, json.RawMessage(args))
}

func memTC(ses string) *runtime.TaskContext {
	return &runtime.TaskContext{OwnerID: "o1", SessionID: ses, ParentRunID: "run_1", Depth: 0}
}

func TestMemoryWriteRoundtrip(t *testing.T) {
	defs, ses := newMemTools(t)
	out, err := callMem(t, defs, "memory_write", `{"content":"用户偏好简洁回复"}`, memTC(ses))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "saved") {
		t.Fatalf("unexpected output %q", out)
	}
	got, err := callMem(t, defs, "memory_read", `{}`, memTC(ses))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "用户偏好简洁回复") {
		t.Fatalf("read back failed: %q", got)
	}
}

func TestMemoryWriteTooLarge(t *testing.T) {
	defs, ses := newMemTools(t)
	big := strings.Repeat("字", 2049) // > 2048 字节上限
	if _, err := callMem(t, defs, "memory_write", `{"content":"`+big+`"}`, memTC(ses)); err == nil {
		t.Fatal("oversized content must be rejected")
	}
}

func TestMemoryWriteRequiresTaskContext(t *testing.T) {
	defs, _ := newMemTools(t)
	if _, err := callMem(t, defs, "memory_write", `{"content":"x"}`, nil); err == nil {
		t.Fatal("missing task context must error")
	}
	if _, err := callMem(t, defs, "memory_read", `{}`, nil); err == nil {
		t.Fatal("missing task context must error")
	}
}

func TestMemoryReadQueryFilter(t *testing.T) {
	defs, ses := newMemTools(t)
	_, _ = callMem(t, defs, "memory_write", `{"content":"喜欢 Go 语言"}`, memTC(ses))
	_, _ = callMem(t, defs, "memory_write", `{"content":"讨厌冗长文档"}`, memTC(ses))
	got, err := callMem(t, defs, "memory_read", `{"query":"GO"}`, memTC(ses)) // 大小写不敏感
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "喜欢 Go 语言") || strings.Contains(got, "讨厌冗长文档") {
		t.Fatalf("query filter broken: %q", got)
	}
	// 无 query → 返回最近全部（≤20）
	all, err := callMem(t, defs, "memory_read", `{}`, memTC(ses))
	if err != nil || !strings.Contains(all, "讨厌冗长文档") {
		t.Fatalf("no-query read broken: %q err=%v", all, err)
	}
}
