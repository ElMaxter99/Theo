package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/execx"
	"github.com/ElMaxter99/Theo/internal/prefs"
)

// latestReleaseURL answers with the newest release of Theo.
const latestReleaseURL = "https://api.github.com/repos/ElMaxter99/Theo/releases/latest"

// fetchLatest asks for the newest version. Tests replace it.
var fetchLatest = func(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "theo-update-check")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var rel struct {
		Tag string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return "", err
	}
	return strings.TrimPrefix(rel.Tag, "v"), nil
}

// updateNotice returns the lines that say a new version exists, or nil.
// It does nothing unless the person turned `update-check` on, asks the
// network at most once a day, and stays silent on any failure.
func updateNotice(ctx context.Context, e *engine.Engine, now time.Time) []string {
	store := e.Prefs()
	if store.Get("update-check") != "on" || execx.Explaining() || strings.Contains(engine.Version, "dev") {
		return nil
	}
	info := store.UpdateInfo()
	if info.CheckedAt.IsZero() || now.Sub(info.CheckedAt) >= 24*time.Hour {
		latest, err := fetchLatest(ctx)
		info.CheckedAt = now
		if err == nil && latest != "" {
			info.Latest = latest
		}
		_ = store.SetUpdateInfo(prefs.Update{Latest: info.Latest, CheckedAt: info.CheckedAt})
	}
	if info.Latest == "" || !newerVersion(info.Latest, engine.Version) {
		return nil
	}
	return []string{
		fmt.Sprintf("A new version is available: %s (you have %s). Release notes:", info.Latest, strings.TrimPrefix(engine.Version, "v")),
		"https://github.com/ElMaxter99/Theo/releases/tag/v" + info.Latest,
		"Turn this notice off: theo config set update-check off",
	}
}

// newerVersion reports whether a is a newer release than b (x.y.z; a
// suffix after "-" is ignored).
func newerVersion(a, b string) bool {
	pa, pb := versionParts(a), versionParts(b)
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func versionParts(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, _, _ = strings.Cut(v, "-")
	var out [3]int
	for i, p := range strings.SplitN(v, ".", 3) {
		out[i], _ = strconv.Atoi(p)
	}
	return out
}
