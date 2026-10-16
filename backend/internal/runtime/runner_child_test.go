package runtime

import (
	"testing"
)

// containsStr 小助手。
func containsStr(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}

// filterToolNames：nil 输入保持 nil（全量交给 buildAgentFiltered 处理，不能在此折叠成空切片）；
// 非 nil 则只保留命中项；depth>=1 剔除 "task"。
func TestFilterToolNames(t *testing.T) {
	if got := filterToolNames(nil, 1); got != nil {
		t.Fatalf("nil input must stay nil (caller treats nil as full), got %v", got)
	}
	got := filterToolNames([]string{"file_read", "task", "knowledge_search"}, 1)
	if containsStr(got, "task") {
		t.Fatalf("depth1 must strip task, got %v", got)
	}
	if !containsStr(got, "file_read") || !containsStr(got, "knowledge_search") {
		t.Fatalf("kept read-only tools expected, got %v", got)
	}
}
