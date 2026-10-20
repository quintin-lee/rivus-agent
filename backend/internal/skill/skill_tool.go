package skill

import (
	"context"
	"encoding/json"
	"fmt"

	"rivus-agent-backend/internal/tool"
)

// InvokeSkill builds the `invoke_skill` tool.Definition backed by the Loader.
// Risk=read, Approval=never, RequiredScopes empty. Handler returns the SKILL.md
// body (truncated to maxBytes) as the tool result for a known skill name.
func InvokeSkill(l *Loader, maxBytes int) tool.Definition {
	return tool.Definition{
		Name:        "invoke_skill",
		Description: "Load a skill's instructions as context. Args: {\"name\":\"<skill>\"}.",
		Version:     "v1",
		InputSchema: "{\"type\":\"object\",\"properties\":{\"name\":{\"type\":\"string\"}},\"required\":[\"name\"]}",
		Risk:        tool.RiskRead,
		Approval:    tool.ApprovalNever,
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(args, &in); err != nil || in.Name == "" {
				return "", fmt.Errorf("invoke_skill: invalid args")
			}
			body, ok := l.Get(in.Name)
			if !ok {
				return "", fmt.Errorf("unknown or unavailable skill %q", in.Name)
			}
			if maxBytes > 0 && len(body) > maxBytes {
				body = body[:maxBytes]
			}
			return body, nil
		},
	}
}
