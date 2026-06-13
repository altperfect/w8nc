<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { Edit3, History, Plus, Power, RefreshCw, Search, Trash2, X } from '@lucide/vue'
import { api } from '../api/client'
import EndpointForm from '../components/EndpointForm.vue'
import type { Endpoint, EndpointCheck, EndpointInput, ScreenshotAttempt, Tag } from '../types'

const endpoints = ref<Endpoint[]>([])
const tags = ref<Tag[]>([])
const total = ref(0)
const error = ref('')
const formError = ref('')
const busy = ref(false)
const modal = ref<'create' | 'edit' | null>(null)
const editing = ref<Endpoint | null>(null)
const deleteTarget = ref<Endpoint | null>(null)
const deleteBusy = ref(false)
const deleteError = ref('')
const detail = ref<Endpoint | null>(null)
const checks = ref<EndpointCheck[]>([])
const screenshotPreview = ref<ScreenshotAttempt | null>(null)
const retryingScreenshot = ref('')

const filters = reactive({
  page: 1,
  page_size: 20,
  sort: 'created_desc',
  state: '',
  active: '',
  method: '',
  tag: '',
  search: ''
})

const pages = computed(() => Math.max(1, Math.ceil(total.value / filters.page_size)))
const shownCount = computed(() => endpoints.value.length)
const activeCount = computed(() => endpoints.value.filter((endpoint) => endpoint.active).length)
const alertCount = computed(
  () => endpoints.value.filter((endpoint) => endpoint.state === 'warning' || endpoint.state === 'offline').length
)
const inactiveCount = computed(() => endpoints.value.filter((endpoint) => !endpoint.active).length)

function params() {
  const query = new URLSearchParams()
  query.set('page', String(filters.page))
  query.set('page_size', String(filters.page_size))
  query.set('sort', filters.sort)
  if (filters.state) query.set('state', filters.state)
  if (filters.active) query.set('active', filters.active)
  if (filters.method) query.set('method', filters.method)
  if (filters.tag) query.set('tag', filters.tag)
  if (filters.search) query.set('search', filters.search)
  return query
}

async function load() {
  error.value = ''
  try {
    const response = await api.listEndpoints(params())
    const items = Array.isArray(response.items) ? response.items : []
    endpoints.value = items
    total.value = typeof response.total === 'number' ? response.total : items.length
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Could not load endpoints'
  }
}

async function loadTags() {
  try {
    const response = await api.listTags()
    tags.value = Array.isArray(response.items) ? response.items : []
  } catch {
    tags.value = []
  }
}

async function saveEndpoint(input: EndpointInput) {
  busy.value = true
  formError.value = ''
  try {
    if (modal.value === 'edit' && editing.value) await api.updateEndpoint(editing.value.id, input)
    else await api.createEndpoint(input)
    closeEndpointModal()
    await loadTags()
    await load()
  } catch (err) {
    formError.value = err instanceof Error ? err.message : 'Could not save endpoint'
  } finally {
    busy.value = false
  }
}

function openDelete(endpoint: Endpoint) {
  deleteTarget.value = endpoint
  deleteError.value = ''
}

function closeDeleteModal() {
  if (deleteBusy.value) return
  deleteTarget.value = null
  deleteError.value = ''
}

async function confirmDelete() {
  if (!deleteTarget.value) return
  deleteBusy.value = true
  deleteError.value = ''
  try {
    await api.deleteEndpoint(deleteTarget.value.id)
    deleteTarget.value = null
    deleteError.value = ''
    await load()
  } catch (err) {
    deleteError.value = err instanceof Error ? err.message : 'Could not delete endpoint'
  } finally {
    deleteBusy.value = false
  }
}

async function action(endpoint: Endpoint, kind: 'activate' | 'deactivate' | 'ping') {
  if (kind === 'activate') await api.activateEndpoint(endpoint.id)
  if (kind === 'deactivate') await api.deactivateEndpoint(endpoint.id)
  if (kind === 'ping') await api.pingNow(endpoint.id)
  await load()
}

async function showChecks(endpoint: Endpoint) {
  detail.value = endpoint
  await loadChecks()
  startHistoryRefresh()
}

async function loadChecks() {
  if (!detail.value) return
  const response = await api.listChecks(detail.value.id)
  checks.value = Array.isArray(response.items) ? response.items : []
}

function closeChecks() {
  detail.value = null
  checks.value = []
  screenshotPreview.value = null
  clearHistoryRefresh()
}

function openCreate() {
  editing.value = null
  formError.value = ''
  modal.value = 'create'
}

function openEdit(endpoint: Endpoint) {
  editing.value = endpoint
  formError.value = ''
  modal.value = 'edit'
}

function closeEndpointModal() {
  modal.value = null
  editing.value = null
  formError.value = ''
}

