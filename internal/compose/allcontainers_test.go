package compose

import (
	"reflect"
	"testing"
)

func TestParseInspectGroupsByProjectAndKeepsWhatPSReports(t *testing.T) {
	out := []byte(`{"name":"/Shop-web-1","project":"shop","service":"web","state":"running","exit":0,"health":"unhealthy","ports":{"8080/tcp":[{"HostIp":"0.0.0.0","HostPort":"32768"},{"HostIp":"::","HostPort":"32768"}],"9229/tcp":null}}
{"name":"/shop-db-1","project":"shop","service":"db","state":"exited","exit":137,"health":"","ports":{}}

{"name":"/other-job-1","project":"Other","service":"job","state":"exited","exit":0,"health":"","ports":{"53/udp":[{"HostIp":"127.0.0.1","HostPort":"5353"}]}}
{"name":"/stray","project":"","service":"","state":"running","exit":0,"health":"","ports":{}}
`)
	got, err := ParseInspect(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("projects = %v, want shop and other (the container with no project is not Compose's)", got)
	}
	shop := got["shop"]
	if len(shop) != 2 || shop[0].Name != "Shop-web-1" && shop[0].Name != "shop-db-1" {
		t.Fatalf("shop = %+v", shop)
	}
	var web, db Container
	for _, c := range shop {
		if c.Service == "web" {
			web = c
		} else {
			db = c
		}
	}
	if web.Name != "Shop-web-1" || web.State != "running" || web.Health != "unhealthy" {
		t.Errorf("web = %+v", web)
	}
	wantPub := []Publisher{
		{URL: "0.0.0.0", TargetPort: 8080, PublishedPort: 32768, Protocol: "tcp"},
		{URL: "::", TargetPort: 8080, PublishedPort: 32768, Protocol: "tcp"},
		{TargetPort: 9229, Protocol: "tcp"}, // exposed, not published: PublishedPort 0, as docker compose ps says
	}
	if !reflect.DeepEqual(web.Publishers, wantPub) {
		t.Errorf("publishers = %+v, want %+v", web.Publishers, wantPub)
	}
	if db.State != "exited" || db.ExitCode != 137 || len(db.Publishers) != 0 {
		t.Errorf("db = %+v", db)
	}
	if o := got["other"]; len(o) != 1 || o[0].Publishers[0].Protocol != "udp" || o[0].Publishers[0].PublishedPort != 5353 {
		t.Errorf("other = %+v (project names are lowercase keys)", o)
	}
}

func TestParseInspectEmptyAndBroken(t *testing.T) {
	if got, err := ParseInspect(nil); err != nil || len(got) != 0 {
		t.Errorf("empty: %v, %v", got, err)
	}
	if _, err := ParseInspect([]byte("not json\n")); err == nil {
		t.Error("broken output must be an error, not an empty list")
	}
}
