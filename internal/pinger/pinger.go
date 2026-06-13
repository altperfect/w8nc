package pinger

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	secretbox "w8nc/internal/crypto"
	"w8nc/internal/models"
	"w8nc/internal/socks5"
	"w8nc/internal/validation"
)

type Pinger struct {
	Client              *http.Client
	Timeout             time.Duration
	MaxResponseBytes    int64
	AllowPrivateTargets bool
	Secrets             *secretbox.SecretBox
}

func New(timeout time.Duration, maxBytes int64, allowPrivate bool, secrets *secretbox.SecretBox) *Pinger {
	dialer := &net.Dialer{Timeout: timeout}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableKeepAlives = true
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if allowPrivate {
			return dialer.DialContext(ctx, network, address)
		}
		_, port, ip, err := publicTargetIP(ctx, address)
		if err != nil {
			return nil, err
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ip, port))
	}
	client := &http.Client{
		Timeout:       timeout,
		Transport:     transport,
		CheckRedirect: redirectPolicy(allowPrivate),
	}
	return &Pinger{
		Client:              client,
		Timeout:             timeout,
		MaxResponseBytes:    maxBytes,
		AllowPrivateTargets: allowPrivate,
		Secrets:             secrets,
	}
}

func (p *Pinger) Ping(ctx context.Context, endpoint models.Endpoint) models.PingResult {
	started := time.Now().UTC()
	attempts := 1
	if retryableMethod(endpoint.HTTPMethod) {
		attempts = 2
	}
	var best models.PingResult
	for attempt := 1; attempt <= attempts; attempt++ {
		result := p.pingOnce(ctx, endpoint, started)
		if result.Error == nil {
			return result
		}
		if attempt == 1 || (best.StatusCode == nil && result.StatusCode != nil) || (best.StatusCode == nil && result.StatusCode == nil) {
			best = result
		}
		if attempt == attempts || !transientPingError(*result.Error) {
			return best
		}
	}
	return best
}

