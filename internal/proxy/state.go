package proxy

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// stateFile is where a running proxy says where it listens, in Theo's data
// directory, so that `theo status` and `theo proxy routes` can show its URLs.
const stateFile = "proxy.json"

// State is what a running proxy writes.
type State struct {
	Addr    string    `json:"addr"`
	Port    int       `json:"port"`
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
}

// WriteState records a running proxy and returns the function that removes
// the record (only if it is still this one).
func WriteState(dir string, s State) (func(), error) {
	data, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, stateFile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	return func() {
		if cur, ok := readState(dir); ok && cur.PID == s.PID && cur.Port == s.Port {
			os.Remove(path)
		}
	}, nil
}

func readState(dir string) (State, bool) {
	var s State
	data, err := os.ReadFile(filepath.Join(dir, stateFile))
	if err != nil || json.Unmarshal(data, &s) != nil || s.Port <= 0 {
		return State{}, false
	}
	return s, true
}

// Running returns the proxy recorded in dir when something still listens at
// its address. A record left by a proxy that was killed is ignored.
func Running(dir string) (State, bool) {
	s, ok := readState(dir)
	if !ok {
		return State{}, false
	}
	host := s.Addr
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	c, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(s.Port)), 300*time.Millisecond)
	if err != nil {
		return State{}, false
	}
	c.Close()
	return s, true
}
