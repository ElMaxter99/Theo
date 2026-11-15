package tools

import "testing"

func TestRegisterListGet(t *testing.T) {
	saved := registry
	registry = map[string]Tool{}
	t.Cleanup(func() { registry = saved })

	Register(Tool{ID: "zeta", Title: "Z", Scope: "x"})
	Register(Tool{ID: "alpha", Title: "A", Scope: "x"})
	l := List()
	if len(l) != 2 || l[0].ID != "alpha" || l[1].ID != "zeta" {
		t.Fatalf("the list must be sorted by ID: %+v", l)
	}
	if _, ok := Get("alpha"); !ok {
		t.Error("alpha must be found")
	}
	if _, ok := Get("nope"); ok {
		t.Error("an unknown tool must not be found")
	}
}

func TestRegisterRejectsMistakes(t *testing.T) {
	saved := registry
	registry = map[string]Tool{}
	t.Cleanup(func() { registry = saved })

	for name, tool := range map[string]Tool{
		"no id":    {Title: "T", Scope: "s"},
		"bad id":   {ID: "Bad Id", Title: "T", Scope: "s"},
		"no title": {ID: "a", Scope: "s"},
		"no scope": {ID: "a", Title: "T"},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: Register must panic", name)
				}
			}()
			Register(tool)
		}()
	}
	Register(Tool{ID: "dup", Title: "T", Scope: "s"})
	defer func() {
		if recover() == nil {
			t.Error("a duplicate must panic")
		}
	}()
	Register(Tool{ID: "dup", Title: "T", Scope: "s"})
}
