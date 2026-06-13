export type ConditionType =
  | 'body_contains'
  | 'status_code_equals'
  | 'status_code_changed'
  | 'response_length_changed'

export interface HeaderValue {
  name: string
  value: string
  sensitive: boolean
  masked: boolean
}

export interface ProxyConfig {
  enabled: boolean
  address?: string
  username?: string
  password?: string
  password_set?: boolean
}

export interface NotifyCondition {
  type: ConditionType
  value?: string | number
  tolerance_bytes?: number
}

export interface Endpoint {
  id: string
  name?: string
  url: string
  http_method: string
  headers: HeaderValue[]
  request_body_enabled: boolean
  request_body: string
  proxy: ProxyConfig
  ping_interval_seconds: number
  ping_interval: string
  deactivate_after_seconds?: number | null
  deactivate_after?: string | null
  notify_condition: NotifyCondition
  notify_once: boolean
  notification_template: string
  screenshot_on_match: boolean
  active: boolean
  state: string
  created_at: string
  updated_at: string
  last_checked_at?: string
  next_run_at?: string
  deactivate_at?: string
  last_status_code?: number
  last_response_length?: number
  last_error?: string
  last_duration_ms?: number
  notified_at?: string
  deactivated_reason?: string
  version: number
}

export interface EndpointInput {
  name?: string | null
  url: string
  http_method: string
  headers: HeaderValue[]
  request_body_enabled: boolean
  request_body: string
  proxy: ProxyConfig
  ping_interval: string
  deactivate_after?: string | null
  notify_condition: NotifyCondition
  notification_template: string
  screenshot_on_match: boolean
  active: boolean
}

export interface EndpointTestResult {
  status_code?: number | null
  response_headers?: Record<string, string[]>
  response_length?: number | null
  duration_ms: number
  error?: string | null
  truncated: boolean
  body_preview: string
  body_preview_truncated: boolean
}

export interface TemplatePlaceholdersResponse {
  items: string[]
}

export interface ListResponse<T> {
  items: T[]
  page: number
  page_size: number
  total: number
}

export interface EndpointCheck {
  id: string
  endpoint_id: string
  started_at: string
  finished_at: string
  status_code?: number
  response_length?: number
  duration_ms: number
  error?: string
  truncated: boolean
  condition_matched: boolean
  notification_event_id?: string
  screenshot_attempts?: ScreenshotAttempt[]
  created_at: string
}

export interface ScreenshotAttempt {
  id: string
  endpoint_id: string
  endpoint_check_id: string
  notification_event_id?: string
  status: 'pending' | 'capturing' | 'succeeded' | 'failed' | 'unsupported'
  error?: string
  image_available: boolean
  image_content_type?: string
  image_size_bytes?: number
  capture_started_at?: string
  capture_finished_at?: string
  telegram_sent_at?: string
  created_at: string
  updated_at: string
}

export interface MeResponse {
  authenticated: boolean
  auth_enabled: boolean
  warning?: string
}

export interface NotificationSettings {
  telegram_enabled: boolean
  telegram_api_key_set: boolean
  telegram_chat_id?: string
  telegram_parse_mode: string
  timezone: string
  timezone_options?: string[]
  proxy: ProxyConfig
}
