package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"w8nc/internal/auth"
	"w8nc/internal/conditions"
	"w8nc/internal/config"
	secretbox "w8nc/internal/crypto"
	"w8nc/internal/db"
	"w8nc/internal/models"
	"w8nc/internal/notifier"
	"w8nc/internal/pinger"
	"w8nc/internal/scheduler"
	"w8nc/internal/security"
	tz "w8nc/internal/timezone"
	"w8nc/internal/validation"
)

const (
	bodyPreviewRuneLimit       = 12000
	maxEndpointRequestBodySize = 256 * 1024
)

type Server struct {
	Config    config.Config
	Store     *db.Store
	Auth      *auth.Manager
	Scheduler *scheduler.Service
	Notifier  *notifier.Notifier
	Secrets   *secretbox.SecretBox
	Static    http.Handler
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		s.serveAPI(w, r)
		return
	}
	if s.Static != nil {
		s.Static.ServeHTTP(w, r)
		return
	}
	http.NotFound(w, r)
}

func (s *Server) serveAPI(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/health":
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		s.handleHealth(w, r)
		return
	case "/api/auth/login":
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		if !s.Config.AuthEnabled {
			writeError(w, http.StatusBadRequest, "authentication is disabled")
			return
		}
		s.Auth.Login(w, r)
		return
	case "/api/auth/logout":
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		s.Auth.Logout(w, r)
		return
	case "/api/auth/me":
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		s.Auth.Optional(http.HandlerFunc(s.Auth.Me)).ServeHTTP(w, r)
		return
	}

	protected := http.HandlerFunc(s.serveProtectedAPI)
	s.Auth.Middleware(protected).ServeHTTP(w, r)
}

func (s *Server) serveProtectedAPI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/endpoints" {
		switch r.Method {
		case http.MethodGet:
			s.handleListEndpoints(w, r)
		case http.MethodPost:
			s.handleCreateEndpoint(w, r)
		default:
			methodNotAllowed(w)
		}
		return
	}
	if r.URL.Path == "/api/endpoints/test-request" {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		s.handleTestEndpointRequest(w, r)
		return
	}
	if r.URL.Path == "/api/template-placeholders" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		s.handleTemplatePlaceholders(w, r)
		return
	}
	if r.URL.Path == "/api/auth/password" {
		if r.Method != http.MethodPut {
			methodNotAllowed(w)
			return
		}
		s.Auth.ChangePassword(w, r)
		return
	}
	if r.URL.Path == "/api/proxies/last" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		s.handleLastProxy(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/endpoints/") {
		s.handleEndpointPath(w, r)
		return
	}
	if r.URL.Path == "/api/settings/notifications" {
		switch r.Method {
		case http.MethodGet:
			s.handleGetNotificationSettings(w, r)
		case http.MethodPut:
			s.handleUpdateNotificationSettings(w, r)
		default:
			methodNotAllowed(w)
		}
		return
	}
	if r.URL.Path == "/api/settings/notifications/test" {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		s.handleTestNotification(w, r)
		return
	}
	http.NotFound(w, r)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	dbStatus := "ok"
	if err := s.Store.Ping(r.Context()); err != nil {
		dbStatus = "error"
	}
	notifyStatus := s.Notifier.Check(r.Context()).Status
	status := "ok"
	if dbStatus != "ok" {
		status = "degraded"
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":        status,
		"database":      dbStatus,
		"notify_binary": notifyStatus,
	})
}

func (s *Server) handleTemplatePlaceholders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"items": conditions.TemplatePlaceholders(),
	})
}

