package skill

import (
	"context"
	"encoding/json"
	"fmt"

	"rivus-agent-backend/internal/tool"
)

// InvokeSkill builds the `invoke_skill` tool.Definition backed by the Loader.
// Risk=read, Approval=never, RequiredScopes empty. Args:
//
//	{"name":"<skill>", "reference":"<file.md>"(optional), "input":{...}(optional)}
//
// 无 reference：返回 SKILL.md 正文（截断到 maxBytes）+ references/ 索引 + input 注入块。
// 有 reference：返回该引用文件内容（路径守卫 + 16KB 上限见 Loader.ReadReference）。
func InvokeSkill(l *Loader, maxBytes int) tool.Definition {
	return tool.Definition{
		Name: "invoke_skill",
		Description: "Load a skill's instructions as context. Args: {\"name\":\"<skill>\",\"reference\":\"<file.md>\",\"input\":{...}}. " +
			"reference loads a file from the skill's references/; input appends runtime context.",
		Version:        "v2",
		InputSchema:    `{"type":"object","properties":{"name":{"type":"string"},"reference":{"type":"string"},"input":{"type":"object"}},"required":["name"]}`,
		Risk:           tool.RiskRead,
		Approval:       tool.ApprovalNever,
		MaxResultBytes: 64 * 1024, // matches the mcp bridge cap; 0 would make the executor wipe non-empty results
		Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
			var in struct {
				Name      string          `json:"name"`
				Reference string          `json:"reference"`
				Input     json.RawMessage `json:"input"`
			}
			if err := json.Unmarshal(args, &in); err != nil || in.Name == "" {
				return "", fmt.Errorf("invoke_skill: invalid args")
			}

			// 引用模式：直接返回引用文件
			if in.Reference != "" {
				return l.ReadReference(in.Name, in.Reference)
			}

			body, ok := l.Get(in.Name)
			if !ok {
				return "", fmt.Errorf("unknown or unavailable skill %q", in.Name)
			}
			if maxBytes > 0 && len(body) > maxBytes {
				body = body[:maxBytes]
			}
			out := body

			// input 注入：不做模板替换，原文追加 JSON 块
			if len(in.Input) > 0 {
				var probe map[string]any
				if err := json.Unmarshal(in.Input, &probe); err != nil {
					return "", fmt.Errorf("invoke_skill: input must be an object")
				}
				out += "\n\n## 运行时输入\n```json\n" + string(in.Input) + "\n```"
			}

			// 引用索引（无 references 目录时省略）
			if refs, err := l.ListReferences(in.Name); err == nil && len(refs) > 0 {
				out += "\n\n## References\n"
				for _, r := range refs {
					out += "- " + r + "\n"
				}
			}
			return out, nil
		},
	}
}
