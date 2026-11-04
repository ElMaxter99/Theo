package repoconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMissingFilesIsEmpty(t *testing.T) {
	l, err := Load(t.TempDir())
	if err != nil || !l.Empty() || len(l.Sources) != 0 {
		t.Fatalf("got %+v, %v", l, err)
	}
}

func TestLoadMergesBothFiles(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, FileName, `{"version":1,"copy":[".env"," .env.local "],"after_create":["npm ci",""]}`)
	write(t, dir, WorktreeIncludes, "# secrets\n.env\n/config/local.yml\n!keep\nbuild/\r\n\n")
	l, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{".env", ".env.local", "config/local.yml"}; !reflect.DeepEqual(l.Copy, want) {
		t.Errorf("copy = %v, want %v", l.Copy, want)
	}
	if !reflect.DeepEqual(l.AfterCreate, []string{"npm ci"}) {
		t.Errorf("after_create = %v", l.AfterCreate)
	}
	if !reflect.DeepEqual(l.Sources, []string{FileName, WorktreeIncludes}) {
		t.Errorf("sources = %v", l.Sources)
	}
	if len(l.Notes) != 2 {
		t.Errorf("notes = %v", l.Notes)
	}
}

func TestLoadInvalidJSONIsAnError(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, FileName, `{"copy": [`)
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), FileName) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadNewerVersionIsANote(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, FileName, `{"version":9,"copy":["a"]}`)
	l, err := Load(dir)
	if err != nil || len(l.Notes) != 1 || len(l.Copy) != 1 {
		t.Fatalf("got %+v, %v", l, err)
	}
}

func TestFingerprint(t *testing.T) {
	a, b := Fingerprint([]string{"a", "b"}), Fingerprint([]string{"ab"})
	if a == b || a != Fingerprint([]string{"a", "b"}) {
		t.Fatal("fingerprint must depend on the list, not its concatenation")
	}
}

func TestLoadAcceptsBOM(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, FileName, "\ufeff"+`{"copy":["a"]}`)
	write(t, dir, WorktreeIncludes, "\ufeff.npmrc\r\n")
	l, err := Load(dir)
	if err != nil || !reflect.DeepEqual(l.Copy, []string{"a", ".npmrc"}) {
		t.Fatalf("got %+v, %v", l, err)
	}
}
