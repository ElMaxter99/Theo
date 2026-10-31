package desktop

import (
	"path/filepath"
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
		dirs = append(dirs, filepath.Join(home, ".docker", "bin"), filepath.Join(home, ".rd", "bin"))
	}
	return dirs
}

// AugmentPath returns path with the usual tool directories of goos appended
// when they are missing. Only macOS needs it: an app opened from Finder does
// not inherit the shell's PATH, so `docker` and `git` installed by Docker
// Desktop or Homebrew would not be found. Directories already present keep
// their position; nothing is prepended, so it never shadows a tool the user
// already has first in PATH.
func AugmentPath(path, goos, home string) string {
	if goos != "darwin" {
		return path
	}
	sep := string(filepath.ListSeparator)
	have := map[string]bool{}
	var parts []string
	if path != "" {
		parts = strings.Split(path, sep)
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
