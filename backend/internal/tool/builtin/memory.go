package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"rivus-agent-backend/internal/domain"
	"rivus-agent-backend/internal/runtime"
	"rivus-agent-backend/internal/store"
	"rivus-agent-backend/internal/tool"
)

const (
	memoryMaxBytes   = 2048 // 单条上限（字节）
	memoryReadLatest = 20   // 无 query 时返回条数
)

// MemoryTools 返回 memory_write / memory_read 两个工具定义。
// repo 由调用方注入（app.go / harness），builtin 保持无全局状态。
func MemoryTools(repo *store.MemoryRepo) []tool.Definition {
	return []tool.Definition{
		{
			Name:        "memory_write",
			Description: "把值得长期记住的事实写入会话记忆（跨 run 共享）。Args: {\"content\":\"<一句事实>\"}",
			Version:     "v1",
			InputSchema: `{"type":"object","properties":{"content":{"type":"string"}},"required":["content"]}`,
			Risk:        tool.RiskWrite, Approval: tool.ApprovalNever, Idempotent: false,
			TimeoutMs: 5000, MaxResultBytes: 1024,
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				var in struct {
					Content string `json:"content"`
				}
				if err := json.Unmarshal(args, &in); err != nil || strings.TrimSpace(in.Content) == "" {
					return "", fmt.Errorf("memory_write: content required")
				}
				if len(in.Content) > memoryMaxBytes {
					return "", fmt.Errorf("memory_write: content too large (%d > %d bytes)", len(in.Content), memoryMaxBytes)
				}
				tc, ok := runtime.TaskContextFrom(ctx)
				if !ok {
					return "", fmt.Errorf("memory_write: no task context")
				}
				if err := repo.Write(ctx, &domain.Memory{
					SessionID: tc.SessionID, OwnerID: tc.OwnerID,
					Content: strings.TrimSpace(in.Content), SourceRunID: tc.ParentRunID,
				}); err != nil {
					return "", fmt.Errorf("memory_write: %w", err)
				}
				return `{"status":"saved"}`, nil
			},
		},
		{
			Name:        "memory_read",
			Description: "读取会话记忆；可带 query 子串过滤（大小写不敏感），不带则返回最近 20 条。Args: {\"query\":\"<可选>\"}",
			Version:     "v1",
			InputSchema: `{"type":"object","properties":{"query":{"type":"string"}}}`,
			Risk:        tool.RiskRead, Approval: tool.ApprovalNever, Idempotent: true,
			TimeoutMs: 5000, MaxResultBytes: 16 * 1024,
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				var in struct {
					Query string `json:"query"`
				}
				_ = json.Unmarshal(args, &in)
				tc, ok := runtime.TaskContextFrom(ctx)
				if !ok {
					return "", fmt.Errorf("memory_read: no task context")
				}
				mems, err := repo.Latest(ctx, tc.SessionID, 200)
				if err != nil {
					return "", fmt.Errorf("memory_read: %w", err)
				}
				q := strings.ToLower(strings.TrimSpace(in.Query))
				lines := make([]string, 0, len(mems))
				for _, m := range mems {
					if q != "" && !strings.Contains(strings.ToLower(m.Content), q) {
						continue
					}
					lines = append(lines, "- "+m.Content)
					if len(lines) >= memoryReadLatest {
						break
					}
				}
				if len(lines) == 0 {
					return "（无记忆）", nil
				}
				// Latest 已按新→旧排序，query 过滤保序
				return strings.Join(lines, "\n"), nil
			},
		},
	}
}
