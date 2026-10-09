package security

import "errors"

// Tenant 是授权域归属。
type Tenant struct {
	OwnerID string
	Scopes  []string
}

// CheckOwner 校验资源归属：owner 为空或不一致都拒绝。
func CheckOwner(resourceOwner, callerOwner string) error {
	if callerOwner == "" {
		return errors.New("missing owner")
	}
	if resourceOwner != callerOwner {
		return errors.New("cross-owner access denied")
	}
	return nil
}

// HasScope 校验作用域。
func (t Tenant) HasScope(scope string) bool {
	for _, s := range t.Scopes {
		if s == scope || s == "*" {
			return true
		}
	}
	return false
}
