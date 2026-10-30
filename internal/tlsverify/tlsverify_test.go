package tlsverify

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	der  []byte
}

func newCA(t *testing.T, parent *testCA, commonName string) *testCA {
	t.Helper()
	if parent == nil {
		parent = &testCA{}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          serial(t),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(48 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	issuer, issuerKey := template, key
	if parent.cert != nil {
		issuer, issuerKey = parent.cert, parent.key
	}
	der, err := x509.CreateCertificate(rand.Reader, template, issuer, &key.PublicKey, issuerKey)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return &testCA{cert: certificate, key: key, der: der}
}

type leafOptions struct {
	dnsNames  []string
	ipNames   []net.IP
	aiaURLs   []string
	notBefore time.Time
	notAfter  time.Time
}

func loopback() []net.IP { return []net.IP{net.ParseIP("127.0.0.1")} }

func newLeaf(t *testing.T, issuer *testCA, options leafOptions) tls.Certificate {
	t.Helper()
	notBefore := options.notBefore
	if notBefore.IsZero() {
		notBefore = time.Now().Add(-time.Hour)
	}
	notAfter := options.notAfter
	if notAfter.IsZero() {
		notAfter = time.Now().Add(48 * time.Hour)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          serial(t),
		Subject:               pkix.Name{CommonName: "leaf"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:              options.dnsNames,
		IPAddresses:           options.ipNames,
		IssuingCertificateURL: options.aiaURLs,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, issuer.cert, &key.PublicKey, issuer.key)
	if err != nil {
		t.Fatalf("create leaf: %v", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: certificate}
}

func serial(t *testing.T) *big.Int {
	t.Helper()
	seed := make([]byte, 8)
	if _, err := rand.Read(seed); err != nil {
		t.Fatalf("read random: %v", err)
	}
	digest := sha256.Sum256(seed)
	return new(big.Int).SetBytes(digest[:])
}

func encode(certificate []byte, pemEncode bool) []byte {
	if !pemEncode {
		return certificate
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate})
}

// issuerServer serves issuerDER on demand and counts how often it was asked.
func issuerServer(t *testing.T, issuerDER []byte, pemEncode bool) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	body := encode(issuerDER, pemEncode)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = writer.Write(body)
	}))
	t.Cleanup(server.Close)
	return server, &hits
}

// chainServer serves HTTPS presenting exactly the certificates it is given.
func chainServer(t *testing.T, certificates [][]byte, key *ecdsa.PrivateKey) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("ok"))
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: certificates, PrivateKey: key}}}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func poolWith(certificates ...*x509.Certificate) *x509.CertPool {
	pool := x509.NewCertPool()
	for _, certificate := range certificates {
		pool.AddCert(certificate)
	}
	return pool
}

func trustOnly(t *testing.T, certificates ...*x509.Certificate) {
	t.Helper()
	pool := poolWith(certificates...)
	previous := systemRoots
	systemRoots = func() (*x509.CertPool, error) { return pool, nil }
	t.Cleanup(func() { systemRoots = previous })
}

// testClient builds the client the application builds: a wrapped transport.
func testClient() *http.Client {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.TLSClientConfig = nil
	base.Proxy = nil
	return &http.Client{Timeout: 10 * time.Second, Transport: Wrap(base)}
}

// stdlibClient performs the same checks with stock crypto/tls.
func stdlibClient(pool *x509.CertPool) *http.Client {
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
	}
}

func fetch(t *testing.T, client *http.Client, target string) error {
	t.Helper()
	response, err := client.Get(target)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	return nil
}

