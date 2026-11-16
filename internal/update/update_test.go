package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ElMaxter99/Theo/internal/prefs"
)

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.11.0", "0.10.0", 1},
		{"0.10.9", "0.11.0", -1},
		{"0.11.0", "0.11.0", 0},
		{"v0.11.0", "0.11.0", 0},
		{"0.12.0-rc.1", "0.11.0", 1},
		{"0.11.0-rc.2", "0.11.0", -1},
		{"0.11.0-rc.2", "0.11.0-rc.1", 1},
		{"0.11.0", "0.11.0-rc.9", 1},
		{"1.0.0", "0.99.99", 1},
	}
	for _, c := range cases {
		if got := Compare(ParseVersion(c.a), ParseVersion(c.b)); got != c.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	for _, dev := range []string{"0.0.0-dev", "dev", "0.1", "1.2.3-beta", "1.2.3-rc.x", ""} {
		if !ParseVersion(dev).Dev {
			t.Errorf("%q should be a development version", dev)
		}
		if Newer("9.9.9", dev) || Newer(dev, "0.1.0") {
			t.Errorf("a development version %q never updates and is never offered", dev)
		}
	}
	if !Newer("0.11.0", "0.11.0-rc.3") {
		t.Error("the final release replaces its release candidate")
	}
	if got := ParseVersion("v0.12.0-rc.2").String(); got != "0.12.0-rc.2" {
		t.Errorf("String = %q", got)
	}
}

func TestNewest(t *testing.T) {
	list := []Release{
		{Tag: "v0.10.0"}, {Tag: "v0.11.0-rc.1", Prerelease: true}, {Tag: "v0.11.0-rc.2", Prerelease: true},
		{Tag: "v0.9.0"}, {Tag: "v0.12.0", Draft: true}, {Tag: "nightly"},
	}
	if r, ok := Newest(list, false); !ok || r.Tag != "v0.10.0" {
		t.Errorf("stable: %v %v", r.Tag, ok)
	}
	if r, ok := Newest(list, true); !ok || r.Tag != "v0.11.0-rc.2" {
		t.Errorf("with rc: %v %v", r.Tag, ok)
	}
	list = append(list, Release{Tag: "v0.11.0"})
	if r, _ := Newest(list, true); r.Tag != "v0.11.0" {
		t.Errorf("the final release beats its candidates: %v", r.Tag)
	}
	if _, ok := Newest(nil, true); ok {
		t.Error("no releases, no answer")
	}
}

func newChecker(t *testing.T, current string, rels []Release, calls *int) Checker {
	t.Helper()
	return Checker{
		Store: prefs.Store{Dir: t.TempDir()}, Current: current, Desktop: true,
		List: func(context.Context) ([]Release, error) { *calls++; return rels, nil },
	}
}

func TestCheckerChannelsAndNotice(t *testing.T) {
	rels := []Release{{Tag: "v0.11.0"}, {Tag: "v0.12.0-rc.1", Prerelease: true}}
	calls := 0
	c := newChecker(t, "v0.11.0", rels, &calls)
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	c.Now = func() time.Time { return now }

	if !c.Enabled() {
		t.Error("the desktop app looks by default")
	}
	cli := c
	cli.Desktop = false
	if cli.Enabled() {
		t.Error("the command line alone does not look by default")
	}
	if err := c.Store.Set("update-check", "off"); err != nil || c.Enabled() {
		t.Error("the person's off wins over the default")
	}
	_ = c.Store.Unset("update-check")

	info, err := c.Check(context.Background(), false)
	if err != nil || info.Available || calls != 1 {
		t.Fatalf("stable channel, nothing newer: %+v %v calls=%d", info, err, calls)
	}
	// Not asked again before the interval, asked after it.
	_, _ = c.Check(context.Background(), false)
	if calls != 1 {
		t.Errorf("asked again at once: %d", calls)
	}
	now = now.Add(Interval + time.Minute)
	_, _ = c.Check(context.Background(), false)
	if calls != 2 {
		t.Errorf("not asked after the interval: %d", calls)
	}

	// Opting into candidates offers the candidate; opting out hides it at once.
	if err := c.Store.Set("update-channel", "rc"); err != nil {
		t.Fatal(err)
	}
	info, _ = c.Check(context.Background(), true)
	if !info.Available || info.Latest != "0.12.0-rc.1" || !info.Prerelease {
		t.Fatalf("rc channel: %+v", info)
	}
	if _, first := c.ShouldNotify(); !first {
		t.Error("the first notice of a version is shown")
	}
	if _, first := c.ShouldNotify(); first {
		t.Error("the same version is notified once")
	}
	if err := c.Dismiss(); err != nil {
		t.Fatal(err)
	}
	if !c.Info().Dismissed {
		t.Error("dismissed banner is remembered")
	}
	_ = c.Store.Set("update-channel", "stable")
	if c.Info().Available {
		t.Error("back to stable hides the candidate")
	}
	if err := c.Store.Set("update-channel", "nightly"); err == nil {
		t.Error("channel must be stable or rc")
	}
}

