package validation

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

func NormalizeURL(raw string, allowPrivate bool) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("invalid URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("URL must use http:// or https://")
	}
	if parsed.Hostname() == "" {
		return "", fmt.Errorf("URL must include a host")
	}
	parsed.Fragment = ""
	if !allowPrivate && IsPrivateHost(parsed.Hostname()) {
		return "", fmt.Errorf("private, localhost, and metadata targets are disabled")
	}
	return parsed.String(), nil
}

func IsPrivateHost(host string) bool {
	normalized := strings.Trim(strings.ToLower(host), "[]")
	if normalized == "localhost" || strings.HasSuffix(normalized, ".localhost") {
		return true
	}
	ip := net.ParseIP(normalized)
	if ip == nil {
		return false
	}
	return IsPrivateIP(ip)
}

func IsPrivateIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalMulticast() || ip.IsLinkLocalUnicast() || ip.IsPrivate() || ip.IsUnspecified() {
		return true
	}
	if ip.Equal(net.ParseIP("169.254.169.254")) {
		return true
	}
	return false
}