func (p *Pinger) pingOnce(ctx context.Context, endpoint models.Endpoint, started time.Time) models.PingResult {
	result := models.PingResult{StartedAt: started}
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()

	parsed, err := url.Parse(endpoint.URL)
	if err != nil {
		return finishError(result, err)
	}
	if !p.AllowPrivateTargets && validation.IsPrivateHost(parsed.Hostname()) {
		return finishError(result, fmt.Errorf("private target is blocked"))
	}
	if endpoint.Proxy.Enabled {
		if !p.AllowPrivateTargets {
			if err := rejectPrivateTarget(ctx, parsed.Hostname()); err != nil {
				return finishError(result, err)
			}
		}
	}
	var body io.Reader
	if endpoint.RequestBodyEnabled {
		body = strings.NewReader(endpoint.RequestBody)
	}
	req, err := http.NewRequestWithContext(ctx, endpoint.HTTPMethod, endpoint.URL, body)
	if err != nil {
		return finishError(result, err)
	}
	for _, header := range endpoint.Headers {
		value := ""
		if header.Sensitive {
			if header.ValueEncrypted == nil || p.Secrets == nil {
				return finishError(result, fmt.Errorf("sensitive header %q cannot be decrypted", header.Name))
			}
			decrypted, err := p.Secrets.Decrypt(*header.ValueEncrypted)
			if err != nil {
				return finishError(result, fmt.Errorf("sensitive header %q cannot be decrypted", header.Name))
			}
			value = decrypted
		} else if header.ValuePlain != nil {
			value = *header.ValuePlain
		}
		req.Header.Set(header.Name, value)
	}

	client, err := p.clientFor(endpoint)
	if err != nil {
		return finishError(result, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return finishError(result, err)
	}
	defer resp.Body.Close()
	statusCode := resp.StatusCode
	result.StatusCode = &statusCode
	result.ResponseHeaders = resp.Header.Clone()
	if endpoint.HTTPMethod == "HEAD" {
		length := int64(0)
		if resp.ContentLength >= 0 {
			length = resp.ContentLength
		} else if raw := resp.Header.Get("Content-Length"); raw != "" {
			if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed >= 0 {
				length = parsed
			}
		}
		result.ResponseLength = &length
		result.FinishedAt = time.Now().UTC()
		result.DurationMS = int(result.FinishedAt.Sub(started).Milliseconds())
		return result
	}

	limited := io.LimitReader(resp.Body, p.MaxResponseBytes+1)
	data, readErr := io.ReadAll(limited)
	if readErr != nil {
		return finishError(result, readErr)
	}
	length := int64(len(data))
	if length > p.MaxResponseBytes {
		result.Truncated = true
		data = data[:p.MaxResponseBytes]
		length = p.MaxResponseBytes
		warning := "response body exceeded max bytes and was truncated"
		result.Error = &warning
	}
	result.Body = data
	result.ResponseLength = &length
	result.FinishedAt = time.Now().UTC()
	result.DurationMS = int(result.FinishedAt.Sub(started).Milliseconds())
	return result
}

func retryableMethod(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func transientPingError(message string) bool {
	lowered := strings.ToLower(message)
	return strings.Contains(lowered, "context deadline exceeded") ||
		strings.Contains(lowered, "client.timeout") ||
		strings.Contains(lowered, "i/o timeout") ||
		strings.Contains(lowered, "tls handshake timeout") ||
		strings.Contains(lowered, "connection reset by peer") ||
		strings.Contains(lowered, "unexpected eof") ||
		strings.Contains(lowered, "read: connection timed out")
}

func (p *Pinger) clientFor(endpoint models.Endpoint) (*http.Client, error) {
	if !endpoint.Proxy.Enabled {
		return p.Client, nil
	}
	password, err := p.proxyPassword(endpoint.Proxy)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableKeepAlives = true
	dialer := socks5.Dialer{
		ProxyAddress: endpoint.Proxy.Address,
		Username:     endpoint.Proxy.Username,
		Password:     password,
		Timeout:      p.Timeout,
	}
	transport.DialContext = dialer.DialContext
	return &http.Client{
		Timeout:       p.Timeout,
		Transport:     transport,
		CheckRedirect: redirectPolicy(p.AllowPrivateTargets),
	}, nil
}

func (p *Pinger) proxyPassword(proxy models.ProxyConfig) (string, error) {
	if proxy.Username == "" {
		return "", nil
	}
	if proxy.Password != "" && proxy.Password != "********" {
		return proxy.Password, nil
	}
	if proxy.PasswordEncrypted == nil || *proxy.PasswordEncrypted == "" {
		return "", fmt.Errorf("SOCKS5 proxy password is not configured")
	}
	if p.Secrets == nil {
		return "", fmt.Errorf("SOCKS5 proxy password cannot be decrypted")
	}
	password, err := p.Secrets.Decrypt(*proxy.PasswordEncrypted)
	if err != nil {
		return "", fmt.Errorf("SOCKS5 proxy password cannot be decrypted")
	}
	return password, nil
}

func redirectPolicy(allowPrivate bool) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		if !allowPrivate {
			if err := rejectPrivateTarget(req.Context(), req.URL.Hostname()); err != nil {
				return fmt.Errorf("redirect to private target is blocked")
			}
		}
		return nil
	}
}

func publicTargetIP(ctx context.Context, address string) (string, string, string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", "", "", err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return "", "", "", err
	}
	if len(ips) == 0 {
		return "", "", "", fmt.Errorf("target host did not resolve")
	}
	for _, resolved := range ips {
		if validation.IsPrivateIP(resolved.IP) {
			return "", "", "", fmt.Errorf("resolved private target is blocked")
		}
	}
	return host, port, ips[0].IP.String(), nil
}

func rejectPrivateTarget(ctx context.Context, host string) error {
	if validation.IsPrivateHost(host) {
		return fmt.Errorf("private target is blocked")
	}
	_, _, _, err := publicTargetIP(ctx, net.JoinHostPort(host, "80"))
	return err
}

func finishError(r models.PingResult, err error) models.PingResult {
	message := err.Error()
	r.Error = &message
	r.FinishedAt = time.Now().UTC()
	r.DurationMS = int(r.FinishedAt.Sub(r.StartedAt).Milliseconds())
	return r
}
