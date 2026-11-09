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
