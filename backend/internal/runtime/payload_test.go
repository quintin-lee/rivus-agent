package runtime

import "testing"

func TestMergeEventPayload(t *testing.T) {
	// 无 extra：与现状字节一致（map 序列化键有序 text,tool）
	if got := mergeEventPayload("hello", "", ""); got != `{"text":"hello","tool":""}` {
		t.Fatalf("baseline changed: %s", got)
	}
	// extra 合并，extra 键胜出
	got := mergeEventPayload("task", "task", `{"args":"{}","output":"ok"}`)
	want := `{"args":"{}","output":"ok","text":"task","tool":"task"}`
	if got != want {
		t.Fatalf("merge wrong:\n got %s\nwant %s", got, want)
	}
	// 非法 extra 忽略
	if got := mergeEventPayload("a", "b", "{not json"); got != `{"text":"a","tool":"b"}` {
		t.Fatalf("invalid extra must be ignored: %s", got)
	}
}
