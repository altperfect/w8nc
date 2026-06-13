package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"bug-bounty-endpoint-pinger/internal/models"
	tz "bug-bounty-endpoint-pinger/internal/timezone"
	"bug-bounty-endpoint-pinger/internal/validation"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	Pool *pgxpool.Pool
}

type RuntimeResume struct {
	Downtime          time.Duration
	ExtendedEndpoints int64
}

func Connect(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{Pool: pool}, nil
}

func (s *Store) Close() {
	if s != nil && s.Pool != nil {
		s.Pool.Close()
	}
}

func (s *Store) Ping(ctx context.Context) error {
	return s.Pool.Ping(ctx)
}

func (s *Store) Migrate(ctx context.Context, dir string) error {
	if _, err := s.Pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".sql") {
			names = append(names, file.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		var exists bool
		if err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version=$1)`, name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		sqlBytes, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		tx, err := s.Pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, name); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

type User struct {
	ID           string
	Username     string
	PasswordHash string
}

func (s *Store) CreateSingleUser(ctx context.Context, username, passwordHash string) (User, error) {
	var user User
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash)
		VALUES ($1, $2)
		RETURNING id::text, username, password_hash`, username, passwordHash).
		Scan(&user.ID, &user.Username, &user.PasswordHash)
	return user, err
}

func (s *Store) PrimaryUser(ctx context.Context) (User, error) {
	var user User
	err := s.Pool.QueryRow(ctx, `
		SELECT id::text, username, password_hash
		FROM users
		ORDER BY created_at ASC, id ASC
		LIMIT 1`).
		Scan(&user.ID, &user.Username, &user.PasswordHash)
	return user, err
}

func (s *Store) UserByID(ctx context.Context, id string) (User, error) {
	var user User
	err := s.Pool.QueryRow(ctx, `SELECT id::text, username, password_hash FROM users WHERE id=$1`, id).
		Scan(&user.ID, &user.Username, &user.PasswordHash)
	return user, err
}

func (s *Store) CreateSession(ctx context.Context, userID, tokenHash string, expiresAt time.Time) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO sessions (user_id, token_hash, expires_at) VALUES ($1, $2, $3)`, userID, tokenHash, expiresAt)
	return err
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash=$1`, tokenHash)
	return err
}

func (s *Store) ResetToSingleUser(ctx context.Context, id, username, passwordHash string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM users WHERE id<>$1`, id); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE users
		SET username=$2, password_hash=$3, updated_at=now()
		WHERE id=$1`, id, username, passwordHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) UpdateUserPassword(ctx context.Context, id, passwordHash, keepSessionHash string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE users SET password_hash=$2, updated_at=now() WHERE id=$1`, id, passwordHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	if keepSessionHash == "" {
		_, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1`, id)
	} else {
		_, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id=$1 AND token_hash<>$2`, id, keepSessionHash)
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) UserBySession(ctx context.Context, tokenHash string) (User, error) {
	var user User
	err := s.Pool.QueryRow(ctx, `
		SELECT u.id::text, u.username, u.password_hash
		FROM sessions s
		JOIN users u ON u.id=s.user_id
		WHERE s.token_hash=$1 AND s.expires_at > now()`, tokenHash).
		Scan(&user.ID, &user.Username, &user.PasswordHash)
	return user, err
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= now()`)
	return err
}

