package capture

import (
	"encoding/json"
	"strings"
	"testing"
)

const goodInventory = `{
  "version": 1, "app": "portal-cliente", "entrada": "login",
  "viewports": [{"id":"escritorio","width":1440,"height":900},{"id":"movil","width":390,"height":844,"mobile":true,"scale":2}],
  "mocks": {"modo":"mock","carpeta":"mocks/"},
  "pantallas": [
    {"id":"login","ruta":"#/login","estados":[{"id":"vacio"}]},
    {"id":"facturas","titulo":"Facturas","ruta":"/facturas","origen":["src/Facturas.tsx"],"estados":[
      {"id":"con-datos","datos":{"GET /api/facturas":"mocks/f.json"},"acciones":[{"esperar":"tabla"}]},
      {"id":"filtros","acciones":[{"click":{"rol":"button","nombre":"Filtros"}},{"escribir":{"en":{"selector":"#q"},"texto":"a"}},{"scroll":"fin"}]}
    ],"ignorar":[{"selector":"[data-testid=reloj]"}]}
  ],
  "flujos": [{"id":"alta","pasos":["login#vacio","facturas#con-datos"]}]
}`

func TestInventoryValid(t *testing.T) {
	inv, err := ParseInventory([]byte(goodInventory))
	if err != nil {
		t.Fatal(err)
	}
	if errs := inv.Validate(); len(errs) != 0 {
		t.Fatalf("%v", errs)
	}
	s := inv.Summarize()
	if s.Viewports != 2 || s.Screens != 2 || s.States != 3 || s.Flows != 1 || s.NoOrigin != 1 || s.Entry != "login" {
		t.Fatalf("%+v", s)
	}
}

func TestInventoryErrors(t *testing.T) {
	cases := map[string]string{
		`"click":{"rol":"button"}`:                 `"click" needs "rol" and "nombre", or "selector"`,
		`"saltar":"x"`:                             `"saltar" is not an action`,
		`"esperar":""`:                             `"esperar" needs a text`,
		`"scroll":"abajo"`:                         `"scroll" needs pixels or "fin"`,
		`"escribir":{"texto":"x"}`:                 `"escribir" needs "en"`,
		`"click":{"selector":"a"},"tecla":"Enter"`: `exactly one verb`,
	}
	for action, want := range cases {
		doc := strings.Replace(goodInventory, `{"esperar":"tabla"}`, `{`+action+`}`, 1)
		inv, err := ParseInventory([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		errs := strings.Join(inv.Validate(), "\n")
		if !strings.Contains(errs, want) || !strings.Contains(errs, "screens[1].states[0].actions[0]") {
			t.Errorf("%s: %q", action, errs)
		}
	}
	bad := strings.NewReplacer(`"login#vacio","facturas#con-datos"`, `"login#vacio","factura-nueva#enviado"`, `"entrada": "login"`, `"entrada": "nada"`, `"modo":"mock"`, `"modo":"otro"`, `"ruta":"/facturas"`, `"ruta":"facturas"`).Replace(goodInventory)
	inv, _ := ParseInventory([]byte(bad))
	errs := strings.Join(inv.Validate(), "\n")
	for _, w := range []string{`flows[0].steps[1]: "factura-nueva#enviado" is not a state`, `entry: "nada"`, `mocks.modo: "otro"`, `must start with / or #`} {
		if !strings.Contains(errs, w) {
			t.Errorf("missing %q in\n%s", w, errs)
		}
	}
	if _, err := ParseInventory([]byte(`{"version":1,"extra":true}`)); err == nil {
		t.Error("unknown keys must be refused")
	}
}

func TestSchemaIsJSON(t *testing.T) {
	var v map[string]any
	if err := json.Unmarshal(Schema, &v); err != nil || v["title"] == nil {
		t.Fatalf("%v", err)
	}
}
