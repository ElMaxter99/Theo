// Package filelock serialises writers of Theo's state files across
// processes. `theo ui` and a CLI command can run at the same time on the
// same registry (NFR-010); the files are replaced atomically, and this lock
// makes the read-modify-write cycle of each writer exclusive, so one does
// not undo the other's change.
//
// The lock is a file created with O_EXCL next to the state it protects. It
// works the same on every platform and needs no system call beyond the
// standard library. A lock left by a process that died is broken after
// StaleAfter.
package filelock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// WriteFile replaces the file at path with data: it writes a temporary file
// with a name of its own in the same directory and renames it over path, so
// a reader never sees half a file and two writers never share a temporary
// file.
func WriteFile(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, perm); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// StaleAfter is how long a lock file may exist before it is considered
// abandoned. Writers hold the lock for milliseconds.
var StaleAfter = 30 * time.Second

// Timeout is how long Lock waits for another holder.
var Timeout = 15 * time.Second

// inProcess serialises goroutines of this process by lock path, so they do
// not spin on the file.
var (
	mu      sync.Mutex
	pathMus = map[string]*sync.Mutex{}
)

func mutexFor(path string) *sync.Mutex {
	mu.Lock()
	defer mu.Unlock()
	m := pathMus[path]
	if m == nil {
		m = &sync.Mutex{}
		pathMus[path] = m
	}
	return m
}

// Lock takes the lock stored in the file at path and returns the function
// that releases it. It is not reentrant.
func Lock(path string) (unlock func(), err error) {
	m := mutexFor(path)
	m.Lock()
	deadline := time.Now().Add(Timeout)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			f.Close()
			return func() {
				os.Remove(path)
				m.Unlock()
			}, nil
		}
		// On Windows, creating the file while another process is deleting it
		// (its unlock) fails with "access denied" instead of "exists": retry
		// until the deadline, and only then report it as the error it is.
		busy := errors.Is(err, os.ErrExist) || errors.Is(err, os.ErrPermission)
		if !busy {
			m.Unlock()
			return nil, fmt.Errorf("cannot lock %s: %w", path, err)
		}
		if errors.Is(err, os.ErrPermission) && time.Now().After(deadline) {
			m.Unlock()
			return nil, fmt.Errorf("cannot lock %s: %w", path, err)
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > StaleAfter {
			os.Remove(path) // abandoned by a process that died
			continue
		}
		if time.Now().After(deadline) {
			m.Unlock()
			return nil, errors.New("another Theo process is writing " + strconv.Quote(path) + " and did not finish in time; try again")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
