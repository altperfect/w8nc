<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { ArrowLeft, ArrowRight, ChevronRight, Copy, Plus, Search, Terminal, X } from '@lucide/vue'
import { api } from '../api/client'
import { parseCurl } from '../utils/curl'
import type { Endpoint, EndpointInput } from '../types'

const emit = defineEmits<{ select: [Partial<EndpointInput>, string?]; cancel: [] }>()
const step = ref<'choose' | 'import' | 'duplicate'>('choose')
const root = ref<HTMLElement | null>(null)
const titles = { choose: 'Add endpoint', import: 'Import from curl', duplicate: 'Duplicate endpoint' }
const descriptions = {
  choose: 'How would you like to get started?',
  import: 'Bring an existing request into your monitoring setup.',
  duplicate: 'Choose an endpoint to use as your starting point.'
}
let previousStep = 'choose'
async function focusStep() {
  await nextTick()
  const target = step.value === 'choose' ? `[data-choice="${previousStep === 'choose' ? 'create' : previousStep}"]` : '[data-step-input]'
  root.value?.querySelector<HTMLElement>(target)?.focus({ preventScroll: true })
}
onMounted(focusStep)
watch(step, (_, previous) => { previousStep = previous; void focusStep() })
function back() { step.value = 'choose'; error.value = '' }
const curlPlaceholder = "curl 'https://api.example.com/health' \\\n  -H 'Accept: application/json'"
const command = ref('')
const error = ref('')
const loading = ref(false)
const endpoints = ref<Endpoint[]>([])
const search = ref('')
const visible = computed(() => endpoints.value.filter(endpoint => `${endpoint.name || ''} ${endpoint.url}`.toLowerCase().includes(search.value.toLowerCase())))

function importCommand() {
  error.value = ''
  try { emit('select', parseCurl(command.value)) }
  catch (err) { error.value = err instanceof Error ? err.message : 'Could not import command.' }
}
async function loadEndpoints() {
  step.value = 'duplicate'
  loading.value = true
  error.value = ''
  endpoints.value = []
  try {
    for (let page = 1; ; page++) {
      const response = await api.listEndpoints(new URLSearchParams({ page: String(page), page_size: '100', sort: 'created_desc' }))
      const items = response.items || []
      endpoints.value.push(...items)
      if (!items.length || endpoints.value.length >= response.total) break
    }
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Could not load endpoints.'
  } finally { loading.value = false }
}
function duplicate(endpoint: Endpoint) {
  emit('select', { ...endpoint, name: `${endpoint.name || endpoint.url} - copy` }, endpoint.id)
}
</script>

