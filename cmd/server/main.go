package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"bug-bounty-endpoint-pinger/internal/api"
	"bug-bounty-endpoint-pinger/internal/auth"
	"bug-bounty-endpoint-pinger/internal/config"
	secretbox "bug-bounty-endpoint-pinger/internal/crypto"
	"bug-bounty-endpoint-pinger/internal/db"
	"bug-bounty-endpoint-pinger/internal/logging"
	"bug-bounty-endpoint-pinger/internal/models"
	"bug-bounty-endpoint-pinger/internal/notifier"
	"bug-bounty-endpoint-pinger/internal/pinger"
	"bug-bounty-endpoint-pinger/internal/scheduler"
	"bug-bounty-endpoint-pinger/internal/static"
)

// Docker runs the server on Linux, where SIGUSR1 is signal 10. Using the
// numeric value keeps local cross-target builds compiling when GOOS=windows.
const loginAttemptResetSignal = syscall.Signal(10)

func main() {
	cfg := config.Load()
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "set-password":
			password, err := setPassword(context.Background(), cfg)
			if err != nil {
				fmt.Fprintf(os.Stderr, "set-password failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Fprintf(os.Stdout, "New password: %s\n", password)
			fmt.Fprintln(os.Stdout, "Existing sessions were invalidated.")
			return
		case "rotate-encryption-key":
			count, err := rotateEncryptionKey(context.Background(), cfg)
			if err != nil {
				fmt.Fprintf(os.Stderr, "rotate-encryption-key failed: %v\n", err)
				os.Exit(1)
			}
			fmt.Fprintf(os.Stdout, "Rotated encrypted values: %d\n", count)
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
			os.Exit(2)
		}
	}

	logger := logging.New(cfg.LogLevel)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer store.Close()
	if err := store.Migrate(ctx, "migrations"); err != nil {
		logger.Error("database migration failed", "error", err)
		os.Exit(1)
	}
	resume, err := store.ResumeAfterDowntime(ctx, time.Now().UTC())
	if err != nil {
		logger.Error("runtime state resume failed", "error", err)
		os.Exit(1)
	}
	if resume.ExtendedEndpoints > 0 {
		logger.Info(
			"preserved endpoint deactivation deadlines after downtime",
			"downtime", resume.Downtime.String(),
			"endpoints", resume.ExtendedEndpoints,
		)
	}
	startRuntimeHeartbeat(ctx, store, logger, 5*time.Second)

	var secrets *secretbox.SecretBox
	if cfg.EncryptionKeyConfigured {
		secrets, err = secretbox.NewSecretBox(cfg.EncryptionKey)
		if err != nil {
			logger.Error("encryption setup failed", "error", err)
			os.Exit(1)
		}
	} else {
		hasSecrets, err := store.HasEncryptedSecrets(ctx)
		if err != nil {
			logger.Error("could not inspect encrypted secret state", "error", err)
			os.Exit(1)
		}
		if hasSecrets {
			logger.Error("ENCRYPTION_KEY is required because encrypted values already exist")
			os.Exit(1)
		}
		logger.Warn("ENCRYPTION_KEY is not configured; saving sensitive values will be rejected")
	}

	authManager := auth.NewManager(store, cfg.AuthEnabled, cfg.SessionSecret, cfg.CookieSecure)
	loginAttemptReset := make(chan os.Signal, 1)
	signal.Notify(loginAttemptReset, loginAttemptResetSignal)
	defer signal.Stop(loginAttemptReset)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-loginAttemptReset:
				cleared := authManager.RateLimiter.ResetAll()
				logger.Info("login attempt limiter reset", "entries", cleared)
			}
		}
	}()

	if cfg.AuthEnabled {
		bootstrap, err := authManager.BootstrapPassword(ctx)
		if err != nil {
			logger.Error("password bootstrap failed", "error", err)
			os.Exit(1)
		}
		if bootstrap.Generated {
			logger.Warn(
				"login password generated; save it now and change it in Settings",
				"password", bootstrap.Password,
				"legacy_reset", bootstrap.LegacyReset,
			)
		}
	} else {
		logger.Warn("authentication is disabled; keep the app bound to localhost unless protected another way")
	}
	if !cfg.SessionSecretConfigured {
		logger.Warn("SESSION_SECRET is not configured; active sessions will be invalidated on restart")
	}

	notify := notifier.New(cfg.NotifyBinPath, cfg.NotifyProviderConfigPath, 15*time.Second, secrets)
	status := notify.Check(ctx)
	if status.Status != "ok" {
		logger.Warn("notify binary is not ready", "status", status.Status, "message", status.Message)
	}

	ping := pinger.New(cfg.DefaultRequestTimeout, cfg.DefaultMaxResponseBytes, cfg.AllowPrivateTargets, secrets)
	sched := &scheduler.Service{
		Store: store, Pinger: ping, Notifier: notify, Tick: cfg.SchedulerTickInterval,
		Concurrency: cfg.PingWorkerConcurrency, LockFor: cfg.DefaultRequestTimeout + 5*time.Second,
		Logger: logger,
	}
	sched.Start(ctx)

	server := &http.Server{
		Addr: cfg.AppAddr,
		Handler: &api.Server{
			Config: cfg, Store: store, Auth: authManager, Scheduler: sched,
			Notifier: notify, Secrets: secrets, Static: static.Handler(),
		},
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("server listening", "addr", cfg.AppAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("server shutdown failed", "error", err)
	}
}