func (s *Server) handleListEndpoints(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	page, _ := strconv.Atoi(query.Get("page"))
	pageSize, _ := strconv.Atoi(query.Get("page_size"))
	var active *bool
	if value := query.Get("active"); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err == nil {
			active = &parsed
		}
	}
	items, total, err := s.Store.ListEndpoints(r.Context(), db.ListEndpointsParams{
		Page: page, PageSize: pageSize, Sort: query.Get("sort"),
		State: query.Get("state"), Active: active, Method: query.Get("method"), Search: query.Get("search"),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list endpoints")
		return
	}
	params := db.ListEndpointsParams{Page: page, PageSize: pageSize, Sort: query.Get("sort")}
	params.Normalize()
	writeJSON(w, http.StatusOK, map[string]any{
		"items":     items,
		"page":      params.Page,
		"page_size": params.PageSize,
		"total":     total,
	})
}

func (s *Server) handleCreateEndpoint(w http.ResponseWriter, r *http.Request) {
	var input models.EndpointInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	record, _, err := s.endpointRecord(r.Context(), input, nil)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	endpoint, err := s.Store.CreateEndpoint(r.Context(), record)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create endpoint")
		return
	}
	writeJSON(w, http.StatusCreated, endpoint)
}

func (s *Server) handleTestEndpointRequest(w http.ResponseWriter, r *http.Request) {
	var input models.EndpointInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	endpoint, err := s.endpointForTestRequest(r.Context(), input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	timeout := s.Config.DefaultRequestTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	maxBytes := s.Config.DefaultMaxResponseBytes
	if maxBytes <= 0 {
		maxBytes = 5242880
	}
	result := pinger.New(timeout, maxBytes, s.Config.AllowPrivateTargets, s.Secrets).Ping(r.Context(), endpoint)
	bodyPreview, bodyPreviewTruncated := responseBodyPreview(result.Body)
	writeJSON(w, http.StatusOK, map[string]any{
		"status_code":            result.StatusCode,
		"response_headers":       result.ResponseHeaders,
		"response_length":        result.ResponseLength,
		"duration_ms":            result.DurationMS,
		"error":                  result.Error,
		"truncated":              result.Truncated,
		"body_preview":           bodyPreview,
		"body_preview_truncated": bodyPreviewTruncated,
	})
}

func (s *Server) handleEndpointPath(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/endpoints/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			s.handleGetEndpoint(w, r, id)
		case http.MethodPut:
			s.handleUpdateEndpoint(w, r, id)
		case http.MethodDelete:
			s.handleDeleteEndpoint(w, r, id)
		default:
			methodNotAllowed(w)
		}
		return
	}
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	switch parts[1] {
	case "activate":
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		endpoint, err := s.Store.ActivateEndpoint(r.Context(), id)
		s.writeEndpointMutation(w, endpoint, err)
	case "deactivate":
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		endpoint, err := s.Store.DeactivateEndpoint(r.Context(), id)
		s.writeEndpointMutation(w, endpoint, err)
	case "ping-now":
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		s.handlePingNow(w, r, id)
	case "checks":
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		s.handleChecks(w, r, id)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleGetEndpoint(w http.ResponseWriter, r *http.Request, id string) {
	endpoint, err := s.Store.GetEndpoint(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, endpoint)
}

func (s *Server) handleUpdateEndpoint(w http.ResponseWriter, r *http.Request, id string) {
	current, err := s.Store.GetEndpoint(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	var input models.EndpointInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	record, intervalChanged, err := s.endpointRecord(r.Context(), input, &current)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	endpoint, err := s.Store.UpdateEndpoint(r.Context(), id, record, intervalChanged)
	s.writeEndpointMutation(w, endpoint, err)
}

func (s *Server) handleDeleteEndpoint(w http.ResponseWriter, r *http.Request, id string) {
	if err := s.Store.DeleteEndpoint(r.Context(), id); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePingNow(w http.ResponseWriter, r *http.Request, id string) {
	endpoint, err := s.Store.GetEndpoint(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if err := s.Scheduler.PingNow(r.Context(), endpoint); err != nil {
		writeError(w, http.StatusInternalServerError, "ping failed")
		return
	}
	updated, err := s.Store.GetEndpoint(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleChecks(w http.ResponseWriter, r *http.Request, id string) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	checks, total, err := s.Store.ListChecks(r.Context(), id, page, pageSize)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list checks")
		return
	}
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": checks, "page": page, "page_size": pageSize, "total": total})
}

func (s *Server) handleGetNotificationSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.Store.GetNotificationSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load notification settings")
		return
	}
	settings.TimezoneOptions = tz.Options(settings.Timezone)
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleUpdateNotificationSettings(w http.ResponseWriter, r *http.Request) {
	current, err := s.Store.GetNotificationSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load notification settings")
		return
	}
	settings, keepToken, keepProxyPassword, err := s.notificationSettingsFromRequest(r, &current)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := s.Store.UpdateNotificationSettings(r.Context(), settings, keepToken, keepProxyPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not update notification settings")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleTestNotification(w http.ResponseWriter, r *http.Request) {
	settings, err := s.Store.GetNotificationSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load notification settings")
		return
	}
	if err := s.Notifier.Send(r.Context(), settings, "[w8nc] test ping!"); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLastProxy(w http.ResponseWriter, r *http.Request) {
	proxy, err := s.Store.LastProxyConfig(r.Context())
	if err != nil {
		if db.IsNotFound(err) {
			writeJSON(w, http.StatusOK, map[string]any{"available": false})
			return
		}
		writeError(w, http.StatusInternalServerError, "could not load last proxy")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"available": true,
		"proxy":     proxy,
	})
}

func (s *Server) writeEndpointMutation(w http.ResponseWriter, endpoint models.Endpoint, err error) {
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, endpoint)
}

func (s *Server) endpointRecord(ctx context.Context, input models.EndpointInput, existing *models.Endpoint) (db.EndpointRecord, bool, error) {
	url, err := validation.NormalizeURL(input.URL, s.Config.AllowPrivateTargets)
	if err != nil {
		return db.EndpointRecord{}, false, err
	}
	method := strings.ToUpper(strings.TrimSpace(input.HTTPMethod))
	if method == "" {
		method = "GET"
	}
	if !allowedMethod(method) {
		return db.EndpointRecord{}, false, fmt.Errorf("unsupported HTTP method")
	}
	if err := security.ValidateHeaders(input.Headers); err != nil {
		return db.EndpointRecord{}, false, err
	}
	requestBody, err := validatedRequestBody(input.RequestBodyEnabled, input.RequestBody)
	if err != nil {
		return db.EndpointRecord{}, false, err
	}
	intervalSeconds, err := validation.ValidateInterval(input.PingInterval, s.Config.MinPingInterval, s.Config.MaxPingInterval)
	if err != nil {
		return db.EndpointRecord{}, false, err
	}
	deactivateAfterSeconds, err := optionalDurationSeconds(input.DeactivateAfter, "deactivate after")
	if err != nil {
		return db.EndpointRecord{}, false, err
	}
	if err := conditions.ValidateCondition(input.NotifyCondition); err != nil {
		return db.EndpointRecord{}, false, err
	}
	template := input.NotificationTemplate
	if strings.TrimSpace(template) == "" {
		template = models.DefaultNotificationTemplate
	}
	headers, err := s.prepareHeaders(input.Headers, existing)
	if err != nil {
		return db.EndpointRecord{}, false, err
	}
	var existingProxy *models.ProxyConfig
	if existing != nil {
		existingProxy = &existing.Proxy
	}
	proxy, _, err := s.prepareStoredProxy(ctx, input.Proxy, existingProxy)
	if err != nil {
		return db.EndpointRecord{}, false, err
	}
	name := input.Name
	if name != nil {
		trimmed := strings.TrimSpace(*name)
		if trimmed == "" {
			name = nil
		} else {
			name = &trimmed
		}
	}
	intervalChanged := existing != nil && existing.PingIntervalSeconds != intervalSeconds
	return db.EndpointRecord{
		Name: name, URL: url, HTTPMethod: method, Headers: headers,
		RequestBodyEnabled: input.RequestBodyEnabled, RequestBody: requestBody, Proxy: proxy,
		PingIntervalSeconds: intervalSeconds, DeactivateAfterSeconds: deactivateAfterSeconds,
		NotifyCondition: input.NotifyCondition, NotificationTemplate: template, Active: input.Active,
	}, intervalChanged, nil
}

func (s *Server) endpointForTestRequest(ctx context.Context, input models.EndpointInput) (models.Endpoint, error) {
	url, err := validation.NormalizeURL(input.URL, s.Config.AllowPrivateTargets)
	if err != nil {
		return models.Endpoint{}, err
	}
	method := strings.ToUpper(strings.TrimSpace(input.HTTPMethod))
	if method == "" {
		method = "GET"
	}
	if !allowedMethod(method) {
		return models.Endpoint{}, fmt.Errorf("unsupported HTTP method")
	}
	if err := security.ValidateHeaders(input.Headers); err != nil {
		return models.Endpoint{}, err
	}
	requestBody, err := validatedRequestBody(input.RequestBodyEnabled, input.RequestBody)
	if err != nil {
		return models.Endpoint{}, err
	}
	proxy, err := s.runtimeProxy(ctx, input.Proxy)
	if err != nil {
		return models.Endpoint{}, err
	}
	headers := make([]models.Header, 0, len(input.Headers))
	for _, input := range input.Headers {
		masked := input.Sensitive != nil && *input.Sensitive
		if input.Masked != nil {
			masked = masked || *input.Masked
		}
		if masked && strings.TrimSpace(input.Value) == "********" {
			return models.Endpoint{}, fmt.Errorf("replace masked header %q before testing", strings.TrimSpace(input.Name))
		}
		value := input.Value
		headers = append(headers, models.Header{
			Name:       strings.TrimSpace(input.Name),
			ValuePlain: &value,
		})
	}
	return models.Endpoint{
		URL:                url,
		HTTPMethod:         method,
		Headers:            headers,
		RequestBodyEnabled: input.RequestBodyEnabled,
		RequestBody:        requestBody,
		Proxy:              proxy,
		Active:             true,
	}, nil
}

func validatedRequestBody(enabled bool, body string) (string, error) {
	if !enabled {
		return "", nil
	}
	if len([]byte(body)) > maxEndpointRequestBodySize {
		return "", fmt.Errorf("request body must be %d KiB or smaller", maxEndpointRequestBodySize/1024)
	}
	if strings.ContainsRune(body, 0) {
		return "", fmt.Errorf("request body cannot contain null bytes")
	}
	return body, nil
}

func responseBodyPreview(body []byte) (string, bool) {
	preview := strings.ToValidUTF8(string(body), "\uFFFD")
	runes := []rune(preview)
	if len(runes) <= bodyPreviewRuneLimit {
		return preview, false
	}
	return string(runes[:bodyPreviewRuneLimit]), true
}

func optionalDurationSeconds(value, label string) (*int, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, nil
	}
	duration, err := validation.ParseInterval(trimmed)
	if err != nil {
		return nil, fmt.Errorf("%s must match digits plus unit: s, m, h, or d", label)
	}
	seconds := int(duration / time.Second)
	return &seconds, nil
}

