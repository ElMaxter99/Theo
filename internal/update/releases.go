package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Repo is the GitHub repository Theo is released from.
const Repo = "ElMaxter99/Theo"

// EnvAPI overrides the address of the list of releases (a test, or a mirror).
// With it set, the files of a release may come from any address; without it
// only from this repository's release downloads.
const EnvAPI = "THEO_UPDATE_API"

const defaultAPI = "https://api.github.com/repos/" + Repo + "/releases?per_page=30"
const downloadPrefix = "https://github.com/" + Repo + "/releases/download/"

// Asset is one file of a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Release is a published release or pre-release.
type Release struct {
	Tag        string  `json:"tag_name"`
	Prerelease bool    `json:"prerelease"`
	Draft      bool    `json:"draft"`
	Page       string  `json:"html_url"`
	Assets     []Asset `json:"assets"`
}

// Version is the release's version.
func (r Release) Version() Version { return ParseVersion(r.Tag) }

// Asset finds a file of the release by name.
func (r Release) Asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// Lister returns the published releases. Tests replace it.
type Lister func(ctx context.Context) ([]Release, error)

func apiURL() string {
	if v := os.Getenv(EnvAPI); v != "" {
		return v
	}
	return defaultAPI
}

// customSource says whether releases come from somewhere else than GitHub.
func customSource() bool { return os.Getenv(EnvAPI) != "" }

// ListReleases asks GitHub for the latest releases.
func ListReleases(ctx context.Context) ([]Release, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "theo-update-check")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var list []Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&list); err != nil {
		return nil, err
	}
	return list, nil
}

// Newest picks the newest release among list: final releases only, or with
// release candidates too. Drafts and tags that are not versions are skipped.
func Newest(list []Release, includeRC bool) (Release, bool) {
	var best Release
	var bestV Version
	found := false
	for _, r := range list {
		v := r.Version()
		if r.Draft || v.Dev || (v.IsRC() && !includeRC) || (!v.IsRC() && r.Prerelease) {
			continue
		}
		if !found || Compare(v, bestV) > 0 {
			best, bestV, found = r, v, true
		}
	}
	return best, found
}

// allowedDownload says whether Theo may fetch this address for an update.
func allowedDownload(raw string) bool {
	if customSource() {
		return strings.HasPrefix(raw, "http://127.0.0.1:") || strings.HasPrefix(raw, "https://")
	}
	return strings.HasPrefix(raw, downloadPrefix)
}
