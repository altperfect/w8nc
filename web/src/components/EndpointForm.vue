<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { Info, Send, Trash2, X } from '@lucide/vue'
import { api } from '../api/client'
import HeaderEditor from './HeaderEditor.vue'
import type { ConditionType, Endpoint, EndpointInput, EndpointTestResult, HeaderValue, ProxyConfig, Tag } from '../types'

type DurationUnit = 's' | 'm' | 'h' | 'd'
type DurationParts = {
  amount: number | string | null
  unit: DurationUnit
}
type ProxyAddressParts = {
  host: string
  port: string
}
type TagDraft = {
  name: string
  color: string
}

const props = defineProps<{ endpoint?: Endpoint | null; error?: string }>()
const emit = defineEmits<{ save: [EndpointInput]; cancel: [] }>()
const descriptionLimit = 200

const defaultTemplate = `[{{state}}] {{method}} {{url}}
Condition: {{condition_type}}
Status: {{status_code}}
Length: {{response_length}}
Duration: {{duration_ms}}ms
Checked: {{checked_at}}`

const durationUnits: { value: DurationUnit; label: string }[] = [
  { value: 's', label: 's (seconds)' },
  { value: 'm', label: 'm (minutes)' },
  { value: 'h', label: 'h (hours)' },
  { value: 'd', label: 'd (days)' }
]

const state = reactive<EndpointInput>({
  name: props.endpoint?.name || '',
  description: props.endpoint?.description || '',
  url: props.endpoint?.url || '',
  http_method: props.endpoint?.http_method || 'GET',
  headers: props.endpoint ? props.endpoint.headers.map((h) => ({ ...h })) : ([] as HeaderValue[]),
  request_body_enabled: props.endpoint?.request_body_enabled || false,
  request_body: props.endpoint?.request_body || '',
  proxy: props.endpoint?.proxy
    ? { ...props.endpoint.proxy }
    : { enabled: false, address: '', username: '', password: '', password_set: false },
  ping_interval: '15s',
  deactivate_after: null,
  notify_condition: props.endpoint?.notify_condition || { type: 'status_code_changed' },
  notification_template: props.endpoint?.notification_template || defaultTemplate,
  screenshot_on_match: props.endpoint?.screenshot_on_match || false,
  tags: props.endpoint?.tags ? props.endpoint.tags.map((tag) => ({ name: tag.name, color: tag.color })) : [],
  active: props.endpoint?.active ?? true
})
const pingInterval = reactive<DurationParts>(parseDuration(props.endpoint?.ping_interval || '15s', 15, 's'))
const deactivateAfter = reactive<DurationParts>(parseDuration(props.endpoint?.deactivate_after || '', null, 'h'))
const proxyAddress = reactive<ProxyAddressParts>(splitProxyAddress(props.endpoint?.proxy?.address || ''))
const testBusy = ref(false)
const testAttempted = ref(false)
const testError = ref('')
const testResult = ref<EndpointTestResult | null>(null)
const lastProxy = ref<ProxyConfig | null>(null)
const lastProxyLoaded = ref(false)
const proxyReusePromptVisible = ref(false)
const templatePlaceholders = ref<string[]>([])
const availableTags = ref<Tag[]>([])
const tagColors = ref<string[]>(['slate', 'blue', 'teal', 'green', 'amber', 'rose', 'violet', 'gray'])
const tagDraft = reactive<TagDraft>({ name: '', color: 'slate' })
const tagError = ref('')
const deletingTagID = ref('')
const tagDeleteTarget = ref<Tag | null>(null)
let proxyReuseTimer: number | undefined

const conditionType = computed({
  get: () => state.notify_condition.type,
  set: (value: ConditionType) => {
    if (value === 'body_contains') state.notify_condition = { type: value, value: '' }
    else if (value === 'status_code_equals') state.notify_condition = { type: value, value: 200 }
    else if (value === 'response_length_changed') state.notify_condition = { type: value, tolerance_bytes: 0 }
    else state.notify_condition = { type: value }
  }
})

