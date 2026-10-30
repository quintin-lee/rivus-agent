package buildinfo

import "testing"

func TestDefaults(t *testing.T) {
	if Version != "0.1.0" {
		t.Errorf("Version default = %q, want 0.1.0", Version)
	}
	if Commit != "unknown" {
		t.Errorf("Commit default = %q, want unknown", Commit)
	}
	if BuildDate != "unknown" {
		t.Errorf("BuildDate default = %q, want unknown", BuildDate)
	}
}
