// Package tlsverify builds the TLS client configuration used for endpoint
// pings, test requests and notification calls.
//
// It verifies what crypto/tls verifies by default - a chain that reaches a
// certificate from the system pool, the hostname (or IP address), the validity
// window and the server-auth extended key usage - with one repair. When a
// server sends an incomplete chain (the leaf certificate without its
// intermediate), the missing issuer is fetched from the certificate's Authority
// Information Access (caIssuers) URL, which is what every browser does. Go's
// crypto/x509 does not do this on its own, so an otherwise valid endpoint
// otherwise fails with "x509: certificate signed by unknown authority".
//
// Fetched certificates are only ever added as intermediates: the chain still
// has to reach a certificate from the system pool, so this never introduces a
// new trust anchor.
package tlsverify

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// aiaFetchEnv turns the repair off when it is set to a falsey value.
const aiaFetchEnv = "TLS_AIA_FETCH"

const (
	// maxAIAHops bounds how far the chain is followed through caIssuers URLs.
	maxAIAHops = 4
	// maxAIAURLsPerCert bounds how many caIssuers URLs are tried per certificate.
	maxAIAURLsPerCert = 3
	// maxAIABytes bounds a single caIssuers response body.
	maxAIABytes = 64 << 10
	// maxAIACacheEntries bounds the process-wide issuer cache.
	maxAIACacheEntries = 256
	// maxHostTransports bounds how many per-host transports Wrap keeps.
	maxHostTransports = 128
	// aiaTimeout bounds a single caIssuers request.
	aiaTimeout = 3 * time.Second
	// issuerFreshnessGrace tolerates a little clock skew on fetched issuers.
	issuerFreshnessGrace = 5 * time.Minute
)

// systemRoots is swapped in tests; production always uses the system pool.
var systemRoots = x509.SystemCertPool

var (
	aiaFetchEnabled atomic.Bool
	issuerCache     = struct {
		mu      sync.Mutex
		entries map[string]*x509.Certificate
		order   []string
	}{entries: map[string]*x509.Certificate{}}
)

func init() {
	aiaFetchEnabled.Store(envFlagDefault(aiaFetchEnv, true))
}

// TLSConfig returns a TLS client configuration for connections to serverName.
//
// serverName is the name to check the certificate against, exactly as
// tls.Config.ServerName would be: crypto/tls derives ConnectionState.ServerName
// from the SNI extension, which is empty when the peer is dialled by IP
// address, so it cannot be recovered from the connection state.
//
// Verification runs in VerifyConnection rather than inside the handshake,
// because repairing an incomplete chain needs the certificates the peer sent.
func TLSConfig(serverName string) *tls.Config {
	return &tls.Config{
		ServerName: serverName,
		// The handshake cannot verify a chain that is missing its
		// intermediate, so verification is performed by Verify.
		InsecureSkipVerify: true,
		VerifyConnection: func(connection tls.ConnectionState) error {
			return Verify(connection, serverName)
		},
	}
}

// Verify runs the checks crypto/tls would have run, plus the missing-intermediate
// repair. It returns nil only when the chain reaches a system root, the
// certificate matches serverName and every certificate is inside its validity
// window.
func Verify(connection tls.ConnectionState, serverName string) error {
	if len(connection.PeerCertificates) == 0 {
		return errors.New("tls: server sent no certificate chain")
	}

	roots, err := systemRoots()
	if err != nil {
		return fmt.Errorf("tls: system certificate pool unavailable: %w", err)
	}
	if roots == nil {
		return errors.New("tls: system certificate pool unavailable")
	}

	intermediates := x509.NewCertPool()
	for _, certificate := range connection.PeerCertificates[1:] {
		intermediates.AddCert(certificate)
	}
	leaf := connection.PeerCertificates[0]

	options := x509.VerifyOptions{
		DNSName:       serverName,
		Roots:         roots,
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	chainErr := verifyChain(leaf, options)
	if chainErr == nil {
		return nil
	}
	if !aiaFetchEnabled.Load() || !isUnknownAuthority(chainErr) {
		return chainErr
	}

	// Walk up the chain the peer did send, pulling in whatever issuer is
	// missing, and re-verify after every addition.
	current := connection.PeerCertificates[len(connection.PeerCertificates)-1]
	for hop := 0; hop < maxAIAHops; hop++ {
		issuer, err := fetchIssuer(current)
		if err != nil {
			return chainErr
		}
		intermediates.AddCert(issuer)
		if verifyChain(leaf, options) == nil {
			return nil
		}
		current = issuer
	}
	return chainErr
}

// Wrap returns a RoundTripper that behaves like base, except that every host
// gets its own TLS configuration pinned to that host. Redirects to another host
// therefore keep being checked against the host they actually reach.
func Wrap(base *http.Transport) http.RoundTripper {
	return &wrapTransport{base: base, hosts: make(map[string]*http.Transport)}
}

type wrapTransport struct {
	base  *http.Transport
	mu    sync.Mutex
	hosts map[string]*http.Transport
	order []string
}

func (w *wrapTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL == nil || request.URL.Scheme != "https" {
		return w.base.RoundTrip(request)
	}
	host := request.URL.Hostname()
	if host == "" {
		return w.base.RoundTrip(request)
	}
	return w.transportFor(host).RoundTrip(request)
}

func (w *wrapTransport) transportFor(host string) *http.Transport {
	w.mu.Lock()
	defer w.mu.Unlock()
	if transport, ok := w.hosts[host]; ok {
		return transport
	}
	transport := w.base.Clone()
	transport.TLSClientConfig = TLSConfig(host)
	for len(w.order) >= maxHostTransports {
		evicted := w.order[0]
		w.order = w.order[1:]
		if stale, ok := w.hosts[evicted]; ok {
			stale.CloseIdleConnections()
			delete(w.hosts, evicted)
		}
	}
	w.hosts[host] = transport
	w.order = append(w.order, host)
	return transport
}

func verifyChain(leaf *x509.Certificate, options x509.VerifyOptions) error {
	_, err := leaf.Verify(options)
	return err
}

func isUnknownAuthority(err error) bool {
	var unknownAuthority x509.UnknownAuthorityError
	return errors.As(err, &unknownAuthority)
}

func fetchIssuer(child *x509.Certificate) (*x509.Certificate, error) {
	var lastErr error
	for index, rawURL := range child.IssuingCertificateURL {
		if index >= maxAIAURLsPerCert {
			break
		}
		if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
			lastErr = fmt.Errorf("tls: unsupported caIssuers URL %.64s", rawURL)
			continue
		}
		certificate, err := issuerFromURL(rawURL, child)
		if err != nil {
			lastErr = err
			continue
		}
		return certificate, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("tls: no usable caIssuers URL on %q", child.Subject.String())
	}
	return nil, lastErr
}

