package validation

import "testing"

func TestNormalizeSocks5ProxyAddress(t *testing.T) {
	tests := map[string]string{
		"127.0.0.1:9050":        "127.0.0.1:9050",
		"socks5://example:1080": "example:1080",
		"[::1]:1080":            "[::1]:1080",
	}
	for input, want := range tests {
		got, err := NormalizeSocks5ProxyAddress(input)
		if err != nil {
			t.Fatalf("NormalizeSocks5ProxyAddress(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("NormalizeSocks5ProxyAddress(%q)=%q, want %q", input, got, want)
		}
	}
}

func TestNormalizeSocks5ProxyAddressRejectsInvalid(t *testing.T) {
	for _, input := range []string{"", "example", "example:0", "example:99999", "http://example:1080", "socks5://user:pass@example:1080"} {
		if _, err := NormalizeSocks5ProxyAddress(input); err == nil {
			t.Fatalf("expected %q to be rejected", input)
		}
	}
}
