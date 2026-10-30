package api_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/api"
	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/engine"
)

// A Compose older than the minimum (NFR-041) is a failed check with a fix,
// in /doctor and in /diagnostics. A fake `docker` on PATH simulates it.
func TestDoctorDetectsAComposeThatIsTooOld(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake docker is a shell script")
	}
	bin := t.TempDir()
	script := `#!/bin/sh
case "$1" in
  --version) echo "Docker version 27.0.0, build ub5" ;;
  info) echo "27.0.0 linux" ;;
  compose) echo "2.20.0" ;;
  version) echo "27.0.0" ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	e := newEnv(t)
	var d api.Diagnostics
	if st := e.c.call("GET", "/api/v1/diagnostics", nil, &d); st != 200 {
		t.Fatalf("diagnostics = %d", st)
	}
	var found bool
	for _, c := range d.Doctor.Checks {
		if c.ID == "docker-compose" {
			found = true
			if c.Status != "fail" || !strings.Contains(c.Detail, "too old") || c.Fix == "" {
				t.Errorf("compose check = %+v", c)
			}
		}
	}
	if !found || d.Doctor.OK {
		t.Errorf("doctor = %+v", d.Doctor)
	}
	if d.Meta.Compose.Version != "2.20.0" || d.Meta.Compose.MinVersion != compose.MinVersion.String() {
		t.Errorf("meta.compose = %+v", d.Meta.Compose)
	}
	if !strings.Contains(d.Report, "Docker Compose: 2.20.0 (Theo needs "+compose.MinVersion.String()+" or later)") || !strings.Contains(d.Report, "[fail]") {
		t.Errorf("report:\n%s", d.Report)
	}
	var doc engine.DoctorReport
	e.c.call("GET", "/api/v1/doctor", nil, &doc)
	if doc.OK {
		t.Error("/doctor says ok")
	}
}
