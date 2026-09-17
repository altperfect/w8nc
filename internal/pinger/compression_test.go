package pinger

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"w8nc/internal/conditions"
	"w8nc/internal/models"
)

func compressedFixture(t *testing.T, encoding string, data []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	var w io.WriteCloser
	var err error
	switch encoding {
	case "gzip":
		w = gzip.NewWriter(&b)
	case "deflate":
		w = zlib.NewWriter(&b)
	case "raw-deflate":
		w, err = flate.NewWriter(&b, flate.DefaultCompression)
	case "br":
		w = brotli.NewWriter(&b)
	case "zstd":
		w, err = zstd.NewWriter(&b)
	default:
		t.Fatalf("unknown fixture encoding %s", encoding)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write(data); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestPingDecodesExplicitlyNegotiatedCompression(t *testing.T) {
	const body = `{"status":"ready for pickup","message":"Spremno za preuzimanje ✓"}`
	for _, encoding := range []string{"gzip", "deflate", "raw-deflate", "br", "zstd", "gzip, br", "identity"} {
		t.Run(encoding, func(t *testing.T) {
			wire := []byte(body)
			if encoding != "identity" {
				for _, part := range strings.Split(encoding, ", ") {
					wire = compressedFixture(t, part, wire)
				}
			}
			responseEncoding := encoding
			if encoding == "raw-deflate" {
				responseEncoding = "deflate"
			}
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Accept-Encoding") != "gzip, deflate, br, zstd" {
					t.Error("configured encoding header was changed")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Content-Encoding", responseEncoding)
				_, _ = w.Write(wire)
			}))
			defer target.Close()
			accept := "gzip, deflate, br, zstd"
			endpoint := models.Endpoint{URL: target.URL, HTTPMethod: "POST", Headers: []models.Header{{Name: "Accept-Encoding", ValuePlain: &accept}}, NotifyCondition: models.Condition{Type: "body_contains", Value: []byte(`"ready for pickup"`)}}
			result := New(2*time.Second, 1024, true, nil).Ping(context.Background(), endpoint)
			if result.Error != nil {
				t.Fatal(*result.Error)
			}
			if string(result.Body) != body {
				t.Fatalf("got %d encoded/unexpected bytes instead of decoded JSON", len(result.Body))
			}
			if result.ResponseLength == nil || *result.ResponseLength != int64(len(body)) {
				t.Fatalf("wrong decoded length: %v", result.ResponseLength)
			}
			evaluation, err := conditions.Evaluate(endpoint, result)
			if err != nil || !evaluation.Matched {
				t.Fatalf("decoded text did not match: %v", err)
			}
		})
	}
}

func TestPingLimitsDecodedResponseSize(t *testing.T) {
	for _, encoding := range []string{"gzip", "deflate", "br", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			wire := compressedFixture(t, encoding, []byte(strings.Repeat("a", 100000)))
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Encoding", encoding)
				_, _ = w.Write(wire)
			}))
			defer target.Close()
			result := New(2*time.Second, 1024, true, nil).Ping(context.Background(), models.Endpoint{URL: target.URL, HTTPMethod: "POST", Headers: []models.Header{{Name: "Accept-Encoding", ValuePlain: &encoding}}})
			if !result.Truncated || result.Error == nil || len(result.Body) != 1024 || string(result.Body) != strings.Repeat("a", 1024) {
				t.Fatalf("decoded body limit not applied: size=%d truncated=%v error=%v", len(result.Body), result.Truncated, result.Error)
			}
		})
	}
}

func TestPingRejectsInvalidEncodedResponses(t *testing.T) {
	for _, encoding := range []string{"gzip", "deflate", "br", "zstd", "unsupported"} {
		t.Run(encoding, func(t *testing.T) {
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Encoding", encoding)
				_, _ = w.Write([]byte{0xff, 0xff, 0xff})
			}))
			defer target.Close()
			result := New(2*time.Second, 1024, true, nil).Ping(context.Background(), models.Endpoint{URL: target.URL, HTTPMethod: "POST"})
			if result.Error == nil || len(result.Body) != 0 {
				t.Fatal("invalid encoded bytes were treated as response text")
			}
			if result.StatusCode == nil || *result.StatusCode != 200 {
				t.Fatal("lost HTTP response status")
			}
		})
	}
}

func TestPingEmptyCompressedResponses(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusNotModified} {
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Encoding", "gzip")
			w.WriteHeader(status)
		}))
		encoding := "gzip"
		result := New(2*time.Second, 1024, true, nil).Ping(context.Background(), models.Endpoint{URL: target.URL, HTTPMethod: "GET", Headers: []models.Header{{Name: "Accept-Encoding", ValuePlain: &encoding}}})
		target.Close()
		if result.Error != nil || len(result.Body) != 0 {
			t.Fatalf("empty status %d: %v", status, result.Error)
		}
	}
}

func TestPingKeepsAutomaticGzipAndHeadBehavior(t *testing.T) {
	const body = "decoded body"
	wire := compressedFixture(t, "gzip", []byte(body))
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", fmt.Sprint(len(wire)))
		if r.Method != "HEAD" {
			_, _ = w.Write(wire)
		}
	}))
	defer target.Close()
	p := New(2*time.Second, 1024, true, nil)
	result := p.Ping(context.Background(), models.Endpoint{URL: target.URL, HTTPMethod: "GET"})
	if result.Error != nil || string(result.Body) != body {
		t.Fatal("automatic gzip decoding regressed")
	}
	result = p.Ping(context.Background(), models.Endpoint{URL: target.URL, HTTPMethod: "HEAD"})
	if result.Error != nil || len(result.Body) != 0 || result.ResponseLength == nil || *result.ResponseLength != int64(len(wire)) {
		t.Fatal("HEAD content-length behavior regressed")
	}
}
