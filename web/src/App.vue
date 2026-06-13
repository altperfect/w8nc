<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { Activity, LogOut, Moon, Settings, Sun, X } from '@lucide/vue'
import { api } from './api/client'
import LoginPage from './pages/LoginPage.vue'
import DashboardPage from './pages/DashboardPage.vue'
import SettingsPage from './pages/SettingsPage.vue'
import type { MeResponse } from './types'

const loading = ref(true)
const me = ref<MeResponse | null>(null)
const showSettings = ref(false)
const health = ref<{ status: string; database: string; notify_binary: string } | null>(null)
type Theme = 'dark' | 'light'

function preferredTheme(): Theme {
  const stored = window.localStorage.getItem('theme')
  if (stored === 'dark' || stored === 'light') return stored
  return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
}

const theme = ref<Theme>(preferredTheme())
const themeTitle = computed(() => (theme.value === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'))

function applyTheme() {
  document.documentElement.dataset.theme = theme.value
  document.documentElement.style.colorScheme = theme.value
  window.localStorage.setItem('theme', theme.value)
}

function toggleTheme() {
  theme.value = theme.value === 'dark' ? 'light' : 'dark'
  applyTheme()
}

async function loadSession() {
  loading.value = true
  try {
    me.value = await api.me()
    health.value = await api.health()
  } finally {
    loading.value = false
  }
}

async function logout() {
  await api.logout()
  await loadSession()
}

onMounted(() => {
  applyTheme()
  void loadSession()
})
</script>

<template>
  <main class="app-shell">
    <button
      v-if="loading || (me?.auth_enabled && !me.authenticated)"
      class="icon-button theme-toggle floating-theme-toggle"
      :title="themeTitle"
      :aria-label="themeTitle"
      @click="toggleTheme"
    >
      <Sun v-if="theme === 'dark'" :size="16" />
      <Moon v-else :size="16" />
    </button>
    <div v-if="loading" class="loading">Loading...</div>
    <LoginPage v-else-if="me?.auth_enabled && !me.authenticated" @logged-in="loadSession" />
    <template v-else>
      <header class="topbar">
        <div class="brand-block">
          <span class="brand-mark"><Activity :size="16" stroke-width="2" /></span>
          <h1>w8nc</h1>
          <p>Self-hosted endpoint monitoring</p>
        </div>
        <nav class="nav-actions">
          <button class="nav-button" :class="{ active: !showSettings }" @click="showSettings = false">
            <Activity :size="15" />
            Endpoints
          </button>
          <button class="nav-button" :class="{ active: showSettings }" @click="showSettings = true">
            <Settings :size="15" />
            Settings
          </button>
          <button class="icon-button theme-toggle" :title="themeTitle" :aria-label="themeTitle" @click="toggleTheme">
            <Sun v-if="theme === 'dark'" :size="16" />
            <Moon v-else :size="16" />
          </button>
          <button v-if="me?.auth_enabled" class="icon-button" title="Logout" @click="logout">
            <LogOut :size="16" />
          </button>
        </nav>
      </header>

      <section v-if="!me?.auth_enabled" class="banner danger">
        Authentication is disabled. Keep this app bound to localhost or enable auth before exposing it.
      </section>
      <section v-if="health && health.notify_binary !== 'ok'" class="banner warn">
        ProjectDiscovery notify is {{ health.notify_binary }}. Notification events will fail until the binary is configured.
      </section>

      <DashboardPage />

      <div v-if="showSettings" class="modal-backdrop" @click.self="showSettings = false">
        <section class="modal panel settings-modal">
          <header class="modal-header">
            <h2>Settings</h2>
            <button class="icon-button" title="Close" @click="showSettings = false"><X :size="16" /></button>
          </header>
          <SettingsPage />
        </section>
      </div>
    </template>
  </main>
</template>
