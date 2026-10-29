package ports

import (
	"net"
	"reflect"
	"testing"
)

func TestFreeSeesAListeningPort(t *testing.T) {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Skip("cannot listen:", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if Free("", port, "tcp") {
		t.Errorf("port %d is taken but reported free", port)
	}
	l.Close()
	if !Free("", port, "tcp") {
		t.Errorf("port %d was released but reported taken", port)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	s := Open(t.TempDir())
	st, err := s.Load()
	if err != nil || len(st) != 0 {
		t.Fatalf("empty store: %v %v", st, err)
	}
	want := State{"/w": {Key("api", 3000, "tcp"): 3304}}
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestFreeAddressNotOnThisHost(t *testing.T) {
	// 192.0.2.0/24 is reserved for documentation: no interface has it.
	if !Free("192.0.2.1", 8080, "tcp") {
		t.Error("a port on an address this host does not have must count as free (not testable), not taken")
	}
}
