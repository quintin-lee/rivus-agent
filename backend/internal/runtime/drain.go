package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"rivus-agent-backend/internal/domain"
)

// tokenFlushInterval: coalescing window for EvtModelToken emission.
// Each model call produces a small number of EvtModelToken events (one per
// ~200ms window), not one per raw chunk. Injectable in tests (set to a tiny
// value, e.g. 1 * time.Millisecond, to force deterministic flushing).
var tokenFlushInterval = 200 * time.Millisecond

func (r *EinoRunner) drain(ctx context.Context, runID, checkpointID string,
	iter *adk.AsyncIterator[*adk.AgentEvent], out chan<- RuntimeEvent, stream bool) {
	var lastText string
	var lastTool string
	for {
		select {
		case <-ctx.Done():
			r.emit(ctx, out, runID, domain.EvtRunCancelled, ctx.Err().Error(), "", "")
			return
		default:
		}
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev == nil {
			continue
		}
		if ev.Err != nil {
			if errors.Is(ev.Err, domain.ErrNeedsApproval) {
				r.emit(ctx, out, runID, domain.EvtApprovalRequested, ev.Err.Error(), "", "")
				return
			}
			var ce *domain.CodedError
			if errors.As(ev.Err, &ce) && ce.Code == "tool_failed" {
				r.emit(ctx, out, runID, domain.EvtToolFailed, ev.Err.Error(), lastTool, "")
			}
			r.emit(ctx, out, runID, domain.EvtModelFailed, ev.Err.Error(), "", "")
			r.emit(ctx, out, runID, domain.EvtRunFailed, ev.Err.Error(), "", "")
			return
		}
		if ev.Output != nil && ev.Output.MessageOutput != nil {
			mo := ev.Output.MessageOutput
			text := ""
			if mo.Message != nil {
				text = mo.Message.Content
			}
			if text == "" && mo.MessageStream != nil {
				var onDelta func(textSoFar string)
				if stream {
					onDelta = func(txt string) { r.emit(ctx, out, runID, domain.EvtModelToken, txt, "", "") }
				}
				text = drainStream(mo.MessageStream, onDelta)
			}
			if text != "" {
				lastText = text
				r.emit(ctx, out, runID, domain.EvtModelCompleted, text, "", "")
			}
			if mo.ToolName != "" {
				lastTool = mo.ToolName
				extra := ""
				if obs, ok := r.takeToolObserved(runID, mo.ToolName); ok {
					if b, err := json.Marshal(map[string]string{"args": obs.args, "output": obs.output}); err == nil {
						extra = string(b)
					}
				}
				r.emit(ctx, out, runID, domain.EvtToolCompleted, mo.ToolName, mo.ToolName, extra)
			}
		}
		if ev.Action != nil && ev.Action.Interrupted != nil {
			r.emit(ctx, out, runID, domain.EvtApprovalRequested, "interrupted", "", "")
			return
		}
	}
	_ = checkpointID
	_ = lastText
	r.emit(ctx, out, runID, domain.EvtRunFinished, lastText, "", "")
}

// drainStream 消费流；onDelta 在每 ~200ms 窗口携带"到目前为止全文"flush 一次。
// 仍返回完整字符串（保证 EvtModelCompleted + lastText 不变）。onDelta 可为 nil。
func drainStream(sr *schema.StreamReader[*schema.Message], onDelta func(textSoFar string)) string {
	defer sr.Close()
	var sb strings.Builder
	var lastFlush time.Time
	for {
		chunk, err := sr.Recv()
		if err != nil {
			break
		}
		if chunk != nil {
			sb.WriteString(chunk.Content)
			if onDelta != nil && time.Since(lastFlush) >= tokenFlushInterval {
				onDelta(sb.String()) // full-so-far
				lastFlush = time.Now()
			}
			if len(sb.String()) > 200000 {
				break
			}
		}
	}
	return sb.String()
}

// emit 阻塞式投递：消费者总是 range 到 close（runtime.run / RunChild 均如此），
// 故缓冲满时阻塞等待即可、绝不丢事件。ctx.Done 只作为逃生门（消费者停滞时
// 避免永久卡死）。先做一次非阻塞投递：保证“缓冲有空位 + ctx 已取消”场景
// （典型：drain 的 run.cancelled）确定性送达，而非 select 随机二选一丢弃。
func (r *EinoRunner) emit(ctx context.Context, out chan<- RuntimeEvent, runID string, typ domain.EventType, text, tool, payload string) {
	ev := RuntimeEvent{Type: typ, Text: text, ToolName: tool, Payload: payload, RunID: runID}
	select {
	case out <- ev:
	default:
		select {
		case out <- ev:
		case <-ctx.Done():
			return
		}
	}
	if r.onEvent != nil {
		r.onEvent(ev)
	}
}