func TestCheckerDevAndErrors(t *testing.T) {
	calls := 0
	c := newChecker(t, "0.0.0-dev", []Release{{Tag: "v9.0.0"}}, &calls)
	if info, _ := c.Check(context.Background(), true); info.Available || calls != 0 {
		t.Error("a development build never asks and never updates")
	}
	c = Checker{Store: prefs.Store{Dir: t.TempDir()}, Current: "0.10.0", List: func(context.Context) ([]Release, error) { return nil, errors.New("offline") }}
	if _, err := c.Check(context.Background(), true); err == nil {
		t.Error("the error is returned")
	}
	if c.Due() {
		t.Error("a failed look is not repeated at once")
	}
}

// fixtures

func tarGz(t *testing.T, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "theo_v9/" + name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte(content))
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, c := range files {
		w, _ := zw.Create(n)
		_, _ = w.Write([]byte(c))
	}
	_ = zw.Close()
	return buf.Bytes()
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// releaseServer serves the files and returns a release listing them.
func releaseServer(t *testing.T, tag string, files map[string][]byte, sums map[string]string) Release {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b, ok := files[filepath.Base(r.URL.Path)]; ok {
			_, _ = w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Setenv(EnvAPI, srv.URL+"/list")
	rel := Release{Tag: tag}
	var cs string
	for n, b := range files {
		rel.Assets = append(rel.Assets, Asset{Name: n, URL: srv.URL + "/dl/" + n, Size: int64(len(b))})
		s := sum(b)
		if v, ok := sums[n]; ok {
			s = v
		}
		cs += fmt.Sprintf("%s  %s\n", s, n)
	}
	rel.Assets = append(rel.Assets, Asset{Name: "checksums.txt", URL: srv.URL + "/dl/checksums.txt"})
	files["checksums.txt"] = []byte(cs)
	return rel
}

func install(t *testing.T, names ...string) (dir string) {
	t.Helper()
	dir = t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("old "+n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestApplyReplacesBothPrograms(t *testing.T) {
	dir := install(t, "theo", "theo-desktop")
	files := map[string][]byte{
		"theo_v0.11.1_linux_amd64.tar.gz":         tarGz(t, "theo", "new theo"),
		"theo-desktop_v0.11.1_linux_amd64.tar.gz": tarGz(t, "theo-desktop", "new desktop"),
	}
	rel := releaseServer(t, "v0.11.1", files, nil)
	var last int64
	res, err := Apply(context.Background(), rel, Target{Exe: filepath.Join(dir, "theo-desktop"), GOOS: "linux", GOARCH: "amd64"}, func(d, _ int64) { last = d })
	if err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(dir, "theo")) != "new theo" || read(t, filepath.Join(dir, "theo-desktop")) != "new desktop" {
		t.Error("both programs are replaced")
	}
	if len(res.Replaced) != 2 || last == 0 {
		t.Errorf("result %+v, progress %d", res, last)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.old")); len(left) != 0 {
		t.Errorf("leftovers: %v", left)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".theo-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
	st, _ := os.Stat(filepath.Join(dir, "theo"))
	if st.Mode()&0o100 == 0 {
		t.Error("the new program is executable")
	}
}

func TestApplyWindowsZipAndOnlyTheInstalledProgram(t *testing.T) {
	dir := install(t, "theo.exe")
	files := map[string][]byte{"theo_v0.11.1_windows_amd64.zip": zipOf(t, map[string]string{"theo_v0.11.1_windows_amd64/theo.exe": "new exe", "theo_v0.11.1_windows_amd64/README.md": "x"})}
	rel := releaseServer(t, "v0.11.1", files, nil)
	if _, err := Apply(context.Background(), rel, Target{Exe: filepath.Join(dir, "theo.exe"), GOOS: "windows", GOARCH: "amd64"}, nil); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(dir, "theo.exe")) != "new exe" {
		t.Error("exe replaced")
	}
	if _, err := os.Stat(filepath.Join(dir, "theo-desktop.exe")); err == nil {
		t.Error("a program that was not installed is not created")
	}
}

func TestApplyRefusesABadChecksumAndChangesNothing(t *testing.T) {
	dir := install(t, "theo", "theo-desktop")
	files := map[string][]byte{
		"theo_v0.11.1_linux_amd64.tar.gz":         tarGz(t, "theo", "new theo"),
		"theo-desktop_v0.11.1_linux_amd64.tar.gz": tarGz(t, "theo-desktop", "new desktop"),
	}
	rel := releaseServer(t, "v0.11.1", files, map[string]string{"theo-desktop_v0.11.1_linux_amd64.tar.gz": sum([]byte("something else"))})
	_, err := Apply(context.Background(), rel, Target{Exe: filepath.Join(dir, "theo"), GOOS: "linux", GOARCH: "amd64"}, nil)
	if !errors.Is(err, ErrChecksum) {
		t.Fatalf("err = %v, want ErrChecksum", err)
	}
	if read(t, filepath.Join(dir, "theo")) != "old theo" || read(t, filepath.Join(dir, "theo-desktop")) != "old theo-desktop" {
		t.Error("nothing is replaced when one download is not verified")
	}
}

func TestApplyNeedsChecksumsAndAnArchive(t *testing.T) {
	dir := install(t, "theo")
	tg := Target{Exe: filepath.Join(dir, "theo"), GOOS: "linux", GOARCH: "amd64"}
	if _, err := Apply(context.Background(), Release{Tag: "v1.0.0"}, tg, nil); err == nil {
		t.Error("a release without its archive is not installed")
	}
	rel := releaseServer(t, "v1.0.0", map[string][]byte{"theo_v1.0.0_linux_amd64.tar.gz": tarGz(t, "theo", "n")}, nil)
	var kept []Asset
	for _, a := range rel.Assets {
		if a.Name != "checksums.txt" {
			kept = append(kept, a)
		}
	}
	rel.Assets = kept
	if _, err := Apply(context.Background(), rel, tg, nil); err == nil {
		t.Error("without checksums.txt nothing can be verified, so nothing is installed")
	}
	if read(t, filepath.Join(dir, "theo")) != "old theo" {
		t.Error("untouched")
	}
}

func TestApplyOnlyFromTheRepositoryAndOnlyInstalledCopies(t *testing.T) {
	dir := install(t, "theo")
	rel := Release{Tag: "v1.0.0", Assets: []Asset{
		{Name: "theo_v1.0.0_linux_amd64.tar.gz", URL: "https://evil.example/theo.tar.gz"},
		{Name: "checksums.txt", URL: "https://evil.example/checksums.txt"},
	}}
	if _, err := Apply(context.Background(), rel, Target{Exe: filepath.Join(dir, "theo"), GOOS: "linux", GOARCH: "amd64"}, nil); err == nil {
		t.Error("a download from another host is refused")
	}
	if allowedDownload("https://github.com/ElMaxter99/Theo/releases/download/v1/x") != true || allowedDownload("https://github.com/other/Theo/releases/download/v1/x") {
		t.Error("only this repository's release downloads are allowed")
	}
	for _, exe := range []string{"", "/tmp/theo.test", "/tmp/go-build/exe/main"} {
		if _, err := PlanFor(Target{Exe: exe, GOOS: "linux", GOARCH: "amd64"}, "v1"); !errors.Is(err, ErrUnmanaged) {
			t.Errorf("%q: %v", exe, err)
		}
	}
}

func TestPlanForMacBundle(t *testing.T) {
	p, err := PlanFor(Target{Exe: "/Applications/Theo.app/Contents/MacOS/theo-desktop", GOOS: "darwin", GOARCH: "arm64"}, "v0.11.1")
	if err != nil || p.Bundle != "/Applications/Theo.app" || p.Items[0].Asset != "theo-desktop_v0.11.1_darwin_arm64.zip" {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestApplyMacBundleReplacesTheWholeApp(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "Theo.app")
	if err := os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(app, "Contents", "MacOS", "theo-desktop")
	_ = os.WriteFile(exe, []byte("old"), 0o755)
	_ = os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte("old plist"), 0o644)
	z := zipOf(t, map[string]string{
		"theo-desktop_v0.11.1_darwin_arm64/Theo.app/Contents/MacOS/theo-desktop": "new",
		"theo-desktop_v0.11.1_darwin_arm64/Theo.app/Contents/Info.plist":         "new plist",
		"theo-desktop_v0.11.1_darwin_arm64/README.md":                            "readme",
	})
	rel := releaseServer(t, "v0.11.1", map[string][]byte{"theo-desktop_v0.11.1_darwin_arm64.zip": z}, nil)
	if _, err := Apply(context.Background(), rel, Target{Exe: exe, GOOS: "darwin", GOARCH: "arm64"}, nil); err != nil {
		t.Fatal(err)
	}
	if read(t, exe) != "new" || read(t, filepath.Join(app, "Contents", "Info.plist")) != "new plist" {
		t.Error("the whole bundle is replaced")
	}
	if _, err := os.Stat(app + ".old"); err == nil {
		t.Error("old bundle removed")
	}
}

func TestSwapRollsBack(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	_ = os.WriteFile(a, []byte("old a"), 0o755)
	_ = os.WriteFile(b, []byte("old b"), 0o755)
	newA := filepath.Join(dir, "newA")
	_ = os.WriteFile(newA, []byte("new a"), 0o755)
	// The second staged file does not exist: its move fails after the first succeeded.
	if err := swap([]string{newA, filepath.Join(dir, "missing")}, []string{a, b}); err == nil {
		t.Fatal("expected an error")
	}
	if read(t, a) != "old a" || read(t, b) != "old b" {
		t.Error("the first replacement is put back when the second fails")
	}
}

func TestParseChecksums(t *testing.T) {
	h := sum([]byte("x"))
	m := ParseChecksums(h + "  a.tar.gz\n" + h + " *b.zip\nnot a line\n")
	if m["a.tar.gz"] != h || m["b.zip"] != h || len(m) != 2 {
		t.Errorf("%v", m)
	}
}

func TestCleanupRemovesLeftovers(t *testing.T) {
	dir := install(t, "theo.exe", "theo.exe.old", "theo-desktop.exe.old")
	_ = os.MkdirAll(filepath.Join(dir, ".theo-update-123"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, ".theo-update-recent"), 0o755)
	old := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(filepath.Join(dir, ".theo-update-123"), old, old)
	Cleanup(Target{Exe: filepath.Join(dir, "theo.exe"), GOOS: "windows"})
	if _, err := os.Stat(filepath.Join(dir, "theo.exe")); err != nil {
		t.Error("the program stays")
	}
	for _, n := range []string{"theo.exe.old", "theo-desktop.exe.old", ".theo-update-123"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err == nil {
			t.Errorf("%s should be gone", n)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".theo-update-recent")); err != nil {
		t.Error("a recent staging folder may belong to a running update")
	}
}
