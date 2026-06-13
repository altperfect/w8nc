package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"bug-bounty-endpoint-pinger/internal/api"
	"bug-bounty-endpoint-pinger/internal/auth"
	"bug-bounty-endpoint-pinger/internal/config"
	secretbox "bug-bounty-endpoint-pinger/internal/crypto"
	"bug-bounty-endpoint-pinger/internal/db"
	"bug-bounty-endpoint-pinger/internal/models"
)

func TestEndpointTestRequestReturnsResponseMetadata(t *testing.T) {
	body := strings.Repeat("x", 13050)
	requestBody := `{"probe":true}`
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Test"); got != "value" {
			t.Fatalf("test request header=%q", got)
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if got := string(data); got != requestBody {
			t.Fatalf("test request body=%q", got)
		}
		w.Header().Set("X-Trace", "abc123")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(body))
	}))
	defer target.Close()

	server := &api.Server{
		Config: config.Config{
			AuthEnabled:             false,
			AllowPrivateTargets:     true,
			DefaultRequestTimeout:   2 * time.Second,
			DefaultMaxResponseBytes: 20000,
		},
		Auth: auth.NewManager(nil, false, "test-secret", false),
	}

	result := doJSON[struct {
		StatusCode             *int                `json:"status_code"`
		ResponseHeaders        map[string][]string `json:"response_headers"`
		ResponseLength         *int64              `json:"response_length"`
		DurationMS             int                 `json:"duration_ms"`
		BodyPreview            string              `json:"body_preview"`
		BodyPreviewTruncated   bool                `json:"body_preview_truncated"`
		TransportBodyTruncated bool                `json:"truncated"`
	}](t, server, http.MethodPost, "/api/endpoints/test-request", models.EndpointInput{
		URL:                target.URL + "/probe",
		HTTPMethod:         "POST",
		RequestBodyEnabled: true,
		RequestBody:        requestBody,
		Headers: []models.HeaderInput{{
			Name:  "X-Test",
			Value: "value",
		}},
		Active: true,
	}, http.StatusOK)

	if result.StatusCode == nil || *result.StatusCode != http.StatusAccepted {
		t.Fatalf("status_code=%v", result.StatusCode)
	}
	if values := result.ResponseHeaders["X-Trace"]; len(values) != 1 || values[0] != "abc123" {
		t.Fatalf("X-Trace header=%v", values)
	}
	if result.ResponseLength == nil || *result.ResponseLength != int64(len(body)) {
		t.Fatalf("response_length=%v", result.ResponseLength)
	}
	if result.TransportBodyTruncated {
		t.Fatalf("transport body should not be truncated")
	}
	if !result.BodyPreviewTruncated {
		t.Fatalf("body preview should be truncated")
	}
	if len([]rune(result.BodyPreview)) >= len([]rune(body)) {
		t.Fatalf("body preview was not shortened")
	}
	if result.DurationMS < 0 {
		t.Fatalf("duration_ms=%d", result.DurationMS)
	}
}

func TestEndpointRequestBodyValidationAPI(t *testing.T) {
	server := &api.Server{
		Config: config.Config{
			AuthEnabled:         false,
			AllowPrivateTargets: true,
		},
		Auth: auth.NewManager(nil, false, "test-secret", false),
	}

	tooLarge := postJSON(t, server, "/api/endpoints/test-request", models.EndpointInput{
		URL:                "https://example.com/probe",
		HTTPMethod:         "POST",
		RequestBodyEnabled: true,
		RequestBody:        strings.Repeat("x", 257*1024),
		Active:             true,
	}, http.StatusBadRequest)
	if !strings.Contains(tooLarge.Body.String(), "request body must be") {
		t.Fatalf("unexpected large body error: %s", tooLarge.Body.String())
	}

	nullByte := postJSON(t, server, "/api/endpoints/test-request", models.EndpointInput{
		URL:                "https://example.com/probe",
		HTTPMethod:         "POST",
		RequestBodyEnabled: true,
		RequestBody:        "a\x00b",
		Active:             true,
	}, http.StatusBadRequest)
	if !strings.Contains(nullByte.Body.String(), "null bytes") {
		t.Fatalf("unexpected null-byte body error: %s", nullByte.Body.String())
	}
}