<template>
  <div ref="root" class="endpoint-entry">
    <header class="entry-header">
      <div class="entry-heading-row">
        <button v-if="step !== 'choose'" type="button" class="entry-icon-button entry-back" aria-label="Back to add endpoint" @click="back">
          <ArrowLeft :size="18" :stroke-width="1.5" aria-hidden="true" />
        </button>
        <h2 id="endpoint-modal-title">{{ titles[step] }}</h2>
        <button type="button" class="entry-icon-button entry-close" aria-label="Close add endpoint" @click="emit('cancel')">
          <X :size="18" :stroke-width="1.5" aria-hidden="true" />
        </button>
      </div>
      <p>{{ descriptions[step] }}</p>
    </header>

    <div v-if="step === 'choose'" class="entry-options">
      <button type="button" data-choice="create" @click="emit('select', {})">
        <span class="entry-option-icon"><Plus :size="20" :stroke-width="1.5" aria-hidden="true" /></span>
        <span class="entry-option-copy"><strong>Create</strong><span>Start with a blank endpoint.</span></span>
        <ChevronRight :size="16" :stroke-width="1.5" class="entry-chevron" aria-hidden="true" />
      </button>
      <button type="button" data-choice="import" @click="step = 'import'">
        <span class="entry-option-icon"><Terminal :size="20" :stroke-width="1.5" aria-hidden="true" /></span>
        <span class="entry-option-copy"><strong>Import</strong><span>Turn a curl command into an endpoint.</span></span>
        <ChevronRight :size="16" :stroke-width="1.5" class="entry-chevron" aria-hidden="true" />
      </button>
      <button type="button" data-choice="duplicate" @click="loadEndpoints">
        <span class="entry-option-icon"><Copy :size="20" :stroke-width="1.5" aria-hidden="true" /></span>
        <span class="entry-option-copy"><strong>Duplicate</strong><span>Reuse the settings of an existing endpoint.</span></span>
        <ChevronRight :size="16" :stroke-width="1.5" class="entry-chevron" aria-hidden="true" />
      </button>
    </div>

    <form v-else-if="step === 'import'" class="entry-import" @submit.prevent="importCommand">
      <div class="entry-content">
        <label for="curl-command" class="entry-field-label">curl command</label>
        <div class="entry-code-editor" :class="{ 'has-error': error }">
          <div class="entry-code-toolbar"><span><Terminal :size="14" :stroke-width="1.5" aria-hidden="true" /> Request</span><span>curl</span></div>
          <textarea id="curl-command" v-model="command" data-step-input aria-label="curl command" :aria-invalid="Boolean(error)" :aria-describedby="error ? 'curl-error' : 'curl-hint'" rows="7" wrap="off" spellcheck="false" autocapitalize="off" autocomplete="off" :placeholder="curlPlaceholder" required @input="error = ''" />
        </div>
        <p v-if="error" id="curl-error" class="entry-error" role="alert">{{ error }}</p>
        <p v-else id="curl-hint" class="entry-hint">The URL, method, headers and body will be filled in for you.</p>
      </div>
      <footer class="entry-footer">
        <span>Review the details before saving.</span>
        <button class="primary" :disabled="!command.trim()">Continue <ArrowRight :size="14" :stroke-width="1.5" aria-hidden="true" /></button>
      </footer>
    </form>

    <template v-else>
      <div class="entry-content entry-duplicate-content">
        <div class="entry-search">
          <Search :size="16" :stroke-width="1.5" aria-hidden="true" />
          <input v-model="search" data-step-input type="search" aria-label="Search endpoints to duplicate" placeholder="Search by name or URL…" />
        </div>
        <div class="entry-list-heading"><span>Endpoints</span><span v-if="!loading && !error">{{ visible.length }}</span></div>
        <div class="entry-results" :aria-busy="loading">
          <div v-if="loading" class="entry-empty" role="status"><span class="entry-loading-dot" />Loading endpoints…</div>
          <div v-else-if="error" class="entry-empty"><p class="entry-error" role="alert">{{ error }}</p><button type="button" @click="loadEndpoints">Retry</button></div>
          <template v-else>
            <div v-if="!endpoints.length" class="entry-empty"><Copy :size="24" :stroke-width="1.5" aria-hidden="true" /><strong>No endpoints to duplicate yet.</strong><span>Create your first endpoint to get started.</span><button type="button" @click="emit('select', {})">Create endpoint</button></div>
            <div v-else-if="!visible.length" class="entry-empty"><Search :size="24" :stroke-width="1.5" aria-hidden="true" /><strong>No matching endpoints.</strong><span>Try another name or URL.</span><button type="button" @click="search = ''; focusStep()">Clear search</button></div>
            <div v-else class="duplicate-list">
              <button v-for="endpoint in visible" :key="endpoint.id" type="button" @click="duplicate(endpoint)">
                <span class="entry-method">{{ endpoint.http_method }}</span>
                <span class="entry-endpoint-copy"><strong>{{ endpoint.name || endpoint.url }}</strong><span :title="endpoint.url">{{ endpoint.url }}</span></span>
                <ChevronRight :size="16" :stroke-width="1.5" class="entry-chevron" aria-hidden="true" />
              </button>
            </div>
          </template>
        </div>
      </div>
      <footer class="entry-footer"><span>You can rename the copy and review its settings next.</span></footer>
    </template>
  </div>
</template>