func (s *Store) HasEncryptedSecrets(ctx context.Context) (bool, error) {
	var has bool
	err := s.Pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1
				FROM notification_settings
				WHERE telegram_api_key_encrypted IS NOT NULL
				  AND telegram_api_key_encrypted <> ''
			)
			OR EXISTS (
				SELECT 1
				FROM notification_settings
				WHERE proxy_password_encrypted IS NOT NULL
				  AND proxy_password_encrypted <> ''
			)
			OR EXISTS (
				SELECT 1
				FROM endpoints
				WHERE proxy_password_encrypted IS NOT NULL
				  AND proxy_password_encrypted <> ''
			)
			OR EXISTS (
				SELECT 1
				FROM endpoints e
				CROSS JOIN LATERAL jsonb_array_elements(e.headers) AS h
				WHERE COALESCE(h->>'value_encrypted', '') <> ''
			)`).Scan(&has)
	return has, err
}

type ListEndpointsParams struct {
	Page     int
	PageSize int
	Sort     string
	State    string
	Active   *bool
	Method   string
	Search   string
}

func (p *ListEndpointsParams) Normalize() {
	if p.Page < 1 {
		p.Page = 1
	}
	allowedPage := map[int]bool{20: true, 50: true, 100: true, 250: true}
	if !allowedPage[p.PageSize] {
		p.PageSize = 20
	}
	if _, ok := sortExpressions[p.Sort]; !ok {
		p.Sort = "created_desc"
	}
	p.Method = strings.ToUpper(strings.TrimSpace(p.Method))
	p.State = strings.TrimSpace(p.State)
	p.Search = strings.TrimSpace(p.Search)
}

var sortExpressions = map[string]string{
	"created_desc":      "created_at DESC",
	"created_asc":       "created_at ASC",
	"updated_desc":      "updated_at DESC",
	"updated_asc":       "updated_at ASC",
	"active_desc":       "active DESC, created_at DESC",
	"active_asc":        "active ASC, created_at DESC",
	"state_asc":         "state ASC, created_at DESC",
	"state_desc":        "state DESC, created_at DESC",
	"last_checked_desc": "last_checked_at DESC NULLS LAST",
	"last_checked_asc":  "last_checked_at ASC NULLS LAST",
}

func (s *Store) ListEndpoints(ctx context.Context, params ListEndpointsParams) ([]models.Endpoint, int, error) {
	if _, err := s.DeactivateExpiredEndpoints(ctx); err != nil {
		return nil, 0, err
	}
	params.Normalize()
	where, args := endpointWhere(params)
	countSQL := `SELECT count(*) FROM endpoints ` + where
	var total int
	if err := s.Pool.QueryRow(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, params.PageSize, (params.Page-1)*params.PageSize)
	query := `SELECT ` + endpointColumns + ` FROM endpoints ` + where + ` ORDER BY ` + sortExpressions[params.Sort] + ` LIMIT $` + strconv.Itoa(len(args)-1) + ` OFFSET $` + strconv.Itoa(len(args))
	rows, err := s.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items, err := scanEndpoints(rows)
	return items, total, err
}

func endpointWhere(params ListEndpointsParams) (string, []any) {
	var clauses []string
	var args []any
	if params.State != "" {
		args = append(args, params.State)
		clauses = append(clauses, fmt.Sprintf("state=$%d", len(args)))
	}
	if params.Active != nil {
		args = append(args, *params.Active)
		clauses = append(clauses, fmt.Sprintf("active=$%d", len(args)))
	}
	if params.Method != "" {
		args = append(args, params.Method)
		clauses = append(clauses, fmt.Sprintf("http_method=$%d", len(args)))
	}
	if params.Search != "" {
		args = append(args, "%"+params.Search+"%")
		clauses = append(clauses, fmt.Sprintf("(url ILIKE $%d OR name ILIKE $%d)", len(args), len(args)))
	}
	if len(clauses) == 0 {
		return "", args
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

type EndpointRecord struct {
	Name                   *string
	URL                    string
	HTTPMethod             string
	Headers                []models.Header
	RequestBodyEnabled     bool
	RequestBody            string
	Proxy                  models.ProxyConfig
	PingIntervalSeconds    int
	DeactivateAfterSeconds *int
	NotifyCondition        models.Condition
	NotificationTemplate   string
	Active                 bool
}

func (s *Store) CreateEndpoint(ctx context.Context, record EndpointRecord) (models.Endpoint, error) {
	headersJSON, conditionJSON, err := marshalJSON(record.Headers, record.NotifyCondition)
	if err != nil {
		return models.Endpoint{}, err
	}
	now := time.Now().UTC()
	var nextRun any
	var deactivateAt any
	if record.Active {
		nextRun = now
		deactivateAt = deactivateDeadline(now, record.DeactivateAfterSeconds)
	}
	state := "unknown"
	if !record.Active {
		state = "deactivated"
	}
	row := s.Pool.QueryRow(ctx, `
		INSERT INTO endpoints (name, url, http_method, headers, request_body_enabled, request_body, proxy_enabled, proxy_address, proxy_username, proxy_password_encrypted, ping_interval_seconds, deactivate_after_seconds, notify_condition, notification_template, active, state, next_run_at, deactivate_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		RETURNING `+endpointColumns,
		record.Name, record.URL, record.HTTPMethod, headersJSON, record.RequestBodyEnabled, record.RequestBody,
		record.Proxy.Enabled, nullString(record.Proxy.Address), nullString(record.Proxy.Username),
		record.Proxy.PasswordEncrypted, record.PingIntervalSeconds,
		record.DeactivateAfterSeconds, conditionJSON, record.NotificationTemplate, record.Active, state, nextRun, deactivateAt)
	return scanEndpoint(row)
}

func (s *Store) GetEndpoint(ctx context.Context, id string) (models.Endpoint, error) {
	if _, err := s.DeactivateExpiredEndpoints(ctx); err != nil {
		return models.Endpoint{}, err
	}
	return scanEndpoint(s.Pool.QueryRow(ctx, `SELECT `+endpointColumns+` FROM endpoints WHERE id=$1`, id))
}

func (s *Store) UpdateEndpoint(ctx context.Context, id string, record EndpointRecord, intervalChanged bool) (models.Endpoint, error) {
	current, err := s.GetEndpoint(ctx, id)
	if err != nil {
		return models.Endpoint{}, err
	}
	headersJSON, conditionJSON, err := marshalJSON(record.Headers, record.NotifyCondition)
	if err != nil {
		return models.Endpoint{}, err
	}
	now := time.Now().UTC()
	var nextRun any
	var deactivateAt any
	if record.Active {
		if !current.Active || intervalChanged {
			nextRun = now
		} else {
			nextRun = current.NextRunAt
		}
		deactivateAt = deactivateDeadline(now, record.DeactivateAfterSeconds)
	}
	state := current.State
	reason := current.DeactivatedReason
	notifiedAt := current.NotifiedAt
	if record.Active && !current.Active {
		state = "unknown"
		reason = nil
		notifiedAt = nil
	}
	if !record.Active {
		state = "deactivated"
		manual := "manual"
		reason = &manual
	}
	row := s.Pool.QueryRow(ctx, `
		UPDATE endpoints
		SET name=$2, url=$3, http_method=$4, headers=$5, request_body_enabled=$6,
		    request_body=$7, proxy_enabled=$8, proxy_address=$9, proxy_username=$10,
		    proxy_password_encrypted=$11, ping_interval_seconds=$12,
		    deactivate_after_seconds=$13, notify_condition=$14,
		    notification_template=$15, active=$16, state=$17, next_run_at=$18,
		    deactivate_at=$19, deactivated_reason=$20, notified_at=$21,
		    updated_at=now(), version=version+1, locked_until=NULL
		WHERE id=$1
		RETURNING `+endpointColumns,
		id, record.Name, record.URL, record.HTTPMethod, headersJSON, record.RequestBodyEnabled,
		record.RequestBody, record.Proxy.Enabled, nullString(record.Proxy.Address),
		nullString(record.Proxy.Username), record.Proxy.PasswordEncrypted, record.PingIntervalSeconds,
		record.DeactivateAfterSeconds, conditionJSON, record.NotificationTemplate, record.Active,
		state, nextRun, deactivateAt, reason, notifiedAt)
	return scanEndpoint(row)
}

func (s *Store) DeleteEndpoint(ctx context.Context, id string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM endpoints WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Store) ActivateEndpoint(ctx context.Context, id string) (models.Endpoint, error) {
	row := s.Pool.QueryRow(ctx, `
		UPDATE endpoints
		SET active=TRUE, state='unknown', next_run_at=now(), notified_at=NULL,
		    deactivate_at=CASE
		        WHEN deactivate_after_seconds IS NULL THEN NULL
		        ELSE now() + make_interval(secs => deactivate_after_seconds)
		    END,
		    deactivated_reason=NULL, locked_until=NULL, updated_at=now(), version=version+1
		WHERE id=$1
		RETURNING `+endpointColumns, id)
	return scanEndpoint(row)
}

func (s *Store) DeactivateEndpoint(ctx context.Context, id string) (models.Endpoint, error) {
	row := s.Pool.QueryRow(ctx, `
		UPDATE endpoints
		SET active=FALSE, state='deactivated', next_run_at=NULL, deactivate_at=NULL,
		    deactivated_reason='manual', locked_until=NULL, updated_at=now(), version=version+1
		WHERE id=$1
		RETURNING `+endpointColumns, id)
	return scanEndpoint(row)
}

func (s *Store) LockDueEndpoints(ctx context.Context, limit int, lockFor time.Duration) ([]models.Endpoint, error) {
	if _, err := s.DeactivateExpiredEndpoints(ctx); err != nil {
		return nil, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		SELECT `+endpointColumns+`
		FROM endpoints
		WHERE active=TRUE
		  AND next_run_at IS NOT NULL
		  AND next_run_at <= now()
		  AND (locked_until IS NULL OR locked_until < now())
		ORDER BY next_run_at ASC
		LIMIT $1
		FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return nil, err
	}
	endpoints, err := scanEndpoints(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, endpoint := range endpoints {
		if _, err := tx.Exec(ctx, `UPDATE endpoints SET locked_until=now()+$2::interval WHERE id=$1`, endpoint.ID, pgInterval(lockFor)); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return endpoints, nil
}

func (s *Store) DeactivateExpiredEndpoints(ctx context.Context) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE endpoints
		SET active=FALSE, state='deactivated', next_run_at=NULL, deactivate_at=NULL,
		    deactivated_reason='time_limit_expired', locked_until=NULL, updated_at=now(), version=version+1
		WHERE active=TRUE
		  AND deactivate_at IS NOT NULL
		  AND deactivate_at <= now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (s *Store) ResumeAfterDowntime(ctx context.Context, now time.Time) (RuntimeResume, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return RuntimeResume{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var lastSeen time.Time
	err = tx.QueryRow(ctx, `SELECT last_seen_at FROM app_runtime_state WHERE id=1 FOR UPDATE`).Scan(&lastSeen)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := tx.Exec(ctx, `
			INSERT INTO app_runtime_state (id, last_seen_at, updated_at)
			VALUES (1, $1, now())
			ON CONFLICT (id) DO UPDATE
			SET last_seen_at=$1, updated_at=now()`, now); err != nil {
			return RuntimeResume{}, err
		}
		return RuntimeResume{}, tx.Commit(ctx)
	}
	if err != nil {
		return RuntimeResume{}, err
	}

	resume := RuntimeResume{}
	if now.After(lastSeen) {
		resume.Downtime = now.Sub(lastSeen)
		tag, err := tx.Exec(ctx, `
			UPDATE endpoints
			SET deactivate_at = deactivate_at + $1::interval
			WHERE active=TRUE
			  AND deactivate_at IS NOT NULL`, pgInterval(resume.Downtime))
		if err != nil {
			return RuntimeResume{}, err
		}
		resume.ExtendedEndpoints = tag.RowsAffected()
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO app_runtime_state (id, last_seen_at, updated_at)
		VALUES (1, $1, now())
		ON CONFLICT (id) DO UPDATE
		SET last_seen_at=$1, updated_at=now()`, now); err != nil {
		return RuntimeResume{}, err
	}
	return resume, tx.Commit(ctx)
}