const responseHeaderRows = computed(() => {
  const headers = testResult.value?.response_headers || {}
  return Object.entries(headers)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([name, values]) => ({ name, value: values.join(', ') }))
})
const testTitle = computed(() => {
  if (testBusy.value) return 'Sending...'
  if (testAttempted.value) return 'Test request'
  return 'Verify your request behaves as expected (for now).'
})
const lastProxyTarget = computed(() => {
  if (!lastProxy.value?.address) return ''
  const parts = splitProxyAddress(lastProxy.value.address)
  if (parts.host && parts.port) return `${parts.host}:${parts.port}`
  return parts.host || lastProxy.value.address
})
const screenshotSupported = computed(() => state.http_method.toUpperCase() === 'GET')
if (!screenshotSupported.value) state.screenshot_on_match = false
const descriptionRemaining = computed(() => Math.max(0, descriptionLimit - String(state.description || '').length))
const tagSuggestions = computed(() =>
  availableTags.value
    .filter((tag) => !state.tags.some((selected) => selected.name === tag.name))
    .slice(0, 6)
)

watch(
  () => state.http_method,
  () => {
    if (!screenshotSupported.value) state.screenshot_on_match = false
  }
)

watch(
  () => state.proxy.enabled,
  async (enabled) => {
    hideProxyReusePrompt()
    if (!enabled) return
    await loadLastProxy()
    if (state.proxy.enabled && lastProxy.value?.enabled && lastProxy.value.address) {
      showProxyReusePrompt()
    }
  }
)

onUnmounted(() => {
  clearProxyReuseTimer()
})

onMounted(() => {
  void loadTemplatePlaceholders()
  void loadTags()
})

async function loadTemplatePlaceholders() {
  try {
    const response = await api.getTemplatePlaceholders()
    templatePlaceholders.value = Array.isArray(response.items) ? response.items : []
  } catch {
    templatePlaceholders.value = []
  }
}

async function loadTags() {
  try {
    const response = await api.listTags()
    availableTags.value = Array.isArray(response.items) ? response.items : []
    if (Array.isArray(response.colors) && response.colors.length) tagColors.value = response.colors
  } catch {
    availableTags.value = []
  }
}

async function loadLastProxy() {
  if (lastProxyLoaded.value) return
  lastProxyLoaded.value = true
  try {
    const response = await api.getLastProxy()
    lastProxy.value = response.available && response.proxy ? response.proxy : null
  } catch {
    lastProxy.value = null
  }
}

function showProxyReusePrompt() {
  proxyReusePromptVisible.value = true
  clearProxyReuseTimer()
  proxyReuseTimer = window.setTimeout(() => {
    proxyReusePromptVisible.value = false
    proxyReuseTimer = undefined
  }, 5000)
}

function hideProxyReusePrompt() {
  proxyReusePromptVisible.value = false
  clearProxyReuseTimer()
}

function clearProxyReuseTimer() {
  if (proxyReuseTimer !== undefined) {
    window.clearTimeout(proxyReuseTimer)
    proxyReuseTimer = undefined
  }
}

function appendTemplatePlaceholder(placeholder: string) {
  const token = `{{${placeholder}}}`
  const current = state.notification_template || ''
  state.notification_template = current ? `${current}${current.endsWith('\n') ? '' : '\n'}${token}` : token
}

function currentInput(): EndpointInput {
  return {
    ...state,
    name: state.name ? String(state.name).trim() : null,
    description: String(state.description || '').trim(),
    http_method: state.http_method.toUpperCase(),
    request_body_enabled: Boolean(state.request_body_enabled),
    request_body: state.request_body_enabled ? state.request_body || '' : '',
    proxy: proxyInput(),
    ping_interval: durationValue(pingInterval),
    deactivate_after: durationValue(deactivateAfter) || null,
    tags: state.tags.map((tag) => ({ name: tag.name, color: tag.color })),
    headers: state.headers
      .filter((header) => header.name.trim() !== '')
      .map((header) => {
        const masked = Boolean(header.masked || header.sensitive)
        return { ...header, sensitive: masked, masked }
      })
  }
}

