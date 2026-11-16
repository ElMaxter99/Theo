package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var (
	// ErrUnmanaged means the running program is not an installed Theo (a
	// development build, a test binary, a renamed copy): it is not replaced.
	ErrUnmanaged = errors.New("this copy of Theo was not installed from a release, so it cannot update itself")
	// ErrNotWritable means Theo has no permission to replace itself (a package
	// installed for the whole system): the person installs the new version.
	ErrNotWritable = errors.New("Theo cannot write to its install folder, so it cannot replace itself")
)

// maxDownload bounds an archive and what is extracted from it.
const maxDownload = 500 << 20

// Target is the program to replace.
type Target struct {
	Exe    string // path of the running program, symbolic links resolved
	GOOS   string
	GOARCH string
}

// CurrentTarget is the running program.
func CurrentTarget() Target {
	return Target{Exe: Executable(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
}

// Item is one file to replace with a file of an archive.
type Item struct {
	Dest  string // file to replace
	Asset string // archive of the release
	Name  string // file inside the archive, by base name
}

// Plan says what an update replaces.
type Plan struct {
	// Bundle is the macOS application folder replaced as a whole (its code
	// signature covers every file). Empty for the other layouts.
	Bundle string
	Items  []Item
}

func exeExt(goos string) string {
	if goos == "windows" {
		return ".exe"
	}
	return ""
}

// ArchiveName is the release file of a program for a system.
func ArchiveName(program, tag, goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" || (goos == "darwin" && program == "theo-desktop") {
		ext = ".zip"
	}
	return fmt.Sprintf("%s_%s_%s_%s%s", program, tag, goos, goarch, ext)
}

// PlanFor decides what to replace, from where the running program lives:
// theo and theo-desktop side by side in a folder, or the macOS application.
func PlanFor(t Target, tag string) (Plan, error) {
	if t.Exe == "" {
		return Plan{}, ErrUnmanaged
	}
	base := strings.TrimSuffix(filepath.Base(t.Exe), exeExt(t.GOOS))
	if base != "theo" && base != "theo-desktop" {
		return Plan{}, ErrUnmanaged
	}
	if t.GOOS == "darwin" {
		if i := strings.Index(t.Exe, ".app/Contents/MacOS/"); i >= 0 {
			return Plan{Bundle: t.Exe[:i+4], Items: []Item{{Asset: ArchiveName("theo-desktop", tag, t.GOOS, t.GOARCH)}}}, nil
		}
	}
	dir := filepath.Dir(t.Exe)
	var p Plan
	for _, name := range []string{"theo", "theo-desktop"} {
		dest := filepath.Join(dir, name+exeExt(t.GOOS))
		if name != base {
			if st, err := os.Stat(dest); err != nil || st.IsDir() {
				continue
			}
		}
		p.Items = append(p.Items, Item{Dest: dest, Asset: ArchiveName(name, tag, t.GOOS, t.GOARCH), Name: name + exeExt(t.GOOS)})
	}
	return p, nil
}

// Progress receives the bytes downloaded so far and the total (0 if unknown).
type Progress func(done, total int64)

// Result says what was installed.
type Result struct {
	Tag      string   `json:"tag"`
	Replaced []string `json:"replaced"`
}

// Apply downloads the release for this system, checks every archive against
// the release's checksums.txt and replaces the programs. Nothing is replaced
// unless every download is verified; if a replacement fails, those already
// made are put back.
func Apply(ctx context.Context, rel Release, t Target, progress Progress) (Result, error) {
	plan, err := PlanFor(t, rel.Tag)
	if err != nil {
		return Result{}, err
	}
	parent := filepath.Dir(t.Exe)
	if plan.Bundle != "" {
		parent = filepath.Dir(plan.Bundle)
	}
	if err := checkWritable(parent); err != nil {
		return Result{}, err
	}
	sums, err := fetchChecksums(ctx, rel)
	if err != nil {
		return Result{}, err
	}
	work, err := os.MkdirTemp(parent, ".theo-update-")
	if err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrNotWritable, err)
	}
	defer os.RemoveAll(work)

	res := Result{Tag: rel.Tag}
	var staged []string // files or the bundle ready to move into place
	var dests []string
	for i, it := range plan.Items {
		a, ok := rel.Asset(it.Asset)
		if !ok {
			if plan.Bundle == "" && i > 0 { // a sibling the release does not ship for this system
				continue
			}
			return Result{}, fmt.Errorf("release %s has no %s for this system", rel.Tag, it.Asset)
		}
		want, ok := sums[it.Asset]
		if !ok {
			return Result{}, fmt.Errorf("checksums.txt of %s does not list %s", rel.Tag, it.Asset)
		}
		archive := filepath.Join(work, fmt.Sprintf("archive%d", i))
		if err := download(ctx, a, archive, want, progress); err != nil {
			return Result{}, err
		}
		if plan.Bundle != "" {
			dst := filepath.Join(work, "Theo.app")
			if err := extractBundle(archive, dst); err != nil {
				return Result{}, err
			}
			staged, dests = append(staged, dst), append(dests, plan.Bundle)
			continue
		}
		dst := filepath.Join(work, fmt.Sprintf("file%d", i))
		if err := extractFile(archive, it.Name, dst); err != nil {
			return Result{}, err
		}
		if err := os.Chmod(dst, 0o755); err != nil {
			return Result{}, err
		}
		staged, dests = append(staged, dst), append(dests, it.Dest)
	}
	if err := swap(staged, dests); err != nil {
		return Result{}, err
	}
	res.Replaced = dests
	return res, nil
}

