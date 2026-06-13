<script setup lang="ts">
import { ref } from 'vue'
import { api } from '../api/client'

const emit = defineEmits<{ 'logged-in': [] }>()
const password = ref('')
const error = ref('')
const busy = ref(false)

async function submit() {
  error.value = ''
  busy.value = true
  try {
    await api.login(password.value)
    emit('logged-in')
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Login failed'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <section class="login-panel">
    <form class="panel compact" @submit.prevent="submit">
      <h1>Endpoint Pinger</h1>
      <label>
        Password
        <input v-model="password" type="password" autocomplete="current-password" />
      </label>
      <p v-if="error" class="error">{{ error }}</p>
      <button class="primary" :disabled="busy">{{ busy ? 'Signing in...' : 'Sign in' }}</button>
    </form>
  </section>
</template>
