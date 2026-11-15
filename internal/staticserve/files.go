package staticserve

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

// fileExists reports whether rel (a slash path under dir) is a regular file.
// Paths that leave dir count as missing.
func fileExists(dir, rel string) bool {
	clean := path.Clean("/" + rel)
	if strings.Contains(clean, "..") {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(clean)))
	return err == nil && !info.IsDir()
}
