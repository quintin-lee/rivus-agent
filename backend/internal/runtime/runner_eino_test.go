package runtime

import (
	"context"
	"testing"
	"time"

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

// 缓冲满且无消费者时 emit 阻塞（事件不被静默丢弃）；ctx 取消后放弃。
func TestEmitBlocksWhenFull(t *testing.T) {
	r := NewEinoRunner(nil, nil, nil, nil)
	out := make(chan RuntimeEvent) // 无缓冲，无读者
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.emit(ctx, out, "run-1", domain.EvtRunStarted, "started", "", "")
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("emit returned while consumer absent (event dropped)")
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("emit did not return after ctx cancel")
	}
}

// 缓冲有空位时即使 ctx 已取消也必须投递 —— 保证 drain 的 run.cancelled 不被随机丢弃。
func TestEmitDeliversWhenRoomDespiteCancel(t *testing.T) {
	r := NewEinoRunner(nil, nil, nil, nil)
	out := make(chan RuntimeEvent, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.emit(ctx, out, "run-1", domain.EvtRunCancelled, "cancelled", "", "")
	select {
	case ev := <-out:
		if ev.Type != domain.EvtRunCancelled {
			t.Fatalf("ev.Type=%s, want run.cancelled", ev.Type)
		}
	default:
		t.Fatal("event dropped although buffer had room")
	}
}
