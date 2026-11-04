package git

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/testutil"
)

func TestParseTrackAhead(t *testing.T) {
	for track, want := range map[string]struct {
		ahead int
		ok    bool
	}{
		"":                    {0, true},
		"[ahead 2]":           {2, true},
		"[behind 1]":          {0, true},
		"[ahead 3, behind 4]": {3, true},
		"[gone]":              {0, false},
		"garbage":             {0, false},
	} {
		if a, ok := parseTrackAhead(track); a != want.ahead || ok != want.ok {
			t.Errorf("parseTrackAhead(%q) = %d, %v; want %d, %v", track, a, ok, want.ahead, want.ok)
		}
	}
}

// The batch comparison, the upstream counts of LocalBranches and the reflog
// read from its file must say what the one-branch Git commands say.
func TestBatchAnswersMatchTheOneBranchCommands(t *testing.T) {
	main, linked := testutil.NewRepo(t, nil)
	ctx := context.Background()
	g := Git{}
	testutil.WriteFile(t, linked, "a.txt", "a\n")
	testutil.Git(t, linked, "add", "-A")
	testutil.Git(t, linked, "commit", "-q", "-m", "a")
	testutil.Git(t, linked, "commit", "-q", "--allow-empty", "-m", "b")
	testutil.Git(t, main, "commit", "-q", "--allow-empty", "-m", "on main")
	testutil.Git(t, main, "branch", "untouched")

	all, err := g.AheadBehindAll(ctx, main, "main")
	if err != nil {
		t.Skipf("this Git has no %%(ahead-behind:...): %v", err)
	}
	for _, b := range []string{"main", "feature/x", "untouched"} {
		a, bh, err := g.AheadBehind(ctx, main, "main", b)
		if err != nil {
			t.Fatal(err)
		}
		if got := all[b]; got != [2]int{a, bh} {
			t.Errorf("%s: batch %v, rev-list %d %d", b, got, a, bh)
		}
	}
	if all["feature/x"] != [2]int{2, 1} {
		t.Errorf("feature/x = %v, want 2 ahead 1 behind", all["feature/x"])
	}

	// An upstream: feature/x tracks main (a local branch works as upstream).
	testutil.Git(t, main, "branch", "--set-upstream-to=main", "feature/x")
	refs, err := g.LocalBranches(ctx, main)
	if err != nil {
		t.Fatal(err)
	}
	if r := refs["feature/x"]; !r.TrackRead || r.UpstreamAhead != 2 {
		t.Errorf("feature/x upstream = %+v, want 2 ahead", r)
	}

	for _, b := range []string{"main", "feature/x", "untouched"} {
		fromFile := g.BranchMoves(ctx, main, b)
		cmd := exec.Command("git", "reflog", "show", "--format=%H", "refs/heads/"+b, "--")
		cmd.Dir = main
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		n := len(strings.Fields(string(out)))
		if fromFile != n {
			t.Errorf("%s: %d moves from the file, %d from git reflog", b, fromFile, n)
		}
	}
}
