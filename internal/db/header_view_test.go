package db

import (
	"testing"

	"w8nc/internal/models"
)

func TestHeaderViewsMaskSensitiveValues(t *testing.T) {
	secret := "ciphertext"
	plain := "abc"
	views := HeaderViews([]models.Header{
		{Name: "Authorization", ValueEncrypted: &secret, Sensitive: true, Masked: true},
		{Name: "X-Test", ValuePlain: &plain, Sensitive: false, Masked: false},
	})
	if views[0].Value != "********" {
		t.Fatalf("sensitive value was exposed: %q", views[0].Value)
	}
	if views[1].Value != "abc" {
		t.Fatalf("plain value was not returned")
	}
}
