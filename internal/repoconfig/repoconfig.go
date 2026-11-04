// Package repoconfig reads the settings a repository carries with it: the
// versioned file .theo.json and Claude Code's .worktreeinclude. They describe
// what a new worktree needs (files to copy, commands to run) so that anyone,
// or any agent, gets the same result.
package repoconfig

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// File names, in the root of the repository.
const (
	FileName         = ".theo.json"
	WorktreeIncludes = ".worktreeinclude"
)

// maxFile is the largest file Theo reads; these files are small.
const maxFile = 1 << 20

// Config is the content of .theo.json.
type Config struct {
	// Version of the format. Only 1 exists; 0 (absent) means 1.
	Version int `json:"version,omitempty"`
	// Copy lists root-relative files that Git does not track and that a new
	// worktree gets from the main one (like `project set --copy-on-add`).
	Copy []string `json:"copy,omitempty"`
	// AfterCreate lists shell commands run in a new worktree once it exists
	// (install dependencies, generate code). They run code from the
	// repository, so Theo asks before running them the first time.
	AfterCreate []string `json:"after_create,omitempty"`
}

// Loaded is what a repository declares, with where it came from.
type Loaded struct {
	Config
	// Sources are the files read, relative to the root.
	Sources []string `json:"sources,omitempty"`
	// Notes are lines that were ignored, and why.
	Notes []string `json:"notes,omitempty"`
}

// Empty reports whether the repository declares nothing.
func (l Loaded) Empty() bool { return len(l.Copy) == 0 && len(l.AfterCreate) == 0 }

// Load reads .theo.json and .worktreeinclude from root. Missing files are
// not an error. A .theo.json that is not valid JSON is: silently ignoring a
// file the team wrote would hide a mistake.
func Load(root string) (Loaded, error) {
	var out Loaded
	seen := map[string]bool{}
	add := func(list *[]string, items ...string) {
		for _, it := range items {
			if !seen[it] {
				seen[it] = true
				*list = append(*list, it)
			}
		}
	}

	data, err := readSmall(filepath.Join(root, FileName))
	switch {
	case err == nil:
		var c Config
		data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // Notepad and PowerShell write a BOM
		dec := json.NewDecoder(bytes.NewReader(data))
		if err := dec.Decode(&c); err != nil {
			return out, fmt.Errorf("%s is not valid: %w", FileName, err)
		}
		if c.Version > 1 {
			out.Notes = append(out.Notes, fmt.Sprintf("%s has version %d; this Theo reads version 1, so some settings may be ignored", FileName, c.Version))
		}
		out.Version = c.Version
		out.Sources = append(out.Sources, FileName)
		add(&out.Copy, trimAll(c.Copy)...)
		out.AfterCreate = trimAll(c.AfterCreate)
	case !errors.Is(err, fs.ErrNotExist):
		return out, fmt.Errorf("%s: %w", FileName, err)
	}

	data, err = readSmall(filepath.Join(root, WorktreeIncludes))
	switch {
	case err == nil:
		out.Sources = append(out.Sources, WorktreeIncludes)
		patterns, notes := ParseInclude(string(data))
		add(&out.Copy, patterns...)
		out.Notes = append(out.Notes, notes...)
	case !errors.Is(err, fs.ErrNotExist):
		out.Notes = append(out.Notes, fmt.Sprintf("%s could not be read: %v", WorktreeIncludes, err))
	}
	return out, nil
}

// ParseInclude reads the lines of a .worktreeinclude: one file or glob per
// line, `#` comments and blank lines ignored. Negations (`!x`) and
// directories are not supported and are reported in the notes.
func ParseInclude(text string) (patterns, notes []string) {
	text = strings.TrimPrefix(text, "\ufeff")
	for i, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "!"):
			notes = append(notes, fmt.Sprintf("%s line %d: negations (%s) are not supported; ignored", WorktreeIncludes, i+1, line))
			continue
		case strings.HasSuffix(line, "/"):
			notes = append(notes, fmt.Sprintf("%s line %d: %s is a directory; Theo copies files, so list them or use a glob; ignored", WorktreeIncludes, i+1, line))
			continue
		}
		patterns = append(patterns, strings.TrimPrefix(line, "/"))
	}
	return patterns, notes
}

// Fingerprint identifies a list of commands. Theo remembers the one the
// person approved; a changed list asks again.
func Fingerprint(commands []string) string {
	h := sha256.New()
	for _, c := range commands {
		h.Write([]byte(c))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func trimAll(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func readSmall(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > maxFile {
		return nil, fmt.Errorf("larger than %d bytes", maxFile)
	}
	buf := make([]byte, info.Size())
	n, err := f.Read(buf)
	if err != nil && n == 0 && info.Size() > 0 {
		return nil, err
	}
	return buf[:n], nil
}
