<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { api } from '../api/client'

type ProxyAddressParts = {
  host: string
  port: string
}

const form = reactive({
  telegram_enabled: false,
  telegram_api_key: '',
  telegram_chat_id: '',
  telegram_parse_mode: 'Markdown',
  timezone: '',
  proxy: {
    enabled: false,
    address: '',
    username: '',
    password: '',
    password_set: false
  }
})
const proxyAddress = reactive<ProxyAddressParts>({ host: '', port: '' })
const passwordForm = reactive({
  current_password: '',
  new_password: '',
  confirm_password: ''
})
const tokenSet = ref(false)
const timezoneOptions = ref<string[]>([])
const message = ref('')
const error = ref('')
const passwordMessage = ref('')
const passwordError = ref('')

async function load() {
  const settings = await api.getNotificationSettings()
  form.telegram_enabled = settings.telegram_enabled
  form.telegram_api_key = ''
  form.telegram_chat_id = settings.telegram_chat_id || ''
  form.telegram_parse_mode = settings.telegram_parse_mode || 'Markdown'
  form.timezone = settings.timezone || browserTimezone()
  timezoneOptions.value = mergeTimezoneOptions(settings.timezone_options || [], form.timezone)
  form.proxy = {
    enabled: settings.proxy?.enabled || false,
    address: settings.proxy?.address || '',
    username: settings.proxy?.username || '',
    password: settings.proxy?.password || '',
    password_set: settings.proxy?.password_set || false
  }
  const parts = splitProxyAddress(settings.proxy?.address || '')
  proxyAddress.host = parts.host
  proxyAddress.port = parts.port
  tokenSet.value = settings.telegram_api_key_set
}

async function save() {
  error.value = ''
  message.value = ''
  try {
    const settings = await api.updateNotificationSettings({
      telegram_enabled: form.telegram_enabled,
      telegram_api_key: form.telegram_api_key || undefined,
      telegram_chat_id: form.telegram_chat_id,
      telegram_parse_mode: form.telegram_parse_mode,
      timezone: form.timezone,
      proxy: {
        enabled: form.proxy.enabled,
        address: form.proxy.enabled ? formatProxyAddress(proxyAddress.host, proxyAddress.port) : '',
        username: form.proxy.enabled ? form.proxy.username.trim() : '',
        password: form.proxy.enabled ? form.proxy.password : '',
        password_set: form.proxy.password_set
      }
    })
    tokenSet.value = settings.telegram_api_key_set
    form.timezone = settings.timezone || form.timezone
    timezoneOptions.value = mergeTimezoneOptions(settings.timezone_options || timezoneOptions.value, form.timezone)
    form.proxy.password = settings.proxy?.password || ''
    form.proxy.password_set = settings.proxy?.password_set || false
    const parts = splitProxyAddress(settings.proxy?.address || '')
    proxyAddress.host = parts.host
    proxyAddress.port = parts.port
    form.telegram_api_key = ''
    message.value = 'Settings saved'
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Could not save settings'
  }
}

function browserTimezone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
}

function mergeTimezoneOptions(options: string[], current: string): string[] {
  const merged = new Set(options.filter(Boolean))
  if (current) merged.add(current)
  return Array.from(merged).sort((a, b) => a.localeCompare(b))
}

function timezoneLabel(timezone: string): string {
  const offset = timezoneOffsetMinutes(timezone)
  if (offset === null) return timezone
  return `${timezone} (${formatUtcOffset(offset)})`
}

function timezoneOffsetMinutes(timezone: string): number | null {
  try {
    const now = new Date()
    const parts = new Intl.DateTimeFormat('en-US', {
      timeZone: timezone,
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hourCycle: 'h23'
    }).formatToParts(now)
    const value = (type: string) => Number(parts.find((part) => part.type === type)?.value)
    const zonedTime = Date.UTC(value('year'), value('month') - 1, value('day'), value('hour'), value('minute'), value('second'))
    const offset = Math.round((zonedTime - now.getTime()) / 60000)
    return Number.isFinite(offset) ? offset : null
  } catch {
    return null
  }
}

function formatUtcOffset(minutes: number): string {
  const sign = minutes < 0 ? '-' : '+'
  const absolute = Math.abs(minutes)
  const hours = Math.floor(absolute / 60)
  const remainder = absolute % 60
  if (remainder === 0) return `UTC${sign}${hours}`
  return `UTC${sign}${hours}:${String(remainder).padStart(2, '0')}`
}

function splitProxyAddress(value: string | undefined): ProxyAddressParts {
  const raw = String(value || '').trim()
  if (!raw) return { host: '', port: '' }
  if (raw.startsWith('socks5://')) {
    try {
      const parsed = new URL(raw)
      return { host: parsed.hostname, port: parsed.port }
    } catch {
      return { host: raw, port: '' }
    }
  }
  if (raw.startsWith('[')) {
    const end = raw.indexOf(']')
    if (end !== -1) {
      return { host: raw.slice(1, end), port: raw.slice(end + 1).replace(/^:/, '') }
    }
  }
  const splitAt = raw.lastIndexOf(':')
  if (splitAt <= 0) return { host: raw, port: '' }
  return { host: raw.slice(0, splitAt), port: raw.slice(splitAt + 1) }
}

