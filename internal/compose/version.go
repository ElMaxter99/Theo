package compose

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/ElMaxter99/Theo/internal/execx"
)

// MinVersion is the oldest Compose that supports the !override and !reset
// YAML tags, which the files Theo generates use (NFR-041).
var MinVersion = Version{2, 24, 4}

// Version is a Compose version, major.minor.patch.
type Version struct{ Major, Minor, Patch int }

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// AtLeast reports whether v is the same as or newer than o.
func (v Version) AtLeast(o Version) bool {
	if v.Major != o.Major {
		return v.Major > o.Major
	}
	if v.Minor != o.Minor {
		return v.Minor > o.Minor
	}
	return v.Patch >= o.Patch
}

var versionRe = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`)

// ParseVersion reads the output of `docker compose version --short` or
// `docker compose version`: "2.24.4", "v2.29.1-desktop.1", "Docker Compose
// version v5.6.1". A missing patch number counts as 0.
func ParseVersion(s string) (Version, error) {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("cannot read a Compose version from %q", strings.TrimSpace(s))
	}
	var v Version
	v.Major, _ = strconv.Atoi(m[1])
	v.Minor, _ = strconv.Atoi(m[2])
	if m[3] != "" {
		v.Patch, _ = strconv.Atoi(m[3])
	}
	return v, nil
}

var (
	versionMu    sync.Mutex
	versionKnown bool
	versionValue Version
	versionErr   error
)

// Version returns the version of the docker compose plugin. It asks Docker
// once per process and remembers the answer, the failure included.
func (Compose) Version(ctx context.Context) (Version, error) {
	versionMu.Lock()
	defer versionMu.Unlock()
	if !versionKnown {
		out, err := execx.Run(ctx, "", "docker", "compose", "version", "--short")
		if err == nil {
			versionValue, versionErr = ParseVersion(string(out))
		} else {
			versionErr = err
		}
		versionKnown = true
	}
	return versionValue, versionErr
}

// ErrTooOld is the message used when Compose is older than MinVersion.
func ErrTooOld(v Version) error {
	return fmt.Errorf("docker compose %s is too old: Theo's generated files need %s or later (the !override and !reset tags). Run `theo doctor` for how to upgrade", v, MinVersion)
}
