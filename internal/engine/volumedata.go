package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ElMaxter99/Theo/internal/compose"
)

// Defaults of the helper container that reads and writes volume data.
const (
	// EnvHelperImage names the environment variable that changes the helper
	// image. The image needs sh, tar, find, ls and head (alpine and busybox
	// have them).
	EnvHelperImage = "THEO_HELPER_IMAGE"
	// DefaultHelperImage is the helper image when EnvHelperImage is not set.
	DefaultHelperImage = "alpine:3.20"
)

// VolumeData is what the data-safety operations (backup, restore, clone)
// need from Docker, beyond the Compose interface. The real Compose adapter
// implements it; the engine reaches it with a type assertion so that the
// shared interface stays small.
type VolumeData interface {
	EnsureImage(ctx context.Context, image string) error
	InspectVolume(ctx context.Context, name string) (*compose.DockerVolume, error)
	CreateVolume(ctx context.Context, name string, labels map[string]string) error
	VolumeIsEmpty(ctx context.Context, image, volume string) (bool, error)
	ArchiveVolume(ctx context.Context, image, volume string, w io.Writer) error
	ExtractVolume(ctx context.Context, image, volume string, r io.Reader, clear bool) error
	CopyVolume(ctx context.Context, image, from, to string, clear bool) error
}

func (e *Engine) volumeData() (VolumeData, error) {
	vd, ok := e.compose.(VolumeData)
	if !ok {
		return nil, errors.New("this Docker adapter cannot read volume data")
	}
	return vd, nil
}

func helperImage() string {
	if img := strings.TrimSpace(os.Getenv(EnvHelperImage)); img != "" {
		return img
	}
	return DefaultHelperImage
}

// ConfirmError is returned when an operation that changes data is ready but
// was not confirmed with the instance's Compose project name. Nothing was
// changed.
type ConfirmError struct {
	Instance string
	Reason   string
}

func (c *ConfirmError) Error() string { return c.Reason }

// Code is the stable code of the error, for interfaces that translate it.
func (c *ConfirmError) Code() string { return "confirmation_required" }

// dataVolume is a volume of an instance that data operations may touch.
type dataVolume struct {
	Key    string   // name in the Compose file
	Name   string   // Docker name
	Exists bool     // false if Compose never created it (or it was purged)
	Users  []string // containers that mount it
}

// resolveVolume finds the Docker volume that belongs to a non-external
// volume of the instance, and refuses one that belongs to another Compose
// project.
func (e *Engine) resolveVolume(ctx context.Context, vd VolumeData, inst *Instance, key string) (dataVolume, error) {
	name := volumeDockerName(inst, key)
	v := dataVolume{Key: key, Name: name}
	dv, err := vd.InspectVolume(ctx, name)
	if err != nil {
		return v, err
	}
	if dv == nil {
		return v, nil
	}
	if owner := dv.Labels[compose.LabelProject]; owner != "" && owner != inst.Compose {
		return v, foreignVolumeError(name, owner)
	}
	v.Exists = true
	if v.Users, err = e.compose.VolumeUsers(ctx, name); err != nil {
		return v, err
	}
	return v, nil
}

func foreignVolumeError(name, owner string) error {
	if strings.HasSuffix(owner, "-shared") {
		return fmt.Errorf("volume %s belongs to the shared services (Compose project %s), which several instances use; Theo never backs up, restores or clones shared volumes", name, owner)
	}
	return fmt.Errorf("volume %s belongs to another Compose project (%s), not to this instance; Theo does not touch it", name, owner)
}

// volumeDockerName is the Docker name Compose gives a volume of the
// instance: <compose project>_<volume>, unless the Compose file fixes the
// name. The configuration Theo reads is resolved with the project's own name,
// so a name that is just that default is not a fixed name.
func volumeDockerName(inst *Instance, key string) string {
	name := inst.config.Volumes[key].Name
	if name == "" || name == inst.config.Name+"_"+key {
		return inst.Compose + "_" + key
	}
	return name
}

