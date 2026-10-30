package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/ElMaxter99/Theo/internal/filelock"
)

// LockInfo is the content of ui.lock: which process serves the interface,
// where, and the control secret that lets a second `theo ui` ask it for a
// launch URL. The secret is not the session: it opens no data.
type LockInfo struct {
	PID       int       `json:"pid"`
	Port      int       `json:"port"`
	Control   string    `json:"control_secret"`
	StartedAt time.Time `json:"started_at"`
}

// Launch is a launch URL and the server it opens.
type Launch struct {
	URL  string `json:"url"`
	Port int    `json:"port"`
	PID  int    `json:"pid"`
}

// LockPath is where ui.lock lives.
func LockPath(dataDir string) string { return filepath.Join(dataDir, "ui.lock") }

// GuardStart serialises the start of servers that share a data directory, so
// that two `theo ui` started at the same moment do not both serve.
func GuardStart(dataDir string) (unlock func(), err error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	return filelock.Lock(filepath.Join(dataDir, "ui.start.lock"))
}

// FindRunning looks for a live server that owns ui.lock and asks it for a
// launch URL. A lock whose process is gone, or that does not answer, is
// stale and reports false.
func FindRunning(dataDir string) (*Launch, bool) {
	data, err := os.ReadFile(LockPath(dataDir))
	if err != nil {
		return nil, false
	}
	var info LockInfo
	if json.Unmarshal(data, &info) != nil || info.Port == 0 || info.Control == "" {
		return nil, false
	}
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:"+strconv.Itoa(info.Port)+apiBase+"/launch", nil)
	if err != nil {
		return nil, false
	}
	req.Header.Set(controlHdr, info.Control)
	resp, err := client.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	var l Launch
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &l) != nil || l.URL == "" {
		return nil, false
	}
	return &l, true
}

// WriteLock records this server in ui.lock (readable by the user only) and
// returns the function that removes it.
func (s *Server) WriteLock(dataDir string) (release func(), err error) {
	info := LockInfo{PID: os.Getpid(), Port: s.port, Control: s.auth.control, StartedAt: time.Now().UTC()}
	data, err := json.Marshal(info)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	path := LockPath(dataDir)
	if err := filelock.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return nil, fmt.Errorf("cannot write %s: %w", path, err)
	}
	return func() {
		cur, err := os.ReadFile(path)
		var got LockInfo
		if err == nil && json.Unmarshal(cur, &got) == nil && got.PID == info.PID && got.Port == info.Port {
			os.Remove(path)
		}
	}, nil
}
