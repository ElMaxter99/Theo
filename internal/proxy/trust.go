package proxy

// Trusting Theo's local authority: `theo proxy trust` and `untrust`, and the
// button of the interface on Windows and macOS. Each system has its own
// store and its own tool; Theo runs that tool and the system asks for its
// own confirmation. Firefox keeps a store of its own, which Theo does not
// touch (docs/cli/proxy.md explains how to import the authority there).

import (
	"bytes"
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// TrustStatus says who trusts the authority.
type TrustStatus struct {
	// System is true when the system's store trusts it: what Chrome, Edge
	// and Safari use on Windows and macOS, and curl and most tools on Linux.
	System bool `json:"system"`
	// NSS is the state of the store Chrome and Chromium read on Linux
	// (~/.pki/nssdb): "trusted", "not-trusted", or "unknown" when certutil
	// is not installed. Empty on other systems.
	NSS string `json:"nss,omitempty"`
}

// Trusted reports whether the browsers of this system will accept the
// authority's certificates.
func (t TrustStatus) Trusted() bool {
	return t.System && (t.NSS == "" || t.NSS == "trusted")
}

// CanTrustWithoutTerminal reports whether trusting can be started from the
// interface: on Windows and macOS the system shows its own confirmation; on
// Linux it needs sudo in a terminal.
func CanTrustWithoutTerminal() bool {
	return runtime.GOOS == "windows" || runtime.GOOS == "darwin"
}

// runCmd runs a command of the system's trust tools. Tests replace it.
var runCmd = func(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	return cmd.Run()
}

// lookPath finds a tool. Tests replace it.
var lookPath = exec.LookPath

// nssDB is the store Chrome and Chromium read on Linux.
func nssDB() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pki", "nssdb")
}

// nickname names the authority in NSS stores.
func (c *CA) nickname() string { return c.Cert.Subject.CommonName }

// Trust reports who trusts the authority now.
func (c *CA) Trust(ctx context.Context) TrustStatus {
	var st TrustStatus
	switch runtime.GOOS {
	case "windows", "darwin":
		// The system verifier is asked each time, so it sees a change at once.
		st.System = c.verify(nil, Suffix) == nil
	default:
		st.System = c.inSystemBundle()
		st.NSS = c.nssState(ctx)
	}
	return st
}

// systemBundles are where Linux distributions keep the trusted roots once
// they are put together.
var systemBundles = []string{
	"/etc/ssl/certs/ca-certificates.crt",                // Debian, Ubuntu, Arch, Alpine
	"/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem", // Fedora, RHEL
	"/etc/pki/tls/certs/ca-bundle.crt",                  // older Fedora
	"/etc/ssl/ca-bundle.pem",                            // openSUSE
}

// inSystemBundle reads the bundles again on each call (Go's own pool is
// read once per process, and the interface runs for days).
func (c *CA) inSystemBundle() bool {
	files := systemBundles
	if f := os.Getenv("SSL_CERT_FILE"); f != "" {
		files = append([]string{f}, files...)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for {
			var b *pem.Block
			b, data = pem.Decode(data)
			if b == nil {
				break
			}
			if b.Type == "CERTIFICATE" && bytes.Equal(b.Bytes, c.Cert.Raw) {
				return true
			}
		}
	}
	return false
}

func (c *CA) nssState(ctx context.Context) string {
	if _, err := lookPath("certutil"); err != nil {
		return "unknown"
	}
	db := nssDB()
	if db == "" {
		return "unknown"
	}
	if _, err := os.Stat(db); err != nil {
		return "not-trusted"
	}
	var out bytes.Buffer
	if err := runCmd(ctx, nil, &out, io.Discard, "certutil", "-d", "sql:"+db, "-L", "-n", c.nickname(), "-a"); err != nil {
		return "not-trusted"
	}
	if samePEM(out.Bytes(), c.CertPEM) {
		return "trusted"
	}
	return "not-trusted"
}

// linuxAnchor is where a Linux distribution takes extra roots from, and the
// command that puts its bundle together again.
func linuxAnchor() (dir, file string, update []string, err error) {
	if _, e := lookPath("update-ca-certificates"); e == nil {
		return "/usr/local/share/ca-certificates", "theo-local-ca.crt", []string{"update-ca-certificates"}, nil
	}
	if _, e := lookPath("update-ca-trust"); e == nil {
		dir := "/etc/pki/ca-trust/source/anchors"
		if _, e := os.Stat(dir); e != nil {
			dir = "/etc/ca-certificates/trust-source/anchors" // Arch
		}
		return dir, "theo-local-ca.pem", []string{"update-ca-trust", "extract"}, nil
	}
	return "", "", nil, errors.New("found neither update-ca-certificates nor update-ca-trust: add " +
		"the authority to the system's trusted roots by hand")
}

