package validation

import (
	"testing"
	"time"
)

func TestParseInterval(t *testing.T) {
	tests := map[string]time.Duration{
		"15s": 15 * time.Second,
		"1m":  time.Minute,
		"8h":  8 * time.Hour,
		"2d":  48 * time.Hour,
	}
	for input, expected := range tests {
		got, err := ParseInterval(input)
		if err != nil {
			t.Fatalf("ParseInterval(%q): %v", input, err)
		}
		if got != expected {
			t.Fatalf("ParseInterval(%q)=%s, want %s", input, got, expected)
		}
	}
}

func TestParseIntervalRejectsInvalid(t *testing.T) {
	for _, input := range []string{"0s", "-1m", "1.5h", "1w", "abc", "10"} {
		if _, err := ParseInterval(input); err == nil {
			t.Fatalf("ParseInterval(%q) succeeded", input)
		}
	}
}

func TestValidateIntervalMinMax(t *testing.T) {
	if _, err := ValidateInterval("4s", 5*time.Second, time.Hour); err == nil {
		t.Fatal("expected interval below minimum to fail")
	}
	if _, err := ValidateInterval("2h", 5*time.Second, time.Hour); err == nil {
		t.Fatal("expected interval above maximum to fail")
	}
	if seconds, err := ValidateInterval("5s", 5*time.Second, time.Hour); err != nil || seconds != 5 {
		t.Fatalf("ValidateInterval returned %d, %v", seconds, err)
	}
}
