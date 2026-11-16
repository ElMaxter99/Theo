package update

import (
	"context"
	"os"
	"time"

	"github.com/ElMaxter99/Theo/internal/prefs"
)

// Interval is how often Theo looks for a new version while it is open.
const Interval = 6 * time.Hour

// Checker looks for new versions and remembers the answer in the preferences.
type Checker struct {
	Store   prefs.Store
	Current string
	// Desktop is true in the installed app, where looking is on by default.
	Desktop bool
	// List returns the releases; nil asks GitHub.
	List Lister
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

func (c Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Enabled reports whether Theo may look for updates by itself: the person's
// update-check setting, or on in the desktop app and off in the command line
// when it was never set.
func (c Checker) Enabled() bool {
	switch c.Store.Get("update-check") {
	case "on":
		return true
	case "off":
		return false
	}
	return c.Desktop
}

// IncludeRC reports whether release candidates are offered.
func (c Checker) IncludeRC() bool { return c.Store.Get("update-channel") == "rc" }

// Info is what the person is shown.
type Info struct {
	Current    string    `json:"current"`
	Latest     string    `json:"latest,omitempty"`
	Tag        string    `json:"tag,omitempty"`
	Available  bool      `json:"available"`
	Prerelease bool      `json:"prerelease,omitempty"`
	Page       string    `json:"page,omitempty"`
	CheckedAt  time.Time `json:"checked_at,omitzero"`
	Dismissed  bool      `json:"dismissed,omitempty"`
}

// Info is the last answer, judged against the current version and channel:
// switching the channel off hides a release candidate at once.
func (c Checker) Info() Info {
	u := c.Store.UpdateInfo()
	info := Info{Current: trimV(c.Current), CheckedAt: u.CheckedAt, Latest: u.Latest, Tag: u.Tag, Prerelease: u.Prerelease}
	if u.Latest != "" {
		info.Page = "https://github.com/" + Repo + "/releases/tag/v" + u.Latest
	}
	info.Available = u.Latest != "" && Newer(u.Latest, c.Current) && (!u.Prerelease || c.IncludeRC())
	info.Dismissed = info.Available && u.Dismissed == u.Latest
	return info
}

// Due reports whether the last look is older than Interval (or never done).
func (c Checker) Due() bool {
	at := c.Store.UpdateInfo().CheckedAt
	return at.IsZero() || c.now().Sub(at) >= Interval
}

// Check asks for the releases and stores the answer. force skips the wait
// since the last look. It does nothing for a development build.
func (c Checker) Check(ctx context.Context, force bool) (Info, error) {
	if ParseVersion(c.Current).Dev {
		return c.Info(), nil
	}
	if !force && !c.Due() {
		return c.Info(), nil
	}
	list := c.List
	if list == nil {
		list = ListReleases
	}
	rels, err := list(ctx)
	now := c.now()
	if err != nil {
		// Remember that we looked, so a failing network is not asked again at once.
		_ = c.Store.ChangeUpdateInfo(func(u *prefs.Update) { u.CheckedAt = now })
		return c.Info(), err
	}
	best, ok := Newest(rels, c.IncludeRC())
	err = c.Store.ChangeUpdateInfo(func(u *prefs.Update) {
		u.CheckedAt = now
		if ok {
			u.Latest, u.Tag, u.Prerelease = trimV(best.Tag), best.Tag, best.Version().IsRC()
		}
	})
	return c.Info(), err
}

// ShouldNotify reports whether a notification is due for the available
// version, and records it so the same version is only notified once.
func (c Checker) ShouldNotify() (Info, bool) {
	info := c.Info()
	if !info.Available {
		return info, false
	}
	first := false
	_ = c.Store.ChangeUpdateInfo(func(u *prefs.Update) {
		if u.Notified != info.Latest {
			u.Notified, first = info.Latest, true
		}
	})
	return info, first
}

// Dismiss closes the banner of the version that is available now.
func (c Checker) Dismiss() error {
	info := c.Info()
	return c.Store.ChangeUpdateInfo(func(u *prefs.Update) { u.Dismissed = info.Latest })
}

func trimV(s string) string {
	if len(s) > 0 && s[0] == 'v' {
		return s[1:]
	}
	return s
}

// Executable is the path of the running program, with symbolic links
// resolved, or "" when it cannot be known.
func Executable() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	return resolve(p)
}
