package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/compose"
)

// dataCompose is a fake Docker with volumes that hold a string of data.
type dataCompose struct {
	*fakeCompose
	vols      map[string]*compose.DockerVolume
	data      map[string]string
	clears    []bool // the clear argument of each extract or copy
	archives  int
	failAfter bool // ArchiveVolume fails
}

func (d *dataCompose) add(name, project, key, data string) {
	d.vols[name] = &compose.DockerVolume{Name: name, Labels: map[string]string{compose.LabelProject: project, compose.LabelVolume: key}}
	d.data[name] = data
}

func (d *dataCompose) Volumes(_ context.Context, project string) ([]compose.DockerVolume, error) {
	var out []compose.DockerVolume
	for _, v := range d.vols {
		if v.Labels[compose.LabelProject] == project {
			out = append(out, *v)
		}
	}
	return out, nil
}

func (d *dataCompose) RemoveVolume(ctx context.Context, name string) error {
	delete(d.vols, name)
	delete(d.data, name)
	return d.fakeCompose.RemoveVolume(ctx, name)
}

func (d *dataCompose) EnsureImage(context.Context, string) error { return nil }

func (d *dataCompose) InspectVolume(_ context.Context, name string) (*compose.DockerVolume, error) {
	return d.vols[name], nil
}

func (d *dataCompose) CreateVolume(_ context.Context, name string, labels map[string]string) error {
	d.vols[name] = &compose.DockerVolume{Name: name, Labels: labels}
	d.data[name] = ""
	return nil
}

func (d *dataCompose) VolumeIsEmpty(_ context.Context, _, volume string) (bool, error) {
	return d.data[volume] == "", nil
}

func (d *dataCompose) ArchiveVolume(_ context.Context, _, volume string, w io.Writer) error {
	d.archives++
	if d.failAfter {
		return errors.New("tar failed")
	}
	_, err := io.WriteString(w, "TAR:"+d.data[volume])
	return err
}

func (d *dataCompose) ExtractVolume(_ context.Context, _, volume string, r io.Reader, clear bool) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	d.clears = append(d.clears, clear)
	d.data[volume] = strings.TrimPrefix(string(b), "TAR:")
	return nil
}

func (d *dataCompose) CopyVolume(_ context.Context, _, from, to string, clear bool) error {
	d.clears = append(d.clears, clear)
	d.data[to] = d.data[from]
	return nil
}

