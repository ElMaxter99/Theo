package proxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestEnsureCAMakesOneAuthorityAndKeepsIt(t *testing.T) {
	dir := t.TempDir()
	ca, created, err := EnsureCA(dir)
	if err != nil || !created {
		t.Fatalf("EnsureCA = %v, created %v", err, created)
	}
	if !ca.Cert.IsCA || !ca.Cert.PermittedDNSDomainsCritical || len(ca.Cert.PermittedDNSDomains) != 1 || ca.Cert.PermittedDNSDomains[0] != "localhost" {
		t.Fatalf("constraints: CA %v, critical %v, permitted %v", ca.Cert.IsCA, ca.Cert.PermittedDNSDomainsCritical, ca.Cert.PermittedDNSDomains)
	}
	if len(ca.Cert.ExcludedIPRanges) != 2 {
		t.Fatalf("excluded IP ranges = %v, want every IPv4 and IPv6 address", ca.Cert.ExcludedIPRanges)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, "proxy", "ca-key.pem"))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("key file mode = %v, %v; want 0600", info.Mode().Perm(), err)
		}
	}
	again, created, err := EnsureCA(dir)
	if err != nil || created || again.Fingerprint() != ca.Fingerprint() {
		t.Fatalf("second EnsureCA = %v, created %v, same %v", err, created, again.Fingerprint() == ca.Fingerprint())
	}
}

func TestLeafOnlyForLocalhost(t *testing.T) {
	ca, _, err := EnsureCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	for _, name := range []string{"localhost", "main.shop.localhost", "WEB.Main.Shop.localhost."} {
		if err := ca.verify(roots, strings.ToLower(strings.TrimSuffix(name, "."))); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, name := range []string{"example.com", "localhost.example.com", "evil-localhost", "a..localhost", ""} {
		if _, err := ca.Leaf(name, time.Now()); err == nil {
			t.Errorf("Leaf(%q) signed a name outside .localhost", name)
		}
	}
	a, _ := ca.Leaf("main.shop.localhost", time.Now())
	b, _ := ca.Leaf("main.shop.localhost", time.Now())
	if a != b {
		t.Error("the certificate of a name is made again on each handshake")
	}
}

// Even with its key, the authority cannot vouch for another site: a client
// that trusts it rejects such a certificate (the name constraints).
func TestNameConstraintsHoldEvenWithTheKey(t *testing.T) {
	ca, _, err := EnsureCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	for _, tc := range []struct {
		dns []string
		ips []net.IP
	}{
		{dns: []string{"www.example.com"}},
		{ips: []net.IP{net.ParseIP("127.0.0.1")}},
		{ips: []net.IP{net.ParseIP("::1")}},
	} {
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "x"},
			DNSNames: tc.dns, IPAddresses: tc.ips,
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
			ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.Cert, ca.key.Public(), ca.key)
		if err != nil {
			t.Fatal(err)
		}
		leaf, _ := x509.ParseCertificate(der)
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots}); err == nil {
			t.Errorf("a certificate for %v %v verified", tc.dns, tc.ips)
		}
	}
}

func TestServeTLS(t *testing.T) {
	ca, _, err := EnsureCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.Host+" "+r.Header.Get("X-Forwarded-Proto"))
	}))
	defer backend.Close()
	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(backend.URL, "http://"))
	hostPort, _ := strconv.Atoi(portStr)
	targets := []Target{{Project: "shop", Worktree: "/w/shop", Main: true, ComposeProject: "shop", Service: "web",
		ContainerPort: 80, HostIP: "127.0.0.1", HostPort: hostPort, Running: true}}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	h := &Handler{Source: func(context.Context) ([]Target, error) { return targets, nil }, Port: 7080, HTTPSPort: port}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServeTLS(ctx, ln, h, ca) }()
	defer func() { cancel(); <-done }()

	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	addr := ln.Addr().String()
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}}
	res, err := client.Get("https://main.shop.localhost/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "main.shop.localhost https" {
		t.Fatalf("backend saw %q", body)
	}

	// The page that lists the routes links to their HTTPS addresses.
	res, err = client.Get("https://localhost/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if want := "https://main.shop.localhost:" + strconv.Itoa(port); !bytes.Contains(body, []byte(want)) {
		t.Fatalf("route page lacks %s:\n%s", want, body)
	}

	// A name outside .localhost gets no certificate at all.
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: "example.com", RootCAs: roots})
	if err == nil {
		conn.Close()
		t.Fatal("handshake for example.com succeeded")
	}
}

func TestTableSetHTTPS(t *testing.T) {
	tbl := BuildTable([]Target{{Project: "shop", Worktree: "/w", Main: true, Service: "web", ContainerPort: 80, HostPort: 8080}}, 7080)
	tbl.SetHTTPS(7443)
	if got := tbl.Routes[0].HTTPSURL; got != "https://main.shop.localhost:7443" {
		t.Fatalf("HTTPSURL = %q", got)
	}
	tbl.SetHTTPS(443)
	if got := tbl.Routes[0].HTTPSURL; got != "https://main.shop.localhost" {
		t.Fatalf("HTTPSURL on 443 = %q", got)
	}
	tbl.SetHTTPS(0)
	if got := tbl.Routes[0].HTTPSURL; got != "" {
		t.Fatalf("HTTPSURL without HTTPS = %q", got)
	}
}

func TestTrustOnLinuxRunsTheDistributionTools(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the Linux steps")
	}
	ca, _, err := EnsureCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	var ran []string
	oldRun, oldLook := runCmd, lookPath
	defer func() { runCmd, lookPath = oldRun, oldLook }()
	runCmd = func(_ context.Context, _ io.Reader, _, _ io.Writer, name string, args ...string) error {
		ran = append(ran, name+" "+strings.Join(args, " "))
		return nil
	}
	lookPath = func(name string) (string, error) {
		if name == "update-ca-certificates" || name == "certutil" {
			return "/usr/bin/" + name, nil
		}
		return "", os.ErrNotExist
	}
	if err := ca.AddTrust(context.Background(), nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	all := strings.Join(ran, "\n")
	for _, want := range []string{
		"install -D -m 0644 " + ca.CertPath() + " /usr/local/share/ca-certificates/theo-local-ca.crt",
		"update-ca-certificates",
		"certutil -d sql:" + filepath.Join(os.Getenv("HOME"), ".pki", "nssdb") + " -N --empty-password",
		"-A -t C,, -n " + ca.nickname() + " -i " + ca.CertPath(),
	} {
		if !strings.Contains(all, want) {
			t.Errorf("did not run %q; ran:\n%s", want, all)
		}
	}
	if os.Geteuid() != 0 && !strings.HasPrefix(ran[0], "sudo ") {
		t.Errorf("the system store was changed without sudo: %s", ran[0])
	}
}

func TestInSystemBundleRereadsTheFile(t *testing.T) {
	ca, _, err := EnsureCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), "bundle.pem")
	t.Setenv("SSL_CERT_FILE", bundle)
	os.WriteFile(bundle, []byte("nothing"), 0o644)
	if ca.inSystemBundle() {
		t.Fatal("trusted before it was added")
	}
	os.WriteFile(bundle, ca.CertPEM, 0o644)
	if !ca.inSystemBundle() {
		t.Fatal("not trusted once in the bundle")
	}
}
