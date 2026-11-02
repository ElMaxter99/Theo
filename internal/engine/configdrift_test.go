package engine

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/compose"
)

func TestConfigChangedComparesWithTheLastStart(t *testing.T) {
	e, _, _, c := newEngine()
	ctx := context.Background()
	if _, err := e.AddProject(ctx, root, "", ""); err != nil {
		t.Fatal(err)
	}
	c.configs[feature] = &compose.Project{Name: "app-feature", Digest: "one"}
	inst, err := e.Instance(ctx, "app", "feature/login", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ch := e.ConfigChanged(inst); ch != nil {
		t.Fatalf("never started: ConfigChanged = %+v, want nil", ch)
	}
	if err := e.Up(ctx, inst, compose.UpOptions{}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if ch := e.ConfigChanged(inst); ch != nil {
		t.Fatalf("same configuration: ConfigChanged = %+v, want nil", ch)
	}

	c.configs[feature] = &compose.Project{Name: "app-feature", Digest: "two"}
	changed, err := e.Instance(ctx, "app", "feature/login", nil)
	if err != nil {
		t.Fatal(err)
	}
	ch := e.ConfigChanged(changed)
	if ch == nil || ch.Operation != "up" {
		t.Fatalf("changed configuration: ConfigChanged = %+v, want a change since up", ch)
	}
	w := ComposeChangedWarning(changed, ch, "feature/login")
	if w.Kind != WarnComposeChanged || w.Fix != "theo up app -w feature/login" || !strings.Contains(w.Message, "changed since the last up") {
		t.Errorf("warning = %+v", w)
	}

	// A recreate with the new configuration settles it.
	if err := e.Recreate(ctx, changed, compose.UpOptions{}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	if ch := e.ConfigChanged(changed); ch != nil {
		t.Errorf("after recreate: ConfigChanged = %+v, want nil", ch)
	}
}

func TestConfigChangedIgnoresUnknownDigests(t *testing.T) {
	e, _, _, c := newEngine()
	ctx := context.Background()
	if _, err := e.AddProject(ctx, root, "", ""); err != nil {
		t.Fatal(err)
	}
	c.configs[feature] = &compose.Project{Name: "app-feature"} // no digest, like an older start
	inst, err := e.Instance(ctx, "app", "feature/login", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Up(ctx, inst, compose.UpOptions{}, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	c.configs[feature] = &compose.Project{Name: "app-feature", Digest: "new"}
	now, _ := e.Instance(ctx, "app", "feature/login", nil)
	if ch := e.ConfigChanged(now); ch != nil {
		t.Errorf("start without a digest: ConfigChanged = %+v, want nil", ch)
	}
}
