package validation

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

func NormalizeSocks5ProxyAddress(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", fmt.Errorf("SOCKS5 proxy address is required")
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil {
			return "", fmt.Errorf("invalid SOCKS5 proxy address")
		}
		if parsed.Scheme != "socks5" {
			return "", fmt.Errorf("SOCKS5 proxy address must use socks5://")
		}
		if parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", fmt.Errorf("SOCKS5 proxy address must only include host and port")
		}
		value = parsed.Host
	}
	host, portText, err := net.SplitHostPort(value)
	if err != nil || strings.TrimSpace(host) == "" {
		return "", fmt.Errorf("SOCKS5 proxy address must be host:port")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("SOCKS5 proxy port must be between 1 and 65535")
	}
	return net.JoinHostPort(strings.TrimSpace(host), strconv.Itoa(port)), nil
}