func TestTemplatePlaceholdersAPI(t *testing.T) {
	server := &api.Server{
		Config: config.Config{AuthEnabled: false},
		Auth:   auth.NewManager(nil, false, "test-secret", false),
	}
	result := doJSON[struct {
		Items []string `json:"items"`
	}](t, server, http.MethodGet, "/api/template-placeholders", nil, http.StatusOK)
	values := strings.Join(result.Items, ",")
	for _, expected := range []string{"checked_at", "duration_ms", "response_body", "response_headers"} {
		if !strings.Contains(values, expected) {
			t.Fatalf("placeholder %q missing from %q", expected, values)
		}
	}
}

func TestEndpointCRUDAndSensitiveMaskingAPI(t *testing.T) {
	store := integrationStore(t)
	secrets, err := secretbox.NewSecretBox([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	server := &api.Server{
		Config: config.Config{
			AuthEnabled:         false,
			AllowPrivateTargets: true,
			MinPingInterval:     5 * time.Second,
			MaxPingInterval:     30 * 24 * time.Hour,
		},
		Store:   store,
		Auth:    auth.NewManager(store, false, "test-secret", false),
		Secrets: secrets,
	}

	sensitive := true
	masked := true
	createBody := models.EndpointInput{
		Name:               stringPtr("Admin"),
		URL:                "https://example.com/admin",
		HTTPMethod:         "GET",
		RequestBodyEnabled: true,
		RequestBody:        "<xml>\n  <probe>true</probe>\n</xml>",
		Headers: []models.HeaderInput{{
			Name:      "Authorization",
			Value:     "Bearer secret-token",
			Sensitive: &sensitive,
			Masked:    &masked,
		}},
		Proxy: models.ProxyConfig{
			Enabled:  true,
			Address:  "socks5://proxy.example:1080",
			Username: "proxy-user",
			Password: "proxy-pass",
		},
		PingInterval:         "15s",
		DeactivateAfter:      "2h",
		NotifyCondition:      condition("status_code_changed", nil),
		NotificationTemplate: models.DefaultNotificationTemplate,
		Active:               true,
	}

	created := doJSON[models.Endpoint](t, server, http.MethodPost, "/api/endpoints", createBody, http.StatusCreated)
	defer func() { _ = store.DeleteEndpoint(context.Background(), created.ID) }()
	if got := created.HeaderViews[0].Value; got != "********" {
		t.Fatalf("sensitive header was exposed on create: %q", got)
	}
	if created.DeactivateAfter == nil || *created.DeactivateAfter != "2h" || created.DeactivateAt == nil {
		t.Fatalf("deactivate after was not set: after=%v at=%v", created.DeactivateAfter, created.DeactivateAt)
	}
	if !created.Proxy.Enabled || created.Proxy.Address != "proxy.example:1080" || !created.Proxy.PasswordSet || created.Proxy.Password != "********" {
		t.Fatalf("proxy was not masked on create: %+v", created.Proxy)
	}
	if !created.RequestBodyEnabled || created.RequestBody != createBody.RequestBody {
		t.Fatalf("request body was not persisted on create: enabled=%v body=%q", created.RequestBodyEnabled, created.RequestBody)
	}

	fetched := doJSON[models.Endpoint](t, server, http.MethodGet, "/api/endpoints/"+created.ID, nil, http.StatusOK)
	if got := fetched.HeaderViews[0].Value; got != "********" {
		t.Fatalf("sensitive header was exposed on get: %q", got)
	}
	if !fetched.Proxy.PasswordSet || fetched.Proxy.Password != "********" {
		t.Fatalf("proxy password was exposed on get: %+v", fetched.Proxy)
	}
	if !fetched.RequestBodyEnabled || fetched.RequestBody != createBody.RequestBody {
		t.Fatalf("request body was not returned on get: enabled=%v body=%q", fetched.RequestBodyEnabled, fetched.RequestBody)
	}

	createBody.PingInterval = "1m"
	createBody.Headers[0].Value = "********"
	createBody.Proxy.Password = "********"
	updated := doJSON[models.Endpoint](t, server, http.MethodPut, "/api/endpoints/"+created.ID, createBody, http.StatusOK)
	if updated.PingIntervalSeconds != 60 {
		t.Fatalf("update did not change interval: %d", updated.PingIntervalSeconds)
	}
	if !updated.Proxy.PasswordSet || updated.Proxy.Password != "********" {
		t.Fatalf("proxy password was not preserved on update: %+v", updated.Proxy)
	}
	if !updated.RequestBodyEnabled || updated.RequestBody != createBody.RequestBody {
		t.Fatalf("request body was not preserved on update: enabled=%v body=%q", updated.RequestBodyEnabled, updated.RequestBody)
	}

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/endpoints/"+created.ID, nil)
	server.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestNotificationSettingsProxyMaskingAPI(t *testing.T) {
	store := integrationStore(t)
	secrets, err := secretbox.NewSecretBox([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	server := &api.Server{
		Config:  config.Config{AuthEnabled: false},
		Store:   store,
		Auth:    auth.NewManager(store, false, "test-secret", false),
		Secrets: secrets,
	}
	token := "telegram-token"
	chatID := "12345"
	body := models.NotificationSettingsInput{
		TelegramEnabled:   true,
		TelegramAPIKey:    &token,
		TelegramChatID:    &chatID,
		TelegramParseMode: "Markdown",
		Timezone:          "Asia/Yekaterinburg",
		Proxy: models.ProxyConfig{
			Enabled:  true,
			Address:  "socks5://proxy.example:1080",
			Username: "proxy-user",
			Password: "proxy-pass",
		},
	}
	updated := doJSON[models.NotificationSettings](t, server, http.MethodPut, "/api/settings/notifications", body, http.StatusOK)
	if !updated.Proxy.Enabled || updated.Proxy.Address != "proxy.example:1080" || !updated.Proxy.PasswordSet || updated.Proxy.Password != "********" {
		t.Fatalf("notification proxy was not masked on update: %+v", updated.Proxy)
	}
	if updated.Timezone != "Asia/Yekaterinburg" {
		t.Fatalf("timezone=%q, want Asia/Yekaterinburg", updated.Timezone)
	}
	fetched := doJSON[models.NotificationSettings](t, server, http.MethodGet, "/api/settings/notifications", nil, http.StatusOK)
	if !fetched.Proxy.PasswordSet || fetched.Proxy.Password != "********" {
		t.Fatalf("notification proxy password was exposed on get: %+v", fetched.Proxy)
	}
	if fetched.Timezone != "Asia/Yekaterinburg" || len(fetched.TimezoneOptions) == 0 {
		t.Fatalf("timezone options missing on get: timezone=%q options=%d", fetched.Timezone, len(fetched.TimezoneOptions))
	}
	body.TelegramAPIKey = nil
	body.Proxy.Password = "********"
	kept := doJSON[models.NotificationSettings](t, server, http.MethodPut, "/api/settings/notifications", body, http.StatusOK)
	if !kept.Proxy.PasswordSet || kept.Proxy.Password != "********" {
		t.Fatalf("notification proxy password was not preserved: %+v", kept.Proxy)
	}
}

func TestPasswordOnlyAuthFlow(t *testing.T) {
	store := authIntegrationStore(t)
	if _, err := store.Pool.Exec(context.Background(), `TRUNCATE users CASCADE`); err != nil {
		t.Fatal(err)
	}
	authManager := auth.NewManager(store, true, "test-secret", false)
	bootstrap, err := authManager.BootstrapPassword(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !bootstrap.Generated || len(bootstrap.Password) < 24 {
		t.Fatalf("generated password missing or too short: generated=%v length=%d", bootstrap.Generated, len(bootstrap.Password))
	}
	server := &api.Server{
		Config: config.Config{AuthEnabled: true},
		Store:  store,
		Auth:   authManager,
	}

	login := postJSON(t, server, "/api/auth/login", map[string]string{"password": bootstrap.Password}, http.StatusOK)
	if strings.Contains(login.Body.String(), "username") {
		t.Fatalf("login response exposed username: %s", login.Body.String())
	}
	cookie := sessionCookie(t, login)

	me := requestWithCookie(t, server, http.MethodGet, "/api/auth/me", nil, cookie, http.StatusOK)
	if strings.Contains(me.Body.String(), "username") {
		t.Fatalf("me response exposed username: %s", me.Body.String())
	}

	change := requestWithCookie(t, server, http.MethodPut, "/api/auth/password", map[string]string{
		"current_password": bootstrap.Password,
		"new_password":     "new-generated-password",
	}, cookie, http.StatusOK)
	if !strings.Contains(change.Body.String(), "ok") {
		t.Fatalf("change password response=%s", change.Body.String())
	}
	requestWithCookie(t, server, http.MethodPut, "/api/auth/password", map[string]string{
		"current_password": "wrong-password",
		"new_password":     "another-new-password",
	}, cookie, http.StatusUnauthorized)
	postJSON(t, server, "/api/auth/login", map[string]string{"password": bootstrap.Password}, http.StatusUnauthorized)
	postJSON(t, server, "/api/auth/login", map[string]string{"password": "new-generated-password"}, http.StatusOK)

	resetPassword, err := authManager.SetGeneratedPassword(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(resetPassword) < 24 || resetPassword == "new-generated-password" {
		t.Fatalf("reset password was not newly generated: length=%d", len(resetPassword))
	}
	postJSON(t, server, "/api/auth/login", map[string]string{"password": "new-generated-password"}, http.StatusUnauthorized)
	postJSON(t, server, "/api/auth/login", map[string]string{"password": resetPassword}, http.StatusOK)
}

func doJSON[T any](t *testing.T, handler http.Handler, method, path string, body any, status int) T {
	t.Helper()
	var requestBody bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&requestBody).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &requestBody)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != status {
		t.Fatalf("%s %s status=%d body=%s", method, path, recorder.Code, recorder.Body.String())
	}
	var value T
	if err := json.NewDecoder(recorder.Body).Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func postJSON(t *testing.T, handler http.Handler, path string, body any, status int) *httptest.ResponseRecorder {
	t.Helper()
	return requestWithCookie(t, handler, http.MethodPost, path, body, nil, status)
}

func requestWithCookie(t *testing.T, handler http.Handler, method, path string, body any, cookie *http.Cookie, status int) *httptest.ResponseRecorder {
	t.Helper()
	var requestBody bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&requestBody).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &requestBody)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != status {
		t.Fatalf("%s %s status=%d body=%s", method, path, recorder.Code, recorder.Body.String())
	}
	return recorder
}

func sessionCookie(t *testing.T, recorder *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == "pinger_session" {
			return cookie
		}
	}
	t.Fatal("session cookie was not set")
	return nil
}

func integrationStore(t *testing.T) *db.Store {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run Postgres integration tests")
	}
	store, err := db.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	if err := store.Migrate(context.Background(), "../../migrations"); err != nil {
		t.Fatal(err)
	}
	return store
}

func authIntegrationStore(t *testing.T) *db.Store {
	t.Helper()
	dsn := os.Getenv("TEST_AUTH_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_AUTH_DATABASE_URL to run isolated auth integration tests")
	}
	store, err := db.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	if err := store.Migrate(context.Background(), "../../migrations"); err != nil {
		t.Fatal(err)
	}
	return store
}

func stringPtr(value string) *string {
	return &value
}

func condition(kind string, value any) models.Condition {
	raw, _ := json.Marshal(value)
	if value == nil {
		raw = nil
	}
	return models.Condition{Type: kind, Value: raw}
}
