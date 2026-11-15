//go:build !windows

package runner

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// A dev server often starts children (a bundler, a watcher). Stop must take
// them all, or they keep the port and the CPU after Theo is done.
func TestStopLeavesNoOrphans(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	p, err := Start(context.Background(), Spec{Dir: t.TempDir(), Command: helperCmd(), Env: helperEnv(map[string]string{"HELPER_CHILD": pidFile}), ReadyTimeout: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the application did not start its child: %v", err)
	}
	pid, _ := strconv.Atoi(string(b))
	if syscall.Kill(pid, 0) != nil {
		t.Fatal("the child must be running before Stop")
	}
	p.Stop()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		// A killed child is a zombie until its parent reaps it; init does, so poll.
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("child %d survived Stop", pid)
}
