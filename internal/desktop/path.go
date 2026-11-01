package desktop

import (
	"path"
	"strings"
)

// darwinToolDirs are where `git` and `docker` live on a Mac but that Finder
// and the Dock do not put in PATH for an app they launch (they hand over
// /usr/bin:/bin:/usr/sbin:/sbin only).
func darwinToolDirs(home string) []string {
	dirs := []string{
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/Applications/Docker.app/Contents/Resources/bin",
	}
	if home != "" {
		dirs = append(dirs, path.Join(home, ".docker", "bin"), path.Join(home, ".rd", "bin"))
	}
	return dirs
}

// AugmentPath returns pathEnv with the usual tool directories of goos appended
// when they are missing. Only macOS needs it: an app opened from Finder does
// not inherit the shell's PATH, so `docker` and `git` installed by Docker
// Desktop or Homebrew would not be found. Directories already present keep
// their position; nothing is prepended, so it never shadows a tool the user
// already has first in PATH.
func AugmentPath(pathEnv, goos, home string) string {
	if goos != "darwin" {
		return path
	}
	const sep = ":" // macOS only: the separator does not depend on the machine that runs the tests
	have := map[string]bool{}
	var parts []string
	if pathEnv != "" {
		parts = strings.Split(pathEnv, sep)
	}
	for _, p := range parts {
		have[p] = true
	}
	for _, d := range darwinToolDirs(home) {
		if !have[d] {
			parts = append(parts, d)
			have[d] = true
		}
	}
	return strings.Join(parts, sep)
}