// WritableFor says whether the program of t can be replaced: the folder it
// lives in (or the one holding its macOS application) accepts new files.
func WritableFor(t Target) error {
	if t.Exe == "" {
		return ErrUnmanaged
	}
	dir := filepath.Dir(t.Exe)
	if i := strings.Index(t.Exe, ".app/Contents/MacOS/"); t.GOOS == "darwin" && i >= 0 {
		dir = filepath.Dir(t.Exe[:i+4])
	}
	return checkWritable(dir)
}

// checkWritable proves Theo can create and remove files in dir.
func checkWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".theo-write-")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNotWritable, err)
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

// swap moves each staged file over its destination, keeping the old one as
// <dest>.old until all are done, and puts the old ones back on any failure.
func swap(staged, dests []string) error {
	var done []int
	rollback := func() {
		for i := len(done) - 1; i >= 0; i-- {
			d := dests[done[i]]
			_ = os.RemoveAll(d)
			_ = os.Rename(d+".old", d)
		}
	}
	for i := range dests {
		old := dests[i] + ".old"
		_ = os.RemoveAll(old) // a leftover of an earlier update
		if err := os.Rename(dests[i], old); err != nil {
			rollback()
			return fmt.Errorf("replacing %s: %w", dests[i], err)
		}
		if err := moveInto(staged[i], dests[i]); err != nil {
			_ = os.Rename(old, dests[i])
			rollback()
			return fmt.Errorf("replacing %s: %w", dests[i], err)
		}
		done = append(done, i)
	}
	for _, d := range dests {
		_ = os.RemoveAll(d + ".old") // Windows keeps a running program: Cleanup removes it later
	}
	return nil
}

func moveInto(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	return errors.New("could not move the new version into place")
}

// Cleanup removes what an earlier update left beside the running program (on
// Windows the old program cannot be deleted while it runs).
func Cleanup(t Target) {
	if t.Exe == "" {
		return
	}
	dirs := []string{filepath.Dir(t.Exe)}
	if i := strings.Index(t.Exe, ".app/Contents/MacOS/"); i >= 0 {
		dirs = []string{filepath.Dir(t.Exe[:i+4])}
	}
	for _, d := range dirs {
		for _, n := range []string{"theo", "theo-desktop", "Theo.app"} {
			for _, ext := range []string{"", ".exe"} {
				_ = os.RemoveAll(filepath.Join(d, n+ext+".old"))
			}
		}
		// Staging folders of an update that died; a recent one may be running.
		left, _ := filepath.Glob(filepath.Join(d, ".theo-update-*"))
		for _, l := range left {
			if st, err := os.Stat(l); err == nil && time.Since(st.ModTime()) > time.Hour {
				_ = os.RemoveAll(l)
			}
		}
	}
}