function sanitizeTagDraft(event: Event) {
  const input = event.target as HTMLInputElement
  const value = normalizeTagNameInput(input.value)
  input.value = value
  tagDraft.name = value
  tagError.value = ''
}

function normalizeTagNameInput(value: string) {
  return String(value || '')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9_-]/g, '')
    .slice(0, 16)
}

function addDraftTag() {
  addTag({ name: tagDraft.name, color: tagDraft.color }, true)
}

function addTag(tag: { name: string; color: string }, clearDraft = false) {
  const name = normalizeTagNameInput(tag.name)
  if (!name) {
    tagError.value = 'Tag name is required'
    return
  }
  if (!/^[a-z0-9]/.test(name)) {
    tagError.value = 'Tag must start with a letter or number'
    return
  }
  if (state.tags.some((selected) => selected.name === name)) {
    tagError.value = ''
    if (clearDraft) tagDraft.name = ''
    return
  }
  if (state.tags.length >= 8) {
    tagError.value = 'Use at most 8 tags'
    return
  }
  const color = tagColors.value.includes(tag.color) ? tag.color : 'slate'
  state.tags.push({ name, color })
  tagError.value = ''
  if (clearDraft) tagDraft.name = ''
}

function removeTag(name: string) {
  state.tags = state.tags.filter((tag) => tag.name !== name)
}

async function deleteReusableTag(tag: Tag) {
  if (!tag.id || deletingTagID.value) return
  if ((tag.endpoint_count || 0) > 0) {
    tagDeleteTarget.value = tag
    return
  }
  await confirmDeleteReusableTag(tag)
}

function cancelDeleteReusableTag() {
  if (deletingTagID.value) return
  tagDeleteTarget.value = null
}

async function confirmDeleteReusableTag(tag = tagDeleteTarget.value) {
  if (!tag?.id || deletingTagID.value) return
  deletingTagID.value = tag.id
  tagError.value = ''
  try {
    await api.deleteTag(tag.id)
    availableTags.value = availableTags.value.filter((item) => item.id !== tag.id)
    state.tags = state.tags.filter((item) => item.name !== tag.name)
    tagDeleteTarget.value = null
  } catch (err) {
    tagError.value = err instanceof Error ? err.message : 'Could not delete tag'
  } finally {
    deletingTagID.value = ''
  }
}

function tagColorLabel(color: string) {
  return color.charAt(0).toUpperCase() + color.slice(1)
}

function proxyInput() {
  if (!state.proxy.enabled) return { enabled: false, address: '', username: '', password: '' }
  return {
    enabled: true,
    address: formatProxyAddress(proxyAddress.host, proxyAddress.port),
    username: String(state.proxy.username || '').trim(),
    password: state.proxy.password || '',
    password_set: state.proxy.password_set
  }
}

