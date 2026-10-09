package tool

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

func ArgsHash(args json.RawMessage) string {
	sum := sha256.Sum256(args)
	return hex.EncodeToString(sum[:])
}

type ApprovalBinding struct {
	RunID      string
	ToolCallID string
	ToolName   string
	ArgsHash   string
}

func BindingMatches(b ApprovalBinding, runID, toolCallID, toolName string, args json.RawMessage) bool {
	return b.RunID == runID && b.ToolCallID == toolCallID && b.ToolName == toolName && b.ArgsHash == ArgsHash(args)
}
