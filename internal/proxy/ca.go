// Package proxy is an HTTP(S) debugging proxy: it terminates TLS with certificates signed by its own
// root CA, forwards the request, and keeps what went through. It knows nothing about devices.
package proxy

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
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
	"path/filepath"
	"sync"
	"time"

	"github.com/smallstep/pkcs7"
)

const (
	caFile    = "ca.pem"
	caKeyFile = "ca-key.pem"
	// leafValidity stays under Apple's 398-day limit for server certificates so iOS accepts the leaves.
	leafValidity = 365 * 24 * time.Hour
	caValidity   = 10 * 365 * 24 * time.Hour
)

// CA is the root that signs one leaf per host on demand; the same leaf key serves every host.
type CA struct {
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
	certPEM []byte
	path    string
	leafKey *ecdsa.PrivateKey
	mu      sync.Mutex
	leaves  map[string]*tls.Certificate
}

// LoadOrCreateCA reads dir/ca.pem and dir/ca-key.pem, creating both when they are missing.
// The key is written 0600: whoever holds it can impersonate any site to a device that trusts the CA.
func LoadOrCreateCA(dir string) (*CA, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	certPath, keyPath := filepath.Join(dir, caFile), filepath.Join(dir, caKeyFile)
	certPEM, certErr := os.ReadFile(certPath)
	keyPEM, keyErr := os.ReadFile(keyPath)
	switch {
	case certErr == nil && keyErr == nil:
		if err := checkKeyPermissions(keyPath); err != nil {
			return nil, err
		}
		ca, err := parseCA(certPEM, keyPEM)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", dir, err)
		}
		ca.path = certPath
		return ca, ca.initLeafKey()
	case errors.Is(certErr, os.ErrNotExist) && errors.Is(keyErr, os.ErrNotExist):
	case certErr != nil:
		return nil, certErr
	default:
		return nil, keyErr
	}
	ca, err := newCA()
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(ca.key)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(certPath, ca.certPEM, 0o644); err != nil {
		return nil, err
	}
	ca.path = certPath
	return ca, ca.initLeafKey()
}

// checkKeyPermissions refuses a private key others can read. Whoever holds it can impersonate any
// site to every device that trusts this CA, so a widened mode (a restored backup, a copied cache)
// has to stop the capture rather than be used anyway.
func checkKeyPermissions(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return fmt.Errorf("%s is readable by other users (mode %04o); "+
			"chmod 600 it or delete it and sims will make a new one", path, mode)
	}
	return nil
}

func newCA() (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "sims proxy CA", Organization: []string{"sims"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(caValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
		SubjectKeyId:          keyID(&key.PublicKey),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &CA{cert: cert, key: key, certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}, nil
}

func parseCA(certPEM, keyPEM []byte) (*CA, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("ca.pem holds no certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	block, _ = pem.Decode(keyPEM)
	if block == nil || block.Type != "EC PRIVATE KEY" {
		return nil, errors.New("ca-key.pem holds no EC private key")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	if !key.PublicKey.Equal(cert.PublicKey) {
		return nil, errors.New("ca-key.pem does not match ca.pem")
	}
	return &CA{cert: cert, key: key, certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})}, nil
}

func (c *CA) initLeafKey() error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	c.leafKey = key
	c.leaves = map[string]*tls.Certificate{}
	return nil
}

// CertPEM is the root certificate a device must trust.
func (c *CA) CertPEM() []byte { return c.certPEM }

// CertDER is the same certificate in the form an iOS configuration profile carries.
func (c *CA) CertDER() []byte { return c.cert.Raw }

// Path is where the certificate lives on disk; empty for a CA that was not loaded from a directory.
func (c *CA) Path() string { return c.path }

// Fingerprint is the SHA-256 of the certificate, hex, as trust UIs show it.
func (c *CA) Fingerprint() string {
	sum := sha256.Sum256(c.cert.Raw)
	return hex.EncodeToString(sum[:])
}

// NotAfter is when the root itself expires.
func (c *CA) NotAfter() time.Time { return c.cert.NotAfter }

// SignCMS wraps data in a CMS/PKCS#7 signature made with this CA. An iOS configuration profile has
// to arrive signed: devicectl reads an unsigned one as a provisioning profile and refuses it with
// "CMS/PKCS#7 envelope is invalid".
func (c *CA) SignCMS(data []byte) ([]byte, error) {
	signed, err := pkcs7.NewSignedData(data)
	if err != nil {
		return nil, err
	}
	signed.SetDigestAlgorithm(pkcs7.OIDDigestAlgorithmSHA256)
	if err := signed.AddSigner(c.cert, c.key, pkcs7.SignerInfoConfig{}); err != nil {
		return nil, err
	}
	return signed.Finish()
}

// Leaf returns a server certificate for host (a DNS name or an IP), signing one the first time and
// again once a cached one is within a day of expiring.
func (c *CA) Leaf(host string) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if leaf, ok := c.leaves[host]; ok && time.Until(leaf.Leaf.NotAfter) > 24*time.Hour {
		return leaf, nil
	}
	leaf, err := c.sign(host)
	if err != nil {
		return nil, err
	}
	c.leaves[host] = leaf
	return leaf, nil
}

func (c *CA) sign(host string) (*tls.Certificate, error) {
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:   serial,
		Subject:        pkix.Name{CommonName: host, Organization: []string{"sims proxy"}},
		NotBefore:      now.Add(-time.Hour),
		NotAfter:       now.Add(leafValidity),
		KeyUsage:       x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:    []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		AuthorityKeyId: c.cert.SubjectKeyId,
		SubjectKeyId:   keyID(&c.leafKey.PublicKey),
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &c.leafKey.PublicKey, c.key)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{Certificate: [][]byte{der, c.cert.Raw}, PrivateKey: c.leafKey, Leaf: leaf}, nil
}

func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 127)
	return rand.Int(rand.Reader, limit)
}

func keyID(pub *ecdsa.PublicKey) []byte {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil
	}
	sum := sha256.Sum256(der)
	return sum[:20]
}
