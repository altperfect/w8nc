<script setup lang="ts">
import type { HeaderValue } from '../types'

const headers = defineModel<HeaderValue[]>({ required: true })

const markers = ['authorization', 'token', 'key', 'api-key', 'apikey', 'bearer', 'basic', 'secret', 'cookie', 'session', 'jwt']

function addHeader() {
  headers.value.push({ name: '', value: '', sensitive: false, masked: false })
}

function removeHeader(index: number) {
  headers.value.splice(index, 1)
}

function detect(index: number) {
  const header = headers.value[index]
  const combined = `${header.name} ${header.value}`.toLowerCase()
  const sensitive = markers.some((marker) => combined.includes(marker))
  if (sensitive) {
    setMask(index, true)
  }
}

function setMask(index: number, masked: boolean) {
  headers.value[index].masked = masked
  headers.value[index].sensitive = masked
}

function toggleMask(index: number, event: Event) {
  setMask(index, (event.target as HTMLInputElement).checked)
}
</script>

<template>
  <div class="header-editor">
    <div class="section-title">
      <h3>Headers</h3>
      <button type="button" @click="addHeader">Add header</button>
    </div>
    <div v-for="(header, index) in headers" :key="index" class="header-row">
      <input v-model="header.name" placeholder="Header name" @input="detect(index)" />
      <input v-model="header.value" placeholder="Value" @input="detect(index)" />
      <label class="inline">
        <input type="checkbox" :checked="header.masked || header.sensitive" @change="toggleMask(index, $event)" />
        Mask
      </label>
      <button type="button" class="ghost danger-text" @click="removeHeader(index)">Remove</button>
    </div>
  </div>
</template>