// asRoot prefixes a command with sudo unless Theo already runs as root.
func asRoot(args ...string) []string {
	if os.Geteuid() == 0 {
		return args
	}
	return append([]string{"sudo"}, args...)
}

// AddTrust adds the authority to the system's store (and on Linux to
// Chrome's), running the system's tool. stdin is passed on for sudo's
// password; out receives what the tools print.
func (c *CA) AddTrust(ctx context.Context, stdin io.Reader, out io.Writer) error {
	path := c.CertPath()
	switch runtime.GOOS {
	case "windows":
		// The current user's store: no administrator needed, and Windows
		// shows its own warning to confirm.
		return c.run(ctx, stdin, out, "certutil", "-user", "-addstore", "Root", path)
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		return c.run(ctx, stdin, out, "security", "add-trusted-cert", "-r", "trustRoot",
			"-k", filepath.Join(home, "Library", "Keychains", "login.keychain-db"), path)
	}
	dir, file, update, err := linuxAnchor()
	if err != nil {
		return err
	}
	install := asRoot("install", "-D", "-m", "0644", path, filepath.Join(dir, file))
	if err := c.run(ctx, stdin, out, install[0], install[1:]...); err != nil {
		return err
	}
	upd := asRoot(update...)
	if err := c.run(ctx, stdin, out, upd[0], upd[1:]...); err != nil {
		return err
	}
	return c.addNSS(ctx, out)
}

// addNSS adds the authority to Chrome's store on Linux, when certutil is
// there. Without it, it says how to get it.
func (c *CA) addNSS(ctx context.Context, out io.Writer) error {
	if _, err := lookPath("certutil"); err != nil {
		fmt.Fprintln(out, "theo: Chrome and Chromium on Linux keep their own store, and certutil is not installed;"+
			" install it (libnss3-tools on Debian and Ubuntu, nss-tools on Fedora) and run `theo proxy trust` again")
		return nil
	}
	db := nssDB()
	if _, err := os.Stat(filepath.Join(db, "cert9.db")); err != nil {
		if err := os.MkdirAll(db, 0o700); err != nil {
			return err
		}
		if err := c.run(ctx, nil, out, "certutil", "-d", "sql:"+db, "-N", "--empty-password"); err != nil {
			return err
		}
	}
	return c.run(ctx, nil, out, "certutil", "-d", "sql:"+db, "-A", "-t", "C,,", "-n", c.nickname(), "-i", c.CertPath())
}

// RemoveTrust takes the authority out of the stores AddTrust put it in.
func (c *CA) RemoveTrust(ctx context.Context, stdin io.Reader, out io.Writer) error {
	switch runtime.GOOS {
	case "windows":
		return c.run(ctx, stdin, out, "certutil", "-user", "-delstore", "Root", c.Fingerprint())
	case "darwin":
		if err := c.run(ctx, stdin, out, "security", "remove-trusted-cert", c.CertPath()); err != nil {
			return err
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		return c.run(ctx, stdin, out, "security", "delete-certificate", "-Z", c.Fingerprint(),
			filepath.Join(home, "Library", "Keychains", "login.keychain-db"))
	}
	var errs []error
	if c.nssState(ctx) == "trusted" {
		errs = append(errs, c.run(ctx, nil, out, "certutil", "-d", "sql:"+nssDB(), "-D", "-n", c.nickname()))
	}
	dir, file, update, err := linuxAnchor()
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	if _, err := os.Stat(filepath.Join(dir, file)); err == nil {
		rm := asRoot("rm", "-f", filepath.Join(dir, file))
		if err := c.run(ctx, stdin, out, rm[0], rm[1:]...); err != nil {
			return errors.Join(append(errs, err)...)
		}
		upd := asRoot(update...)
		errs = append(errs, c.run(ctx, stdin, out, upd[0], upd[1:]...))
	}
	return errors.Join(errs...)
}

func (c *CA) run(ctx context.Context, stdin io.Reader, out io.Writer, name string, args ...string) error {
	var errBuf bytes.Buffer
	if err := runCmd(ctx, stdin, out, io.MultiWriter(out, &errBuf), name, args...); err != nil {
		msg := strings.TrimSpace(errBuf.String())
		if msg != "" {
			msg = ": " + firstLine(msg)
		}
		return fmt.Errorf("%s %s failed: %w%s", name, strings.Join(args, " "), err, msg)
	}
	return nil
}
