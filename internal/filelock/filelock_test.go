package filelock

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestLockIsExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	var wg sync.WaitGroup
	counter, max := 0, 0
	var cm sync.Mutex
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, err := Lock(path)
			if err != nil {
				t.Error(err)
				return
			}
			cm.Lock()
			counter++
			if counter > max {
				max = counter
			}
			cm.Unlock()
			time.Sleep(2 * time.Millisecond)
			cm.Lock()
			counter--
			cm.Unlock()
			unlock()
		}()
	}
	wg.Wait()
	if max != 1 {
		t.Errorf("%d holders at once", max)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the lock file is left behind: %v", err)
	}
}

func TestStaleLockIsBroken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	if err := os.WriteFile(path, []byte("99999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	unlock, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}
