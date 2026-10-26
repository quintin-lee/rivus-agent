package observability

import (
	"expvar"
	"sync"
)

var (
	runsStarted   = expvar.NewInt("runs_started")
	runsSucceeded = expvar.NewInt("runs_succeeded")
	runsFailed    = expvar.NewInt("runs_failed")
	toolCalls     = expvar.NewMap("tool_calls_total")
	toolErrors    = expvar.NewMap("tool_errors_total")

	eventAppendFailed = expvar.NewInt("event_append_failed")

	// latencyBuckets：5 个桶（<50/<100/<200/<500/≥500 ms），外部抓取 expvar
	// 后可直方图分位数（P50/P95）。
	latencyBuckets = []*expvar.Int{
		expvar.NewInt("latency_ms_0_50"),
		expvar.NewInt("latency_ms_50_100"),
		expvar.NewInt("latency_ms_100_200"),
		expvar.NewInt("latency_ms_200_500"),
		expvar.NewInt("latency_ms_500_plus"),
	}

	mu       sync.Mutex
	latSumMs = map[string]int64{}
	latCount = map[string]int64{}
)

// IncrRunStarted 记录 Run 启动。
func IncrRunStarted() { runsStarted.Add(1) }

// IncrRunFinished 记录 Run 终态。
func IncrRunFinished(ok bool) {
	if ok {
		runsSucceeded.Add(1)
	} else {
		runsFailed.Add(1)
	}
}

// ObserveTool 记录工具调用次数与时延（P50/P95 由外部抓取后计算，此处保留和与计数）。
func ObserveTool(name string, ok bool, latencyMs int64) {
	toolCalls.Add(name, 1)
	if !ok {
		toolErrors.Add(name, 1)
	}
	mu.Lock()
	latSumMs[name] += latencyMs
	latCount[name]++
	mu.Unlock()
	ObserveLatencyMs(latencyMs)
}

// IncrEventAppendFailed 记录事件落库失败（配合 runtime 侧 slog Warn）。
func IncrEventAppendFailed() { eventAppendFailed.Add(1) }

// ObserveLatencyMs 把时延计入 5 个桶（50/100/200/500/∞ ms），
// 外部抓取 expvar 后可算直方图分位数。
func ObserveLatencyMs(ms int64) {
	switch {
	case ms < 50:
		latencyBuckets[0].Add(1)
	case ms < 100:
		latencyBuckets[1].Add(1)
	case ms < 200:
		latencyBuckets[2].Add(1)
	case ms < 500:
		latencyBuckets[3].Add(1)
	default:
		latencyBuckets[4].Add(1)
	}
}
