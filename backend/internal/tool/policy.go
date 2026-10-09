package tool

import "rivus-agent-backend/internal/security"

// PolicyDecide 工具权限与风险策略的判定结果。
type PolicyDecide struct {
	Allow        bool
	NeedApproval bool
	DenyReason   string
}

// CheckPolicy 统一校验：禁止操作直接拒绝；鉴权看 scopes；高风险默认要审批。
func CheckPolicy(d Definition, scopes []string, approved bool) PolicyDecide {
	if d.Risk == RiskDenied {
		return PolicyDecide{DenyReason: "tool is denied by policy"}
	}
	for _, need := range d.RequiredScopes {
		ok := false
		for _, have := range scopes {
			if have == need || have == "*" {
				ok = true
				break
			}
		}
		if !ok {
			return PolicyDecide{DenyReason: "missing scope " + need}
		}
	}
	switch d.Approval {
	case ApprovalAlways:
		if !approved {
			return PolicyDecide{NeedApproval: true}
		}
	case ApprovalConditional:
		if (d.Risk == RiskWrite || d.Risk == RiskExternal) && !approved {
			return PolicyDecide{NeedApproval: true}
		}
	}
	_ = security.Redact
	return PolicyDecide{Allow: true}
}
