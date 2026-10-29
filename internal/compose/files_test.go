package compose

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/testutil"
)

func TestSplitComposeFile(t *testing.T) {
	tests := []struct {
		value, sep string
		want       []string
	}{
		{"a.yaml:b.yaml", ":", []string{"a.yaml", "b.yaml"}},
		{"a.yaml;b.yaml", ";", []string{"a.yaml", "b.yaml"}},
		{"a.yaml,b.yaml", ",", []string{"a.yaml", "b.yaml"}},
		{" a.yaml : :b.yaml: ", ":", []string{"a.yaml", "b.yaml"}},
		{"", ":", nil},
		{"one.yaml", "", []string{"one.yaml"}},
	}
	for _, tt := range tests {
		if got := SplitComposeFile(tt.value, tt.sep); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("SplitComposeFile(%q, %q) = %v, want %v", tt.value, tt.sep, got, tt.want)
		}
	}
	want := ":"
	if runtime.GOOS == "windows" {
		want = ";"
	}
	if PathSeparator() != want {
		t.Errorf("PathSeparator() = %q, want %q", PathSeparator(), want)
	}
}

func TestEnvFileValue(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.env")
	second := filepath.Join(dir, "second.env")
	os.WriteFile(first, []byte("\xef\xbb\xbfCOMPOSE_FILE=a.yaml\n# COMPOSE_PATH_SEPARATOR=;\nOTHER=1\n"), 0o644)
	os.WriteFile(second, []byte("export COMPOSE_FILE=\"b.yaml:c.yaml\" # comment\nQUOTED='x y'\nPLAIN=v # note\n"), 0o644)

	tests := []struct {
		files []string
		key   string
		want  string
		ok    bool
	}{
		{[]string{first}, "COMPOSE_FILE", "a.yaml", true},
		{[]string{first, second}, "COMPOSE_FILE", "b.yaml:c.yaml", true}, // the last file wins
		{[]string{second, first}, "COMPOSE_FILE", "a.yaml", true},
		{[]string{first, second}, "COMPOSE_PATH_SEPARATOR", "", false}, // commented out
		{[]string{second}, "QUOTED", "x y", true},
		{[]string{second}, "PLAIN", "v", true},
		{[]string{filepath.Join(dir, "missing.env")}, "COMPOSE_FILE", "", false},
		{nil, "COMPOSE_FILE", "", false},
	}
	for _, tt := range tests {
		got, ok := EnvFileValue(tt.files, tt.key)
		if got != tt.want || ok != tt.ok {
			t.Errorf("EnvFileValue(%v, %s) = %q, %v; want %q, %v", tt.files, tt.key, got, ok, tt.want, tt.ok)
		}
	}
}

func TestResolvePaths(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(t.TempDir(), "x.yaml")
	got := ResolvePaths(dir, []string{"a.yaml", "sub/b.yaml", abs})
	want := []string{filepath.Join(dir, "a.yaml"), filepath.Join(dir, "sub", "b.yaml"), abs}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResolvePaths = %v, want %v", got, want)
	}
}

// ConfigFiles must give what `docker compose -f B -f A config` gives, in
// the order of the files (FR-011). It needs the docker compose command, not
// a daemon.
func TestConfigFilesFollowsTheOrderOfTheFiles(t *testing.T) {
	testutil.RequireCompose(t)
	dir := testutil.TempDir(t)
	testutil.WriteFile(t, dir, "compose.yaml", "name: t2-decoy\nservices:\n  decoy:\n    image: scratch\n")
	testutil.WriteFile(t, dir, "a.yaml", "name: t2-order\nservices:\n  web:\n    image: scratch\n    environment:\n      WHO: a\n")
	testutil.WriteFile(t, dir, "b.yaml", "services:\n  web:\n    environment:\n      WHO: b\n")

	for _, order := range [][]string{{"a.yaml", "b.yaml"}, {"b.yaml", "a.yaml"}} {
		p, err := Compose{}.ConfigFiles(context.Background(), dir, nil, order)
		if err != nil {
			t.Fatalf("%v: %v", order, err)
		}
		if _, decoy := p.Services["decoy"]; decoy || len(p.Services) != 1 {
			t.Errorf("%v: services = %v, the default compose.yaml must not be loaded", order, SortedKeys(p.Services))
		}
		// The last file's value wins.
		who := strings.TrimSuffix(order[len(order)-1], ".yaml")
		text, err := Compose{}.ConfigText(context.Background(), Target{Dir: dir, Name: "t2-order", Files: order})
		if err != nil || !strings.Contains(string(text), "WHO: "+who) {
			t.Errorf("%v: config text %q (%v), want WHO: %s", order, text, err, who)
		}
	}
}