function formatDate(value?: string) {
  if (!value) return ''
  return new Date(value).toLocaleString()
}

function conditionLabel(endpoint: Endpoint) {
  const condition = endpoint.notify_condition
  if (condition.type === 'status_code_equals') return `status = ${condition.value}`
  if (condition.type === 'body_contains') return `body contains "${condition.value || ''}"`
  if (condition.type === 'response_length_changed') return `length changed > ${condition.tolerance_bytes || 0} bytes`
  return 'status changed'
}

function splitUrl(url: string) {
  const match = url.match(/^([a-z][a-z0-9+.-]*:\/\/[^/?#]+)([/?#].*)?$/i)
  if (!match) return { origin: url, path: '' }
  return { origin: match[1], path: match[2] || '' }
}

function urlOrigin(url: string) {
  return splitUrl(url).origin
}

function urlPath(url: string) {
  return splitUrl(url).path
}

function visibleTags(endpoint: Endpoint) {
  return (endpoint.tags || []).slice(0, 3)
}

function hiddenTagCount(endpoint: Endpoint) {
  return Math.max(0, (endpoint.tags || []).length - visibleTags(endpoint).length)
}

function resultLabel(state: string) {
  if (state === 'live') return 'Live'
  if (state === 'warning') return 'HTTP error'
  if (state === 'offline') return 'Network error'
  if (state === 'deactivated') return 'Deactivated'
  return 'Not checked'
}

function screenshotStatusLabel(attempt: ScreenshotAttempt) {
  if (attempt.status === 'pending') return 'Pending'
  if (attempt.status === 'capturing') return 'Capturing'
  if (attempt.status === 'succeeded') return 'Succeeded'
  if (attempt.status === 'unsupported') return 'Unsupported'
  return 'Failed'
}

function screenshotImageURL(attempt: ScreenshotAttempt) {
  return api.screenshotImageURL(attempt.id)
}

async function retryScreenshot(attempt: ScreenshotAttempt) {
  if (attempt.status !== 'failed' || retryingScreenshot.value) return
  retryingScreenshot.value = attempt.id
  try {
    const updated = await api.retryScreenshotAttempt(attempt.id)
    replaceScreenshotAttempt(updated)
    await loadChecks()
  } finally {
    retryingScreenshot.value = ''
  }
}

function replaceScreenshotAttempt(updated: ScreenshotAttempt) {
  for (const check of checks.value) {
    const attempts = check.screenshot_attempts || []
    const index = attempts.findIndex((attempt) => attempt.id === updated.id)
    if (index !== -1) {
      attempts[index] = updated
      check.screenshot_attempts = attempts
      return
    }
  }
}

function startHistoryRefresh() {
  clearHistoryRefresh()
  historyTimer = window.setInterval(loadChecks, 3000)
}

function clearHistoryRefresh() {
  if (historyTimer) {
    window.clearInterval(historyTimer)
    historyTimer = undefined
  }
}

let timer: number | undefined
let historyTimer: number | undefined
onMounted(() => {
  void load()
  void loadTags()
  timer = window.setInterval(load, 5000)
})
onUnmounted(() => {
  if (timer) window.clearInterval(timer)
  clearHistoryRefresh()
})
</script>

<template>
  <section class="dashboard-hero">
    <div>
      <p class="eyebrow">Recon monitor</p>
      <h2>Endpoints</h2>
      <p class="subtle">Monitor endpoint checks, response changes, and notify-once alerts from one workspace.</p>
    </div>
    <button class="primary hero-action" @click="openCreate">
      <Plus :size="18" />
      Add endpoint
    </button>
  </section>

  <section class="metric-strip">
    <div class="metric">
      <span>Shown</span>
      <strong>{{ shownCount }}</strong>
    </div>
    <div class="metric">
      <span>Active</span>
      <strong>{{ activeCount }}</strong>
    </div>
    <div class="metric">
      <span>Deactivated</span>
      <strong>{{ inactiveCount }}</strong>
    </div>
    <div class="metric">
      <span>Needs attention</span>
      <strong>{{ alertCount }}</strong>
    </div>
  </section>

  <section class="toolbar">
    <label class="search-field">
      <Search :size="16" />
      <input v-model="filters.search" placeholder="Search by URL or name" @change="load" />
    </label>
    <div class="filters">
      <select v-model="filters.active" @change="load">
        <option value="">Monitoring: any</option>
        <option value="true">Monitoring: on</option>
        <option value="false">Monitoring: off</option>
      </select>
      <select v-model="filters.state" @change="load">
        <option value="">Last result: any</option>
        <option value="unknown">Not checked</option>
        <option value="live">Live</option>
        <option value="warning">HTTP error</option>
        <option value="offline">Network error</option>
        <option value="deactivated">Deactivated</option>
      </select>
      <select v-model="filters.method" @change="load">
        <option value="">Any method</option>
        <option>GET</option>
        <option>HEAD</option>
        <option>POST</option>
        <option>PUT</option>
        <option>PATCH</option>
        <option>DELETE</option>
        <option>OPTIONS</option>
      </select>
      <select v-model="filters.tag" @change="filters.page = 1; load()">
        <option value="">Any tag</option>
        <option v-for="tag in tags" :key="tag.id || tag.name" :value="tag.name">{{ tag.name }}</option>
      </select>
      <select v-model="filters.sort" @change="load">
        <option value="created_desc">Newest created</option>
        <option value="created_asc">Oldest created</option>
        <option value="updated_desc">Recently updated</option>
        <option value="updated_asc">Least recently updated</option>
        <option value="active_desc">Monitoring on first</option>
        <option value="active_asc">Monitoring off first</option>
        <option value="state_asc">Last result</option>
        <option value="last_checked_desc">Recently checked</option>
      </select>
    </div>
  </section>

  <p v-if="error" class="error">{{ error }}</p>

  <section class="table-wrap">
    <table>
      <thead>
        <tr>
          <th class="last-result-cell">Last result</th>
          <th class="name-cell">Name</th>
          <th class="url-cell">URL</th>
          <th>Method</th>
          <th>Interval</th>
          <th class="condition-cell"><span>Condition</span></th>
          <th>Monitoring</th>
          <th class="date-cell">Last checked</th>
          <th class="date-cell">Created</th>
          <th class="date-cell">Updated</th>
          <th class="actions-cell">Actions</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="endpoint in endpoints" :key="endpoint.id">
          <td class="last-result-cell" data-label="Last result">
            <span v-if="endpoint.state !== 'unknown'" class="state-dot" :class="endpoint.state" />
            {{ resultLabel(endpoint.state) }}
          </td>
          <td class="name-cell" data-label="Name" :title="endpoint.name || ''">
            <span>{{ endpoint.name || '' }}</span>
          </td>
          <td class="url-cell" data-label="URL" :title="endpoint.url">
            <span class="url-origin">{{ urlOrigin(endpoint.url) }}</span><span class="url-path">{{ urlPath(endpoint.url) }}</span>
            <span v-if="endpoint.tags?.length" class="endpoint-tags">
              <span v-for="tag in visibleTags(endpoint)" :key="tag.id || tag.name" class="tag-chip" :class="`tag-color-${tag.color}`">
                {{ tag.name }}
              </span>
              <span v-if="hiddenTagCount(endpoint)" class="tag-chip tag-more">+{{ hiddenTagCount(endpoint) }}</span>
            </span>
          </td>
          <td data-label="Method">{{ endpoint.http_method }}</td>
          <td data-label="Interval">{{ endpoint.ping_interval }}</td>
          <td class="condition-cell" data-label="Condition"><span class="condition-label">{{ conditionLabel(endpoint) }}</span></td>
          <td data-label="Monitoring">{{ endpoint.active ? 'On' : 'Off' }}</td>
          <td class="date-cell" data-label="Last checked">{{ formatDate(endpoint.last_checked_at) }}</td>
          <td class="date-cell" data-label="Created">{{ formatDate(endpoint.created_at) }}</td>
          <td class="date-cell" data-label="Updated">{{ formatDate(endpoint.updated_at) }}</td>
          <td class="actions-cell" data-label="Actions">
            <div class="row-actions">
              <button class="icon-button" title="Edit endpoint" @click="openEdit(endpoint)"><Edit3 :size="15" /></button>
              <button class="icon-button" title="Delete endpoint" @click="openDelete(endpoint)"><Trash2 :size="15" /></button>
              <button
                v-if="endpoint.active"
                class="icon-button"
                title="Turn monitoring off"
                @click="action(endpoint, 'deactivate')"
              >
                <Power :size="15" />
              </button>
              <button v-else class="icon-button" title="Turn monitoring on" @click="action(endpoint, 'activate')">
                <Power :size="15" />
              </button>
              <button class="icon-button" title="Ping now" @click="action(endpoint, 'ping')"><RefreshCw :size="15" /></button>
              <button class="icon-button" title="View check history" @click="showChecks(endpoint)">
                <History :size="15" />
              </button>
            </div>
          </td>
        </tr>
        <tr v-if="endpoints.length === 0">
          <td colspan="11" class="empty-cell">
            <div class="empty-state">
              <p class="eyebrow">No endpoints yet</p>
              <h3>Add your first endpoint</h3>
              <p>Start monitoring an HTTP target with a URL, interval, and notify condition.</p>
              <button class="primary" @click="openCreate">
                <Plus :size="17" />
                Add endpoint
              </button>
            </div>
          </td>
        </tr>
      </tbody>
    </table>
  </section>

  <footer class="pagination">
    <button :disabled="filters.page <= 1" @click="filters.page--; load()">Previous</button>
    <span>Page {{ filters.page }} of {{ pages }} · {{ total }} endpoints</span>
    <button :disabled="filters.page >= pages" @click="filters.page++; load()">Next</button>
    <select v-model.number="filters.page_size" @change="filters.page = 1; load()">
      <option :value="20">20</option>
      <option :value="50">50</option>
      <option :value="100">100</option>
      <option :value="250">250</option>
    </select>
  </footer>

  <div v-if="modal" class="modal-backdrop">
    <section class="modal panel">
      <header class="modal-header">
        <h2>{{ modal === 'edit' ? 'Edit endpoint' : 'Create endpoint' }}</h2>
        <button class="icon-button" title="Close" @click="closeEndpointModal"><X :size="16" /></button>
      </header>
      <EndpointForm :endpoint="editing" :error="formError" @save="saveEndpoint" @cancel="closeEndpointModal" />
      <div v-if="busy" class="overlay">Saving...</div>
    </section>
  </div>

  <div v-if="deleteTarget" class="modal-backdrop" @click.self="closeDeleteModal">
    <section class="modal panel confirmation-modal">
      <header class="modal-header">
        <h2>Delete endpoint</h2>
        <button class="icon-button" title="Close" :disabled="deleteBusy" @click="closeDeleteModal">
          <X :size="16" />
        </button>
      </header>
      <div class="modal-body confirmation-body">
        <div>
          <p class="confirmation-title">{{ deleteTarget.name || deleteTarget.url }}</p>
          <p class="muted">This endpoint and its check history will be removed.</p>
        </div>
        <p v-if="deleteError" class="error">{{ deleteError }}</p>
        <footer class="modal-actions">
          <button type="button" :disabled="deleteBusy" @click="closeDeleteModal">Cancel</button>
          <button type="button" class="danger-button" :disabled="deleteBusy" @click="confirmDelete">
            <Trash2 :size="14" />
            {{ deleteBusy ? 'Deleting...' : 'Delete endpoint' }}
          </button>
        </footer>
      </div>
      <div v-if="deleteBusy" class="overlay">Deleting...</div>
    </section>
  </div>

  <div v-if="detail" class="modal-backdrop">
    <section class="modal panel">
      <header class="modal-header">
        <h2>Check history</h2>
        <button class="icon-button" title="Close" @click="closeChecks"><X :size="16" /></button>
      </header>
      <table>
        <thead>
          <tr>
            <th>Finished</th>
            <th>Status</th>
            <th>Length</th>
            <th>Duration</th>
            <th>Matched</th>
            <th>Error</th>
          </tr>
        </thead>
        <tbody>
          <template v-for="check in checks" :key="check.id">
            <tr>
              <td>{{ formatDate(check.finished_at) }}</td>
              <td>{{ check.status_code ?? '' }}</td>
              <td>{{ check.response_length ?? '' }}{{ check.truncated ? ' truncated' : '' }}</td>
              <td>{{ check.duration_ms }}ms</td>
              <td>{{ check.condition_matched ? 'Yes' : 'No' }}</td>
              <td>{{ check.error || '' }}</td>
            </tr>
            <tr v-for="attempt in check.screenshot_attempts || []" :key="attempt.id" class="screenshot-attempt-row">
              <td colspan="6">
                <div class="screenshot-attempt">
                  <span class="screenshot-attempt-label">Screenshot</span>
                  <span>{{ screenshotStatusLabel(attempt) }}</span>
                  <span v-if="attempt.error" class="muted">{{ attempt.error }}</span>
                  <button v-if="attempt.image_available" type="button" @click="screenshotPreview = attempt">View</button>
                  <button
                    v-if="attempt.status === 'failed'"
                    type="button"
                    :disabled="retryingScreenshot === attempt.id"
                    @click="retryScreenshot(attempt)"
                  >
                    {{ retryingScreenshot === attempt.id ? 'Retrying...' : 'Retry' }}
                  </button>
                </div>
              </td>
            </tr>
          </template>
          <tr v-if="checks.length === 0">
            <td colspan="6" class="empty">No checks recorded.</td>
          </tr>
        </tbody>
      </table>
    </section>
  </div>

  <div v-if="screenshotPreview" class="modal-backdrop" @click.self="screenshotPreview = null">
    <section class="modal panel screenshot-modal">
      <header class="modal-header">
        <h2>Screenshot</h2>
        <button class="icon-button" title="Close" @click="screenshotPreview = null"><X :size="16" /></button>
      </header>
      <div class="screenshot-preview">
        <img :src="screenshotImageURL(screenshotPreview)" alt="Captured endpoint screenshot" />
      </div>
    </section>
  </div>
</template>
