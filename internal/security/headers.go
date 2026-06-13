package security

import (
	"fmt"
	"strings"
	"unicode"

	"bug-bounty-endpoint-pinger/internal/models"
)

var sensitiveMarkers = []string{
	"authorization",
	"token",
	"key",
	"api-key",
	"apikey",
	"bearer",
	"basic",
	"secret",
	"cookie",
	"session",
	"jwt",
}

func DetectSensitive(name, value string) bool {
	combined := strings.ToLower(name + " " + value)
	for _, marker := range sensitiveMarkers {
		if strings.Contains(combined, marker) {
			return true
		}
	}
	return false
}

func ValidateHeaders(headers []models.HeaderInput) error {
	if len(headers) > 50 {
		return fmt.Errorf("maximum 50 headers are allowed")
	}
	for _, header := range headers {
		if header.Name == "" {
			return fmt.Errorf("header name is required")
		}
		if len(header.Name) > 256 {
			return fmt.Errorf("header name is too long")
		}
		if len(header.Value) > 8192 {
			return fmt.Errorf("header value is too long")
		}
		if strings.ContainsAny(header.Name, "\r\n") || strings.ContainsAny(header.Value, "\r\n") {
			return fmt.Errorf("headers must not contain CRLF characters")
		}
		if !validHeaderName(header.Name) {
			return fmt.Errorf("invalid header name %q", header.Name)
		}
	}
	return nil
}

func validHeaderName(name string) bool {
	for _, r := range name {
		if r > unicode.MaxASCII || !isTokenRune(byte(r)) {
			return false
		}
	}
	return name != ""
}

func isTokenRune(r byte) bool {
	if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
		return true
	}
	switch r {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	default:
		return false
	}
}
