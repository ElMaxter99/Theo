package registry

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestLoadMissingFile(t *testing.T) {
	projects, err := Open(t.TempDir()).Load()
	if err != nil || len(projects) != 0 {
		t.Errorf("Load() = %v, %v; want empty", projects, err)
	}
}

func TestSaveAndLoad(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested")
	s := Open(dir)
	want := []Project{
		{Name: "a", Path: "/a", AddedAt: time.Date(2026, 10, 29, 8, 0, 0, 0, time.UTC)},
		{Name: "b", Path: "/b", AddedAt: time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)},
	}
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := Open(dir).Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("expected only the registry file, found %d entries", len(entries))
	}
}

func TestLoadCorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dir).Load(); err == nil {
		t.Error("expected an error for a corrupt registry")
	}
}

func TestDefaultDirHonoursEnv(t *testing.T) {
	t.Setenv(EnvHome, "/custom/theo")
	dir, err := DefaultDir()
	if err != nil || dir != "/custom/theo" {
		t.Errorf("DefaultDir() = %q, %v", dir, err)
	}
}
