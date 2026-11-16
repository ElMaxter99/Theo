// Package update looks for new versions of Theo and installs them in place:
// it asks GitHub for the releases, picks the newest one the person wants
// (stable, or also release candidates), downloads the archive of this system,
// checks it against the release's checksums.txt and replaces the programs.
package update

import (
	"strconv"
	"strings"
)

// Version is a Theo version: X.Y.Z, or X.Y.Z-rc.N for a release candidate.
type Version struct {
	Major, Minor, Patch int
	// RC is the release candidate number; 0 means a final release.
	RC int
	// Dev is a development build (0.0.0-dev and the like): never updated.
	Dev bool
}

// ParseVersion reads "0.11.0", "v0.11.0" and "0.11.0-rc.2". Anything else
// after the dash makes a development version.
func ParseVersion(s string) Version {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	core, pre, hasPre := strings.Cut(s, "-")
	var v Version
	parts := strings.SplitN(core, ".", 3)
	nums := [3]*int{&v.Major, &v.Minor, &v.Patch}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			v.Dev = true
			return v
		}
		*nums[i] = n
	}
	if len(parts) != 3 {
		v.Dev = true
		return v
	}
	if hasPre {
		n, ok := strings.CutPrefix(pre, "rc.")
		rc, err := strconv.Atoi(n)
		if !ok || err != nil || rc < 1 {
			v.Dev = true
			return v
		}
		v.RC = rc
	}
	return v
}

// IsRC reports whether v is a release candidate.
func (v Version) IsRC() bool { return v.RC > 0 }

// String is the version without the leading "v".
func (v Version) String() string {
	s := strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) + "." + strconv.Itoa(v.Patch)
	if v.RC > 0 {
		s += "-rc." + strconv.Itoa(v.RC)
	}
	return s
}

// Compare returns -1, 0 or 1. A release candidate is older than the final
// release of its version and newer than the previous version.
func Compare(a, b Version) int {
	for _, p := range [][2]int{{a.Major, b.Major}, {a.Minor, b.Minor}, {a.Patch, b.Patch}} {
		if p[0] != p[1] {
			if p[0] < p[1] {
				return -1
			}
			return 1
		}
	}
	ra, rb := a.RC, b.RC
	if ra == 0 {
		ra = 1 << 30
	}
	if rb == 0 {
		rb = 1 << 30
	}
	switch {
	case ra < rb:
		return -1
	case ra > rb:
		return 1
	}
	return 0
}

// Newer reports whether candidate is a newer version than current. A
// development build is never told to update.
func Newer(candidate, current string) bool {
	c, cur := ParseVersion(candidate), ParseVersion(current)
	if c.Dev || cur.Dev {
		return false
	}
	return Compare(c, cur) > 0
}
