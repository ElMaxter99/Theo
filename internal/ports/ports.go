// Package ports checks whether host ports are available and remembers the
// host ports Theo assigned to each instance.
package ports

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
)

// Free reports whether nothing is listening on the host port. hostIP may be
// empty, meaning all interfaces, which is what Docker publishes on by
// default. A port that cannot be tested for lack of permission (a privileged
// port, as a normal user) counts as free on Unix: the Docker daemon runs
// with more rights, and Docker reports the real conflict if there is one.
func Free(hostIP string, port int, proto string) bool {
	addr := net.JoinHostPort(hostIP, strconv.Itoa(port))
	var err error
	if proto == "udp" {
		var c net.PacketConn
		if c, err = net.ListenPacket("udp", addr); err == nil {
			c.Close()
		}
	} else {
		var l net.Listener
		if l, err = net.Listen("tcp", addr); err == nil {
			l.Close()
		}
	}
	if err == nil || addrUnavailable(err) {
		return true
	}
	return errors.Is(err, os.ErrPermission) && runtime.GOOS != "windows"
}

// addrUnavailable reports whether listening failed because this host has no
// such address (an IPv6 host_ip on a host without IPv6, an IP of another
// machine) rather than because the port is taken. Theo cannot test those
// ports, and counting them as taken would send the search for a free port
// through the whole range; Docker reports the real conflict if there is one.
// The Windows socket errors are listed by number, since the syscall package
// maps them to other values there.
func addrUnavailable(err error) bool {
	for _, target := range []error{syscall.EADDRNOTAVAIL, syscall.EAFNOSUPPORT, syscall.Errno(10049), syscall.Errno(10047)} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// State maps an instance (the path of its worktree) to the host ports
// assigned to it, by key (see Key).
type State map[string]map[string]int

// Key identifies a published port of an instance: service, container port
// and protocol.
func Key(service string, target uint32, proto string) string {
	return fmt.Sprintf("%s|%d/%s", service, target, proto)
}

// Store reads and writes the assignments file.
type Store struct {
	path string
}

const fileName = "ports.json"

// Open returns a store backed by the ports file in dir. The file is created
// on the first save.
func Open(dir string) *Store {
	return &Store{path: filepath.Join(dir, fileName)}
}

type document struct {
	Version   int   `json:"version"`
	Instances State `json:"instances"`
}

// Load returns the saved assignments; there are none before the first save.
func (s *Store) Load() (State, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return nil, err
	}
	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s is not valid: %w", s.path, err)
	}
	if doc.Instances == nil {
		doc.Instances = State{}
	}
	return doc.Instances, nil
}

// Save replaces the saved assignments.
func (s *Store) Save(st State) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(document{Version: 1, Instances: st}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
