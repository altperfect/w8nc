package validation

import "testing"

func TestNormalizeURL(t *testing.T) {
	got, err := NormalizeURL("https://example.com/admin#frag", false)
	if err != nil {
		t.Fatalf("NormalizeURL: %v", err)
	}
	if got != "https://example.com/admin" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeURLRejectsPrivateTargets(t *testing.T) {
	for _, raw := range []string{"http://localhost", "http://127.0.0.1", "http://10.0.0.1", "http://[::1]", "http://169.254.169.254"} {
		if _, err := NormalizeURL(raw, false); err == nil {
			t.Fatalf("expected %s to be blocked", raw)
		}
	}
	if _, err := NormalizeURL("http://127.0.0.1", true); err != nil {
		t.Fatalf("private target should be allowed when configured: %v", err)
	}
}

func TestNormalizeURLRejectsMalformed(t *testing.T) {
	for _, raw := range []string{"", "ftp://example.com", "https:///missing-host"} {
		if _, err := NormalizeURL(raw, false); err == nil {
			t.Fatalf("expected %s to fail", raw)
		}
	}
}
