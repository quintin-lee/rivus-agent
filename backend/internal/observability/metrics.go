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
}
