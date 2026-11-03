package proxy

// HTTPS for the local domains (ADR-0015 of the workspace): Theo keeps a
// certificate authority of its own in its data directory, limited with name
// constraints to localhost and the names under it, and signs a certificate
// per name on the fly. Trusting it is a separate, explicit step (trust.go).

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ElMaxter99/Theo/internal/filelock"
)

// DefaultHTTPSPort is the port the proxy serves HTTPS on by default.
const DefaultHTTPSPort = 7443

// The files of the authority, in <data dir>/proxy/.
const (
	caDirName  = "proxy"
	caCertFile = "ca.pem"
	caKeyFile  = "ca-key.pem"
)

// caCommonName starts the subject of every authority Theo makes, so that it
// can be found again in the system's stores.
const caCommonName = "Theo local authority"

// leafLifetime is how long a certificate for one name lasts. The proxy makes
// new ones each time it starts, so it can be short.
const leafLifetime = 30 * 24 * time.Hour

// CA is Theo's local certificate authority.
type CA struct {
	Cert    *x509.Certificate
	CertPEM []byte
	key     crypto.Signer
	Dir     string

	leafKey  *ecdsa.PrivateKey
	mu       sync.Mutex
	leaves   map[string]*tls.Certificate
	issuedAt map[string]time.Time
}

// CADir is where the authority of the data directory dir lives.
func CADir(dir string) string { return filepath.Join(dir, caDirName) }

// CertPath is the authority's certificate, the file to trust.
func (c *CA) CertPath() string { return filepath.Join(c.Dir, caCertFile) }

// Fingerprint is the SHA-1 of the certificate, the way certutil and the
// macOS keychain name it.
func (c *CA) Fingerprint() string {
	sum := sha1.Sum(c.Cert.Raw)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// LoadCA reads the authority of the data directory dir. It returns
// os.ErrNotExist when there is none yet.
func LoadCA(dir string) (*CA, error) {
	cdir := CADir(dir)
	certPEM, err := os.ReadFile(filepath.Join(cdir, caCertFile))
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(filepath.Join(cdir, caKeyFile))
	if err != nil {
		return nil, err
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("the local authority in %s is damaged: %w (remove the folder to make a new one, then trust it again)", cdir, err)
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, err
	}
	signer, ok := pair.PrivateKey.(crypto.Signer)
	if !ok || !cert.IsCA {
		return nil, fmt.Errorf("the local authority in %s is not a certificate authority", cdir)
	}
	return &CA{Cert: cert, CertPEM: certPEM, key: signer, Dir: cdir}, nil
}

// EnsureCA loads the authority of dir, making it first when there is none.
// created says whether it was made now.
func EnsureCA(dir string) (ca *CA, created bool, err error) {
	ca, err = LoadCA(dir)
	if err == nil {
		return ca, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, false, err
	}
	unlock, err := filelock.Lock(filepath.Join(dir, "proxy-ca.lock"))
	if err == nil {
		defer unlock()
	}
	if ca, err = LoadCA(dir); err == nil { // made by another process meanwhile
		return ca, false, nil
	}
	ca, err = newCA(CADir(dir), time.Now())
	return ca, err == nil, err
}

// newCA makes an authority that can only sign localhost and the names under
// it: DNS names are constrained to localhost, and every IP address is
// excluded, so its key could not sign for any other site even if it leaked.
func newCA(cdir string, now time.Time) (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := randSerial()
	if err != nil {
		return nil, err
	}
	who := "Theo"
	if u, err := user.Current(); err == nil && u.Username != "" {
		who = u.Username
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		who += "@" + h
	}
	_, all4, _ := net.ParseCIDR("0.0.0.0/0")
	_, all6, _ := net.ParseCIDR("::/0")
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:         caCommonName + " " + now.UTC().Format("2006-01-02"),
			Organization:       []string{"Theo"},
			OrganizationalUnit: []string{who},
		},
		NotBefore:                   now.Add(-time.Hour),
		NotAfter:                    now.AddDate(10, 0, 0),
		KeyUsage:                    x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid:       true,
		IsCA:                        true,
		MaxPathLenZero:              true,
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         []string{Suffix},
		ExcludedIPRanges:            []*net.IPNet{all4, all6},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := os.MkdirAll(cdir, 0o700); err != nil {
		return nil, err
	}
	// The key first: a certificate without its key would look like a damaged
	// authority; a key alone is made again.
	if err := filelock.WriteFile(filepath.Join(cdir, caKeyFile), keyPEM, 0o600); err != nil {
		return nil, err
	}
	if err := filelock.WriteFile(filepath.Join(cdir, caCertFile), certPEM, 0o644); err != nil {
		return nil, err
	}
	return &CA{Cert: cert, CertPEM: certPEM, key: key, Dir: cdir}, nil
}

func randSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
}

// Signable reports whether the authority can sign name: localhost or a name
// under it.
func Signable(name string) bool {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	return name == Suffix || strings.HasSuffix(name, "."+Suffix) && !strings.Contains(name, "..")
}

// Leaf returns a certificate for name, signed by the authority, made once
// per name and kept while it is valid.
func (c *CA) Leaf(name string, now time.Time) (*tls.Certificate, error) {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	if !Signable(name) {
		return nil, fmt.Errorf("theo proxy only serves HTTPS for names under .%s, not %q", Suffix, name)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if cert, ok := c.leaves[name]; ok && now.Sub(c.issuedAt[name]) < leafLifetime/2 {
		return cert, nil
	}
	if c.leafKey == nil {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, err
		}
		c.leafKey = k
	}
	serial, err := randSerial()
	if err != nil {
		return nil, err
	}
	notAfter := now.Add(leafLifetime)
	if notAfter.After(c.Cert.NotAfter) {
		notAfter = c.Cert.NotAfter
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: name, Organization: []string{"Theo"}},
		DNSNames:     []string{name},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.Cert, c.leafKey.Public(), c.key)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	cert := &tls.Certificate{Certificate: [][]byte{der, c.Cert.Raw}, PrivateKey: c.leafKey, Leaf: leaf}
	if c.leaves == nil {
		c.leaves, c.issuedAt = map[string]*tls.Certificate{}, map[string]time.Time{}
	}
	c.leaves[name], c.issuedAt[name] = cert, now
	return cert, nil
}

// TLSConfig is the server configuration of the proxy's HTTPS listener. A
// client that sends no name (an address typed by IP) gets the certificate
// of localhost, which it will not accept for an IP: the proxy's names are
// the way in.
func (c *CA) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"},
		GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			name := hello.ServerName
			if name == "" {
				name = Suffix
			}
			return c.Leaf(name, time.Now())
		},
	}
}

// Verify checks a certificate for name against the given roots (nil: the
// system's), the way a client would.
func (c *CA) verify(roots *x509.CertPool, name string) error {
	leaf, err := c.Leaf(name, time.Now())
	if err != nil {
		return err
	}
	inter := x509.NewCertPool()
	_, err = leaf.Leaf.Verify(x509.VerifyOptions{DNSName: name, Roots: roots, Intermediates: inter})
	return err
}

// samePEM reports whether two PEM files hold the same certificate.
func samePEM(a, b []byte) bool {
	ba, _ := pem.Decode(a)
	bb, _ := pem.Decode(b)
	return ba != nil && bb != nil && bytes.Equal(ba.Bytes, bb.Bytes)
}