func (s *Server) prepareHeaders(inputs []models.HeaderInput, existing *models.Endpoint) ([]models.Header, error) {
	var headers []models.Header
	for _, input := range inputs {
		detected := security.DetectSensitive(input.Name, input.Value)
		masked := detected
		if input.Masked != nil {
			masked = *input.Masked
		}
		sensitive := masked
		if input.Sensitive != nil {
			sensitive = *input.Sensitive || masked
		}
		header := models.Header{Name: strings.TrimSpace(input.Name), Sensitive: sensitive, Masked: masked}
		if sensitive {
			if input.Value == "" || input.Value == "********" {
				kept, ok := findExistingSecret(existing, header.Name)
				if !ok {
					return nil, fmt.Errorf("sensitive header %q requires a value", header.Name)
				}
				header.ValueEncrypted = kept
			} else {
				if s.Secrets == nil {
					return nil, fmt.Errorf("ENCRYPTION_KEY is required to save sensitive headers")
				}
				encrypted, err := s.Secrets.Encrypt(input.Value)
				if err != nil {
					return nil, fmt.Errorf("could not encrypt sensitive header")
				}
				header.ValueEncrypted = &encrypted
			}
		} else {
			value := input.Value
			header.ValuePlain = &value
		}
		headers = append(headers, header)
	}
	return headers, nil
}

