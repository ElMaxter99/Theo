package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/execx"
	"github.com/ElMaxter99/Theo/internal/update"
)

func init() {
	registerCommand("update", runUpdate)
	registerHelp(58, `
  update [--check] [--json]                    Look for a new version of Theo and install it. --check
                                               only says whether there is one. Release candidates
                                               (test versions) are offered only with
                                               theo config set update-channel rc. The new files are
                                               verified against the release's checksums.txt, and
                                               nothing is replaced if one does not match
`)
	registerCompletion(cmdSpec{name: "update", flags: []flagSpec{{names: []string{"--check"}}, {names: []string{"--json"}}}})
}

// listReleases asks for the releases. Tests replace it.
var listReleases update.Lister = update.ListReleases

func checker(e *engine.Engine, desktop bool) update.Checker {
	return update.Checker{Store: e.Prefs(), Current: engine.Version, Desktop: desktop, List: func(ctx context.Context) ([]update.Release, error) {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		return listReleases(ctx)
	}}
}

// updateNotice returns the lines that say a new version exists, or nil.
// It does nothing unless the person turned `update-check` on, asks the
// network when the last look is old, and stays silent on any failure.
func updateNotice(ctx context.Context, e *engine.Engine, now time.Time) []string {
	if execx.Explaining() {
		return nil
	}
	c := checker(e, false)
	c.Now = func() time.Time { return now }
	if !c.Enabled() {
		return nil
	}
	info, _ := c.Check(ctx, false)
	if !info.Available {
		return nil
	}
	kind := "A new version is available"
	if info.Prerelease {
		kind = "A new test version (release candidate) is available"
	}
	return []string{
		fmt.Sprintf("%s: %s (you have %s). Install it: theo update", kind, info.Latest, info.Current),
		info.Page,
		"Turn this notice off: theo config set update-check off",
	}
}

func runUpdate(e *engine.Engine, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	checkOnly := fs.Bool("check", false, "only say whether a new version exists")
	asJSON := fs.Bool("json", false, "print JSON")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 0 {
		return usageErrorf("update takes no arguments")
	}
	ctx := context.Background()
	c := checker(e, false)
	if update.ParseVersion(engine.Version).Dev {
		return errors.New("this is a development build: there is no release to compare it with")
	}
	info, err := c.Check(ctx, true)
	if err != nil {
		return fmt.Errorf("looking for a new version: %w", err)
	}
	say := func(format string, a ...any) {
		if !*asJSON {
			fmt.Fprintf(stdout, format+"\n", a...)
		}
	}
	if !info.Available {
		say("You have the latest version (%s).", info.Current)
		if *asJSON {
			return writeJSON(stdout, map[string]any{"current": info.Current, "available": false})
		}
		return nil
	}
	say("You have %s. A new version exists: %s", info.Current, info.Latest)
	say("Release notes: %s", info.Page)
	if *checkOnly {
		if *asJSON {
			return writeJSON(stdout, info)
		}
		say("Install it: theo update")
		return nil
	}
	rels, err := listReleases(ctx)
	if err != nil {
		return err
	}
	var rel update.Release
	for _, r := range rels {
		if r.Tag == info.Tag {
			rel = r
		}
	}
	if rel.Tag == "" {
		return fmt.Errorf("release %s is no longer listed", info.Tag)
	}
	last := -1
	res, err := update.Apply(ctx, rel, update.CurrentTarget(), func(done, total int64) {
		if *asJSON || total <= 0 {
			return
		}
		if pct := int(done * 100 / total); pct/10 != last/10 {
			last = pct
			fmt.Fprintf(stderr, "Downloading... %d%%\n", pct)
		}
	})
	switch {
	case errors.Is(err, update.ErrNotWritable):
		return fmt.Errorf("%w. Install the new version yourself: %s", err, info.Page)
	case errors.Is(err, update.ErrUnmanaged):
		return fmt.Errorf("%w. Download it from %s", err, info.Page)
	case err != nil:
		return err
	}
	if *asJSON {
		return writeJSON(stdout, res)
	}
	say("Theo is now %s (%s). Restart Theo if it is open.", strings.TrimPrefix(res.Tag, "v"), strings.Join(res.Replaced, ", "))
	return nil
}