// dataFixture has two instances of project app: the main worktree (Compose
// project "app") and feature/login ("app-app-feature" in the other tests;
// here "app-app-feature"), both declaring the volume "data" and the external
// volume "shared".
func dataFixture(t *testing.T) (*Engine, *dataCompose, *Instance, *Instance) {
	t.Helper()
	e, _, _, fc := newEngine()
	d := &dataCompose{fakeCompose: fc, vols: map[string]*compose.DockerVolume{}, data: map[string]string{}}
	e.compose = d
	e.dataDir = t.TempDir()
	ctx := context.Background()
	if _, err := e.AddProject(ctx, root, "", ""); err != nil {
		t.Fatal(err)
	}
	cfg := func(name string) *compose.Project {
		return &compose.Project{Name: name, Services: map[string]compose.Service{"api": {}}, Volumes: map[string]compose.VolumeConfig{
			"data":   {Name: name + "_data"}, // as `docker compose config` renders it
			"shared": {Name: "gr-shared", External: json.RawMessage("true")},
		}}
	}
	fc.configs[root] = cfg("app")
	fc.configs[feature] = cfg("app") // same file, same resolved name
	main, err := e.Instance(ctx, "app", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	feat, err := e.Instance(ctx, "app", "feature/login", nil)
	if err != nil {
		t.Fatal(err)
	}
	return e, d, main, feat
}

func TestBackupWritesArchiveAndManifest(t *testing.T) {
	e, d, main, _ := dataFixture(t)
	ctx := context.Background()
	d.add("app_data", "app", "data", "hello")
	d.users = map[string][]string{"app_data": {"app-api-1"}}

	report, err := e.BackupVolumes(ctx, main, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Backups) != 1 || len(report.Warnings) != 1 || !strings.Contains(report.Warnings[0], "inconsistent") {
		t.Fatalf("report = %+v", report)
	}
	b := report.Backups[0]
	if want := filepath.Join(e.dataDir, "backups", "app"); filepath.Dir(b.File) != want {
		t.Errorf("default directory = %s, want %s", filepath.Dir(b.File), want)
	}
	if want := "app__app__data__20261029T080000Z.tar.gz"; filepath.Base(b.File) != want {
		t.Errorf("file = %s, want %s", filepath.Base(b.File), want)
	}
	got, err := os.ReadFile(b.File)
	if err != nil || string(got) != "TAR:hello" {
		t.Fatalf("archive = %q, %v", got, err)
	}
	sum, _ := fileSHA256(b.File)
	var m BackupManifest
	raw, _ := os.ReadFile(b.ManifestFile)
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m.SHA256 != sum || m.Size != 9 || m.Volume != "data" || m.Docker != "app_data" || m.Instance != "app" || m.Image != DefaultHelperImage || m.Archive != filepath.Base(b.File) {
		t.Errorf("manifest = %+v", m)
	}
	if ev, _ := e.History(ctx, "app", "", 0); len(ev) != 1 || ev[0].Operation != "volumes backup" {
		t.Errorf("history = %+v", ev)
	}
	if _, err := os.Stat(b.File + ".part"); err == nil {
		t.Error("partial file left behind")
	}
}

func TestBackupHelperImageFromEnvironment(t *testing.T) {
	e, d, main, _ := dataFixture(t)
	d.add("app_data", "app", "data", "x")
	t.Setenv(EnvHelperImage, "busybox:1.36")
	report, err := e.BackupVolumes(context.Background(), main, nil, t.TempDir())
	if err != nil || report.Backups[0].Image != "busybox:1.36" {
		t.Fatalf("%v %+v", err, report)
	}
}

func TestBackupRefusals(t *testing.T) {
	e, d, main, _ := dataFixture(t)
	ctx := context.Background()
	out := t.TempDir()
	d.add("app_data", "app", "data", "x")
	d.add("app-shared_cache", "app-shared", "cache", "y")
	d.add("other_data", "other", "data", "z")

	for sel, want := range map[string]string{
		"shared":           "external",
		"gr-shared":        "external",
		"app-shared_cache": "shared services",
		"nope":             "has no volume",
	} {
		if _, err := e.BackupVolumes(ctx, main, []string{sel}, out); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("backup %q: err = %v, want %q", sel, err, want)
		}
	}
	// A fixed-name volume that another project owns is not ours to read.
	main.config.Volumes["odd"] = compose.VolumeConfig{Name: "other_data"}
	if _, err := e.BackupVolumes(ctx, main, []string{"odd"}, out); err == nil || !strings.Contains(err.Error(), "another Compose project") {
		t.Errorf("foreign volume: %v", err)
	}
	delete(main.config.Volumes, "odd")

	// Never created: skipped without a name, an error with it.
	delete(d.vols, "app_data")
	if _, err := e.BackupVolumes(ctx, main, nil, out); err == nil || !strings.Contains(err.Error(), "nothing to back up") {
		t.Errorf("no volume: %v", err)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Errorf("refused backups wrote %d files", len(entries))
	}
	if d.archives != 0 {
		t.Error("a refused backup read data")
	}
}

func TestFailedBackupLeavesNoFile(t *testing.T) {
	e, d, main, _ := dataFixture(t)
	d.add("app_data", "app", "data", "x")
	d.failAfter = true
	out := t.TempDir()
	if _, err := e.BackupVolumes(context.Background(), main, nil, out); err == nil {
		t.Fatal("expected an error")
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Errorf("left %d files", len(entries))
	}
	if ev, _ := e.History(context.Background(), "app", "", 0); len(ev) != 1 || ev[0].Result != "error" {
		t.Errorf("history = %+v", ev)
	}
}