func findExistingSecret(existing *models.Endpoint, name string) (*string, bool) {
	if existing == nil {
		return nil, false
	}
	for _, header := range existing.Headers {
		if strings.EqualFold(header.Name, name) && header.ValueEncrypted != nil {
			return header.ValueEncrypted, true
		}
	}
	return nil, false
}

func (s *Server) prepareStoredProxy(ctx context.Context, input models.ProxyConfig, existing *models.ProxyConfig) (models.ProxyConfig, bool, error) {
	proxy, err := normalizedProxy(input)
	if err != nil {
		return models.ProxyConfig{}, false, err
	}
	if !proxy.Enabled {
		return proxy, false, nil
	}
	password := input.Password
	if proxy.Username == "" {
		if strings.TrimSpace(password) != "" && password != "********" {
			return models.ProxyConfig{}, false, fmt.Errorf("SOCKS5 proxy username is required when password is set")
		}
		return proxy, false, nil
	}
	if password == "" || password == "********" {
		if existing != nil && existing.PasswordEncrypted != nil {
			proxy.PasswordEncrypted = existing.PasswordEncrypted
			proxy.PasswordSet = true
			proxy.Password = "********"
			return proxy, true, nil
		}
		if input.PasswordSet {
			if copied, ok, err := s.lastProxyPassword(ctx, proxy); err != nil {
				return models.ProxyConfig{}, false, err
			} else if ok {
				proxy.PasswordEncrypted = copied.PasswordEncrypted
				proxy.PasswordSet = true
				proxy.Password = "********"
				return proxy, false, nil
			}
		}
		return models.ProxyConfig{}, false, fmt.Errorf("SOCKS5 proxy password is required when username is set")
	}
	if s.Secrets == nil {
		return models.ProxyConfig{}, false, fmt.Errorf("ENCRYPTION_KEY is required to save SOCKS5 proxy password")
	}
	encrypted, err := s.Secrets.Encrypt(password)
	if err != nil {
		return models.ProxyConfig{}, false, fmt.Errorf("could not encrypt SOCKS5 proxy password")
	}
	proxy.PasswordEncrypted = &encrypted
	proxy.PasswordSet = true
	proxy.Password = "********"
	return proxy, false, nil
}

