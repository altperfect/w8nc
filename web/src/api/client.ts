import type {
  Endpoint,
  EndpointCheck,
  EndpointInput,
  EndpointTestResult,
  ListResponse,
  MeResponse,
  NotificationSettings,
  ProxyConfig,
  TemplatePlaceholdersResponse
} from '../types'

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(path, {
    credentials: 'include',
    headers: {
      'Content-Type': 'application/json',
      ...(init.headers || {})
    },
    ...init
  })
  if (!response.ok) {
    let message = response.statusText
    try {
      const body = await response.json()
      message = body.error || message
    } catch {
      // Keep the HTTP status text when the response is not JSON.
    }
    throw new Error(message)
  }
  if (response.status === 204) {
    return undefined as T
  }
  return response.json()
}

export const api = {
  me: () => request<MeResponse>('/api/auth/me'),
  login: (password: string) =>
    request<MeResponse>('/api/auth/login', {
      method: 'POST',
      body: JSON.stringify({ password })
    }),
  logout: () => request<{ ok: boolean }>('/api/auth/logout', { method: 'POST' }),
  changePassword: (input: { current_password: string; new_password: string }) =>
    request<{ ok: boolean }>('/api/auth/password', { method: 'PUT', body: JSON.stringify(input) }),
  health: () => request<{ status: string; database: string; notify_binary: string }>('/api/health'),
  listEndpoints: (params: URLSearchParams) => request<ListResponse<Endpoint>>(`/api/endpoints?${params.toString()}`),
  getLastProxy: () => request<{ available: boolean; proxy?: ProxyConfig }>('/api/proxies/last'),
  getTemplatePlaceholders: () => request<TemplatePlaceholdersResponse>('/api/template-placeholders'),
  createEndpoint: (input: EndpointInput) =>
    request<Endpoint>('/api/endpoints', { method: 'POST', body: JSON.stringify(input) }),
  testEndpointRequest: (input: EndpointInput) =>
    request<EndpointTestResult>('/api/endpoints/test-request', { method: 'POST', body: JSON.stringify(input) }),
  updateEndpoint: (id: string, input: EndpointInput) =>
    request<Endpoint>(`/api/endpoints/${id}`, { method: 'PUT', body: JSON.stringify(input) }),
  deleteEndpoint: (id: string) => request<void>(`/api/endpoints/${id}`, { method: 'DELETE' }),
  activateEndpoint: (id: string) => request<Endpoint>(`/api/endpoints/${id}/activate`, { method: 'POST' }),
  deactivateEndpoint: (id: string) => request<Endpoint>(`/api/endpoints/${id}/deactivate`, { method: 'POST' }),
  pingNow: (id: string) => request<Endpoint>(`/api/endpoints/${id}/ping-now`, { method: 'POST' }),
  listChecks: (id: string, page = 1, pageSize = 20) =>
    request<ListResponse<EndpointCheck>>(`/api/endpoints/${id}/checks?page=${page}&page_size=${pageSize}`),
  getNotificationSettings: () => request<NotificationSettings>('/api/settings/notifications'),
  updateNotificationSettings: (input: {
    telegram_enabled: boolean
    telegram_api_key?: string
    telegram_chat_id?: string
    telegram_parse_mode: string
    timezone: string
    proxy: ProxyConfig
  }) => request<NotificationSettings>('/api/settings/notifications', { method: 'PUT', body: JSON.stringify(input) }),
  testNotification: () => request<{ ok: boolean }>('/api/settings/notifications/test', { method: 'POST' })
}
