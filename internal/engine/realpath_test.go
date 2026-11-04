package engine

import (
	"path/filepath"
	"sync/atomic"
	"testing"
)

// Matching N worktrees with N records compares every pair; each distinct
// path must be resolved once, not once per comparison.
func TestSamePathResolvesEachPathOnce(t *testing.T) {
	var calls atomic.Int32
	orig := evalSymlinks
	evalSymlinks = func(p string) (string, error) { calls.Add(1); return orig(p) }
	defer func() { evalSymlinks = orig }()

	dir := t.TempDir()
	const n = 30
	paths := make([]string, n)
	for i := range paths {
		paths[i] = filepath.Join(dir, "wt", string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	for _, a := range paths {
		for _, b := range paths {
			if samePath(a, b) != (a == b) {
				t.Fatalf("samePath(%s, %s) is wrong", a, b)
			}
		}
	}
	if got := calls.Load(); got > n {
		t.Fatalf("EvalSymlinks ran %d times for %d paths", got, n)
	}
}