func (s *Server) runtimeProxy(ctx context.Context, input models.ProxyConfig) (models.ProxyConfig, error) {
	proxy, err := normalizedProxy(input)
	if err != nil {
		return models.ProxyConfig{}, err
	}
	if !proxy.Enabled {
		return proxy, nil
	}
	if proxy.Username == "" {
		if strings.TrimSpace(input.Password) != "" && input.Password != "********" {
			return models.ProxyConfig{}, fmt.Errorf("SOCKS5 proxy username is required when password is set")
		}
		return proxy, nil
	}
	if input.Password == "" || input.Password == "********" {
		if input.PasswordSet {
			if copied, ok, err := s.lastProxyPassword(ctx, proxy); err != nil {
				return models.ProxyConfig{}, err
			} else if ok {
				proxy.PasswordEncrypted = copied.PasswordEncrypted
				proxy.PasswordSet = true
				proxy.Password = "********"
				return proxy, nil
			}
		}
		return models.ProxyConfig{}, fmt.Errorf("SOCKS5 proxy password is required before testing")
	}
	proxy.Password = input.Password
	proxy.PasswordSet = true
	return proxy, nil
}

func (s *Server) lastProxyPassword(ctx context.Context, proxy models.ProxyConfig) (models.ProxyConfig, bool, error) {
	last, err := s.Store.LastProxyConfig(ctx)
	if err != nil {
		if db.IsNotFound(err) {
			return models.ProxyConfig{}, false, nil
		}
		return models.ProxyConfig{}, false, fmt.Errorf("could not load last SOCKS5 proxy")
	}
	if !sameProxyIdentity(last, proxy) || last.PasswordEncrypted == nil {
		return models.ProxyConfig{}, false, nil
	}
	return last, true, nil
}

