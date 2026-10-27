package buildinfo

import "testing"

func TestDefaults(t *testing.T) {
	if Version != "dev" {
		t.Errorf("Version default = %q, want dev", Version)
	}
	if Commit != "unknown" {
		t.Errorf("Commit default = %q, want unknown", Commit)
	}
	if BuildDate != "unknown" {
		t.Errorf("BuildDate default = %q, want unknown", BuildDate)
	}
}
