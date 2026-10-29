package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ElMaxter99/Theo/internal/compose"
	"github.com/ElMaxter99/Theo/internal/instances"
	"github.com/ElMaxter99/Theo/internal/ports"
)

func kinds(r *Report) []string {
	var out []string
	for _, f := range r.Findings {
		out = append(out, f.Kind+" "+f.Subject)
	}
	return out
}

func has(r *Report, kind string) bool {
	for _, f := range r.Findings {
		if f.Kind == kind {
			return true
		}
	}
	return false
}

func TestReconcileForgetsOnlyTheosOwnRecordsOfGoneWorktrees(t *testing.T) {
	e, _, _, _ := newEngine()
	e.dataDir = t.TempDir()
	ctx := context.Background()
	if _, err := e.AddProject(ctx, root, "", ""); err != nil {
		t.Fatal(err)
	}
	// Directories that exist, so only the missing ones count as gone.
	alive := t.TempDir()
	gone := filepath.Join(t.TempDir(), "gone")
	otherGone := filepath.Join(t.TempDir(), "other-gone")

	e.settings.Save(instances.State{
		gone:      {Project: "app", Env: map[string]instances.Var{"A": {Value: "1"}}},
		otherGone: {Project: "other", NeutralizeContainerNames: true},
		alive:     {Project: "app", NeutralizeContainerNames: true},
	})
	e.portStore.Save(ports.State{gone: {"web|80/tcp": 8081}, otherGone: {"web|80/tcp": 9000}, alive: {"web|80/tcp": 8082}})
	e.settings.Append(instances.Event{Project: "app", Workspace: gone, Compose: "app-gone", Operation: "up", Result: "ok"})
	// A generated file of the gone worktree.
	file := e.instanceFilesOf(gone, "app-gone")[0]
	os.MkdirAll(filepath.Dir(file), 0o755)
	os.WriteFile(file, []byte("services: {}\n"), 0o600)

	r, err := e.Reconcile(ctx, "app")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{FindStaleSettings, FindStalePorts, FindStaleFiles} {
		if !has(r, k) {
			t.Errorf("missing %q in %v", k, kinds(r))
		}
	}
	for _, f := range r.Findings {
		if strings.Contains(f.Subject, "other-gone") || f.Subject == alive {
			t.Errorf("another project's or a live record was reported: %+v", f)
		}
	}

	n, err := e.ApplyReconcile(r)
	if err != nil || n != 3 {
		t.Fatalf("apply: %d %v", n, err)
	}
	st, _ := e.settings.Load()
	as, _ := e.portStore.Load()
	if _, ok := st[gone]; ok {
		t.Errorf("stale settings kept")
	}
	if _, ok := as[gone]; ok {
		t.Errorf("stale ports kept")
	}
	if _, err := os.Stat(file); err == nil {
		t.Errorf("stale file kept")
	}
	if _, ok := st[otherGone]; !ok || st[alive].Project != "app" || as[alive] == nil || as[otherGone] == nil {
		t.Errorf("records that were not stale of this project were forgotten: %v %v", st, as)
	}

	// Without a project it covers the other project's records too.
	r, _ = e.Reconcile(ctx, "")
	if !has(r, FindStaleSettings) {
		t.Errorf("the global run misses the other project's records: %v", kinds(r))
	}
}

func TestReconcileReportsProjectAndWorktreeGone(t *testing.T) {
	e, _, g, _ := newEngine()
	ctx := context.Background()
	if _, err := e.AddProject(ctx, root, "", ""); err != nil {
		t.Fatal(err)
	}
	r, _ := e.Reconcile(ctx, "app")
	// The fake paths do not exist on disk: the project and its worktrees are reported, never fatal.
	if !has(r, FindProjectMissing) {
		t.Errorf("findings = %v", kinds(r))
	}
	_ = g
}

func TestReconcileWithoutDockerSaysSo(t *testing.T) {
	e, _, _, c := newEngine()
	_ = c
	e.compose = noDocker{c}
	r, err := e.Reconcile(context.Background(), "")
	if err != nil || r.DockerError == "" {
		t.Errorf("err=%v report=%+v", err, r)
	}
}

type noDocker struct{ *fakeCompose }

func (noDocker) Stacks(context.Context) ([]compose.Stack, error) {
	return nil, os.ErrNotExist
}