func sameProxyIdentity(a, b models.ProxyConfig) bool {
	return strings.EqualFold(strings.TrimSpace(a.Address), strings.TrimSpace(b.Address)) &&
		strings.TrimSpace(a.Username) == strings.TrimSpace(b.Username)
}

func normalizedProxy(input models.ProxyConfig) (models.ProxyConfig, error) {
	if !input.Enabled {
		return models.ProxyConfig{}, nil
	}
	address, err := validation.NormalizeSocks5ProxyAddress(input.Address)
	if err != nil {
		return models.ProxyConfig{}, err
	}
	username := strings.TrimSpace(input.Username)
	if len(username) > 255 {
		return models.ProxyConfig{}, fmt.Errorf("SOCKS5 proxy username is too long")
	}
	if len(input.Password) > 255 {
		return models.ProxyConfig{}, fmt.Errorf("SOCKS5 proxy password is too long")
	}
	return models.ProxyConfig{Enabled: true, Address: address, Username: username}, nil
}

func (s *Server) notificationSettingsFromRequest(r *http.Request, existing *models.NotificationSettings) (models.NotificationSettings, bool, bool, error) {
	var input models.NotificationSettingsInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		return models.NotificationSettings{}, false, false, fmt.Errorf("invalid JSON")
	}
	parseMode := input.TelegramParseMode
	if parseMode == "" {
		parseMode = "Markdown"
	}
	if parseMode != "None" && parseMode != "Markdown" && parseMode != "MarkdownV2" && parseMode != "HTML" {
		return models.NotificationSettings{}, false, false, fmt.Errorf("unsupported Telegram parse mode")
	}
	timezoneName, err := tz.Normalize(input.Timezone)
	if err != nil {
		return models.NotificationSettings{}, false, false, fmt.Errorf("unsupported timezone")
	}
	settings := models.NotificationSettings{
		TelegramEnabled:   input.TelegramEnabled,
		TelegramChatID:    input.TelegramChatID,
		TelegramParseMode: parseMode,
		Timezone:          timezoneName,
	}
	keepToken := input.TelegramAPIKey == nil || strings.TrimSpace(*input.TelegramAPIKey) == ""
	if !keepToken {
		if s.Secrets == nil {
			return models.NotificationSettings{}, false, false, fmt.Errorf("ENCRYPTION_KEY is required to save Telegram token")
		}
		encrypted, err := s.Secrets.Encrypt(*input.TelegramAPIKey)
		if err != nil {
			return models.NotificationSettings{}, false, false, fmt.Errorf("could not encrypt Telegram token")
		}
		settings.TelegramAPIKeyEncrypted = &encrypted
	}
	var existingProxy *models.ProxyConfig
	if existing != nil {
		existingProxy = &existing.Proxy
	}
	proxy, keepProxyPassword, err := s.prepareStoredProxy(r.Context(), input.Proxy, existingProxy)
	if err != nil {
		return models.NotificationSettings{}, false, false, err
	}
	settings.Proxy = proxy
	return settings, keepToken, keepProxyPassword, nil
}

func allowedMethod(method string) bool {
	switch method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
		return true
	default:
		return false
	}
}

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, http.ErrNoCookie) {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if db.IsNotFound(err) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "database error")
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
