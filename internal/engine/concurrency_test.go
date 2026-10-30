package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ElMaxter99/Theo/internal/instances"
	"github.com/ElMaxter99/Theo/internal/ports"
	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/testutil"
)

// NFR-010: `theo ui` and a CLI command write the same state files. Several
// processes (this test binary, re-executed) register projects, reserve ports
// and change settings at the same time; nothing may be lost or corrupted.

const helperEnv = "THEO_UB_STATE_HELPER"

func TestStateWriterHelperProcess(t *testing.T) {
	list := os.Getenv(helperEnv)
	if list == "" {
		t.Skip("helper process of TestStateFilesSurviveConcurrentProcesses")
	}
	e, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("THEO_UB_NOLOCK") != "" { // to see the test fail without the lock
		e.lock = nil
	}
	for _, spec := range strings.Split(list, ",") {
		name, path, _ := strings.Cut(spec, "=")
		if _, err := e.AddProject(context.Background(), path, name, ""); err != nil {
			t.Fatalf("AddProject %s: %v", name, err)
		}
		ws := filepath.Join(path, "ws")
		if err := e.savePorts(ws, ports.State{ws: {"web|80/tcp": 20000}}); err != nil {
			t.Fatalf("savePorts: %v", err)
		}
		inst := &Instance{Project: name, Workspace: ws}
		if err := e.updateSettings(inst, func(c *instances.Config) error { c.AutoPorts = true; return nil }); err != nil {
			t.Fatalf("updateSettings: %v", err)
		}
		if err := e.SetWorktreeDir(name, filepath.Join(path, "wt")); err != nil {
			t.Fatalf("SetWorktreeDir: %v", err)
		}
	}
}

func TestStateFilesSurviveConcurrentProcesses(t *testing.T) {
	testutil.RequireGit(t)
	const procs, perProc = 4, 5
	home := t.TempDir()
	t.Setenv(registry.EnvHome, home)
	var specs [procs][]string
	var all []string
	for i := 0; i < procs*perProc; i++ {
		main, _ := testutil.NewRepo(t, nil)
		name := fmt.Sprintf("ubconc%02d", i)
		specs[i%procs] = append(specs[i%procs], name+"="+main)
		all = append(all, name)
	}
	var wg sync.WaitGroup
	outs := make([]string, procs)
	for i := 0; i < procs; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=^TestStateWriterHelperProcess$", "-test.v")
			cmd.Env = append(os.Environ(), helperEnv+"="+strings.Join(specs[i], ","))
			out, err := cmd.CombinedOutput()
			outs[i] = string(out)
			if err != nil {
				t.Errorf("helper %d: %v\n%s", i, err, out)
			}
		}(i)
	}
	wg.Wait()

	// Every file is valid JSON and holds every write.
	var reg struct {
		Projects []registry.Project `json:"projects"`
	}
	readJSON(t, filepath.Join(home, "registry.json"), &reg)
	if len(reg.Projects) != procs*perProc {
		t.Errorf("registry has %d projects, want %d", len(reg.Projects), procs*perProc)
	}
	for _, p := range reg.Projects {
		if p.WorktreeDir == "" {
			t.Errorf("project %s lost its worktree_dir (a concurrent write undid it)", p.Name)
		}
	}
	var pf struct {
		Instances map[string]any `json:"instances"`
	}
	readJSON(t, filepath.Join(home, "ports.json"), &pf)
	if len(pf.Instances) != procs*perProc {
		t.Errorf("ports.json has %d instances, want %d", len(pf.Instances), procs*perProc)
	}
	var sf struct {
		Instances map[string]any `json:"instances"`
	}
	readJSON(t, filepath.Join(home, "instances.json"), &sf)
	if len(sf.Instances) != procs*perProc {
		t.Errorf("instances.json has %d entries, want %d", len(sf.Instances), procs*perProc)
	}
	// No temporary or lock file is left behind.
	entries, _ := os.ReadDir(home)
	for _, en := range entries {
		if strings.HasSuffix(en.Name(), ".tmp") || en.Name() == "state.lock" {
			t.Errorf("left behind: %s", en.Name())
		}
	}
	_ = all
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("%s is not valid JSON: %v", path, err)
	}
}
