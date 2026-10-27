package observability

import (
	"expvar"
	"testing"
)

var bucketNames = []string{
	"latency_ms_0_50", "latency_ms_50_100", "latency_ms_100_200",
	"latency_ms_200_500", "latency_ms_500_plus",
}

func bucketValues(t *testing.T) []int64 {
	t.Helper()
	out := make([]int64, len(bucketNames))
	for i, n := range bucketNames {
		v, ok := expvar.Get(n).(*expvar.Int)
		if !ok {
			t.Fatalf("expvar %q missing", n)
		}
		out[i] = v.Value()
	}
	return out
}

func TestObserveLatencyMsBuckets(t *testing.T) {
	tests := []struct {
		ms    int64
		index int
	}{
		{0, 0}, {49, 0}, {50, 1}, {99, 1}, {100, 2}, {199, 2},
		{200, 3}, {499, 3}, {500, 4}, {1 << 40, 4},
	}
	for _, tt := range tests {
		before := bucketValues(t)
		ObserveLatencyMs(tt.ms)
		after := bucketValues(t)
		for i := range after {
			want := int64(0)
			if i == tt.index {
				want = 1
			}
			if after[i]-before[i] != want {
				t.Fatalf("ms=%d: bucket %d delta=%d, want %d (all=%v)", tt.ms, i, after[i]-before[i], want, after)
			}
		}
	}
}

// ObserveTool 透传到桶。
func TestObserveToolFeedsLatencyBucket(t *testing.T) {
	before := bucketValues(t)
	ObserveTool("probe_tool", true, 75)
	after := bucketValues(t)
	if after[1]-before[1] != 1 {
		t.Fatalf("bucket_50_100 delta=%d, want 1", after[1]-before[1])
	}
}

func TestIncrEventAppendFailed(t *testing.T) {
	v, ok := expvar.Get("event_append_failed").(*expvar.Int)
	if !ok {
		t.Fatal("expvar event_append_failed missing")
	}
	before := v.Value()
	IncrEventAppendFailed()
	if got := v.Value(); got != before+1 {
		t.Fatalf("event_append_failed=%d, want %d", got, before+1)
	}
}

func TestIncrRateLimited(t *testing.T) {
	before := expvar.Get("rate_limited_total").(*expvar.Int).Value()
	IncrRateLimited()
	IncrRateLimited()
	after := expvar.Get("rate_limited_total").(*expvar.Int).Value()
	if after-before != 2 {
		t.Fatalf("want delta 2, got %d", after-before)
	}
}
