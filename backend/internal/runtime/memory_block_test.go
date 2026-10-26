package runtime

import (
	"strings"
	"testing"

	"rivus-agent-backend/internal/domain"
)

func TestComposeMemoryBlockEmpty(t *testing.T) {
	if got := composeMemoryBlock(nil); got != "" {
		t.Fatalf("empty memories must yield empty block, got %q", got)
	}
}

func TestComposeMemoryBlockContent(t *testing.T) {
	got := composeMemoryBlock([]domain.Memory{{Content: "a"}, {Content: "b"}})
	if !strings.Contains(got, "- a") || !strings.Contains(got, "- b") || !strings.Contains(got, "会话记忆") {
		t.Fatalf("missing lines: %q", got)
	}
}

func TestComposeMemoryBlockCap(t *testing.T) {
	mems := make([]domain.Memory, 0, 50)
	for i := 0; i < 50; i++ {
		mems = append(mems, domain.Memory{Content: strings.Repeat("x", 1024)})
	}
	got := composeMemoryBlock(mems)
	if len(got) > 8*1024+200 { // 上限 8KB（留标头余量）
		t.Fatalf("block too large: %d", len(got))
	}
}
