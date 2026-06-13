package auth

import (
	"testing"
	"time"
)

func TestRateLimiter(t *testing.T) {
	limiter := NewRateLimiter(2, time.Minute)
	if limiter.Limited("127.0.0.1") {
		t.Fatal("fresh limiter should not be limited")
	}
	limiter.RecordFailure("127.0.0.1")
	limiter.RecordFailure("127.0.0.1")
	if !limiter.Limited("127.0.0.1") {
		t.Fatal("limiter should block after max failures")
	}
	limiter.Reset("127.0.0.1")
	if limiter.Limited("127.0.0.1") {
		t.Fatal("successful login reset should clear attempts")
	}
	limiter.RecordFailure("127.0.0.1")
	limiter.RecordFailure("192.0.2.1")
	if cleared := limiter.ResetAll(); cleared != 2 {
		t.Fatalf("reset all cleared %d entries, want 2", cleared)
	}
	if limiter.Limited("127.0.0.1") || limiter.Limited("192.0.2.1") {
		t.Fatal("reset all should clear every tracked IP")
	}
}