func issuerFromURL(rawURL string, child *x509.Certificate) (*x509.Certificate, error) {
	if cached := cachedIssuer(rawURL); cached != nil && isIssuerOf(cached, child) {
		return cached, nil
	}

	data, err := download(rawURL)
	if err != nil {
		return nil, err
	}
	for _, candidate := range parseCertificates(data) {
		if !isIssuerOf(candidate, child) {
			continue
		}
		cacheIssuer(rawURL, candidate)
		return candidate, nil
	}
	return nil, fmt.Errorf("tls: %.64s did not return the issuer of %q", rawURL, child.Subject.String())
}

func download(rawURL string) ([]byte, error) {
	client := &http.Client{
		Timeout: aiaTimeout,
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("tls: too many redirects fetching issuer")
			}
			return nil
		},
	}
	request, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "w8nc-tls-verify")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close() //nolint:errcheck
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tls: caIssuers %.64s returned %s", rawURL, response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxAIABytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxAIABytes {
		return nil, fmt.Errorf("tls: caIssuers %.64s response is too large", rawURL)
	}
	return data, nil
}

// isIssuerOf reports whether certificate really issued child: the names match,
// the child's signature verifies under the issuer's key, and the issuer is
// inside its validity window. A caIssuers endpoint that returns anything else is
// ignored instead of trusted.
func isIssuerOf(certificate, child *x509.Certificate) bool {
	if !bytes.Equal(certificate.RawSubject, child.RawIssuer) {
		return false
	}
	if err := child.CheckSignatureFrom(certificate); err != nil {
		return false
	}
	now := time.Now()
	if now.Before(certificate.NotBefore.Add(-issuerFreshnessGrace)) {
		return false
	}
	return !now.After(certificate.NotAfter.Add(issuerFreshnessGrace))
}

// parseCertificates accepts PEM or DER, because caIssuers endpoints serve both.
func parseCertificates(data []byte) []*x509.Certificate {
	var certificates []*x509.Certificate
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		if certificate, err := x509.ParseCertificate(block.Bytes); err == nil {
			certificates = append(certificates, certificate)
		}
	}
	if len(certificates) == 0 {
		if certificate, err := x509.ParseCertificate(data); err == nil {
			certificates = append(certificates, certificate)
		}
	}
	return certificates
}

func cachedIssuer(rawURL string) *x509.Certificate {
	issuerCache.mu.Lock()
	defer issuerCache.mu.Unlock()
	return issuerCache.entries[rawURL]
}

func cacheIssuer(rawURL string, certificate *x509.Certificate) {
	issuerCache.mu.Lock()
	defer issuerCache.mu.Unlock()
	if _, exists := issuerCache.entries[rawURL]; exists {
		return
	}
	for len(issuerCache.order) >= maxAIACacheEntries {
		evicted := issuerCache.order[0]
		issuerCache.order = issuerCache.order[1:]
		delete(issuerCache.entries, evicted)
	}
	issuerCache.entries[rawURL] = certificate
	issuerCache.order = append(issuerCache.order, rawURL)
}

func envFlagDefault(name string, fallback bool) bool {
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
