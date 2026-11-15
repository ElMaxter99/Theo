package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCaptureEngineCheckOnlyWhenUsed(t *testing.T) {
	root := t.TempDir()
	env := DoctorEnv{Projects: []string{root}, DataDir: t.TempDir()}
	if _, ok := checkCaptureEngine(context.Background(), env); ok {
		t.Fatal("a project without pantallas.json must not add the check")
	}
	dir := filepath.Join(root, ".theo", "capturas")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pantallas.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, ok := checkCaptureEngine(context.Background(), env)
	if !ok || c.ID != "capture-engine" {
		t.Fatalf("%+v %v", c, ok)
	}
	if c.Status == StatusOK {
		return // an engine is installed on this machine
	}
	if c.Status != StatusWarn || c.Fix != "theo tool capture setup" {
		t.Errorf("%+v", c)
	}
}
