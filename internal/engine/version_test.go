package engine

import (
	"strings"
	"testing"
)

func TestVersionLineStartsWithTheoAndVersion(t *testing.T) {
	if got := VersionLine(); !strings.HasPrefix(got, "theo "+Version) {
		t.Errorf("VersionLine() = %q, want prefix %q", got, "theo "+Version)
	}
}