func backupOf(t *testing.T, e *Engine, d *dataCompose, inst *Instance, data string) string {
	t.Helper()
	name := inst.Compose + "_data"
	d.add(name, inst.Compose, "data", data)
	r, err := e.BackupVolumes(context.Background(), inst, nil, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return r.Backups[0].File
}

func TestRestoreRefusalsAndSuccess(t *testing.T) {
	e, d, main, feat := dataFixture(t)
	ctx := context.Background()
	file := backupOf(t, e, d, main, "precious")
	histBefore, _ := e.History(ctx, "app", "feature/login", 0)

	// Without a confirmation: tells what it would do, changes nothing.
	var ce *ConfirmError
	if _, err := e.RestoreVolume(ctx, feat, RestoreOptions{From: file}); !errors.As(err, &ce) || ce.Instance != "app-app-feature" {
		t.Fatalf("no confirmation: %v", err)
	}
	if _, ok := d.vols["app-app-feature_data"]; ok {
		t.Fatal("volume created without confirmation")
	}

	// Containers: refused even when confirmed.
	d.containers = []compose.Container{{Service: "api"}}
	if _, err := e.RestoreVolume(ctx, feat, RestoreOptions{From: file, Confirm: "app-app-feature"}); err == nil || !strings.Contains(err.Error(), "theo down") {
		t.Errorf("containers: %v", err)
	}
	d.containers = nil

	// Fresh instance: the volume is created with the Compose labels.
	res, err := e.RestoreVolume(ctx, feat, RestoreOptions{From: file, Confirm: "app-app-feature"})
	if err != nil || !res.Created || !res.Verified {
		t.Fatalf("restore: %v %+v", err, res)
	}
	v := d.vols["app-app-feature_data"]
	if v == nil || v.Labels[compose.LabelProject] != "app-app-feature" || v.Labels[compose.LabelVolume] != "data" {
		t.Fatalf("labels = %+v", v)
	}
	if d.data["app-app-feature_data"] != "precious" || d.clears[len(d.clears)-1] {
		t.Errorf("data = %q clears=%v", d.data["app-app-feature_data"], d.clears)
	}

	// It has data now: needs --replace.
	d.data["app-app-feature_data"] = "newer"
	if _, err := e.RestoreVolume(ctx, feat, RestoreOptions{From: file, Confirm: "app-app-feature"}); err == nil || !strings.Contains(err.Error(), "--replace") || d.data["app-app-feature_data"] != "newer" {
		t.Errorf("non-empty: %v data=%q", err, d.data["app-app-feature_data"])
	}
	// --replace without a confirmation still changes nothing.
	if _, err := e.RestoreVolume(ctx, feat, RestoreOptions{From: file, Replace: true}); !errors.As(err, &ce) || !strings.Contains(err.Error(), "for good") {
		t.Errorf("replace unconfirmed: %v", err)
	}
	res, err = e.RestoreVolume(ctx, feat, RestoreOptions{From: file, Confirm: "app-app-feature", Replace: true})
	if err != nil || !res.Replaced || d.data["app-app-feature_data"] != "precious" || !d.clears[len(d.clears)-1] {
		t.Errorf("replace: %v %+v data=%q", err, res, d.data["app-app-feature_data"])
	}

	histAfter, _ := e.History(ctx, "app", "feature/login", 0)
	if len(histAfter)-len(histBefore) != 2 || histAfter[len(histAfter)-1].Operation != "volumes restore" {
		t.Errorf("history = %+v", histAfter)
	}
}

func TestRestoreChecksTheBackup(t *testing.T) {
	e, d, main, feat := dataFixture(t)
	ctx := context.Background()
	file := backupOf(t, e, d, main, "precious")

	// Wrong volume named.
	if _, err := e.RestoreVolume(ctx, feat, RestoreOptions{From: file, Volume: "other", Confirm: "app-app-feature"}); err == nil {
		t.Error("a volume that is not in the manifest should be refused")
	}
	// Modified archive.
	if err := os.WriteFile(file, []byte("TAR:tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RestoreVolume(ctx, feat, RestoreOptions{From: file, Confirm: "app-app-feature"}); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Errorf("tampered: %v", err)
	}
	// No manifest: needs --volume, and then works unverified.
	bare := filepath.Join(t.TempDir(), "x.tar.gz")
	if err := os.WriteFile(bare, []byte("TAR:raw"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.RestoreVolume(ctx, feat, RestoreOptions{From: bare, Confirm: "app-app-feature"}); err == nil || !strings.Contains(err.Error(), "--volume") {
		t.Errorf("no manifest: %v", err)
	}
	res, err := e.RestoreVolume(ctx, feat, RestoreOptions{From: bare, Volume: "data", Confirm: "app-app-feature"})
	if err != nil || res.Verified || d.data["app-app-feature_data"] != "raw" {
		t.Errorf("bare: %v %+v", err, res)
	}
	// External volumes are never restored into.
	if _, err := e.RestoreVolume(ctx, feat, RestoreOptions{From: bare, Volume: "shared", Confirm: "app-app-feature"}); err == nil || !strings.Contains(err.Error(), "external") {
		t.Errorf("external: %v", err)
	}
}

func TestCloneRules(t *testing.T) {
	e, d, main, feat := dataFixture(t)
	ctx := context.Background()
	d.add("app_data", "app", "data", "seed")
	d.users = map[string][]string{"app_data": {"app-api-1"}} // the source may be running

	if _, err := e.CloneVolumes(ctx, main, main, CloneOptions{Confirm: "app"}); err == nil || !strings.Contains(err.Error(), "same instance") {
		t.Errorf("same instance: %v", err)
	}
	for sel, want := range map[string]string{"shared": "external", "gr-shared": "external"} {
		if _, err := e.CloneVolumes(ctx, main, feat, CloneOptions{Volumes: []string{sel}, Confirm: "app-app-feature"}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("clone %q: %v", sel, err)
		}
	}
	d.add("app-shared_cache", "app-shared", "cache", "y")
	if _, err := e.CloneVolumes(ctx, main, feat, CloneOptions{Volumes: []string{"app-shared_cache"}, Confirm: "app-app-feature"}); err == nil || !strings.Contains(err.Error(), "shared services") {
		t.Errorf("shared: %v", err)
	}

	var ce *ConfirmError
	if _, err := e.CloneVolumes(ctx, main, feat, CloneOptions{}); !errors.As(err, &ce) {
		t.Fatalf("no confirmation: %v", err)
	}
	d.containers = []compose.Container{{Service: "api"}}
	if _, err := e.CloneVolumes(ctx, main, feat, CloneOptions{Confirm: "app-app-feature"}); err == nil || !strings.Contains(err.Error(), "theo down") {
		t.Errorf("target containers: %v", err)
	}
	d.containers = nil
	if _, ok := d.vols["app-app-feature_data"]; ok {
		t.Fatal("a refused clone created a volume")
	}

	report, err := e.CloneVolumes(ctx, main, feat, CloneOptions{Confirm: "app-app-feature"})
	if err != nil || len(report.Volumes) != 1 || !report.Volumes[0].Created || len(report.Warnings) != 1 {
		t.Fatalf("clone: %v %+v", err, report)
	}
	if d.data["app-app-feature_data"] != "seed" || d.vols["app-app-feature_data"].Labels[compose.LabelProject] != "app-app-feature" {
		t.Errorf("target = %q %+v", d.data["app-app-feature_data"], d.vols["app-app-feature_data"])
	}
	if d.data["app_data"] != "seed" {
		t.Error("the source changed")
	}

	// The target has data now.
	d.data["app-app-feature_data"] = "mine"
	if _, err := e.CloneVolumes(ctx, main, feat, CloneOptions{Confirm: "app-app-feature"}); err == nil || !strings.Contains(err.Error(), "--replace") || d.data["app-app-feature_data"] != "mine" {
		t.Errorf("non-empty: %v", err)
	}
	if _, err := e.CloneVolumes(ctx, main, feat, CloneOptions{Replace: true}); !errors.As(err, &ce) || !strings.Contains(err.Error(), "for good") {
		t.Errorf("replace unconfirmed: %v", err)
	}
	if _, err := e.CloneVolumes(ctx, main, feat, CloneOptions{Confirm: "app-app-feature", Replace: true}); err != nil || d.data["app-app-feature_data"] != "seed" || !d.clears[len(d.clears)-1] {
		t.Errorf("replace: %v data=%q", err, d.data["app-app-feature_data"])
	}
	if ev, _ := e.History(ctx, "app", "feature/login", 0); len(ev) != 2 || ev[0].Operation != "volumes clone" {
		t.Errorf("history = %+v", ev)
	}
}

func TestPurgeWithBackupDeletesOnlyAfterTheBackup(t *testing.T) {
	e, d, main, _ := dataFixture(t)
	ctx := context.Background()
	d.add("app_data", "app", "data", "keep me")
	out := t.TempDir()

	// Refused purges write no backup.
	var pe *PurgeError
	if _, _, err := e.PurgeVolumesWithBackup(ctx, main, "", out); !errors.As(err, &pe) {
		t.Fatalf("no confirmation: %v", err)
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 || d.archives != 0 {
		t.Error("a refused purge took a backup")
	}

	// A failing backup stops the purge.
	d.failAfter = true
	if _, _, err := e.PurgeVolumesWithBackup(ctx, main, "app", out); err == nil || !strings.Contains(err.Error(), "nothing was deleted") {
		t.Fatalf("failing backup: %v", err)
	}
	if len(d.removed) != 0 || d.data["app_data"] != "keep me" {
		t.Fatal("volume deleted although the backup failed")
	}

	d.failAfter = false
	removed, report, err := e.PurgeVolumesWithBackup(ctx, main, "app", out)
	if err != nil || len(removed) != 1 || len(report.Backups) != 1 {
		t.Fatalf("purge: %v %v %+v", err, removed, report)
	}
	got, err := os.ReadFile(report.Backups[0].File)
	if err != nil || !bytes.Equal(got, []byte("TAR:keep me")) {
		t.Errorf("backup = %q %v", got, err)
	}
	if fmt.Sprint(d.removed) != "[app_data]" {
		t.Errorf("removed = %v", d.removed)
	}
}
