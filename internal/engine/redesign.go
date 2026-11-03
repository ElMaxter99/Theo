package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// AllHistory is HistoryPage for every project at once: the operations Theo
// ran on any instance, in pages from the newest backwards, with the same
// cursor rules. It is what the home screen of the interface lists.
func (e *Engine) AllHistory(limit, before int) (HistoryPageResult, error) {
	all, err := e.settings.Events()
	if err != nil {
		return HistoryPageResult{}, err
	}
	end := len(all)
	if before >= 0 && before < end {
		end = before
	}
	start := 0
	if limit > 0 && end-limit > 0 {
		start = end - limit
	}
	next := -1
	if start > 0 {
		next = start
	}
	return HistoryPageResult{Events: all[start:end], Total: len(all), Next: next}, nil
}

// Backups lists the backups kept in the default backup directory of a
// project (BackupDir), newest first. Only backups that have their manifest
// are listed; instance limits them to one Compose project when set.
func (e *Engine) Backups(project, instance string) ([]BackupResult, error) {
	dir, err := e.BackupDir(project)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []BackupResult{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []BackupResult{}
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		manifest := filepath.Join(dir, ent.Name())
		data, err := os.ReadFile(manifest)
		if err != nil {
			continue
		}
		var m BackupManifest
		if json.Unmarshal(data, &m) != nil || m.Archive == "" || filepath.Base(m.Archive) != m.Archive {
			continue
		}
		archive := filepath.Join(dir, m.Archive)
		if _, err := os.Stat(archive); err != nil {
			continue
		}
		if instance != "" && m.Instance != instance {
			continue
		}
		out = append(out, BackupResult{BackupManifest: m, File: archive, ManifestFile: manifest})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}
