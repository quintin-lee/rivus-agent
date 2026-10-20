package mcp

import (
	"context"
	"encoding/json"

	"rivus-agent-backend/internal/tool"
)

// MCPDefinitions builds one tool.Definition per discovered MCP tool, named
// mcp__<server>__<tool>. Invariant (spec §4.3): Risk=external, Approval=always,
// RequiredScopes EMPTY (the gateway passes nil scopes, so any scope hard-denies).
// Handler delegates to Manager.Call; the executor already applies TimeoutMs, so
// the handler does NOT add its own context deadline.
func MCPDefinitions(status []StatusEntry, mgr *Manager) []tool.Definition {
	var defs []tool.Definition
	for _, e := range status {
		if e.Status != "up" {
			continue // only live servers expose tools
		}
		for _, tl := range e.Tools {
			name := "mcp__" + e.Name + "__" + tl.Name
			server, t := e.Name, tl.Name
			defs = append(defs, tool.Definition{
				Name:           name,
				Description:    tl.Description,
				Version:        "v1",
				InputSchema:    inputSchemaFor(tl),
				Risk:           tool.RiskExternal,
				Approval:       tool.ApprovalAlways,
				TimeoutMs:      30000,
				MaxResultBytes: 64 * 1024,
				Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
					var a map[string]any
					_ = json.Unmarshal(raw, &a)
					return mgr.Call(ctx, server, t, a)
				},
			})
		}
	}
	return defs
}

func inputSchemaFor(t Tool) string {
	if t.InputSchema != nil {
		if b, err := json.Marshal(t.InputSchema); err == nil {
			return string(b)
		}
	}
	return `{"type":"object"}`
}