func setPassword(ctx context.Context, cfg config.Config) (string, error) {
	store, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return "", err
	}
	defer store.Close()
	if err := store.Migrate(ctx, "migrations"); err != nil {
		return "", err
	}
	authManager := auth.NewManager(store, true, cfg.SessionSecret, cfg.CookieSecure)
	return authManager.SetGeneratedPassword(ctx)
}

func rotateEncryptionKey(ctx context.Context, cfg config.Config) (int, error) {
	if !cfg.EncryptionKeyConfigured {
		return 0, fmt.Errorf("ENCRYPTION_KEY is required as the current key")
	}
	newKey, ok := config.DecodeEncryptionKey(os.Getenv("NEW_ENCRYPTION_KEY"))
	if !ok {
		return 0, fmt.Errorf("NEW_ENCRYPTION_KEY must be set")
	}
	if string(newKey) == string(cfg.EncryptionKey) {
		return 0, fmt.Errorf("NEW_ENCRYPTION_KEY must differ from ENCRYPTION_KEY")
	}
	oldBox, err := secretbox.NewSecretBox(cfg.EncryptionKey)
	if err != nil {
		return 0, err
	}
	newBox, err := secretbox.NewSecretBox(newKey)
	if err != nil {
		return 0, err
	}
	store, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return 0, err
	}
	defer store.Close()
	if err := store.Migrate(ctx, "migrations"); err != nil {
		return 0, err
	}
	tx, err := store.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rotated := 0
	var telegramToken, settingsProxy *string
	if err := tx.QueryRow(ctx, `
		SELECT telegram_api_key_encrypted, proxy_password_encrypted
		FROM notification_settings
		WHERE id=1`).Scan(&telegramToken, &settingsProxy); err != nil {
		return 0, err
	}
	telegramToken, n, err := reencryptString(telegramToken, oldBox, newBox)
	if err != nil {
		return 0, fmt.Errorf("telegram token: %w", err)
	}
	rotated += n
	settingsProxy, n, err = reencryptString(settingsProxy, oldBox, newBox)
	if err != nil {
		return 0, fmt.Errorf("notification proxy password: %w", err)
	}
	rotated += n
	if _, err := tx.Exec(ctx, `
		UPDATE notification_settings
		SET telegram_api_key_encrypted=$1, proxy_password_encrypted=$2, updated_at=now()
		WHERE id=1`, telegramToken, settingsProxy); err != nil {
		return 0, err
	}

	type endpointSecretRow struct {
		id            string
		headersJSON   []byte
		proxyPassword *string
	}
	rows, err := tx.Query(ctx, `SELECT id::text, headers, proxy_password_encrypted FROM endpoints`)
	if err != nil {
		return 0, err
	}
	var endpoints []endpointSecretRow
	for rows.Next() {
		var endpoint endpointSecretRow
		if err := rows.Scan(&endpoint.id, &endpoint.headersJSON, &endpoint.proxyPassword); err != nil {
			return 0, err
		}
		endpoints = append(endpoints, endpoint)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, endpoint := range endpoints {
		proxyPassword, n, err := reencryptString(endpoint.proxyPassword, oldBox, newBox)
		if err != nil {
			return 0, fmt.Errorf("endpoint %s proxy password: %w", endpoint.id, err)
		}
		rotated += n
		headersJSON, n, err := reencryptHeaders(endpoint.headersJSON, oldBox, newBox)
		if err != nil {
			return 0, fmt.Errorf("endpoint %s headers: %w", endpoint.id, err)
		}
		rotated += n
		if _, err := tx.Exec(ctx, `
			UPDATE endpoints
			SET headers=$2, proxy_password_encrypted=$3, updated_at=now(), version=version+1
			WHERE id=$1`, endpoint.id, headersJSON, proxyPassword); err != nil {
			return 0, err
		}
	}
	return rotated, tx.Commit(ctx)
}

func reencryptString(value *string, oldBox, newBox *secretbox.SecretBox) (*string, int, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return value, 0, nil
	}
	plain, err := oldBox.Decrypt(*value)
	if err != nil {
		return nil, 0, err
	}
	encrypted, err := newBox.Encrypt(plain)
	if err != nil {
		return nil, 0, err
	}
	return &encrypted, 1, nil
}

func reencryptHeaders(raw []byte, oldBox, newBox *secretbox.SecretBox) ([]byte, int, error) {
	var headers []models.Header
	if err := json.Unmarshal(raw, &headers); err != nil {
		return nil, 0, err
	}
	rotated := 0
	for i := range headers {
		encrypted, n, err := reencryptString(headers[i].ValueEncrypted, oldBox, newBox)
		if err != nil {
			return nil, 0, err
		}
		headers[i].ValueEncrypted = encrypted
		rotated += n
	}
	updated, err := json.Marshal(headers)
	if err != nil {
		return nil, 0, err
	}
	return updated, rotated, nil
}

func startRuntimeHeartbeat(ctx context.Context, store *db.Store, logger *slog.Logger, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := store.MarkRuntimeSeen(ctx, time.Now().UTC()); err != nil && ctx.Err() == nil {
					logger.Warn("runtime heartbeat failed", "error", err)
				}
			}
		}
	}()
}