func (s *Store) MarkRuntimeSeen(ctx context.Context, at time.Time) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO app_runtime_state (id, last_seen_at, updated_at)
		VALUES (1, $1, now())
		ON CONFLICT (id) DO UPDATE
		SET last_seen_at=$1, updated_at=now()`, at)
	return err
}

func (s *Store) RecordPingResult(ctx context.Context, endpoint models.Endpoint, result models.PingResult, conditionMatched bool, message string, nextRun *time.Time, baselineStatus *int, baselineLength *int64, manual bool) (string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var checkID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO endpoint_checks (endpoint_id, started_at, finished_at, status_code, response_length, duration_ms, error, truncated, condition_matched)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id::text`,
		endpoint.ID, result.StartedAt, result.FinishedAt, result.StatusCode, result.ResponseLength, result.DurationMS, result.Error, result.Truncated, conditionMatched).Scan(&checkID); err != nil {
		return "", err
	}
	state := StateForResult(endpoint.Active || manual, result)
	if conditionMatched && endpoint.Active {
		var eventID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO notification_events (endpoint_id, status, message)
			VALUES ($1, 'pending', $2)
			RETURNING id::text`, endpoint.ID, message).Scan(&eventID); err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `UPDATE endpoint_checks SET notification_event_id=$1 WHERE id=$2`, eventID, checkID); err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE endpoints
			SET active=FALSE, state='deactivated', last_checked_at=$2, last_status_code=$3,
			    last_response_length=$4, last_error=$5, last_duration_ms=$6,
			    baseline_status_code=$7, baseline_response_length=$8, next_run_at=NULL,
			    deactivate_at=NULL, notified_at=now(), deactivated_reason='notify_once_condition_matched',
			    locked_until=NULL, updated_at=now(), version=version+1
			WHERE id=$1`,
			endpoint.ID, result.FinishedAt, result.StatusCode, result.ResponseLength, result.Error, result.DurationMS, baselineStatus, baselineLength); err != nil {
			return "", err
		}
	} else {
		if !endpoint.Active {
			nextRun = nil
			if !manual {
				state = "deactivated"
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE endpoints
			SET state=$2, last_checked_at=$3, last_status_code=$4,
			    last_response_length=$5, last_error=$6, last_duration_ms=$7,
			    baseline_status_code=$8, baseline_response_length=$9, next_run_at=$10,
			    locked_until=NULL, updated_at=now(), version=version+1
			WHERE id=$1`,
			endpoint.ID, state, result.FinishedAt, result.StatusCode, result.ResponseLength, result.Error,
			result.DurationMS, baselineStatus, baselineLength, nextRun); err != nil {
			return "", err
		}
	}
	return checkID, tx.Commit(ctx)
}

func StateForResult(active bool, result models.PingResult) string {
	if !active {
		return "deactivated"
	}
	if result.StatusCode == nil {
		return "offline"
	}
	if *result.StatusCode >= 100 && *result.StatusCode <= 399 {
		return "live"
	}
	return "warning"
}

func (s *Store) ListChecks(ctx context.Context, endpointID string, page, pageSize int) ([]models.EndpointCheck, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 250 {
		pageSize = 20
	}
	var total int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM endpoint_checks WHERE endpoint_id=$1`, endpointID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT id::text, endpoint_id::text, started_at, finished_at, status_code, response_length,
		       duration_ms, error, truncated, condition_matched, notification_event_id::text, created_at
		FROM endpoint_checks
		WHERE endpoint_id=$1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`, endpointID, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	checks := make([]models.EndpointCheck, 0)
	for rows.Next() {
		var check models.EndpointCheck
		var status sql.NullInt64
		var length sql.NullInt64
		var checkErr sql.NullString
		var eventID sql.NullString
		if err := rows.Scan(&check.ID, &check.EndpointID, &check.StartedAt, &check.FinishedAt, &status, &length, &check.DurationMS, &checkErr, &check.Truncated, &check.ConditionMatched, &eventID, &check.CreatedAt); err != nil {
			return nil, 0, err
		}
		check.StatusCode = intPtrFromNull(status)
		check.ResponseLength = int64PtrFromNull(length)
		check.Error = stringPtrFromNull(checkErr)
		check.NotificationEventID = stringPtrFromNull(eventID)
		checks = append(checks, check)
	}
	return checks, total, rows.Err()
}

func (s *Store) GetNotificationSettings(ctx context.Context) (models.NotificationSettings, error) {
	var settings models.NotificationSettings
	var token sql.NullString
	var chatID sql.NullString
	var timezoneName sql.NullString
	var proxyAddress sql.NullString
	var proxyUsername sql.NullString
	var proxyPassword sql.NullString
	err := s.Pool.QueryRow(ctx, `
		SELECT telegram_enabled, telegram_api_key_encrypted, telegram_chat_id, telegram_parse_mode,
		       timezone, proxy_enabled, proxy_address, proxy_username, proxy_password_encrypted
		FROM notification_settings WHERE id=1`).
		Scan(&settings.TelegramEnabled, &token, &chatID, &settings.TelegramParseMode,
			&timezoneName, &settings.Proxy.Enabled, &proxyAddress, &proxyUsername, &proxyPassword)
	settings.TelegramAPIKeyEncrypted = stringPtrFromNull(token)
	settings.TelegramAPIKeySet = token.Valid && token.String != ""
	settings.TelegramChatID = stringPtrFromNull(chatID)
	settings.Timezone = stringFromNull(timezoneName)
	if settings.Timezone == "" {
		settings.Timezone = tz.DefaultName()
	}
	settings.Proxy.Address = stringFromNull(proxyAddress)
	settings.Proxy.Username = stringFromNull(proxyUsername)
	settings.Proxy.PasswordEncrypted = stringPtrFromNull(proxyPassword)
	settings.Proxy.PasswordSet = proxyPassword.Valid && proxyPassword.String != ""
	if settings.Proxy.PasswordSet {
		settings.Proxy.Password = "********"
	}
	return settings, err
}

func (s *Store) UpdateNotificationSettings(ctx context.Context, input models.NotificationSettings, keepToken bool, keepProxyPassword bool) (models.NotificationSettings, error) {
	var row pgx.Row
	if keepToken && keepProxyPassword {
		row = s.Pool.QueryRow(ctx, `
			UPDATE notification_settings
			SET telegram_enabled=$1, telegram_chat_id=$2, telegram_parse_mode=$3,
			    timezone=$4, proxy_enabled=$5, proxy_address=$6, proxy_username=$7, updated_at=now()
			WHERE id=1
			RETURNING telegram_enabled, telegram_api_key_encrypted, telegram_chat_id, telegram_parse_mode,
			          timezone, proxy_enabled, proxy_address, proxy_username, proxy_password_encrypted`,
			input.TelegramEnabled, input.TelegramChatID, input.TelegramParseMode, input.Timezone, input.Proxy.Enabled,
			nullString(input.Proxy.Address), nullString(input.Proxy.Username))
	} else {
		row = s.Pool.QueryRow(ctx, `
			UPDATE notification_settings
			SET telegram_enabled=$1,
			    telegram_api_key_encrypted=CASE WHEN $2 THEN telegram_api_key_encrypted ELSE $3 END,
			    telegram_chat_id=$4, telegram_parse_mode=$5, timezone=$6, proxy_enabled=$7,
			    proxy_address=$8, proxy_username=$9,
			    proxy_password_encrypted=CASE WHEN $10 THEN proxy_password_encrypted ELSE $11 END,
			    updated_at=now()
			WHERE id=1
			RETURNING telegram_enabled, telegram_api_key_encrypted, telegram_chat_id, telegram_parse_mode,
			          timezone, proxy_enabled, proxy_address, proxy_username, proxy_password_encrypted`,
			input.TelegramEnabled, keepToken, input.TelegramAPIKeyEncrypted, input.TelegramChatID, input.TelegramParseMode,
			input.Timezone, input.Proxy.Enabled, nullString(input.Proxy.Address), nullString(input.Proxy.Username),
			keepProxyPassword, input.Proxy.PasswordEncrypted)
	}
	var settings models.NotificationSettings
	var token sql.NullString
	var chatID sql.NullString
	var timezoneName sql.NullString
	var proxyAddress sql.NullString
	var proxyUsername sql.NullString
	var proxyPassword sql.NullString
	if err := row.Scan(&settings.TelegramEnabled, &token, &chatID, &settings.TelegramParseMode,
		&timezoneName, &settings.Proxy.Enabled, &proxyAddress, &proxyUsername, &proxyPassword); err != nil {
		return settings, err
	}
	settings.TelegramAPIKeyEncrypted = stringPtrFromNull(token)
	settings.TelegramAPIKeySet = token.Valid && token.String != ""
	settings.TelegramChatID = stringPtrFromNull(chatID)
	settings.Timezone = stringFromNull(timezoneName)
	if settings.Timezone == "" {
		settings.Timezone = tz.DefaultName()
	}
	settings.Proxy.Address = stringFromNull(proxyAddress)
	settings.Proxy.Username = stringFromNull(proxyUsername)
	settings.Proxy.PasswordEncrypted = stringPtrFromNull(proxyPassword)
	settings.Proxy.PasswordSet = proxyPassword.Valid && proxyPassword.String != ""
	if settings.Proxy.PasswordSet {
		settings.Proxy.Password = "********"
	}
	return settings, nil
}

func (s *Store) LastProxyConfig(ctx context.Context) (models.ProxyConfig, error) {
	var proxy models.ProxyConfig
	var address sql.NullString
	var username sql.NullString
	var password sql.NullString
	err := s.Pool.QueryRow(ctx, `
		SELECT proxy_address, proxy_username, proxy_password_encrypted
		FROM (
			SELECT proxy_address, proxy_username, proxy_password_encrypted, updated_at
			FROM endpoints
			WHERE proxy_enabled=TRUE
			  AND COALESCE(proxy_address, '') <> ''
			UNION ALL
			SELECT proxy_address, proxy_username, proxy_password_encrypted, updated_at
			FROM notification_settings
			WHERE proxy_enabled=TRUE
			  AND COALESCE(proxy_address, '') <> ''
		) proxies
		ORDER BY updated_at DESC
		LIMIT 1`).
		Scan(&address, &username, &password)
	if err != nil {
		return proxy, err
	}
	proxy.Enabled = true
	proxy.Address = stringFromNull(address)
	proxy.Username = stringFromNull(username)
	proxy.PasswordEncrypted = stringPtrFromNull(password)
	proxy.PasswordSet = password.Valid && password.String != ""
	if proxy.PasswordSet {
		proxy.Password = "********"
	}
	return proxy, nil
}

func (s *Store) DueNotificationEvents(ctx context.Context, limit int) ([]models.NotificationEvent, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id::text, endpoint_id::text, status, message, error, attempts, created_at, sent_at, updated_at
		FROM notification_events
		WHERE status='pending'
		   OR (status='failed' AND attempts < 5 AND updated_at < now() - make_interval(secs => LEAST(300, POWER(2, attempts)::int * 10)))
		ORDER BY created_at ASC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]models.NotificationEvent, 0)
	for rows.Next() {
		var event models.NotificationEvent
		var errText sql.NullString
		var sentAt sql.NullTime
		if err := rows.Scan(&event.ID, &event.EndpointID, &event.Status, &event.Message, &errText, &event.Attempts, &event.CreatedAt, &sentAt, &event.UpdatedAt); err != nil {
			return nil, err
		}
		event.Error = stringPtrFromNull(errText)
		event.SentAt = timePtrFromNull(sentAt)
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) MarkNotificationSent(ctx context.Context, id string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE notification_events SET status='sent', sent_at=now(), error=NULL, updated_at=now() WHERE id=$1`, id)
	return err
}

func (s *Store) MarkNotificationFailed(ctx context.Context, id string, message string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE notification_events SET status='failed', attempts=attempts+1, error=$2, updated_at=now() WHERE id=$1`, id, message)
	return err
}

func (s *Store) CreateNotificationEvent(ctx context.Context, endpointID, message string) (string, error) {
	var id string
	err := s.Pool.QueryRow(ctx, `INSERT INTO notification_events (endpoint_id, status, message) VALUES ($1,'pending',$2) RETURNING id::text`, endpointID, message).Scan(&id)
	return id, err
}

func IsNotFound(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func marshalJSON(headers []models.Header, condition models.Condition) ([]byte, []byte, error) {
	headersJSON, err := json.Marshal(headers)
	if err != nil {
		return nil, nil, err
	}
	conditionJSON, err := json.Marshal(condition)
	if err != nil {
		return nil, nil, err
	}
	return headersJSON, conditionJSON, nil
}

const endpointColumns = `
	id::text, name, url, http_method, headers, request_body_enabled, request_body, proxy_enabled, proxy_address,
	proxy_username, proxy_password_encrypted, ping_interval_seconds, notify_condition,
	deactivate_after_seconds, notify_once, notification_template, active, state, created_at, updated_at,
	last_checked_at, next_run_at, deactivate_at, last_status_code, last_response_length,
	last_error, last_duration_ms, baseline_status_code, baseline_response_length,
	notified_at, deactivated_reason, locked_until, version`

type scanner interface {
	Scan(dest ...any) error
}

func scanEndpoints(rows pgx.Rows) ([]models.Endpoint, error) {
	endpoints := make([]models.Endpoint, 0)
	for rows.Next() {
		endpoint, err := scanEndpoint(rows)
		if err != nil {
			return nil, err
		}
		endpoints = append(endpoints, endpoint)
	}
	return endpoints, rows.Err()
}

func scanEndpoint(row scanner) (models.Endpoint, error) {
	var endpoint models.Endpoint
	var name sql.NullString
	var headersJSON []byte
	var proxyAddress sql.NullString
	var proxyUsername sql.NullString
	var proxyPassword sql.NullString
	var conditionJSON []byte
	var deactivateAfter sql.NullInt64
	var lastChecked sql.NullTime
	var nextRun sql.NullTime
	var deactivateAt sql.NullTime
	var lastStatus sql.NullInt64
	var lastLength sql.NullInt64
	var lastErr sql.NullString
	var lastDuration sql.NullInt64
	var baselineStatus sql.NullInt64
	var baselineLength sql.NullInt64
	var notifiedAt sql.NullTime
	var reason sql.NullString
	var lockedUntil sql.NullTime
	err := row.Scan(
		&endpoint.ID, &name, &endpoint.URL, &endpoint.HTTPMethod, &headersJSON,
		&endpoint.RequestBodyEnabled, &endpoint.RequestBody,
		&endpoint.Proxy.Enabled, &proxyAddress, &proxyUsername, &proxyPassword,
		&endpoint.PingIntervalSeconds, &conditionJSON, &deactivateAfter,
		&endpoint.NotifyOnce, &endpoint.NotificationTemplate, &endpoint.Active,
		&endpoint.State, &endpoint.CreatedAt, &endpoint.UpdatedAt, &lastChecked,
		&nextRun, &deactivateAt, &lastStatus, &lastLength, &lastErr,
		&lastDuration, &baselineStatus, &baselineLength, &notifiedAt,
		&reason, &lockedUntil, &endpoint.Version,
	)
	if err != nil {
		return endpoint, err
	}
	endpoint.Name = stringPtrFromNull(name)
	endpoint.Proxy.Address = stringFromNull(proxyAddress)
	endpoint.Proxy.Username = stringFromNull(proxyUsername)
	endpoint.Proxy.PasswordEncrypted = stringPtrFromNull(proxyPassword)
	endpoint.Proxy.PasswordSet = proxyPassword.Valid && proxyPassword.String != ""
	if endpoint.Proxy.PasswordSet {
		endpoint.Proxy.Password = "********"
	}
	endpoint.LastCheckedAt = timePtrFromNull(lastChecked)
	endpoint.NextRunAt = timePtrFromNull(nextRun)
	endpoint.LastStatusCode = intPtrFromNull(lastStatus)
	endpoint.LastResponseLength = int64PtrFromNull(lastLength)
	endpoint.LastError = stringPtrFromNull(lastErr)
	endpoint.LastDurationMS = intPtrFromNull(lastDuration)
	endpoint.BaselineStatusCode = intPtrFromNull(baselineStatus)
	endpoint.BaselineResponseLength = int64PtrFromNull(baselineLength)
	endpoint.NotifiedAt = timePtrFromNull(notifiedAt)
	endpoint.DeactivatedReason = stringPtrFromNull(reason)
	endpoint.LockedUntil = timePtrFromNull(lockedUntil)
	endpoint.PingInterval = validation.FormatInterval(endpoint.PingIntervalSeconds)
	endpoint.DeactivateAfterSeconds = intPtrFromNull(deactivateAfter)
	if endpoint.DeactivateAfterSeconds != nil {
		formatted := validation.FormatInterval(*endpoint.DeactivateAfterSeconds)
		endpoint.DeactivateAfter = &formatted
	}
	endpoint.DeactivateAt = timePtrFromNull(deactivateAt)
	if err := json.Unmarshal(headersJSON, &endpoint.Headers); err != nil {
		return endpoint, err
	}
	if err := json.Unmarshal(conditionJSON, &endpoint.NotifyCondition); err != nil {
		return endpoint, err
	}
	endpoint.NotifyConditionRaw = conditionJSON
	endpoint.HeaderViews = HeaderViews(endpoint.Headers)
	return endpoint, nil
}

func HeaderViews(headers []models.Header) []models.HeaderView {
	views := make([]models.HeaderView, 0, len(headers))
	for _, header := range headers {
		masked := header.Sensitive || header.Masked
		value := ""
		if masked {
			value = "********"
		} else if header.ValuePlain != nil {
			value = *header.ValuePlain
		}
		views = append(views, models.HeaderView{
			Name:      header.Name,
			Value:     value,
			Sensitive: header.Sensitive,
			Masked:    masked,
		})
	}
	return views
}

func deactivateDeadline(now time.Time, seconds *int) any {
	if seconds == nil {
		return nil
	}
	return now.Add(time.Duration(*seconds) * time.Second)
}

func pgInterval(duration time.Duration) string {
	return fmt.Sprintf("%f seconds", duration.Seconds())
}

func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func stringFromNull(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func stringPtrFromNull(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	v := value.String
	return &v
}

func timePtrFromNull(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	v := value.Time
	return &v
}

func intPtrFromNull(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	v := int(value.Int64)
	return &v
}

func int64PtrFromNull(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	v := value.Int64
	return &v
}
