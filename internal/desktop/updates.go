package desktop

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/ElMaxter99/Theo/internal/engine"
	"github.com/ElMaxter99/Theo/internal/notify"
	"github.com/ElMaxter99/Theo/internal/update"
)

// updateStartDelay is the wait before the first look, so the window opens first.
var updateStartDelay = 5 * time.Second

// updateEvery is how often the app looks at whether a look is due.
const updateEvery = 10 * time.Minute

// UpdateMessage is the notification for a new version, in Spanish or English.
func UpdateMessage(info update.Info, spanish bool) notify.Message {
	switch {
	case info.Prerelease && spanish:
		return notify.Message{Title: "Versión de prueba: Theo " + info.Latest, Body: "Solo la ves porque activaste las versiones de prueba. Ábrelo y pulsa «Actualizar ahora»."}
	case info.Prerelease:
		return notify.Message{Title: "Test version: Theo " + info.Latest, Body: "You see it because you turned on test versions. Open Theo and press \"Update now\"."}
	case spanish:
		return notify.Message{Title: "Theo " + info.Latest + " disponible", Body: "Ábrelo y pulsa «Actualizar ahora»."}
	}
	return notify.Message{Title: "Theo " + info.Latest + " available", Body: "Open Theo and press \"Update now\"."}
}

// watchUpdates looks for a new version when the app starts and every few
// hours, and shows one notification per version found. It does nothing when
// the person turned update-check off, and never installs anything.
func watchUpdates(ctx context.Context, o Options) {
	send := o.Notify
	if send == nil {
		send = notify.Send
	}
	lang := strings.ToLower(os.Getenv("LC_ALL") + os.Getenv("LC_MESSAGES") + os.Getenv("LANG"))
	c := update.Checker{Store: o.Engine.Prefs(), Current: engine.Version, Desktop: true, List: o.ListReleases}
	reported := false
	look := func() {
		if !c.Enabled() {
			return
		}
		lctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		_, _ = c.Check(lctx, false)
		if c.Store.Get("update-notify") == "off" {
			return
		}
		if info, first := c.ShouldNotify(); first {
			if err := send(UpdateMessage(info, strings.HasPrefix(lang, "es"))); err != nil && !reported {
				reported = true
				if o.Stderr != nil {
					_, _ = o.Stderr.Write([]byte("theo-desktop: update notification: " + err.Error() + "\n"))
				}
			}
		}
	}
	select {
	case <-time.After(updateStartDelay):
	case <-ctx.Done():
		return
	}
	look()
	t := time.NewTicker(updateEvery)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			look()
		case <-ctx.Done():
			return
		}
	}
}
