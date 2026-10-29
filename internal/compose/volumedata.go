package compose

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/ElMaxter99/Theo/internal/execx"
)

// The helper containers below run with no network and with the entrypoint
// replaced, so any small image that has sh and tar (alpine by default) works.

// EnsureImage makes sure the image exists locally, pulling it only if it is
// missing.
func (Compose) EnsureImage(ctx context.Context, image string) error {
	if _, err := execx.Run(ctx, "", "docker", "image", "inspect", "--format", "{{.Id}}", image); err == nil {
		return nil
	}
	if _, err := execx.Run(ctx, "", "docker", "pull", "--quiet", image); err != nil {
		return fmt.Errorf("the helper image %s is not available locally and could not be pulled: %w\n(set THEO_HELPER_IMAGE to an image that has sh and tar)", image, err)
	}
	return nil
}

// InspectVolume returns the volume, or nil if it does not exist.
func (Compose) InspectVolume(ctx context.Context, name string) (*DockerVolume, error) {
	out, err := execx.Run(ctx, "", "docker", "volume", "ls", "--filter", "name="+name, "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	list, err := ParseVolumes(out)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Name == name { // the filter matches substrings: be exact
			return &list[i], nil
		}
	}
	return nil, nil
}

// CreateVolume creates a volume with labels.
func (Compose) CreateVolume(ctx context.Context, name string, labels map[string]string) error {
	args := []string{"volume", "create"}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--label", k+"="+labels[k])
	}
	_, err := execx.Run(ctx, "", "docker", append(args, "--", name)...)
	return err
}

// helperRun runs a helper container with the given mounts and script.
func helperRun(ctx context.Context, stdin io.Reader, stdout io.Writer, image string, mounts []string, script string) error {
	args := []string{"run", "--rm", "--network", "none", "--entrypoint", "sh"}
	if stdin != nil {
		args = append(args, "-i")
	}
	for _, m := range mounts {
		args = append(args, "--mount", m)
	}
	args = append(args, image, "-c", script)
	return execx.Pipe(ctx, stdin, stdout, "docker", args...)
}

func mount(volume, target string, readOnly bool) string {
	m := "type=volume,source=" + volume + ",target=" + target
	if readOnly {
		m += ",readonly"
	}
	return m
}

// VolumeIsEmpty reports whether the volume has no files. The volume must
// exist: docker would create it otherwise. It is mounted read-only.
func (Compose) VolumeIsEmpty(ctx context.Context, image, volume string) (bool, error) {
	var out bytes.Buffer
	if err := helperRun(ctx, nil, &out, image, []string{mount(volume, "/data", true)}, "ls -A /data | head -n 1"); err != nil {
		return false, err
	}
	return strings.TrimSpace(out.String()) == "", nil
}

// ArchiveVolume writes the contents of the volume to w as a gzip-compressed
// tar archive. The volume is mounted read-only.
func (Compose) ArchiveVolume(ctx context.Context, image, volume string, w io.Writer) error {
	return helperRun(ctx, nil, w, image, []string{mount(volume, "/data", true)}, "cd /data && tar -czf - .")
}

// clearScript empties /to (hidden files included) and keeps the directory.
const clearScript = "find /to -mindepth 1 -maxdepth 1 -exec rm -rf {} + && "

// ExtractVolume unpacks a gzip-compressed tar archive read from r into the
// volume, first emptying it when clear is set.
func (Compose) ExtractVolume(ctx context.Context, image, volume string, r io.Reader, clear bool) error {
	script := "tar -C /to -xzf -"
	if clear {
		script = clearScript + script
	}
	return helperRun(ctx, r, nil, image, []string{mount(volume, "/to", false)}, script)
}

// CopyVolume copies the contents of volume from into volume to, keeping
// permissions and ownership, first emptying to when clear is set. The
// source is mounted read-only.
func (Compose) CopyVolume(ctx context.Context, image, from, to string, clear bool) error {
	script := "cd /from && tar -cf - . | tar -C /to -xf -"
	if clear {
		script = clearScript + script
	}
	return helperRun(ctx, nil, nil, image, []string{mount(from, "/from", true), mount(to, "/to", false)}, script)
}
