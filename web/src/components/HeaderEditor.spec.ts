import { mount } from '@vue/test-utils'
import { defineComponent, ref } from 'vue'
import { describe, expect, it } from 'vitest'
import HeaderEditor from './HeaderEditor.vue'
import type { HeaderValue } from '../types'

describe('HeaderEditor', () => {
  it('auto-detects and masks sensitive headers with one visible control', async () => {
    const wrapper = mount(
      defineComponent({
        components: { HeaderEditor },
        setup() {
          const headers = ref<HeaderValue[]>([])
          return { headers }
        },
        template: '<HeaderEditor v-model="headers" />'
      })
    )

    await wrapper.find('button').trigger('click')
    const inputs = wrapper.findAll('input')
    await inputs[0].setValue('Authorization')
    await inputs[1].setValue('Bearer token')

    const state = (wrapper.vm as unknown as { headers: HeaderValue[] }).headers
    expect(state[0].sensitive).toBe(true)
    expect(state[0].masked).toBe(true)
    expect(wrapper.text()).toContain('Mask')
    expect(wrapper.text()).not.toContain('Sensitive')
  })
})