function reuseLastProxy() {
  if (!lastProxy.value) return
  state.proxy.enabled = true
  const parts = splitProxyAddress(lastProxy.value.address || '')
  proxyAddress.host = parts.host
  proxyAddress.port = parts.port
  state.proxy.username = lastProxy.value.username || ''
  state.proxy.password = lastProxy.value.password_set ? '********' : ''
  state.proxy.password_set = Boolean(lastProxy.value.password_set)
  hideProxyReusePrompt()
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

function parseDuration(value: string | null | undefined, fallbackAmount: number | null, fallbackUnit: DurationUnit): DurationParts {
  const match = /^([1-9]\d*)([smhd])$/.exec(String(value || '').trim())
  if (!match) return { amount: fallbackAmount, unit: fallbackUnit }
  return { amount: Number(match[1]), unit: match[2] as DurationUnit }
}

function durationValue(parts: DurationParts): string {
  const amount = Number(parts.amount)
  if (!Number.isFinite(amount) || amount < 1) return ''
  return `${Math.floor(amount)}${parts.unit}`
}

function allowDigits(event: InputEvent) {
  if (event.data && !/^\d+$/.test(event.data)) {
    event.preventDefault()
  }
}

function sanitizeDurationAmount(parts: DurationParts, event: Event) {
  const input = event.target as HTMLInputElement
  const value = input.value.replace(/\D/g, '')
  input.value = value
  parts.amount = value
}

function sanitizeProxyPort(event: Event) {
  const input = event.target as HTMLInputElement
  const value = input.value.replace(/\D/g, '')
  input.value = value
  proxyAddress.port = value
}

function save() {
  emit('save', currentInput())
}

async function testRequest() {
  testBusy.value = true
  testAttempted.value = true
  testError.value = ''
  testResult.value = null
  try {
    testResult.value = await api.testEndpointRequest(currentInput())
  } catch (err) {
    testError.value = err instanceof Error ? err.message : 'Test request failed'
  } finally {
    testBusy.value = false
  }
}
</script>

<template>
  <form class="modal-body" @submit.prevent="save">
    <div class="form-grid">
      <label>
        Name
        <input v-model="state.name" placeholder="Admin panel" />
      </label>
      <label>
        Method
        <select v-model="state.http_method">
          <option>GET</option>
          <option>HEAD</option>
          <option>POST</option>
          <option>PUT</option>
          <option>PATCH</option>
          <option>DELETE</option>
          <option>OPTIONS</option>
        </select>
      </label>
      <label class="wide">
        URL
        <input v-model="state.url" required placeholder="https://example.com/admin" />
      </label>
      <details class="description-field wide">
        <summary>
          <span class="description-summary-content">
            <span class="description-summary-label">Description</span>
            <span class="description-counter">{{ descriptionRemaining }}/{{ descriptionLimit }} symbols available</span>
          </span>
        </summary>
        <textarea
          v-model="state.description"
          aria-label="Description"
          rows="3"
          :maxlength="descriptionLimit"
          placeholder="Short note about this endpoint"
        />
      </details>
      <div class="tags-field wide">
        <div class="field-heading">
          <span>Tags</span>
          <span>Short labels, up to 16 characters each.</span>
        </div>
        <div class="tag-editor-row">
          <input
            v-model="tagDraft.name"
            maxlength="16"
            placeholder="prod"
            aria-label="Tag name"
            @input="sanitizeTagDraft"
            @keydown.enter.prevent="addDraftTag"
          />
          <div class="tag-color-picker" aria-label="Tag color">
            <button
              v-for="color in tagColors"
              :key="color"
              type="button"
              class="tag-swatch"
              :class="`tag-color-${color}`"
              :title="tagColorLabel(color)"
              :aria-label="tagColorLabel(color)"
              :aria-pressed="tagDraft.color === color"
              @click="tagDraft.color = color"
            />
          </div>
          <button type="button" @click="addDraftTag">Add tag</button>
        </div>
        <div v-if="state.tags.length" class="tag-chip-list">
          <span v-for="tag in state.tags" :key="tag.name" class="tag-chip" :class="`tag-color-${tag.color}`">
            {{ tag.name }}
            <button type="button" :aria-label="`Remove ${tag.name}`" @click="removeTag(tag.name)">
              <X :size="12" />
            </button>
          </span>
        </div>
        <div v-if="tagSuggestions.length" class="tag-suggestions">
          <span>Reuse</span>
          <span
            v-for="tag in tagSuggestions"
            :key="tag.id || tag.name"
            class="tag-chip"
            :class="`tag-color-${tag.color}`"
          >
            <button type="button" class="tag-chip-label" @click="addTag(tag)">{{ tag.name }}</button>
            <button
              type="button"
              :aria-label="`Delete ${tag.name}`"
              :disabled="deletingTagID === tag.id"
              @click.stop="deleteReusableTag(tag)"
            >
              <X :size="12" />
            </button>
          </span>
        </div>
        <p v-if="tagError" class="error compact-error">{{ tagError }}</p>
      </div>
      <label class="inline checkbox-line wide">
        <input v-model="state.request_body_enabled" type="checkbox" />
        Request has body
      </label>
      <label v-if="state.request_body_enabled" class="request-body-field wide">
        Request body
        <textarea
          v-model="state.request_body"
          rows="8"
          spellcheck="false"
          placeholder='{"key":"value"}'
        />
      </label>
      <label>
        Ping interval
        <div class="duration-field">
          <input
            v-model="pingInterval.amount"
            aria-label="Ping interval amount"
            required
            type="text"
            inputmode="numeric"
            pattern="[0-9]*"
            @beforeinput="allowDigits"
            @input="sanitizeDurationAmount(pingInterval, $event)"
          />
          <select v-model="pingInterval.unit" aria-label="Ping interval unit">
            <option v-for="unit in durationUnits" :key="unit.value" :value="unit.value">{{ unit.label }}</option>
          </select>
        </div>
      </label>
      <label>
        Deactivate after
        <div class="duration-field">
          <input
            v-model="deactivateAfter.amount"
            aria-label="Deactivate after amount"
            type="text"
            inputmode="numeric"
            pattern="[0-9]*"
            placeholder="No limit"
            @beforeinput="allowDigits"
            @input="sanitizeDurationAmount(deactivateAfter, $event)"
          />
          <select
            v-model="deactivateAfter.unit"
            aria-label="Deactivate after unit"
            :disabled="!durationValue(deactivateAfter)"
          >
            <option v-for="unit in durationUnits" :key="unit.value" :value="unit.value">{{ unit.label }}</option>
          </select>
        </div>
      </label>
      <label class="inline checkbox-line">
        <input v-model="state.active" type="checkbox" />
        Monitoring on
      </label>
      <label class="inline checkbox-line">
        <input v-model="state.proxy.enabled" type="checkbox" />
        SOCKS5 proxy
      </label>
      <div v-if="state.proxy.enabled" class="proxy-grid wide">
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
          <input v-model="state.proxy.username" autocomplete="off" />
        </label>
        <label>
          Proxy password
          <input
            v-model="state.proxy.password"
            type="password"
            :placeholder="state.proxy.password_set ? 'Password saved - enter a new password to replace' : 'Optional with username'"
            autocomplete="new-password"
          />
        </label>
      </div>
    </div>

    <Transition name="fade">
      <div v-if="proxyReusePromptVisible" class="proxy-reuse-popup" role="status">
        <button type="button" class="icon-button proxy-reuse-close" aria-label="Close reuse proxy prompt" @click="hideProxyReusePrompt">
          <X :size="14" />
        </button>
        <p>Reuse latest socks5 proxy to {{ lastProxyTarget }}?</p>
        <div class="proxy-reuse-actions">
          <button type="button" class="primary" @click="reuseLastProxy">Yes</button>
          <button type="button" class="ghost" @click="hideProxyReusePrompt">No</button>
        </div>
      </div>
    </Transition>

    <Transition name="fade">
      <div v-if="tagDeleteTarget" class="modal-backdrop" @click.self="cancelDeleteReusableTag">
        <section class="modal panel confirmation-modal" role="alertdialog" aria-modal="true" aria-labelledby="tag-delete-title">
          <header class="modal-header">
            <h2 id="tag-delete-title">Delete tag</h2>
            <button
              type="button"
              class="icon-button"
              title="Close"
              :disabled="Boolean(deletingTagID)"
              @click="cancelDeleteReusableTag"
            >
              <X :size="16" />
            </button>
          </header>
          <div class="modal-body confirmation-body">
            <div>
              <p class="confirmation-title">{{ tagDeleteTarget.name }}</p>
              <p class="muted">This tag is being used with other endpoints. Are you sure?</p>
            </div>
            <footer class="modal-actions">
              <button type="button" :disabled="Boolean(deletingTagID)" @click="cancelDeleteReusableTag">Cancel</button>
              <button type="button" class="danger-button" :disabled="Boolean(deletingTagID)" @click="confirmDeleteReusableTag()">
                <Trash2 :size="14" />
                {{ deletingTagID ? 'Deleting...' : 'Delete tag' }}
              </button>
            </footer>
          </div>
        </section>
      </div>
    </Transition>

    <HeaderEditor v-model="state.headers" />

    <section class="test-request-panel">
      <div class="section-title">
        <h3 class="test-request-title">{{ testTitle }}</h3>
        <button type="button" :disabled="testBusy || !state.url" @click="testRequest">
          <Send :size="14" />
          {{ testBusy ? 'Sending...' : 'Send test' }}
        </button>
      </div>

      <p v-if="testError" class="error">{{ testError }}</p>

      <div v-if="testResult" class="test-result">
        <div class="result-grid">
          <div>
            <span>Status</span>
            <strong>{{ testResult.status_code ?? 'No response' }}</strong>
          </div>
          <div>
            <span>Duration</span>
            <strong>{{ testResult.duration_ms }}ms</strong>
          </div>
          <div>
            <span>Length</span>
            <strong>{{ testResult.response_length ?? 0 }}</strong>
          </div>
        </div>

        <p v-if="testResult.error" class="error">{{ testResult.error }}</p>

        <div class="response-headers">
          <h4>Response headers</h4>
          <dl v-if="responseHeaderRows.length">
            <template v-for="header in responseHeaderRows" :key="header.name">
              <dt>{{ header.name }}</dt>
              <dd>{{ header.value }}</dd>
            </template>
          </dl>
          <p v-else class="muted">No response headers</p>
        </div>

        <details class="response-body">
          <summary>Response body</summary>
          <pre>{{ testResult.body_preview || 'Empty body' }}</pre>
          <p v-if="testResult.body_preview_truncated || testResult.truncated" class="muted">
            {{ testResult.body_preview_truncated ? 'Preview truncated' : 'Body truncated by max response size' }}
          </p>
        </details>
      </div>
    </section>

    <div class="form-grid">
      <label>
        Condition
        <select v-model="conditionType">
          <option value="status_code_changed">Status code changed</option>
          <option value="status_code_equals">Status code equals</option>
          <option value="response_length_changed">Response length changed</option>
          <option value="body_contains">Body contains</option>
        </select>
      </label>
      <label v-if="state.notify_condition.type === 'status_code_equals'">
        Status code
        <input v-model.number="state.notify_condition.value" type="number" min="100" max="599" />
      </label>
      <label v-if="state.notify_condition.type === 'body_contains'">
        Text
        <input v-model="state.notify_condition.value" />
      </label>
      <label v-if="state.notify_condition.type === 'response_length_changed'">
        Tolerance bytes
        <input v-model.number="state.notify_condition.tolerance_bytes" type="number" min="0" />
      </label>
      <label class="checkbox-line screenshot-option wide">
        <input v-model="state.screenshot_on_match" type="checkbox" :disabled="!screenshotSupported" />
        <span class="screenshot-option-copy">
          <span class="screenshot-option-title">
            <span>Screenshot on match</span>
            <span class="template-info screenshot-help">
              <button type="button" class="icon-button template-info-button" aria-label="Show screenshot help">
                <Info :size="14" />
              </button>
              <span class="template-tooltip" role="tooltip">
                Screenshotting is supported only with GET methods. Instead, you can use the response_body placeholder in the notification template.
              </span>
            </span>
          </span>
          <span class="checkbox-hint">Attempt to take a screenshot of the page when the condition is met.</span>
        </span>
      </label>
      <label class="wide">
        <span class="template-label-row">
          Notification template
          <span class="template-info">
            <button type="button" class="icon-button template-info-button" aria-label="Show template placeholders">
              <Info :size="14" />
            </button>
            <span class="template-tooltip" role="tooltip">
              <strong>Available values</strong>
              <span v-if="templatePlaceholders.length" class="template-placeholder-list">
                <button
                  v-for="placeholder in templatePlaceholders"
                  :key="placeholder"
                  type="button"
                  class="template-placeholder-button"
                  @click.stop="appendTemplatePlaceholder(placeholder)"
                >
                  {{ placeholder }}
                </button>
              </span>
              <span v-else class="muted">No values loaded</span>
            </span>
          </span>
        </span>
        <textarea v-model="state.notification_template" rows="7" />
      </label>
    </div>

    <footer class="modal-actions">
      <p v-if="props.error" class="error form-error">{{ props.error }}</p>
      <button type="button" @click="emit('cancel')">Cancel</button>
      <button class="primary">Save endpoint</button>
    </footer>
  </form>
</template>