func fetchChecksums(ctx context.Context, rel Release) (map[string]string, error) {
	a, ok := rel.Asset("checksums.txt")
	if !ok {
		return nil, fmt.Errorf("release %s has no checksums.txt, so nothing can be verified", rel.Tag)
	}
	if !allowedDownload(a.URL) {
		return nil, fmt.Errorf("refusing to download from %s", a.URL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "theo-update")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading checksums.txt: %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	return ParseChecksums(string(b)), nil
}

// ParseChecksums reads sha256sum output: "<hash>  <name>" per line.
func ParseChecksums(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && len(f[0]) == 64 {
			out[strings.TrimPrefix(f[1], "*")] = strings.ToLower(f[0])
		}
	}
	return out
}

// ErrChecksum means a download does not match the published checksum.
var ErrChecksum = errors.New("the download does not match its published SHA-256, so nothing was installed")

func download(ctx context.Context, a Asset, dst, wantSum string, progress Progress) error {
	if !allowedDownload(a.URL) {
		return fmt.Errorf("refusing to download from %s", a.URL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "theo-update")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading %s: %s", a.Name, resp.Status)
	}
	total := resp.ContentLength
	if total <= 0 {
		total = a.Size
	}
	if total > maxDownload {
		return fmt.Errorf("%s is larger than %d MB: refusing it", a.Name, maxDownload>>20)
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	var done int64
	buf := make([]byte, 64<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			done += int64(n)
			if done > maxDownload {
				return fmt.Errorf("%s is larger than %d MB: refusing it", a.Name, maxDownload>>20)
			}
			h.Write(buf[:n])
			if _, err := f.Write(buf[:n]); err != nil {
				return err
			}
			if progress != nil {
				progress(done, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	if hex.EncodeToString(h.Sum(nil)) != wantSum {
		return ErrChecksum
	}
	return nil
}

// extractFile copies the first file called name (by base name) out of a
// .tar.gz or .zip archive into dst.
func extractFile(archive, name, dst string) error {
	if strings.HasSuffix(archive, ".zip") || isZip(archive) {
		zr, err := zip.OpenReader(archive)
		if err != nil {
			return err
		}
		defer zr.Close()
		for _, f := range zr.File {
			if f.FileInfo().IsDir() || filepath.Base(f.Name) != name {
				continue
			}
			r, err := f.Open()
			if err != nil {
				return err
			}
			defer r.Close()
			return writeLimited(dst, r, 0o755)
		}
		return fmt.Errorf("%s is not in the archive", name)
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("%s is not in the archive", name)
		}
		if err != nil {
			return err
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == name {
			return writeLimited(dst, tr, 0o755)
		}
	}
}

// isZip looks at the first bytes: the downloaded file has no extension.
func isZip(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	b := make([]byte, 4)
	_, _ = io.ReadFull(f, b)
	return string(b) == "PK\x03\x04"
}

func writeLimited(dst string, r io.Reader, mode os.FileMode) error {
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(r, maxDownload+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxDownload {
		err = errors.New("a file of the archive is too large")
	}
	return err
}

// extractBundle unpacks Theo.app from the macOS archive into dst.
func extractBundle(archive, dst string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()
	found := false
	for _, f := range zr.File {
		i := strings.Index(f.Name, "Theo.app/")
		if i < 0 {
			continue
		}
		rel := f.Name[i+len("Theo.app/"):]
		target := filepath.Join(dst, filepath.FromSlash(rel))
		if !strings.HasPrefix(target, dst+string(filepath.Separator)) && target != dst {
			return fmt.Errorf("unsafe path in the archive: %s", f.Name)
		}
		found = true
		mode := f.Mode()
		switch {
		case f.FileInfo().IsDir():
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case mode&os.ModeSymlink != 0:
			r, err := f.Open()
			if err != nil {
				return err
			}
			link, err := io.ReadAll(io.LimitReader(r, 4096))
			r.Close()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(string(link), target); err != nil {
				return err
			}
		default:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			r, err := f.Open()
			if err != nil {
				return err
			}
			err = writeLimited(target, r, mode.Perm()|0o600)
			r.Close()
			if err != nil {
				return err
			}
		}
	}
	if !found {
		return errors.New("Theo.app is not in the archive")
	}
	return nil
}

// Relaunch starts the program again, detached, with the same arguments. The
// caller then exits.
func Relaunch(exe string, args []string) error {
	cmd := exec.Command(exe, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	detach(cmd)
	return cmd.Start()
}

func resolve(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
