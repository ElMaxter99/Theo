package desktop

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/notify"
	"github.com/ElMaxter99/Theo/internal/prefs"
	"github.com/ElMaxter99/Theo/internal/registry"
	"github.com/ElMaxter99/Theo/internal/update"
)

func TestUpdateMessage(t *testing.T) {
	stable := update.Info{Latest: "0.11.1"}
	rc := update.Info{Latest: "0.12.0-rc.2", Prerelease: true}
	if m := UpdateMessage(stable, true); m.Title != "Theo 0.11.1 disponible" || !strings.Contains(m.Body, "Actualizar ahora") {
		t.Errorf("%+v", m)
	}
	if m := UpdateMessage(stable, false); m.Title != "Theo 0.11.1 available" {
		t.Errorf("%+v", m)
	}
	if m := UpdateMessage(rc, true); !strings.HasPrefix(m.Title, "Versión de prueba: Theo 0.12.0-rc.2") {
		t.Errorf("%+v", m)
	}
	if m := UpdateMessage(rc, false); !strings.HasPrefix(m.Title, "Test version") {
		t.Errorf("%+v", m)
	}
}

func TestWatchUpdatesNotifiesOncePerVersion(t *testing.T) {
	t.Setenv(registry.EnvHome, t.TempDir())
	e, err := engine.Open()
	if err != nil {
		t.Fatal(err)
	}
	oldV, oldD := engine.Version, updateStartDelay
	engine.Version, updateStartDelay = "0.11.0", 0
	t.Cleanup(func() { engine.Version, updateStartDelay = oldV, oldD })

	var mu sync.Mutex
	var got []notify.Message
	releases := []update.Release{{Tag: "v0.11.0"}, {Tag: "v0.11.1"}}
	opts := Options{Engine: e,
		ListReleases: func(context.Context) ([]update.Release, error) { return releases, nil },
		Notify:       func(m notify.Message) error { mu.Lock(); got = append(got, m); mu.Unlock(); return nil }}

	run := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
		defer cancel()
		watchUpdates(ctx, opts)
	}
	run()
	mu.Lock()
	n := len(got)
	mu.Unlock()
	if n != 1 || !strings.Contains(got[0].Title, "0.11.1") {
		t.Fatalf("first run: %+v", got)
	}
	run() // the same version is not announced again, not even after a restart
	if len(got) != 1 {
		t.Errorf("announced twice: %+v", got)
	}
	// A person who turned notifications off gets none for the next version.
	if err := e.Prefs().Set("update-notify", "off"); err != nil {
		t.Fatal(err)
	}
	releases = []update.Release{{Tag: "v0.11.2"}}
	if err := e.Prefs().ChangeUpdateInfo(func(u *prefs.Update) { u.CheckedAt = time.Time{} }); err != nil {
		t.Fatal(err)
	}
	run()
	if len(got) != 1 {
		t.Errorf("update-notify off: %+v", got)
	}
	// And one who turned looking off asks nothing at all.
	_ = e.Prefs().Set("update-check", "off")
	_ = e.Prefs().Set("update-notify", "on")
	_ = e.Prefs().ChangeUpdateInfo(func(u *prefs.Update) { u.CheckedAt = time.Time{}; u.Latest = "" })
	run()
	if len(got) != 1 {
		t.Errorf("update-check off: %+v", got)
	}
}
