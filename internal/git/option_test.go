package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ElMaxter99/Theo/internal/testutil"
)

// A branch or base that starts with "-" would be read by Git as an option
// (--force, --detach, --lock...), which this package promises never to pass.
func TestGitRefsCannotBeOptions(t *testing.T) {
	main, _ := testutil.NewRepo(t, nil)
	ctx := context.Background()
	g := Git{}
	dir := t.TempDir()
	for i, c := range []struct {
		branch string
		isNew  bool
		base   string
	}{
		{"--detach", false, ""},
		{"-f", false, ""},
		{"nb", true, "--force"},
		{"nb2", true, "--lock"},
		{"--orphan", true, ""},
	} {
		path := filepath.Join(dir, string(rune('a'+i)))
		if err := g.AddWorktree(ctx, main, path, c.branch, c.isNew, c.base); err == nil {
			t.Errorf("AddWorktree(branch %q, new %v, base %q) succeeded", c.branch, c.isNew, c.base)
		}
		if _, err := os.Stat(path); err == nil {
			t.Errorf("AddWorktree(branch %q, base %q) created %s", c.branch, c.base, path)
		}
	}
	if err := g.Checkout(ctx, main, "nb", true, "--orphan"); err == nil {
		t.Error("Checkout accepted a base that is an option")
	}
	if err := g.Checkout(ctx, main, "--detach", false, ""); err == nil {
		t.Error("Checkout accepted a branch that is an option")
	}
	if g.ResolvesTo(ctx, main, "--all") {
		t.Error("ResolvesTo accepted an option")
	}
}
