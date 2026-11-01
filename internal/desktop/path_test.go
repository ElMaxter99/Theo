package desktop

import (
	"strings"
	"testing"
)

func TestAugmentPath(t *testing.T) {
	got := AugmentPath("/usr/bin:/bin", "darwin", "/Users/a")
	for _, want := range []string{"/opt/homebrew/bin", "/usr/local/bin", "/Users/a/.docker/bin", "/Applications/Docker.app/Contents/Resources/bin"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	if !strings.HasPrefix(got, "/usr/bin:/bin:") {
		t.Errorf("existing entries must keep first place: %s", got)
	}
	if again := AugmentPath(got, "darwin", "/Users/a"); again != got {
		t.Errorf("not idempotent: %s", again)
	}
	if got := AugmentPath("/x:/usr/local/bin", "darwin", ""); strings.Count(got, "/usr/local/bin") != 1 {
		t.Errorf("duplicated entry: %s", got)
	}
	for _, goos := range []string{"linux", "windows"} {
		if AugmentPath("/a", goos, "/h") != "/a" {
			t.Errorf("%s must be left alone", goos)
		}
	}
	if AugmentPath("", "darwin", "") == "" {
		t.Error("empty PATH on macOS should get the tool directories")
	}
}
