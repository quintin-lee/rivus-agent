package tool

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRegistryMutate(t *testing.T) {
	r := NewRegistry()
	reg := func(name string) {
		h := func(ctx context.Context, args json.RawMessage) (string, error) { return "", nil }
		if err := r.Register(Definition{Name: name, Handler: h}); err != nil {
			t.Fatal(err)
		}
	}
	reg("builtin")
	reg("mcp__fs__read")
	reg("mcp__fs__write")
	reg("mcp__git__status")

	// exact-name remove
	if !r.Remove("mcp__git__status") {
		t.Fatal("Remove(mcp__git__status) should report present")
	}
	if _, ok := r.Get("mcp__git__status"); ok {
		t.Fatal("mcp__git__status should be gone")
	}
	if r.Remove("mcp__git__status") {
		t.Fatal("second Remove must report not-present")
	}

	// prefix remove returns count
	if n := r.RemoveByPrefix("mcp__"); n != 2 {
		t.Fatalf("RemoveByPrefix(mcp__) = %d, want 2", n)
	}

	// Replace clears a prefix then re-adds (through Register's normalization)
	if err := r.Replace("mcp__", []Definition{
		{Name: "mcp__fs__read", Handler: func(ctx context.Context, args json.RawMessage) (string, error) { return "", nil }},
	}); err != nil {
		t.Fatal(err)
	}
	if got := len(r.List()); got != 2 { // builtin + mcp__fs__read
		t.Fatalf("List len = %d, want 2", got)
	}
	if _, ok := r.Get("mcp__fs__write"); ok {
		t.Fatal("stale mcp__fs__write must be gone after Replace")
	}
}

// TestReplaceReturnsRegisterError 校验 Replace 的「返回首次注册错误」契约。
func TestReplaceReturnsRegisterError(t *testing.T) {
	r := NewRegistry()
	// 一个空 Name 的 Definition 会被 Register 拒绝（registry.go 校验 Name 非空）。
	// 即便 defs 里混入一个合法项，Replace 也会先按序处理——空 Name 项返回错误。
	err := r.Replace("mcp__", []Definition{
		{Name: "mcp__fs__read", Handler: func(ctx context.Context, args json.RawMessage) (string, error) { return "", nil }},
		{Name: "", Handler: func(ctx context.Context, args json.RawMessage) (string, error) { return "", nil }},
	})
	if err == nil {
		t.Fatal("Replace with an invalid (empty-name) Definition must return Register's error")
	}
	// 已注册的前一项保留，空名项未加入。
	if _, ok := r.Get("mcp__fs__read"); !ok {
		t.Fatal("valid def registered before the error must remain")
	}
}
