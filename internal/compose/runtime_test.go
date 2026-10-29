package compose

import (
	"reflect"
	"testing"
)

func TestParsePS(t *testing.T) {
	lines := `{"Name":"app-web-1","Service":"web","State":"running","Health":"healthy","Publishers":[{"URL":"0.0.0.0","TargetPort":80,"PublishedPort":8080,"Protocol":"tcp"}]}
{"Name":"app-db-1","Service":"db","State":"exited","ExitCode":1,"Publishers":null}
`
	array := `[{"Name":"app-web-1","Service":"web","State":"running","Health":"healthy","Publishers":[{"URL":"0.0.0.0","TargetPort":80,"PublishedPort":8080,"Protocol":"tcp"}]},
{"Name":"app-db-1","Service":"db","State":"exited","ExitCode":1}]`
	want := []Container{
		{Name: "app-web-1", Service: "web", State: "running", Health: "healthy", Publishers: []Publisher{{URL: "0.0.0.0", TargetPort: 80, PublishedPort: 8080, Protocol: "tcp"}}},
		{Name: "app-db-1", Service: "db", State: "exited", ExitCode: 1},
	}
	for name, out := range map[string]string{"lines": lines, "array": array} {
		got, err := ParsePS([]byte(out))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: ParsePS() =\n%+v\nwant\n%+v", name, got, want)
		}
	}
	if got, err := ParsePS([]byte("\n")); err != nil || got != nil {
		t.Errorf("empty output: %v, %v", got, err)
	}
	if _, err := ParsePS([]byte("{")); err == nil {
		t.Error("expected an error for invalid output")
	}
}

func TestTargetArgs(t *testing.T) {
	tg := Target{Dir: "/w", Name: "app-feature", EnvFiles: []string{"/w/.env"}}
	got := tg.args([]string{"*"}, "down")
	want := []string{"compose", "-p", "app-feature", "--env-file", "/w/.env", "--profile", "*", "down"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("args() = %v, want %v", got, want)
	}
}
