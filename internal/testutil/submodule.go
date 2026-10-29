package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// NewRepoWithSubmodule creates a Git repository called "app" with one commit
// that contains a submodule "libs/shared" and the given files. The
// submodule's repository is a local one in the same temporary directory, so
// no network is used. Git blocks the file transport for submodules by
// default, so the test process is allowed to use it (git reads the
// GIT_CONFIG_* variables, which child processes inherit).
func NewRepoWithSubmodule(t *testing.T, files map[string]string) (main string) {
	t.Helper()
	RequireGit(t)
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "protocol.file.allow")
	t.Setenv("GIT_CONFIG_VALUE_0", "always")

	root := TempDir(t)
	sub := filepath.Join(root, "shared")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	Git(t, sub, "init", "-q", "-b", "main")
	WriteFile(t, sub, "lib.txt", "shared library\n")
	Git(t, sub, "add", "-A")
	Git(t, sub, "commit", "-q", "-m", "shared")

	main = filepath.Join(root, "app")
	if err := os.Mkdir(main, 0o755); err != nil {
		t.Fatal(err)
	}
	Git(t, main, "init", "-q", "-b", "main")
	WriteFile(t, main, "README.md", "test\n")
	for name, content := range files {
		WriteFile(t, main, name, content)
	}
	Git(t, main, "submodule", "add", "-q", sub, "libs/shared")
	Git(t, main, "add", "-A")
	Git(t, main, "commit", "-q", "-m", "init")
	return main
}
