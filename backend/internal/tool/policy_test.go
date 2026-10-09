package tool

import "testing"

func TestCheckPolicy(t *testing.T) {
	denied := Definition{Name: "x", Risk: RiskDenied}
	if d := CheckPolicy(denied, nil, false); d.Allow || d.DenyReason == "" {
		t.Fatal("denied tool must be rejected")
	}
	hi := Definition{Name: "y", Risk: RiskExternal, Approval: ApprovalAlways}
	if d := CheckPolicy(hi, nil, false); !d.NeedApproval {
		t.Fatal("always-approval tool must need approval")
	}
	if d := CheckPolicy(hi, nil, true); !d.Allow {
		t.Fatal("approved tool must be allowed")
	}
}
