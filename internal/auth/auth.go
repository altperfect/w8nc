package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"w8nc/internal/db"

	"golang.org/x/crypto/bcrypt"
)

type contextKey string

const userContextKey contextKey = "user"
const singleUserName = "local"
const minPasswordLength = 12
const maxLoginFailures = 5

type Manager struct {
	Store         *db.Store
	Enabled       bool
	SessionSecret string
	CookieSecure  bool
	RateLimiter   *RateLimiter
}

type User struct {
	ID string `json:"id"`
}

type BootstrapResult struct {
	Generated   bool
	Password    string
	LegacyReset bool
}

func NewManager(store *db.Store, enabled bool, sessionSecret string, cookieSecure bool) *Manager {
	return &Manager{
		Store:         store,
		Enabled:       enabled,
		SessionSecret: sessionSecret,
		CookieSecure:  cookieSecure,
		RateLimiter:   NewRateLimiter(maxLoginFailures, 10*time.Minute),
	}
}

func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

func CheckPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func GeneratePassword() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (m *Manager) BootstrapPassword(ctx context.Context) (BootstrapResult, error) {
	user, err := m.Store.PrimaryUser(ctx)
	if err == nil && user.Username == singleUserName {
		return BootstrapResult{}, nil
	}
	firstUserErr := err
	password, err := GeneratePassword()
	if err != nil {
		return BootstrapResult{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return BootstrapResult{}, err
	}
	if firstUserErr != nil && !db.IsNotFound(firstUserErr) {
		return BootstrapResult{}, firstUserErr
	}
	if db.IsNotFound(firstUserErr) {
		if _, err := m.Store.CreateSingleUser(ctx, singleUserName, hash); err != nil {
			return BootstrapResult{}, err
		}
		return BootstrapResult{Generated: true, Password: password}, nil
	}
	if err := m.Store.ResetToSingleUser(ctx, user.ID, singleUserName, hash); err != nil {
		return BootstrapResult{}, err
	}
	return BootstrapResult{Generated: true, Password: password, LegacyReset: true}, nil
}

func (m *Manager) SetGeneratedPassword(ctx context.Context) (string, error) {
	password, err := GeneratePassword()
	if err != nil {
		return "", err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return "", err
	}
	user, err := m.Store.PrimaryUser(ctx)
	if err != nil {
		if !db.IsNotFound(err) {
			return "", err
		}
		if _, err := m.Store.CreateSingleUser(ctx, singleUserName, hash); err != nil {
			return "", err
		}
		m.RateLimiter.ResetAll()
		return password, nil
	}
	if err := m.Store.ResetToSingleUser(ctx, user.ID, singleUserName, hash); err != nil {
		return "", err
	}
	m.RateLimiter.ResetAll()
	return password, nil
}

func (m *Manager) Login(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	ip := clientIP(r)
	if m.RateLimiter.Limited(ip) {
		writeAuthError(w, http.StatusTooManyRequests, "too many failed login attempts")
		return
	}
	user, err := m.Store.PrimaryUser(r.Context())
	if err != nil || !CheckPassword(user.PasswordHash, request.Password) {
		m.RateLimiter.RecordFailure(ip)
		writeAuthError(w, http.StatusUnauthorized, "invalid password")
		return
	}
	m.RateLimiter.Reset(ip)
	token, err := randomToken()
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	expiresAt := time.Now().Add(7 * 24 * time.Hour)
	if err := m.Store.CreateSession(r.Context(), user.ID, m.tokenHash(token), expiresAt); err != nil {
		writeAuthError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	http.SetCookie(w, m.cookie(token, expiresAt))
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"auth_enabled":  true,
	})
}

func (m *Manager) ChangePassword(w http.ResponseWriter, r *http.Request) {
	var request struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if len(request.NewPassword) < minPasswordLength {
		writeAuthError(w, http.StatusBadRequest, fmt.Sprintf("new password must be at least %d characters", minPasswordLength))
		return
	}
	sessionUser, ok := UserFromContext(r.Context())
	if !ok {
		writeAuthError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	user, err := m.Store.UserByID(r.Context(), sessionUser.ID)
	if err != nil || !CheckPassword(user.PasswordHash, request.CurrentPassword) {
		writeAuthError(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}
	hash, err := HashPassword(request.NewPassword)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "could not update password")
		return
	}
	keepSessionHash := ""
	if cookie, err := r.Cookie("pinger_session"); err == nil {
		keepSessionHash = m.tokenHash(cookie.Value)
	}
	if err := m.Store.UpdateUserPassword(r.Context(), user.ID, hash, keepSessionHash); err != nil {
		writeAuthError(w, http.StatusInternalServerError, "could not update password")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (m *Manager) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("pinger_session"); err == nil {
		_ = m.Store.DeleteSession(r.Context(), m.tokenHash(cookie.Value))
	}
	expired := m.cookie("", time.Now().Add(-time.Hour))
	expired.MaxAge = -1
	http.SetCookie(w, expired)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (m *Manager) Me(w http.ResponseWriter, r *http.Request) {
	if !m.Enabled {
		writeJSON(w, http.StatusOK, map[string]any{
			"authenticated": true,
			"auth_enabled":  false,
			"warning":       "Authentication is disabled. Bind the app to localhost or enable auth before exposing it.",
		})
		return
	}
	_, ok := UserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"authenticated": false,
			"auth_enabled":  true,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"auth_enabled":  true,
	})
}

func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !m.Enabled {
			next.ServeHTTP(w, r)
			return
		}
		user, ok := m.userFromRequest(r)
		if !ok {
			writeAuthError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userContextKey, user)))
	})
}

func (m *Manager) Optional(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.Enabled {
			if user, ok := m.userFromRequest(r); ok {
				r = r.WithContext(context.WithValue(r.Context(), userContextKey, user))
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Manager) userFromRequest(r *http.Request) (User, bool) {
	cookie, err := r.Cookie("pinger_session")
	if err != nil || cookie.Value == "" {
		return User{}, false
	}
	dbUser, err := m.Store.UserBySession(r.Context(), m.tokenHash(cookie.Value))
	if err != nil {
		return User{}, false
	}
	return User{ID: dbUser.ID}, true
}

func (m *Manager) tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token + ":" + m.SessionSecret))
	return hex.EncodeToString(sum[:])
}

func (m *Manager) cookie(token string, expiresAt time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     "pinger_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   m.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
	}
}

func UserFromContext(ctx context.Context) (User, bool) {
	user, ok := ctx.Value(userContextKey).(User)
	return user, ok
}

type RateLimiter struct {
	limit  int
	window time.Duration
	mu     sync.Mutex
	items  map[string]attempts
}

type attempts struct {
	Count     int
	FirstSeen time.Time
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{limit: limit, window: window, items: make(map[string]attempts)}
}

func (r *RateLimiter) Limited(ip string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := limiterKey(ip)
	item, ok := r.items[key]
	if !ok {
		return false
	}
	if time.Since(item.FirstSeen) > r.window {
		delete(r.items, key)
		return false
	}
	return item.Count >= r.limit
}

func (r *RateLimiter) RecordFailure(ip string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := limiterKey(ip)
	item := r.items[key]
	if item.FirstSeen.IsZero() || time.Since(item.FirstSeen) > r.window {
		item = attempts{FirstSeen: time.Now()}
	}
	item.Count++
	r.items[key] = item
}

func (r *RateLimiter) Reset(ip string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.items, limiterKey(ip))
}

func (r *RateLimiter) ResetAll() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := len(r.items)
	r.items = make(map[string]attempts)
	return count
}

func limiterKey(ip string) string {
	return ip
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func writeAuthError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
