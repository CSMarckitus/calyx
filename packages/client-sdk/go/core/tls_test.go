package core

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/csmarckitus/calyx/packages/client-sdk/go/calyx"
	"github.com/csmarckitus/calyx/packages/shared-types/go/corev1"
)

// testCA is a certificate authority made at test time.
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "calyx test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &testCA{cert: cert, key: key, pool: pool}
}

// issue makes a leaf certificate that the CA signs. serial must be unique for the CA.
func (ca *testCA) issue(t *testing.T, serial int64, usage x509.ExtKeyUsage) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "calyx test leaf"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// newTLSFakeCore starts a fake core with TLS. When clientCAs is not nil, it requires and
// verifies a client certificate.
func newTLSFakeCore(t *testing.T, serverCA *testCA, clientCAs *x509.CertPool) *fakeCore {
	t.Helper()
	cfg := &tls.Config{Certificates: []tls.Certificate{serverCA.issue(t, 2, x509.ExtKeyUsageServerAuth)}}
	if clientCAs != nil {
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
		cfg.ClientCAs = clientCAs
	}
	return newFakeCoreWith(t, grpc.Creds(credentials.NewTLS(cfg)))
}

func tlsClient(t *testing.T, address string, cfg *tls.Config) *Client {
	t.Helper()
	c, err := New(Config{Address: address, Token: testToken, Security: TLS(cfg)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func listOnce(c *Client) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := c.ListInstances(ctx, &corev1.ListInstancesRequest{})
	return err
}

func TestTLSWithCA(t *testing.T) {
	ca := newTestCA(t)
	f := newTLSFakeCore(t, ca, nil)
	// A DNS name checks the DNS SAN of the certificate.
	_, port, err := net.SplitHostPort(f.addr)
	if err != nil {
		t.Fatal(err)
	}
	c := tlsClient(t, "localhost:"+port, &tls.Config{RootCAs: ca.pool})
	if err := listOnce(c); err != nil {
		t.Fatal(err)
	}
	if n := len(f.recorded()); n != 1 {
		t.Fatalf("the fake core saw %d calls, want 1", n)
	}
}

func TestTLSIPAddress(t *testing.T) {
	ca := newTestCA(t)
	f := newTLSFakeCore(t, ca, nil)
	if !strings.HasPrefix(f.addr, "127.0.0.1:") {
		t.Fatalf("address %q is not an IP address", f.addr)
	}
	c := tlsClient(t, f.addr, &tls.Config{RootCAs: ca.pool})
	if err := listOnce(c); err != nil {
		t.Fatalf("an IP address with TLS failed: %v", err)
	}
}

func TestTLSMutual(t *testing.T) {
	ca := newTestCA(t)
	f := newTLSFakeCore(t, ca, ca.pool)
	clientCert := ca.issue(t, 3, x509.ExtKeyUsageClientAuth)
	c := tlsClient(t, f.addr, &tls.Config{RootCAs: ca.pool, Certificates: []tls.Certificate{clientCert}})
	if err := listOnce(c); err != nil {
		t.Fatal(err)
	}
	if n := len(f.recorded()); n != 1 {
		t.Fatalf("the fake core saw %d calls, want 1", n)
	}
}

func TestTLSMutualWithoutClientCertificateFails(t *testing.T) {
	ca := newTestCA(t)
	f := newTLSFakeCore(t, ca, ca.pool)
	c := tlsClient(t, f.addr, &tls.Config{RootCAs: ca.pool})
	if err := listOnce(c); err == nil {
		t.Fatal("a call without a client certificate worked")
	}
	if n := len(f.recorded()); n != 0 {
		t.Fatalf("the fake core saw %d calls, want 0", n)
	}
}

func TestTLSWrongCA(t *testing.T) {
	ca := newTestCA(t)
	f := newTLSFakeCore(t, ca, nil)
	other := newTestCA(t)
	c := tlsClient(t, f.addr, &tls.Config{RootCAs: other.pool})
	err := listOnce(c)
	var ce *calyx.Error
	if !errors.As(err, &ce) {
		t.Fatalf("error %v is not a *calyx.Error", err)
	}
	if ce.Source != calyx.SourceCore || ce.Code != calyx.CodeGRPCUnavailable {
		t.Errorf("got source %q code %q, want core and %q", ce.Source, ce.Code, calyx.CodeGRPCUnavailable)
	}
	if strings.Contains(err.Error(), testToken) {
		t.Error("the error text has the core token")
	}
	if n := len(f.recorded()); n != 0 {
		t.Fatalf("the fake core saw %d calls, want 0", n)
	}
}
