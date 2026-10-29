package compose

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ElMaxter99/Theo/internal/execx"
)

// ConfigFiles is Config for an explicit list of Compose files, passed as
// repeated -f flags in order. Without files it behaves like Config.
func (Compose) ConfigFiles(ctx context.Context, dir string, envFiles, files []string) (*Project, error) {
	args := []string{"compose", "--profile", "*"}
	for _, f := range files {
		args = append(args, "-f", f)
	}
	for _, f := range envFiles {
		args = append(args, "--env-file", f)
	}
	args = append(args, "config", "--format", "json")
	out, err := execx.Run(ctx, dir, "docker", args...)
	if err != nil {
		return nil, err
	}
	return ParseConfig(out)
}

// PathSeparator is the separator of the paths in COMPOSE_FILE when
// COMPOSE_PATH_SEPARATOR does not say otherwise: ':' on Unix, ';' on Windows.
func PathSeparator() string {
	if runtime.GOOS == "windows" {
		return ";"
	}
	return ":"
}

// SplitComposeFile splits the value of COMPOSE_FILE into paths. sep is the
// value of COMPOSE_PATH_SEPARATOR; empty means the OS default. Empty items
// are dropped.
func SplitComposeFile(value, sep string) []string {
	if sep == "" {
		sep = PathSeparator()
	}
	var out []string
	for _, item := range strings.Split(value, sep) {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// EnvFileValue returns the value of key in the env files, which are read in
// order so that the last one that sets it wins, as Compose does. ok is false
// when none sets it. Files that cannot be read are skipped.
func EnvFileValue(files []string, key string) (value string, ok bool) {
	for _, f := range files {
		if v, found := envFileValue(f, key); found {
			value, ok = v, true
		}
	}
	return value, ok
}

// envFileValue reads KEY=VALUE lines of a dotenv file: blank lines and
// comments are skipped, `export ` is accepted, and single or double quotes
// around the value are removed. Variables in the value are not expanded.
func envFileValue(path, key string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	value, ok := "", false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			line, first = strings.TrimPrefix(line, "\xef\xbb\xbf"), false
		}
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		name, raw, found := strings.Cut(line, "=")
		if !found || strings.TrimSpace(name) != key {
			continue
		}
		value, ok = unquote(strings.TrimSpace(raw)), true
	}
	return value, ok
}

func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') {
		if end := strings.IndexByte(v[1:], v[0]); end >= 0 {
			return v[1 : end+1]
		}
	}
	if i := strings.Index(v, " #"); i >= 0 { // inline comment of an unquoted value
		v = strings.TrimSpace(v[:i])
	}
	return v
}

// ResolvePaths makes the paths absolute, relative ones against dir.
func ResolvePaths(dir string, paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, filepath.FromSlash(p))
		}
		out[i] = filepath.Clean(p)
	}
	return out
}
