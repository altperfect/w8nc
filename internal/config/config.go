package config

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"w8nc/internal/validation"
)

type Config struct {
	AppAddr                  string
	DatabaseURL              string
	AuthEnabled              bool
	SessionSecret            string
	SessionSecretConfigured  bool
	CookieSecure             bool
	EncryptionKey            []byte
	EncryptionKeyConfigured  bool
	NotifyBinPath            string
	NotifyProviderConfigPath string
	DefaultRequestTimeout    time.Duration
	DefaultMaxResponseBytes  int64
	SchedulerTickInterval    time.Duration
	PingWorkerConcurrency    int
	MinPingInterval          time.Duration
	MaxPingInterval          time.Duration
	ScreenshotsEnabled       bool
	ScreenshotChromePath     string
	ScreenshotStoragePath    string
	ScreenshotTimeout        time.Duration
	ScreenshotViewportWidth  int
	ScreenshotViewportHeight int
	ScreenshotMaxConcurrency int
	AllowPrivateTargets      bool
	LogLevel                 slog.Level
}

func Load() Config {
	minInterval, _ := validation.ParseInterval(env("MIN_PING_INTERVAL", "5s"))
	maxInterval, _ := validation.ParseInterval(env("MAX_PING_INTERVAL", "30d"))
	level := slog.LevelInfo
	switch strings.ToLower(env("LOG_LEVEL", "info")) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	key, configured := DecodeEncryptionKey(env("ENCRYPTION_KEY", ""))
	sessionSecret, sessionSecretConfigured := sessionSecret()
	return Config{
		AppAddr:                  env("APP_ADDR", "0.0.0.0:8080"),
		DatabaseURL:              env("DATABASE_URL", "postgres://pinger:pinger@localhost:5432/pinger?sslmode=disable"),
		AuthEnabled:              envBool("AUTH_ENABLED", true),
		SessionSecret:            sessionSecret,
		SessionSecretConfigured:  sessionSecretConfigured,
		CookieSecure:             envBool("COOKIE_SECURE", false),
		EncryptionKey:            key,
		EncryptionKeyConfigured:  configured,
		NotifyBinPath:            env("NOTIFY_BIN_PATH", "/usr/local/bin/notify"),
		NotifyProviderConfigPath: env("NOTIFY_PROVIDER_CONFIG_PATH", "/app/data/notify-provider.yaml"),
		DefaultRequestTimeout:    envDuration("DEFAULT_REQUEST_TIMEOUT", 10*time.Second),
		DefaultMaxResponseBytes:  envInt64("DEFAULT_MAX_RESPONSE_BYTES", 5242880),
		SchedulerTickInterval:    envDuration("SCHEDULER_TICK_INTERVAL", time.Second),
		PingWorkerConcurrency:    envInt("PING_WORKER_CONCURRENCY", 10),
		MinPingInterval:          minInterval,
		MaxPingInterval:          maxInterval,
		ScreenshotsEnabled:       envBool("SCREENSHOTS_ENABLED", true),
		ScreenshotChromePath:     env("SCREENSHOT_CHROME_PATH", "/usr/bin/chromium-browser"),
		ScreenshotStoragePath:    env("SCREENSHOT_STORAGE_PATH", "/app/data/screenshots"),
		ScreenshotTimeout:        envDuration("SCREENSHOT_TIMEOUT", 20*time.Second),
		ScreenshotViewportWidth:  envInt("SCREENSHOT_VIEWPORT_WIDTH", 1365),
		ScreenshotViewportHeight: envInt("SCREENSHOT_VIEWPORT_HEIGHT", 900),
		ScreenshotMaxConcurrency: envInt("SCREENSHOT_MAX_CONCURRENCY", 1),
		AllowPrivateTargets:      envBool("ALLOW_PRIVATE_TARGETS", false),
		LogLevel:                 level,
	}
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func envInt64(key string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func DecodeEncryptionKey(value string) ([]byte, bool) {
	if strings.TrimSpace(value) == "" {
		return nil, false
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err == nil && len(decoded) == 32 {
		return decoded, true
	}
	sum := sha256.Sum256([]byte(value))
	return sum[:], true
}

func sessionSecret() (string, bool) {
	if value := strings.TrimSpace(os.Getenv("SESSION_SECRET")); value != "" {
		return value, true
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err == nil {
		return base64.RawURLEncoding.EncodeToString(buf), false
	}
	sum := sha256.Sum256([]byte(time.Now().String()))
	return base64.RawURLEncoding.EncodeToString(sum[:]), false
}
