package security

import (
	"testing"

	"w8nc/internal/models"
)

func TestDetectSensitive(t *testing.T) {
	if !DetectSensitive("Authorization", "Bearer token") {
		t.Fatal("authorization bearer should be sensitive")
	}
	if !DetectSensitive("X-Api-Key", "abc") {
		t.Fatal("api key header should be sensitive")
	}
	if DetectSensitive("X-Test", "public") {
		t.Fatal("public header should not be sensitive")
	}
}

func TestValidateHeaders(t *testing.T) {
	err := ValidateHeaders([]models.HeaderInput{{Name: "X-Test", Value: "ok"}})
	if err != nil {
		t.Fatalf("valid header rejected: %v", err)
	}
	if err := ValidateHeaders([]models.HeaderInput{{Name: "Bad Header", Value: "ok"}}); err == nil {
		t.Fatal("invalid header name accepted")
	}
	if err := ValidateHeaders([]models.HeaderInput{{Name: "X-Test", Value: "bad\r\nvalue"}}); err == nil {
		t.Fatal("CRLF header value accepted")
	}
}
