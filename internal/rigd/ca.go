// Package rigd is the local secret broker (docs/rigd.md): a certificate authority that exists only in memory, surrogate
// values, host patterns, sessions, and the intercepting egress proxy that swaps a surrogate for the real secret only on a
// request to the host the secret is bound to.
package rigd

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"sync"
	"time"
)

// CA is a certificate authority whose private key lives only in this process's memory: nothing is written to disk, so
// there is no CA key to steal, and a new one is made every time the broker starts. Its certificate is given only to
// the child processes the broker serves, through their own environment (docs/rigd.md §2), never to the OS trust store.
type CA struct {
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
	pemCert []byte

	mu     sync.Mutex
	leaves map[string]*tls.Certificate
	now    func() time.Time
}

// LeafValidity is how long a generated certificate for a host is valid.
const LeafValidity = 24 * time.Hour

func serial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
}

// NewCA creates a fresh CA valid for 30 days (it is renewed by restarting the broker, which ends all sessions anyway).
func NewCA(now func() time.Time) (*CA, error) {
	if now == nil {
		now = time.Now
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	sn, err := serial()
	if err != nil {
		return nil, err
	}
	t := now()
	tmpl := &x509.Certificate{
		SerialNumber:          sn,
		Subject:               pkix.Name{CommonName: "Rigfile rigd local CA (per-process, in memory)", Organization: []string{"Rigfile"}},
		NotBefore:             t.Add(-5 * time.Minute),
		NotAfter:              t.Add(30 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &CA{cert: cert, key: key, pemCert: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), leaves: map[string]*tls.Certificate{}, now: now}, nil
}

// PEM is the CA certificate, for a child's CA bundle variables.
func (c *CA) PEM() []byte { return c.pemCert }

// Certificate returns the parsed CA certificate (tests use it to build a trust pool).
func (c *CA) Certificate() *x509.Certificate { return c.cert }

// LeafFor returns a certificate for host (a DNS name or an IP literal) signed by the CA, creating and caching it.
func (c *CA) LeafFor(host string) (*tls.Certificate, error) {
	if host == "" || len(host) > 253 {
		return nil, errors.New("rigd: invalid host for a certificate")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if l, ok := c.leaves[host]; ok && l.Leaf != nil && c.now().Before(l.Leaf.NotAfter.Add(-time.Hour)) {
		return l, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	sn, err := serial()
	if err != nil {
		return nil, err
	}
	t := c.now()
	tmpl := &x509.Certificate{
		SerialNumber: sn,
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    t.Add(-5 * time.Minute),
		NotAfter:     t.Add(LeafValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, c.cert, &key.PublicKey, c.key)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	tc := &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
	if len(c.leaves) > 2000 { // bound the cache
		c.leaves = map[string]*tls.Certificate{}
	}
	c.leaves[host] = tc
	return tc, nil
}
