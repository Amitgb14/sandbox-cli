package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

func TestCheckNodeFlags(t *testing.T) {
	for _, tc := range []struct {
		name string
		o    nodeOptions
		tls  bool
		says string // "" accepts
	}{
		{name: "standalone", o: nodeOptions{}},
		{name: "a node", o: nodeOptions{id: "n17", labels: []string{"region=west", "disk=nvme"}, cpus: 4}},
		{name: "upper case", o: nodeOptions{id: "N17"}, says: "--node-id"},
		// An underscore would make "sbx_a_b_<hex>" split two ways.
		{name: "underscore", o: nodeOptions{id: "a_b"}, says: "--node-id"},
		{name: "leading dash", o: nodeOptions{id: "-n"}, says: "--node-id"},
		{name: "too long", o: nodeOptions{id: strings.Repeat("n", 32)}, says: "--node-id"},
		{name: "label without =", o: nodeOptions{labels: []string{"region"}}, says: "key=value"},
		{name: "label twice", o: nodeOptions{labels: []string{"a=1", "a=2"}}, says: "twice"},
		{name: "bad label key", o: nodeOptions{labels: []string{"Region=west"}}, says: "--node-label"},
		{name: "negative", o: nodeOptions{memoryMB: -1}, says: "negative"},
		{name: "client CA without TLS", o: nodeOptions{clientCA: "ca.pem"}, says: "--tls-cert"},
		{name: "client CA with TLS", o: nodeOptions{clientCA: "ca.pem"}, tls: true},
	} {
		_, err := checkNodeFlags(tc.o, tc.tls)
		switch {
		case tc.says == "" && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case tc.says != "" && (err == nil || !strings.Contains(err.Error(), tc.says)):
			t.Errorf("%s: got %v, want a refusal naming %q", tc.name, err, tc.says)
		}
	}
}

// The flags are checked before a backend is made or a port opened.
func TestRunRefusesABadNodeID(t *testing.T) {
	err := run([]string{"--backend", "fake", "--node-id", "Bad_ID", "--listen", "unix://" + filepath.Join(t.TempDir(), "s.sock")})
	if err == nil || !strings.Contains(err.Error(), "--node-id") {
		t.Fatalf("got %v", err)
	}
	err = run([]string{"--backend", "fake", "--client-ca", "ca.pem", "--listen", "unix://" + filepath.Join(t.TempDir(), "s.sock")})
	if err == nil || !strings.Contains(err.Error(), "--client-ca") {
		t.Fatalf("--client-ca without TLS: %v", err)
	}
	// Metrics have no credential: loopback only.
	err = run([]string{"--backend", "fake", "--metrics-listen", "0.0.0.0:9100", "--listen", "unix://" + filepath.Join(t.TempDir(), "s.sock")})
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("--metrics-listen off loopback: %v", err)
	}
}

func TestMemTotalMB(t *testing.T) {
	dir := t.TempDir()
	write := func(s string) string {
		p := filepath.Join(dir, "meminfo")
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if got := memTotalMB(write("MemFree:  1 kB\nMemTotal:       16384000 kB\n")); got != 16000 {
		t.Errorf("got %d", got)
	}
	if got := memTotalMB(write("MemTotal: lots\n")); got != 0 {
		t.Errorf("garbage: %d", got)
	}
	if got := memTotalMB(filepath.Join(dir, "absent")); got != 0 {
		t.Errorf("absent: %d", got)
	}
}

func TestCapacityFlagsWinOverDetection(t *testing.T) {
	c := capacity(nodeOptions{cpus: 2.5, memoryMB: 100, diskMB: 200}, t.TempDir())
	if c != (api.NodeResources{CPUs: 2.5, MemoryMB: 100, DiskMB: 200}) {
		t.Fatalf("%+v", c)
	}
	if c := capacity(nodeOptions{}, t.TempDir()); c.CPUs < 1 {
		t.Fatalf("detected %+v", c)
	}
}

// With --client-ca only a client presenting a certificate that CA signed gets
// as far as a request, and the token is still required of one that does.
func TestClientCARequiresTheGatewaysCertificate(t *testing.T) {
	dir := t.TempDir()
	ca, caKey := newCA(t, "gateway CA")
	rogue, rogueKey := newCA(t, "someone else")
	writePEM(t, filepath.Join(dir, "ca.pem"), "CERTIFICATE", ca.Raw)

	srvCert, srvKey := leaf(t, ca, caKey, "node", x509.ExtKeyUsageServerAuth)
	writePEM(t, filepath.Join(dir, "srv.pem"), "CERTIFICATE", srvCert.Certificate[0])
	keyDER, err := x509.MarshalECPrivateKey(srvKey)
	if err != nil {
		t.Fatal(err)
	}
	writePEM(t, filepath.Join(dir, "srv.key"), "EC PRIVATE KEY", keyDER)

	cfg, err := serverTLS(filepath.Join(dir, "srv.pem"), filepath.Join(dir, "srv.key"), filepath.Join(dir, "ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	be := fake.New()
	h := (&server.Server{Backend: be, Policy: spec.DefaultPolicyFor(be.Capabilities()), Token: "0123456789abcdef-token"}).Handler()
	hs := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go hs.Serve(tls.NewListener(ln, cfg))
	t.Cleanup(func() { hs.Close() })
	url := "https://" + ln.Addr().String() + "/v1/node"

	roots := x509.NewCertPool()
	roots.AddCert(ca)
	get := func(certs []tls.Certificate, token string) (int, error) {
		c := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: roots, Certificates: certs, MinVersion: tls.VersionTLS12}}}
		req, _ := http.NewRequest("GET", url, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := c.Do(req)
		if err != nil {
			return 0, err
		}
		resp.Body.Close()
		return resp.StatusCode, nil
	}

	gw, _ := leaf(t, ca, caKey, "gateway", x509.ExtKeyUsageClientAuth)
	other, _ := leaf(t, rogue, rogueKey, "gateway", x509.ExtKeyUsageClientAuth)
	if _, err := get(nil, "0123456789abcdef-token"); err == nil {
		t.Error("a client with no certificate was answered")
	}
	if _, err := get([]tls.Certificate{other}, "0123456789abcdef-token"); err == nil {
		t.Error("a client with a certificate from another CA was answered")
	}
	if code, err := get([]tls.Certificate{gw}, ""); err != nil || code != http.StatusUnauthorized {
		t.Errorf("the gateway's certificate without the token: %d %v", code, err)
	}
	if code, err := get([]tls.Certificate{gw}, "0123456789abcdef-token"); err != nil || code != http.StatusOK {
		t.Errorf("the gateway's certificate and the token: %d %v", code, err)
	}
}

func newCA(t *testing.T, name string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

func leaf(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, name string, usage x509.ExtKeyUsage) (tls.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, key
}

func writePEM(t *testing.T, path, typ string, der []byte) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}