function formatProxyAddress(host: string, port: string): string {
  const trimmedHost = String(host || '').trim()
  const trimmedPort = String(port || '').trim()
  if (!trimmedHost || !trimmedPort) return trimmedHost
  const formattedHost = trimmedHost.includes(':') && !trimmedHost.startsWith('[') ? `[${trimmedHost}]` : trimmedHost
  return `${formattedHost}:${trimmedPort}`
}

function allowDigits(event: InputEvent) {
  if (event.data && !/^\d+$/.test(event.data)) {
    event.preventDefault()
  }
}

function sanitizeProxyPort(event: Event) {
  const input = event.target as HTMLInputElement
  const value = input.value.replace(/\D/g, '')
  input.value = value
  proxyAddress.port = value
}

async function test() {
  error.value = ''
  message.value = ''
  try {
    await api.testNotification()
    message.value = 'Test notification sent'
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Test notification failed'
  }
}

async function changePassword() {
  passwordError.value = ''
  passwordMessage.value = ''
  if (passwordForm.new_password !== passwordForm.confirm_password) {
    passwordError.value = 'New passwords do not match'
    return
  }
  try {
    await api.changePassword({
      current_password: passwordForm.current_password,
      new_password: passwordForm.new_password
    })
    passwordForm.current_password = ''
    passwordForm.new_password = ''
    passwordForm.confirm_password = ''
    passwordMessage.value = 'Password changed'
  } catch (err) {
    passwordError.value = err instanceof Error ? err.message : 'Could not change password'
  }
}

onMounted(load)
</script>

<template>
  <section class="settings-panel">
    <h2>Notifications</h2>
    <form class="settings-form" @submit.prevent="save">
      <div class="settings-toggle-row">
        <span>Telegram</span>
        <label class="inline">
          <input v-model="form.telegram_enabled" type="checkbox" />
          Enabled
        </label>
      </div>
      <label class="wide">
        Telegram bot API token
        <input
          v-model="form.telegram_api_key"
          class="token-input"
          type="password"
          :placeholder="tokenSet ? 'Token saved - enter a new token to replace' : 'Bot token'"
          autocomplete="new-password"
        />
      </label>
      <div class="settings-grid">
        <label>
          Telegram chat ID
          <input v-model="form.telegram_chat_id" />
        </label>
        <label>
          Parse mode
          <select v-model="form.telegram_parse_mode">
            <option>None</option>
            <option>Markdown</option>
            <option>MarkdownV2</option>
            <option>HTML</option>
          </select>
        </label>
        <label class="wide">
          Notification timezone
          <select v-model="form.timezone" aria-label="Notification timezone">
            <option v-for="timezone in timezoneOptions" :key="timezone" :value="timezone">{{ timezoneLabel(timezone) }}</option>
          </select>
        </label>
      </div>
      <div class="settings-toggle-row">
        <span>SOCKS5 proxy</span>
        <label class="inline">
          <input v-model="form.proxy.enabled" type="checkbox" />
          Enabled
        </label>
      </div>
      <div v-if="form.proxy.enabled" class="proxy-grid">
        <label>
          Proxy host
          <input v-model="proxyAddress.host" placeholder="127.0.0.1" />
        </label>
        <label>
          Proxy port
          <input
            v-model="proxyAddress.port"
            type="text"
            inputmode="numeric"
            pattern="[0-9]*"
            placeholder="9050"
            @beforeinput="allowDigits"
            @input="sanitizeProxyPort"
          />
        </label>
        <label>
          Proxy username
          <input v-model="form.proxy.username" autocomplete="off" />
        </label>
        <label>
          Proxy password
          <input
            v-model="form.proxy.password"
            type="password"
            :placeholder="form.proxy.password_set ? 'Password saved - enter a new password to replace' : 'Optional with username'"
            autocomplete="new-password"
          />
        </label>
      </div>
      <div class="settings-actions">
        <button class="primary">Save settings</button>
        <button type="button" @click="test">Test notification</button>
      </div>
    </form>
    <p v-if="message" class="success">{{ message }}</p>
    <p v-if="error" class="error">{{ error }}</p>

    <h2>Password</h2>
    <form class="settings-form" @submit.prevent="changePassword">
      <label>
        Current password
        <input v-model="passwordForm.current_password" type="password" autocomplete="current-password" required />
      </label>
      <div class="settings-grid">
        <label>
          New password
          <input v-model="passwordForm.new_password" type="password" autocomplete="new-password" required minlength="12" />
        </label>
        <label>
          Confirm new password
          <input v-model="passwordForm.confirm_password" type="password" autocomplete="new-password" required minlength="12" />
        </label>
      </div>
      <div class="settings-actions">
        <button class="primary">Change password</button>
      </div>
    </form>
    <p v-if="passwordMessage" class="success">{{ passwordMessage }}</p>
    <p v-if="passwordError" class="error">{{ passwordError }}</p>
  </section>
</template>
