package models

import (
	"encoding/json"
	"time"
)

const DefaultNotificationTemplate = `[{{state}}] {{method}} {{url}}
Condition: {{condition_type}}
Status: {{status_code}}
Length: {{response_length}}
Duration: {{duration_ms}}ms
Checked: {{checked_at}}`

type Header struct {
	Name           string  `json:"name"`
	ValuePlain     *string `json:"value_plain,omitempty"`
	ValueEncrypted *string `json:"value_encrypted,omitempty"`
	Sensitive      bool    `json:"sensitive"`
	Masked         bool    `json:"masked"`
}

type HeaderInput struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Sensitive *bool  `json:"sensitive,omitempty"`
	Masked    *bool  `json:"masked,omitempty"`
}

type HeaderView struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Sensitive bool   `json:"sensitive"`
	Masked    bool   `json:"masked"`
}

type ProxyConfig struct {
	Enabled           bool    `json:"enabled"`
	Address           string  `json:"address,omitempty"`
	Username          string  `json:"username,omitempty"`
	Password          string  `json:"password,omitempty"`
	PasswordEncrypted *string `json:"-"`
	PasswordSet       bool    `json:"password_set"`
}

type Condition struct {
	Type           string          `json:"type"`
	Value          json.RawMessage `json:"value,omitempty"`
	ToleranceBytes *int64          `json:"tolerance_bytes,omitempty"`
}

type Endpoint struct {
	ID                     string          `json:"id"`
	Name                   *string         `json:"name,omitempty"`
	URL                    string          `json:"url"`
	HTTPMethod             string          `json:"http_method"`
	Headers                []Header        `json:"-"`
	HeaderViews            []HeaderView    `json:"headers"`
	RequestBodyEnabled     bool            `json:"request_body_enabled"`
	RequestBody            string          `json:"request_body"`
	Proxy                  ProxyConfig     `json:"proxy"`
	PingIntervalSeconds    int             `json:"ping_interval_seconds"`
	PingInterval           string          `json:"ping_interval"`
	DeactivateAfterSeconds *int            `json:"deactivate_after_seconds,omitempty"`
	DeactivateAfter        *string         `json:"deactivate_after,omitempty"`
	NotifyCondition        Condition       `json:"notify_condition"`
	NotifyConditionRaw     json.RawMessage `json:"-"`
	NotifyOnce             bool            `json:"notify_once"`
	NotificationTemplate   string          `json:"notification_template"`
	Active                 bool            `json:"active"`
	State                  string          `json:"state"`
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at"`
	LastCheckedAt          *time.Time      `json:"last_checked_at,omitempty"`
	NextRunAt              *time.Time      `json:"next_run_at,omitempty"`
	DeactivateAt           *time.Time      `json:"deactivate_at,omitempty"`
	LastStatusCode         *int            `json:"last_status_code,omitempty"`
	LastResponseLength     *int64          `json:"last_response_length,omitempty"`
	LastError              *string         `json:"last_error,omitempty"`
	LastDurationMS         *int            `json:"last_duration_ms,omitempty"`
	BaselineStatusCode     *int            `json:"baseline_status_code,omitempty"`
	BaselineResponseLength *int64          `json:"baseline_response_length,omitempty"`
	NotifiedAt             *time.Time      `json:"notified_at,omitempty"`
	DeactivatedReason      *string         `json:"deactivated_reason,omitempty"`
	LockedUntil            *time.Time      `json:"-"`
	Version                int64           `json:"version"`
}

type EndpointInput struct {
	Name                 *string       `json:"name"`
	URL                  string        `json:"url"`
	HTTPMethod           string        `json:"http_method"`
	Headers              []HeaderInput `json:"headers"`
	RequestBodyEnabled   bool          `json:"request_body_enabled"`
	RequestBody          string        `json:"request_body"`
	Proxy                ProxyConfig   `json:"proxy"`
	PingInterval         string        `json:"ping_interval"`
	DeactivateAfter      string        `json:"deactivate_after,omitempty"`
	NotifyCondition      Condition     `json:"notify_condition"`
	NotificationTemplate string        `json:"notification_template"`
	Active               bool          `json:"active"`
}

type EndpointCheck struct {
	ID                  string    `json:"id"`
	EndpointID          string    `json:"endpoint_id"`
	StartedAt           time.Time `json:"started_at"`
	FinishedAt          time.Time `json:"finished_at"`
	StatusCode          *int      `json:"status_code,omitempty"`
	ResponseLength      *int64    `json:"response_length,omitempty"`
	DurationMS          int       `json:"duration_ms"`
	Error               *string   `json:"error,omitempty"`
	Truncated           bool      `json:"truncated"`
	ConditionMatched    bool      `json:"condition_matched"`
	NotificationEventID *string   `json:"notification_event_id,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
}

type PingResult struct {
	StartedAt       time.Time
	FinishedAt      time.Time
	StatusCode      *int
	ResponseHeaders map[string][]string
	ResponseLength  *int64
	DurationMS      int
	Error           *string
	Truncated       bool
	Body            []byte
}

type NotificationSettings struct {
	TelegramEnabled         bool        `json:"telegram_enabled"`
	TelegramAPIKeyEncrypted *string     `json:"-"`
	TelegramAPIKeySet       bool        `json:"telegram_api_key_set"`
	TelegramChatID          *string     `json:"telegram_chat_id"`
	TelegramParseMode       string      `json:"telegram_parse_mode"`
	Timezone                string      `json:"timezone"`
	TimezoneOptions         []string    `json:"timezone_options,omitempty"`
	Proxy                   ProxyConfig `json:"proxy"`
}

type NotificationSettingsInput struct {
	TelegramEnabled   bool        `json:"telegram_enabled"`
	TelegramAPIKey    *string     `json:"telegram_api_key"`
	TelegramChatID    *string     `json:"telegram_chat_id"`
	TelegramParseMode string      `json:"telegram_parse_mode"`
	Timezone          string      `json:"timezone"`
	Proxy             ProxyConfig `json:"proxy"`
}

type NotificationEvent struct {
	ID         string     `json:"id"`
	EndpointID string     `json:"endpoint_id"`
	Status     string     `json:"status"`
	Message    string     `json:"message"`
	Error      *string    `json:"error,omitempty"`
	Attempts   int        `json:"attempts"`
	CreatedAt  time.Time  `json:"created_at"`
	SentAt     *time.Time `json:"sent_at,omitempty"`
	UpdatedAt  time.Time  `json:"updated_at"`
}