func TestVerifyRepairsMissingIntermediateFromAIA(t *testing.T) {
	root := newCA(t, nil, "Test Root")
	intermediate := newCA(t, root, "Test Intermediate")
	issuers, hits := issuerServer(t, intermediate.der, false)
	leaf := newLeaf(t, intermediate, leafOptions{ipNames: loopback(), aiaURLs: []string{issuers.URL}})
	server := chainServer(t, [][]byte{leaf.Certificate[0]}, leaf.PrivateKey.(*ecdsa.PrivateKey))
	trustOnly(t, root.cert)

	client := testClient()
	if err := fetch(t, client, server.URL); err != nil {
		t.Fatalf("first request: %v", err)
	}
	if err := fetch(t, client, server.URL); err != nil {
		t.Fatalf("second request: %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("issuer was fetched %d times, want 1 (cached)", got)
	}
}

func TestVerifyRepairsFromPEMEncodedIssuer(t *testing.T) {
	root := newCA(t, nil, "Test Root")
	intermediate := newCA(t, root, "Test Intermediate")
	issuers, _ := issuerServer(t, intermediate.der, true)
	leaf := newLeaf(t, intermediate, leafOptions{ipNames: loopback(), aiaURLs: []string{issuers.URL}})
	server := chainServer(t, [][]byte{leaf.Certificate[0]}, leaf.PrivateKey.(*ecdsa.PrivateKey))
	trustOnly(t, root.cert)

	if err := fetch(t, testClient(), server.URL); err != nil {
		t.Fatalf("request with PEM caIssuers: %v", err)
	}
}

func TestVerifyAcceptsCompleteChainWithoutFetching(t *testing.T) {
	root := newCA(t, nil, "Test Root")
	intermediate := newCA(t, root, "Test Intermediate")
	issuers, hits := issuerServer(t, intermediate.der, false)
	leaf := newLeaf(t, intermediate, leafOptions{ipNames: loopback(), aiaURLs: []string{issuers.URL}})
	server := chainServer(t, [][]byte{leaf.Certificate[0], intermediate.der}, leaf.PrivateKey.(*ecdsa.PrivateKey))
	trustOnly(t, root.cert)

	if err := fetch(t, testClient(), server.URL); err != nil {
		t.Fatalf("request with complete chain: %v", err)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("caIssuers was fetched %d times for a complete chain, want 0", got)
	}
}

func TestVerifyRejectsChainThatStillMissesARoot(t *testing.T) {
	trusted := newCA(t, nil, "Trusted Root")
	other := newCA(t, nil, "Untrusted Root")
	intermediate := newCA(t, other, "Untrusted Intermediate")
	issuers, _ := issuerServer(t, intermediate.der, false)
	leaf := newLeaf(t, intermediate, leafOptions{ipNames: loopback(), aiaURLs: []string{issuers.URL}})
	server := chainServer(t, [][]byte{leaf.Certificate[0]}, leaf.PrivateKey.(*ecdsa.PrivateKey))
	trustOnly(t, trusted.cert)

	err := fetch(t, testClient(), server.URL)
	if err == nil {
		t.Fatal("expected failure: a fetched intermediate must not act as a trust anchor")
	}
	if !strings.Contains(err.Error(), "unknown authority") {
		t.Fatalf("error %v, want unknown authority", err)
	}
}

func TestVerifyIgnoresUnrelatedCertificateFromAIA(t *testing.T) {
	root := newCA(t, nil, "Test Root")
	intermediate := newCA(t, root, "Test Intermediate")
	unrelated := newCA(t, nil, "Unrelated CA")
	issuers, _ := issuerServer(t, unrelated.der, false)
	leaf := newLeaf(t, intermediate, leafOptions{ipNames: loopback(), aiaURLs: []string{issuers.URL}})
	server := chainServer(t, [][]byte{leaf.Certificate[0]}, leaf.PrivateKey.(*ecdsa.PrivateKey))
	trustOnly(t, root.cert)

	err := fetch(t, testClient(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "unknown authority") {
		t.Fatalf("error %v, want unknown authority", err)
	}
}

func TestVerifySurvivesUnreachableIssuer(t *testing.T) {
	root := newCA(t, nil, "Test Root")
	intermediate := newCA(t, root, "Test Intermediate")
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := dead.URL
	dead.Close()
	leaf := newLeaf(t, intermediate, leafOptions{ipNames: loopback(), aiaURLs: []string{deadURL}})
	server := chainServer(t, [][]byte{leaf.Certificate[0]}, leaf.PrivateKey.(*ecdsa.PrivateKey))
	trustOnly(t, root.cert)

	err := fetch(t, testClient(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "unknown authority") {
		t.Fatalf("error %v, want the original unknown authority failure", err)
	}
}

func TestVerifyFetchCanBeDisabled(t *testing.T) {
	root := newCA(t, nil, "Test Root")
	intermediate := newCA(t, root, "Test Intermediate")
	issuers, hits := issuerServer(t, intermediate.der, false)
	leaf := newLeaf(t, intermediate, leafOptions{ipNames: loopback(), aiaURLs: []string{issuers.URL}})
	server := chainServer(t, [][]byte{leaf.Certificate[0]}, leaf.PrivateKey.(*ecdsa.PrivateKey))
	trustOnly(t, root.cert)

	previous := aiaFetchEnabled.Load()
	aiaFetchEnabled.Store(false)
	t.Cleanup(func() { aiaFetchEnabled.Store(previous) })

	err := fetch(t, testClient(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "unknown authority") {
		t.Fatalf("error %v, want unknown authority while the repair is disabled", err)
	}
	if got := hits.Load(); got != 0 {
		t.Fatalf("caIssuers was fetched %d times while disabled, want 0", got)
	}
}

func TestVerifyIgnoresNonHTTPScheme(t *testing.T) {
	root := newCA(t, nil, "Test Root")
	intermediate := newCA(t, root, "Test Intermediate")
	leaf := newLeaf(t, intermediate, leafOptions{ipNames: loopback(), aiaURLs: []string{"ldap://example.invalid/cn=CA"}})
	server := chainServer(t, [][]byte{leaf.Certificate[0]}, leaf.PrivateKey.(*ecdsa.PrivateKey))
	trustOnly(t, root.cert)

	err := fetch(t, testClient(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "unknown authority") {
		t.Fatalf("error %v, want unknown authority", err)
	}
}

func TestParseCertificatesAcceptsDERAndPEM(t *testing.T) {
	root := newCA(t, nil, "Test Root")
	if got := parseCertificates(root.der); len(got) != 1 {
		t.Fatalf("DER: parsed %d certificates, want 1", len(got))
	}
	if got := parseCertificates(encode(root.der, true)); len(got) != 1 {
		t.Fatalf("PEM: parsed %d certificates, want 1", len(got))
	}
	if got := parseCertificates([]byte("not a certificate")); len(got) != 0 {
		t.Fatalf("garbage: parsed %d certificates, want 0", len(got))
	}
}

func TestWrapReusesOneTransportPerHost(t *testing.T) {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.TLSClientConfig = nil
	wrapped := Wrap(base).(*wrapTransport)
	if wrapped.transportFor("a.example") != wrapped.transportFor("a.example") {
		t.Fatal("the same host should reuse one transport")
	}
	if wrapped.transportFor("a.example") == wrapped.transportFor("b.example") {
		t.Fatal("different hosts must not share a pinned hostname")
	}
	if wrapped.transportFor("a.example").TLSClientConfig.ServerName != "a.example" {
		t.Fatal("transport is not pinned to its own host")
	}
}

// TestVerificationIsNeverWeakerThanStdlib pins the security property: for every
// scenario except the missing intermediate, the wrapped client fails exactly
// where crypto/tls fails.
func TestVerificationIsNeverWeakerThanStdlib(t *testing.T) {
	subtests := []struct {
		name           string
		wantStdlibFail bool
		wantOursFail   bool
		disableAIA     bool
		target         func(serverURL string) string
		build          func(t *testing.T) (*httptest.Server, *x509.CertPool, *testCA)
	}{
		{
			name:  "complete chain",
			build: completeChain,
		},
		{
			name:           "expired leaf",
			wantStdlibFail: true,
			wantOursFail:   true,
			build: func(t *testing.T) (*httptest.Server, *x509.CertPool, *testCA) {
				root := newCA(t, nil, "Test Root")
				intermediate := newCA(t, root, "Test Intermediate")
				leaf := newLeaf(t, intermediate, leafOptions{
					ipNames:   loopback(),
					notBefore: time.Now().Add(-48 * time.Hour),
					notAfter:  time.Now().Add(-24 * time.Hour),
				})
				server := chainServer(t, [][]byte{leaf.Certificate[0], intermediate.der}, leaf.PrivateKey.(*ecdsa.PrivateKey))
				return server, poolWith(root.cert), root
			},
		},
		{
			name:           "untrusted root",
			wantStdlibFail: true,
			wantOursFail:   true,
			build: func(t *testing.T) (*httptest.Server, *x509.CertPool, *testCA) {
				trusted := newCA(t, nil, "Trusted Root")
				other := newCA(t, nil, "Other Root")
				intermediate := newCA(t, other, "Other Intermediate")
				leaf := newLeaf(t, intermediate, leafOptions{ipNames: loopback()})
				server := chainServer(t, [][]byte{leaf.Certificate[0], intermediate.der}, leaf.PrivateKey.(*ecdsa.PrivateKey))
				return server, poolWith(trusted.cert), trusted
			},
		},
		{
			name:           "hostname mismatch",
			wantStdlibFail: true,
			wantOursFail:   true,
			target:         func(serverURL string) string { return strings.Replace(serverURL, "127.0.0.1", "localhost", 1) },
			build: func(t *testing.T) (*httptest.Server, *x509.CertPool, *testCA) {
				root := newCA(t, nil, "Test Root")
				intermediate := newCA(t, root, "Test Intermediate")
				leaf := newLeaf(t, intermediate, leafOptions{dnsNames: []string{"somewhere.example"}})
				server := chainServer(t, [][]byte{leaf.Certificate[0], intermediate.der}, leaf.PrivateKey.(*ecdsa.PrivateKey))
				return server, poolWith(root.cert), root
			},
		},
		{
			name:           "IP address mismatch",
			wantStdlibFail: true,
			wantOursFail:   true,
			build: func(t *testing.T) (*httptest.Server, *x509.CertPool, *testCA) {
				root := newCA(t, nil, "Test Root")
				intermediate := newCA(t, root, "Test Intermediate")
				leaf := newLeaf(t, intermediate, leafOptions{dnsNames: []string{"somewhere.example"}})
				server := chainServer(t, [][]byte{leaf.Certificate[0], intermediate.der}, leaf.PrivateKey.(*ecdsa.PrivateKey))
				return server, poolWith(root.cert), root
			},
		},
		{
			name:           "missing intermediate is repaired",
			wantOursFail:   false,
			wantStdlibFail: true,
			build: func(t *testing.T) (*httptest.Server, *x509.CertPool, *testCA) {
				root := newCA(t, nil, "Test Root")
				intermediate := newCA(t, root, "Test Intermediate")
				issuers, _ := issuerServer(t, intermediate.der, false)
				leaf := newLeaf(t, intermediate, leafOptions{ipNames: loopback(), aiaURLs: []string{issuers.URL}})
				server := chainServer(t, [][]byte{leaf.Certificate[0]}, leaf.PrivateKey.(*ecdsa.PrivateKey))
				return server, poolWith(root.cert), root
			},
		},
		{
			name:           "missing intermediate with the repair disabled",
			wantStdlibFail: true,
			wantOursFail:   true,
			disableAIA:     true,
			build: func(t *testing.T) (*httptest.Server, *x509.CertPool, *testCA) {
				root := newCA(t, nil, "Test Root")
				intermediate := newCA(t, root, "Test Intermediate")
				issuers, _ := issuerServer(t, intermediate.der, false)
				leaf := newLeaf(t, intermediate, leafOptions{ipNames: loopback(), aiaURLs: []string{issuers.URL}})
				server := chainServer(t, [][]byte{leaf.Certificate[0]}, leaf.PrivateKey.(*ecdsa.PrivateKey))
				return server, poolWith(root.cert), root
			},
		},
	}

	for _, subtest := range subtests {
		t.Run(subtest.name, func(t *testing.T) {
			server, pool, root := subtest.build(t)
			trustOnly(t, root.cert)
			if subtest.disableAIA {
				previous := aiaFetchEnabled.Load()
				aiaFetchEnabled.Store(false)
				t.Cleanup(func() { aiaFetchEnabled.Store(previous) })
			}
			target := server.URL
			if subtest.target != nil {
				target = subtest.target(server.URL)
			}

			stdlibFailed := fetch(t, stdlibClient(pool), target) != nil
			oursFailed := fetch(t, testClient(), target) != nil

			if stdlibFailed != subtest.wantStdlibFail {
				t.Fatalf("crypto/tls failed=%v, want %v", stdlibFailed, subtest.wantStdlibFail)
			}
			if oursFailed != subtest.wantOursFail {
				t.Fatalf("wrapped client failed=%v, want %v", oursFailed, subtest.wantOursFail)
			}
			if oursFailed && !stdlibFailed {
				t.Fatal("verification is weaker than crypto/tls")
			}
		})
	}
}

func completeChain(t *testing.T) (*httptest.Server, *x509.CertPool, *testCA) {
	root := newCA(t, nil, "Test Root")
	intermediate := newCA(t, root, "Test Intermediate")
	leaf := newLeaf(t, intermediate, leafOptions{ipNames: loopback()})
	server := chainServer(t, [][]byte{leaf.Certificate[0], intermediate.der}, leaf.PrivateKey.(*ecdsa.PrivateKey))
	return server, poolWith(root.cert), root
}
