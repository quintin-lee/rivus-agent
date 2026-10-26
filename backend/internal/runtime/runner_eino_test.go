package runtime

import (
	"testing"

	"rivus-agent-backend/internal/domain"
)

// ResumeRequest 未带预算（全零）→ 回退服务端默认上限（原硬编码值）。
func TestResolveResumeBudgetDefaults(t *testing.T) {
	got := resolveResumeBudget(domain.Budget{})
	want := domain.Budget{MaxIterations: 12, MaxModelCalls: 30, MaxToolCalls: 50, MaxDurationSeconds: 600}
	if got != want {
		t.Fatalf("resolveResumeBudget(zero) = %+v; want %+v", got, want)
	}
}

// 带预算则原样透传（调用方已 ClampByServer，只收紧不放宽）。
func TestResolveResumeBudgetPassthrough(t *testing.T) {
	in := domain.Budget{MaxIterations: 5, MaxModelCalls: 7, MaxToolCalls: 3, MaxDurationSeconds: 90, MaxOutputBytes: 1234}
	if got := resolveResumeBudget(in); got != in {
		t.Fatalf("resolveResumeBudget(%+v) = %+v; want passthrough", in, got)
	}
}
