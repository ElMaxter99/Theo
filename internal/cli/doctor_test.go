package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/registry"
)

// The doctor runs the real tools, so the test only checks that the report is
// consistent with the exit code, in both formats.
func TestDoctorReportAndExitCode(t *testing.T) {
	t.Setenv(registry.EnvHome, t.TempDir())

	stdout, stderr, code := run("doctor", "--json")
	var report engine.DoctorReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("doctor --json: %v\n%s", err, stdout)
	}
	ids := map[string]bool{}
	for _, c := range report.Checks {
		ids[c.ID] = true
		if c.Status == engine.StatusFail && c.Fix == "" {
			t.Errorf("failed check %s has no fix", c.ID)
		}
	}
	for _, id := range []string{"git", "docker", "docker-daemon", "docker-compose", "data-dir", "state-files"} {
		if !ids[id] {
			t.Errorf("no %s check in %v", id, ids)
		}
	}
	// With docker installed, the engine behind it is named, even when the daemon is down.
	var dockerOK bool
	for _, c := range report.Checks {
		if c.ID == "docker" && c.Status == engine.StatusOK {
			dockerOK = true
		}
	}
	if dockerOK && (!ids["engine"] || report.Engine == nil || report.Engine.Kind == "") {
		t.Errorf("docker is installed but no engine: %v %+v", ids, report.Engine)
	}
	if (code == ExitOK) != report.OK {
		t.Errorf("exit code %d but ok = %v (%s)", code, report.OK, stderr)
	}

	text, _, textCode := run("doctor")
	if textCode != code || !strings.Contains(text, "docker compose plugin") {
		t.Errorf("text report: code %d\n%s", textCode, text)
	}
	if _, _, code := run("doctor", "extra"); code != ExitUsage {
		t.Errorf("doctor with an argument: code %d", code)
	}
}
