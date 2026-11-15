package api_test

import "testing"

func TestNotifyMode(t *testing.T) {
	e := newEnv(t)
	var got struct{ Mode string }
	if st := e.c.call("GET", "/api/v1/notify", nil, &got); st != 200 || got.Mode != "off" {
		t.Fatalf("default = %d %+v", st, got)
	}
	if st := e.c.call("POST", "/api/v1/notify", map[string]string{"mode": "waiting"}, &got); st != 200 || got.Mode != "waiting" {
		t.Fatalf("set = %d %+v", st, got)
	}
	if v := e.eng.Prefs().Get("notify.agent"); v != "waiting" {
		t.Errorf("stored %q", v)
	}
	var bad apiErr
	if st := e.c.call("POST", "/api/v1/notify", map[string]string{"mode": "loud"}, &bad); st != 400 {
		t.Errorf("bad mode = %d", st)
	}
}

func TestNotifySkipFocused(t *testing.T) {
	e := newEnv(t)
	var got struct {
		Mode        string
		SkipFocused bool `json:"skip_focused"`
	}
	if st := e.c.call("GET", "/api/v1/notify", nil, &got); st != 200 || got.SkipFocused {
		t.Fatalf("default = %d %+v", st, got)
	}
	// Only the checkbox: the mode stays as it was.
	e.c.call("POST", "/api/v1/notify", map[string]any{"mode": "all"}, &got)
	if st := e.c.call("POST", "/api/v1/notify", map[string]any{"skip_focused": true}, &got); st != 200 || !got.SkipFocused || got.Mode != "all" {
		t.Fatalf("set = %d %+v", st, got)
	}
	if v := e.eng.Prefs().Get("notify.skip-focused"); v != "on" {
		t.Errorf("stored %q", v)
	}
	var bad apiErr
	if st := e.c.call("POST", "/api/v1/notify", map[string]any{}, &bad); st != 400 {
		t.Errorf("empty body = %d", st)
	}
}
