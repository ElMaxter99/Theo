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
	"strconv"
	"sync"
	"time"
)

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
		if !errors.Is(err, os.ErrExist) {
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