// selectVolumes resolves the volumes an operation works on: the ones named
// (by Compose name or Docker name) or, with none named, every volume Compose
// creates for the instance. External volumes are always refused.
func (e *Engine) selectVolumes(ctx context.Context, vd VolumeData, inst *Instance, selectors []string) ([]dataVolume, error) {
	keys := compose.SortedKeys(inst.config.Volumes)
	if len(selectors) == 0 {
		var out []dataVolume
		for _, key := range keys {
			if inst.config.Volumes[key].IsExternal() {
				continue
			}
			v, err := e.resolveVolume(ctx, vd, inst, key)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	}

	var out []dataVolume
	seen := map[string]bool{}
	for _, sel := range selectors {
		key, ok := "", false
		for _, k := range keys {
			dockerName := volumeDockerName(inst, k)
			if inst.config.Volumes[k].IsExternal() && inst.config.Volumes[k].Name == "" {
				dockerName = k
			}
			if sel == k || sel == dockerName {
				key, ok = k, true
				break
			}
		}
		if !ok {
			if dv, err := vd.InspectVolume(ctx, sel); err == nil && dv != nil {
				if owner := dv.Labels[compose.LabelProject]; owner != inst.Compose {
					return nil, foreignVolumeError(sel, owner)
				}
			}
			return nil, fmt.Errorf("%s has no volume %q; its volumes are: %s", inst.Compose, sel, strings.Join(nonExternalKeys(inst), ", "))
		}
		if inst.config.Volumes[key].IsExternal() {
			return nil, fmt.Errorf("volume %s is external: it lives outside this instance and Theo never touches it", sel)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		v, err := e.resolveVolume(ctx, vd, inst, key)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func nonExternalKeys(inst *Instance) []string {
	var keys []string
	for _, k := range compose.SortedKeys(inst.config.Volumes) {
		if !inst.config.Volumes[k].IsExternal() {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return []string{"(none)"}
	}
	return keys
}

// ensureNoContainers refuses to go on while the instance has containers,
// running or stopped.
func (e *Engine) ensureNoContainers(ctx context.Context, inst *Instance, what string) error {
	containers, err := e.compose.PS(ctx, inst.target())
	if err != nil {
		return err
	}
	if len(containers) > 0 {
		return fmt.Errorf("%s still has %d container(s); remove them first with `theo down` (its volumes are kept), then %s", inst.Compose, len(containers), what)
	}
	return nil
}

func (e *Engine) createOwnedVolume(ctx context.Context, vd VolumeData, inst *Instance, v dataVolume) error {
	// The labels Compose looks for, so that `up` adopts the volume as its own.
	return vd.CreateVolume(ctx, v.Name, map[string]string{
		compose.LabelProject: inst.Compose,
		compose.LabelVolume:  v.Key,
	})
}

// BackupManifest is the small JSON file written next to each backup.
type BackupManifest struct {
	Project   string    `json:"project"`
	Instance  string    `json:"instance"` // Compose project name
	Volume    string    `json:"volume"`   // name in the Compose file
	Docker    string    `json:"docker_volume"`
	Image     string    `json:"helper_image"`
	Archive   string    `json:"archive"` // file name of the .tar.gz, next to the manifest
	Size      int64     `json:"size"`    // bytes of the archive
	SHA256    string    `json:"sha256"`  // of the archive
	CreatedAt time.Time `json:"created_at"`
	Theo      string    `json:"theo_version"`
}

// BackupResult is a backup that was written.
type BackupResult struct {
	BackupManifest
	File         string `json:"file"`
	ManifestFile string `json:"manifest_file"`
}

// BackupReport is the outcome of a backup operation.
type BackupReport struct {
	Backups  []BackupResult `json:"backups"`
	Warnings []string       `json:"warnings,omitempty"`
}

// BackupDir returns the default directory for the backups of a project.
func (e *Engine) BackupDir(project string) (string, error) {
	if e.dataDir == "" {
		return "", errors.New("there is no Theo data directory to keep backups in; pass --out")
	}
	return filepath.Join(e.dataDir, "backups", slug(project)), nil
}

// BackupVolumes writes one .tar.gz and one manifest .json per volume to out
// (default: BackupDir). The data is read by a helper container that mounts
// the volume read-only, so the instance may be running; its data can then be
// inconsistent, which is reported in the warnings. Volumes that were never
// created are skipped with a warning unless named explicitly.
func (e *Engine) BackupVolumes(ctx context.Context, inst *Instance, names []string, out string) (*BackupReport, error) {
	vd, err := e.volumeData()
	if err != nil {
		return nil, err
	}
	vols, err := e.selectVolumes(ctx, vd, inst, names)
	if err != nil {
		return nil, err
	}
	if len(vols) == 0 {
		return nil, fmt.Errorf("%s declares no volumes of its own, so there is nothing to back up", inst.Compose)
	}
	report := &BackupReport{}
	var todo []dataVolume
	for _, v := range vols {
		if !v.Exists {
			if len(names) > 0 {
				return nil, fmt.Errorf("volume %s does not exist yet (Compose creates it on the first `up`), so there is nothing to back up", v.Name)
			}
			report.Warnings = append(report.Warnings, fmt.Sprintf("volume %s does not exist yet: skipped", v.Name))
			continue
		}
		todo = append(todo, v)
	}
	if len(todo) == 0 {
		return nil, fmt.Errorf("none of the volumes of %s exists yet, so there is nothing to back up", inst.Compose)
	}
	return e.backup(ctx, vd, inst, todo, out, report)
}

func (e *Engine) backup(ctx context.Context, vd VolumeData, inst *Instance, vols []dataVolume, out string, report *BackupReport) (*BackupReport, error) {
	if out == "" {
		var err error
		if out, err = e.BackupDir(inst.Project); err != nil {
			return report, err
		}
	}
	image := helperImage()
	err := func() error {
		if err := os.MkdirAll(out, 0o700); err != nil {
			return err
		}
		if err := vd.EnsureImage(ctx, image); err != nil {
			return err
		}
		stamp := e.now().UTC()
		for _, v := range vols {
			if len(v.Users) > 0 {
				report.Warnings = append(report.Warnings, fmt.Sprintf("volume %s is in use by %s: the backup was taken while it runs, so the data may be inconsistent (a database may need a dump instead)", v.Name, strings.Join(v.Users, ", ")))
			}
			res, err := writeBackup(ctx, vd, inst, v, image, out, stamp)
			if err != nil {
				return fmt.Errorf("backing up %s (%d of %d done): %w", v.Name, len(report.Backups), len(vols), err)
			}
			res.Theo = Version
			report.Backups = append(report.Backups, res)
		}
		return nil
	}()
	e.record(inst, "volumes backup", err)
	return report, err
}

// backupBase is the file name of a backup, without extension: project,
// instance, volume and the UTC time.
func backupBase(inst *Instance, volume string, t time.Time) string {
	return strings.Join([]string{slug(inst.Project), slug(inst.Compose), slug(volume), t.Format("20060102T150405Z")}, "__")
}

func writeBackup(ctx context.Context, vd VolumeData, inst *Instance, v dataVolume, image, out string, t time.Time) (BackupResult, error) {
	base := backupBase(inst, v.Key, t)
	archive := filepath.Join(out, base+".tar.gz")
	manifest := filepath.Join(out, base+".json")

	f, err := os.OpenFile(archive+".part", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return BackupResult{}, err
	}
	keep := false
	defer func() {
		f.Close()
		if !keep {
			os.Remove(archive + ".part")
		}
	}()
	if _, err := os.Stat(archive); err == nil {
		return BackupResult{}, fmt.Errorf("%s already exists; wait a second and try again", archive)
	}
	sum := sha256.New()
	cw := &countWriter{w: io.MultiWriter(f, sum)}
	if err := vd.ArchiveVolume(ctx, image, v.Name, cw); err != nil {
		return BackupResult{}, err
	}
	if err := f.Close(); err != nil {
		return BackupResult{}, err
	}
	if err := os.Rename(archive+".part", archive); err != nil {
		return BackupResult{}, err
	}
	keep = true

	m := BackupManifest{
		Project: inst.Project, Instance: inst.Compose, Volume: v.Key, Docker: v.Name, Image: image,
		Archive: base + ".tar.gz", Size: cw.n, SHA256: hex.EncodeToString(sum.Sum(nil)), CreatedAt: t, Theo: Version,
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return BackupResult{}, err
	}
	if err := os.WriteFile(manifest, append(data, '\n'), 0o600); err != nil {
		os.Remove(archive) // an archive without its manifest is not a backup Theo can vouch for
		return BackupResult{}, err
	}
	return BackupResult{BackupManifest: m, File: archive, ManifestFile: manifest}, nil
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// PurgeVolumesWithBackup is PurgeVolumes preceded by a backup of every volume
// it would delete. The volumes are deleted only if all the backups succeeded.
// The refusals of PurgeVolumes (containers, no confirmation) come first, so
// a refused purge writes no backup.
func (e *Engine) PurgeVolumesWithBackup(ctx context.Context, inst *Instance, confirm, out string) ([]VolumeInfo, *BackupReport, error) {
	doomed, err := e.purgePlan(ctx, inst, confirm)
	if err != nil || len(doomed) == 0 {
		return nil, nil, err
	}
	vd, err := e.volumeData()
	if err != nil {
		return nil, nil, fmt.Errorf("%w; nothing was deleted", err)
	}
	vols := make([]dataVolume, len(doomed))
	for i, d := range doomed {
		key := d.Volume
		if key == "" {
			key = d.Name
		}
		vols[i] = dataVolume{Key: key, Name: d.Name, Exists: true, Users: d.Users}
	}
	report, err := e.backup(ctx, vd, inst, vols, out, &BackupReport{})
	if err != nil {
		return nil, report, fmt.Errorf("the backup failed, so nothing was deleted: %w", err)
	}
	removed, err := e.deleteVolumes(ctx, inst, doomed)
	return removed, report, err
}

// RestoreOptions are the arguments of RestoreVolume.
type RestoreOptions struct {
	From    string // the .tar.gz written by BackupVolumes
	Volume  string // Compose name of the volume; read from the manifest if empty
	Confirm string // must equal the instance's Compose project name
	Replace bool   // allow overwriting a volume that has data
}

// RestoreResult is a volume that was restored.
type RestoreResult struct {
	Volume   string `json:"volume"`
	Docker   string `json:"docker_volume"`
	From     string `json:"from"`
	Created  bool   `json:"created"`  // the volume did not exist and was created with the Compose labels
	Replaced bool   `json:"replaced"` // existing data was deleted first
	Verified bool   `json:"verified"` // the archive matched the sha256 of its manifest
}

// manifestPath is where the manifest of a backup archive is expected.
func manifestPath(archive string) string {
	return strings.TrimSuffix(archive, ".tar.gz") + ".json"
}

// RestoreVolume unpacks a backup into a volume of the instance. It refuses
// while the instance has containers, and a volume that already has data
// unless Replace is set; the volume is created with the labels Compose puts
// on its own volumes if it does not exist. A backup that has a manifest is
// checked against its sha256 first.
func (e *Engine) RestoreVolume(ctx context.Context, inst *Instance, opts RestoreOptions) (*RestoreResult, error) {
	vd, err := e.volumeData()
	if err != nil {
		return nil, err
	}
	archive, err := filepath.Abs(opts.From)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(archive); err != nil {
		return nil, fmt.Errorf("backup %s: %w", opts.From, err)
	}
	key := opts.Volume
	verified := false
	if data, err := os.ReadFile(manifestPath(archive)); err == nil {
		var m BackupManifest
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("manifest %s: %w", manifestPath(archive), err)
		}
		if m.SHA256 != "" {
			got, err := fileSHA256(archive)
			if err != nil {
				return nil, err
			}
			if got != m.SHA256 {
				return nil, fmt.Errorf("%s does not match the sha256 in its manifest: the backup is corrupt or was modified; nothing was restored", archive)
			}
			verified = true
		}
		if key == "" {
			key = m.Volume
		} else if m.Volume != "" && key != m.Volume && key != m.Docker {
			return nil, fmt.Errorf("the manifest says this is a backup of volume %q, not %q; nothing was restored", m.Volume, key)
		}
	}
	if key == "" {
		return nil, errors.New("there is no manifest next to the backup to say which volume it holds; pass --volume")
	}
	vols, err := e.selectVolumes(ctx, vd, inst, []string{key})
	if err != nil {
		return nil, err
	}
	v := vols[0]
	if err := e.ensureNoContainers(ctx, inst, "restore"); err != nil {
		return nil, err
	}
	if len(v.Users) > 0 {
		return nil, fmt.Errorf("volume %s is still used by %s; nothing was restored", v.Name, strings.Join(v.Users, ", "))
	}
	image := helperImage()
	res := &RestoreResult{Volume: v.Key, Docker: v.Name, From: archive, Created: !v.Exists, Verified: verified}
	if v.Exists {
		if err := vd.EnsureImage(ctx, image); err != nil {
			return nil, err
		}
		empty, err := vd.VolumeIsEmpty(ctx, image, v.Name)
		if err != nil {
			return nil, err
		}
		if !empty {
			if !opts.Replace {
				return nil, fmt.Errorf("volume %s already has data; restoring would mix it with the backup. Pass --replace to delete it first (take a backup of it before with `theo volumes backup`); nothing was restored", v.Name)
			}
			res.Replaced = true
		}
	}
	if opts.Confirm != inst.Compose {
		what := fmt.Sprintf("this writes the backup into volume %s of %s", v.Name, inst.Compose)
		if res.Replaced {
			what = fmt.Sprintf("this deletes the data of volume %s of %s for good and replaces it with the backup", v.Name, inst.Compose)
		}
		return nil, &ConfirmError{Instance: inst.Compose, Reason: what + "; nothing was changed"}
	}

	err = func() error {
		if err := vd.EnsureImage(ctx, image); err != nil {
			return err
		}
		if !v.Exists {
			if err := e.createOwnedVolume(ctx, vd, inst, v); err != nil {
				return err
			}
		}
		f, err := os.Open(archive)
		if err != nil {
			return err
		}
		defer f.Close()
		if err := vd.ExtractVolume(ctx, image, v.Name, f, res.Replaced); err != nil {
			if !v.Exists {
				// Its own, empty-or-partial volume: leave no half restore behind.
				_ = e.compose.RemoveVolume(ctx, v.Name)
			}
			return err
		}
		return nil
	}()
	e.record(inst, "volumes restore", err)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// CloneOptions are the arguments of CloneVolumes.
type CloneOptions struct {
	Volumes []string // Compose names; all the volumes both instances declare if empty
	Confirm string   // must equal the target's Compose project name
	Replace bool     // allow overwriting target volumes that have data
}

// CloneResult is a volume that was copied.
type CloneResult struct {
	Volume   string `json:"volume"`
	From     string `json:"from"`
	To       string `json:"to"`
	Created  bool   `json:"created"`
	Replaced bool   `json:"replaced"`
}

// CloneReport is the outcome of CloneVolumes.
type CloneReport struct {
	Source   string        `json:"source"`
	Target   string        `json:"target"`
	Volumes  []CloneResult `json:"volumes"`
	Warnings []string      `json:"warnings,omitempty"`
}

// CloneVolumes copies the data of volumes from the source instance to the
// target one, to seed a new branch with the data of another. The source is
// mounted read-only and may be running; the target must have no containers,
// and a target volume that has data needs Replace. External and shared
// volumes are never copied. Everything is checked before the first copy.
func (e *Engine) CloneVolumes(ctx context.Context, src, dst *Instance, opts CloneOptions) (*CloneReport, error) {
	vd, err := e.volumeData()
	if err != nil {
		return nil, err
	}
	if src.Compose == dst.Compose {
		return nil, fmt.Errorf("the source and the target are the same instance (%s); choose another worktree with --from", src.Compose)
	}
	report := &CloneReport{Source: src.Compose, Target: dst.Compose}

	srcVols, err := e.selectVolumes(ctx, vd, src, opts.Volumes)
	if err != nil {
		return nil, fmt.Errorf("source %s: %w", src.Compose, err)
	}
	type pair struct{ from, to dataVolume }
	var pairs []pair
	for _, s := range srcVols {
		explicit := len(opts.Volumes) > 0
		dcfg, declared := dst.config.Volumes[s.Key]
		switch {
		case !s.Exists && explicit:
			return nil, fmt.Errorf("volume %s of %s has no data yet (it does not exist), so there is nothing to clone", s.Name, src.Compose)
		case !s.Exists:
			report.Warnings = append(report.Warnings, fmt.Sprintf("volume %s does not exist yet: skipped", s.Name))
			continue
		case !declared || dcfg.IsExternal():
			if explicit {
				return nil, fmt.Errorf("%s does not declare a volume of its own called %q, so it cannot receive the data", dst.Compose, s.Key)
			}
			report.Warnings = append(report.Warnings, fmt.Sprintf("volume %s is not a volume of %s: skipped", s.Key, dst.Compose))
			continue
		}
		t, err := e.resolveVolume(ctx, vd, dst, s.Key)
		if err != nil {
			return nil, fmt.Errorf("target %s: %w", dst.Compose, err)
		}
		if t.Name == s.Name {
			return nil, fmt.Errorf("volume %s is the same Docker volume in both instances (it has a fixed name), so there is nothing to copy", s.Name)
		}
		if len(s.Users) > 0 {
			report.Warnings = append(report.Warnings, fmt.Sprintf("volume %s is in use by %s: it is copied while it runs, so the data may be inconsistent", s.Name, strings.Join(s.Users, ", ")))
		}
		pairs = append(pairs, pair{s, t})
	}
	if len(pairs) == 0 {
		return nil, fmt.Errorf("no volume to clone from %s to %s", src.Compose, dst.Compose)
	}
	if err := e.ensureNoContainers(ctx, dst, "clone"); err != nil {
		return nil, err
	}

	image := helperImage()
	replacing := make([]bool, len(pairs))
	for i, p := range pairs {
		if len(p.to.Users) > 0 {
			return nil, fmt.Errorf("volume %s is still used by %s; nothing was cloned", p.to.Name, strings.Join(p.to.Users, ", "))
		}
		if !p.to.Exists {
			continue
		}
		if err := vd.EnsureImage(ctx, image); err != nil {
			return nil, err
		}
		empty, err := vd.VolumeIsEmpty(ctx, image, p.to.Name)
		if err != nil {
			return nil, err
		}
		if !empty {
			if !opts.Replace {
				return nil, fmt.Errorf("volume %s of %s already has data. Pass --replace to delete it and copy the data of %s over it; nothing was cloned", p.to.Name, dst.Compose, src.Compose)
			}
			replacing[i] = true
		}
	}
	if opts.Confirm != dst.Compose {
		n := 0
		for _, r := range replacing {
			if r {
				n++
			}
		}
		what := fmt.Sprintf("this copies the data of %d volume(s) of %s into %s", len(pairs), src.Compose, dst.Compose)
		if n > 0 {
			what += fmt.Sprintf(" and deletes the existing data of %d of them for good", n)
		}
		return nil, &ConfirmError{Instance: dst.Compose, Reason: what + "; nothing was changed"}
	}

	err = func() error {
		if err := vd.EnsureImage(ctx, image); err != nil {
			return err
		}
		for i, p := range pairs {
			if !p.to.Exists {
				if err := e.createOwnedVolume(ctx, vd, dst, p.to); err != nil {
					return err
				}
			}
			if err := vd.CopyVolume(ctx, image, p.from.Name, p.to.Name, replacing[i]); err != nil {
				if !p.to.Exists {
					_ = e.compose.RemoveVolume(ctx, p.to.Name)
				}
				return fmt.Errorf("copying %s (%d of %d done): %w", p.from.Key, len(report.Volumes), len(pairs), err)
			}
			report.Volumes = append(report.Volumes, CloneResult{Volume: p.from.Key, From: p.from.Name, To: p.to.Name, Created: !p.to.Exists, Replaced: replacing[i]})
		}
		return nil
	}()
	e.record(dst, "volumes clone", err)
	return report, err
}
