package engine

import "testing"

func TestVersion(t *testing.T) {
	if got := New().Version(); got != Version {
		t.Errorf("Version() = %q, want %q", got, Version)
	}
}
