import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import EndpointForm from './EndpointForm.vue'

const mocks = vi.hoisted(() => ({
  testEndpointRequest: vi.fn(),
  getLastProxy: vi.fn(),
  getTemplatePlaceholders: vi.fn(),
  listTags: vi.fn(),
  deleteTag: vi.fn()
}))

vi.mock('../api/client', () => ({
  api: {
    testEndpointRequest: mocks.testEndpointRequest,
    getLastProxy: mocks.getLastProxy,
    getTemplatePlaceholders: mocks.getTemplatePlaceholders,
    listTags: mocks.listTags,
    deleteTag: mocks.deleteTag
  }
}))

describe('EndpointForm', () => {
  beforeEach(() => {
    mocks.testEndpointRequest.mockReset()
    mocks.getLastProxy.mockReset()
    mocks.getTemplatePlaceholders.mockReset()
    mocks.listTags.mockReset()
    mocks.deleteTag.mockReset()
    mocks.getLastProxy.mockResolvedValue({ available: false })
    mocks.getTemplatePlaceholders.mockResolvedValue({
      items: ['url', 'checked_at', 'condition_type', 'duration_ms', 'response_body', 'response_headers']
    })
    mocks.listTags.mockResolvedValue({
      items: [{ id: 'tag-1', name: 'prod', color: 'teal', endpoint_count: 1 }],
      colors: ['slate', 'blue', 'teal', 'green', 'amber', 'rose', 'violet', 'gray']
    })
    mocks.deleteTag.mockResolvedValue(undefined)
    mocks.testEndpointRequest.mockResolvedValue({
      status_code: 202,
      response_headers: {
        'Content-Type': ['application/json'],
        'X-Trace': ['abc123']
      },
      response_length: 18,
      duration_ms: 42,
      truncated: false,
      body_preview: '{"ok":true}',
      body_preview_truncated: false
    })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('tests the request using the current form values', async () => {
    const wrapper = mount(EndpointForm)

    expect(wrapper.text()).toContain('Verify your request behaves as expected (for now).')

    await wrapper.get('input[placeholder="https://example.com/admin"]').setValue('https://example.com/api')
    await wrapper.get('input[aria-label="Ping interval amount"]').setValue(30)
    await wrapper.get('select[aria-label="Ping interval unit"]').setValue('m')
    await wrapper.get('input[aria-label="Deactivate after amount"]').setValue(3)
    await wrapper.findAll('input[type="checkbox"]')[2].setValue(true)
    await wrapper.get('input[placeholder="127.0.0.1"]').setValue('127.0.0.1')
    await wrapper.get('input[placeholder="9050"]').setValue('9050')
    await wrapper.get('input[autocomplete="off"]').setValue('proxy-user')
    await wrapper.get('input[placeholder="Optional with username"]').setValue('proxy-pass')
    await wrapper.findAll('button').find((button) => button.text() === 'Add header')?.trigger('click')
    await wrapper.get('input[placeholder="Header name"]').setValue('X-Test')
    await wrapper.get('input[placeholder="Value"]').setValue('value')

    const sendButton = wrapper.findAll('button').find((button) => button.text().includes('Send test'))
    expect(sendButton).toBeTruthy()
    const request = sendButton!.trigger('click')
    await wrapper.vm.$nextTick()
    expect(wrapper.text()).toContain('Sending...')
    await request
    await flushPromises()

    expect(mocks.testEndpointRequest).toHaveBeenCalledWith(
      expect.objectContaining({
        url: 'https://example.com/api',
        http_method: 'GET',
        ping_interval: '30m',
        deactivate_after: '3h',
        proxy: {
          enabled: true,
          address: '127.0.0.1:9050',
          username: 'proxy-user',
          password: 'proxy-pass',
          password_set: false
        },
        headers: [expect.objectContaining({ name: 'X-Test', value: 'value', masked: false, sensitive: false })]
      })
    )
    expect(wrapper.text()).toContain('Test request')
    expect(wrapper.text()).toContain('202')
    expect(wrapper.text()).toContain('X-Trace')
    expect(wrapper.text()).toContain('abc123')
    expect((wrapper.get('details').element as HTMLDetailsElement).open).toBe(false)
  })

  it('renders save errors inside the form', () => {
    const wrapper = mount(EndpointForm, {
      props: {
        error: 'interval must match digits plus unit: s, m, h, or d'
      }
    })

    expect(wrapper.text()).toContain('interval must match digits plus unit')
  })

  it('collapses the description field by default and updates the remaining symbol count', async () => {
    const wrapper = mount(EndpointForm)

    expect((wrapper.get('.description-field').element as HTMLDetailsElement).open).toBe(false)
    expect(wrapper.text()).toContain('200/200 symbols available')

    await wrapper.get('textarea[aria-label="Description"]').setValue('x'.repeat(75))

    expect(wrapper.text()).toContain('125/200 symbols available')
  })

  it('loads notification template placeholders for the tooltip', async () => {
    const wrapper = mount(EndpointForm)
    await flushPromises()

    expect(mocks.getTemplatePlaceholders).toHaveBeenCalled()
    expect(wrapper.text()).toContain('Available values')
    expect(wrapper.text()).toContain('duration_ms')
    expect(wrapper.text()).toContain('checked_at')
    expect(wrapper.text()).toContain('response_body')
    expect(wrapper.text()).toContain('response_headers')
  })

  it('appends a clicked template placeholder on a new line', async () => {
    const wrapper = mount(EndpointForm)
    await flushPromises()

    const template = wrapper.get('textarea[rows="7"]')
    await template.setValue('Existing template')
    await wrapper
      .findAll('.template-placeholder-button')
      .find((button) => button.text() === 'url')
      ?.trigger('click')

    expect((template.element as HTMLTextAreaElement).value).toBe('Existing template\n{{url}}')
  })

  it('adds tags to the endpoint payload', async () => {
    const wrapper = mount(EndpointForm)
    await flushPromises()

    await wrapper.get('textarea[placeholder="Short note about this endpoint"]').setValue('Watch the login path')
    await wrapper.get('input[aria-label="Tag name"]').setValue('Prod')
    await wrapper.get('button[aria-label="Teal"]').trigger('click')
    await wrapper.findAll('button').find((button) => button.text() === 'Add tag')?.trigger('click')

    expect(wrapper.text()).toContain('prod')

    await wrapper.find('form').trigger('submit')

    expect(wrapper.emitted('save')?.[0]?.[0]).toEqual(
      expect.objectContaining({
        description: 'Watch the login path',
        tags: [{ name: 'prod', color: 'teal' }]
      })
    )
  })

  it('confirms before deleting an in-use reusable tag', async () => {
    const wrapper = mount(EndpointForm)
    await flushPromises()

    await wrapper.get('button[aria-label="Delete prod"]').trigger('click')

    expect(wrapper.text()).toContain('This tag is being used with other endpoints. Are you sure?')
    expect(mocks.deleteTag).not.toHaveBeenCalled()

    await wrapper.findAll('button').find((button) => button.text() === 'Delete tag')?.trigger('click')
    await flushPromises()

    expect(mocks.deleteTag).toHaveBeenCalledWith('tag-1')
    expect(wrapper.text()).not.toContain('This tag is being used with other endpoints')
  })

  it('deletes an unused reusable tag without confirmation', async () => {
    mocks.listTags.mockResolvedValueOnce({
      items: [{ id: 'tag-unused', name: 'old', color: 'gray', endpoint_count: 0 }],
      colors: ['slate', 'blue', 'teal', 'green', 'amber', 'rose', 'violet', 'gray']
    })
    const wrapper = mount(EndpointForm)
    await flushPromises()

    await wrapper.get('button[aria-label="Delete old"]').trigger('click')
    await flushPromises()

    expect(mocks.deleteTag).toHaveBeenCalledWith('tag-unused')
    expect(wrapper.text()).not.toContain('This tag is being used with other endpoints')
  })

  it('reuses the last configured SOCKS5 proxy', async () => {
    mocks.getLastProxy.mockResolvedValueOnce({
      available: true,
      proxy: {
        enabled: true,
        address: 'proxy.example:1080',
        username: 'proxy-user',
        password: '********',
        password_set: true
      }
    })
    const wrapper = mount(EndpointForm)

    await wrapper.findAll('input[type="checkbox"]')[2].setValue(true)
    await flushPromises()
    expect(wrapper.text()).toContain('Reuse latest socks5 proxy to proxy.example:1080?')
    await wrapper.findAll('button').find((button) => button.text() === 'Yes')?.trigger('click')

    expect((wrapper.get('input[placeholder="127.0.0.1"]').element as HTMLInputElement).value).toBe('proxy.example')
    expect((wrapper.get('input[placeholder="9050"]').element as HTMLInputElement).value).toBe('1080')
    expect((wrapper.get('input[autocomplete="off"]').element as HTMLInputElement).value).toBe('proxy-user')
    expect((wrapper.get('input[placeholder="Password saved - enter a new password to replace"]').element as HTMLInputElement).value).toBe('********')

    await wrapper.get('input[placeholder="https://example.com/admin"]').setValue('https://example.com/api')
    const sendButton = wrapper.findAll('button').find((button) => button.text().includes('Send test'))
    await sendButton!.trigger('click')
    await flushPromises()

    expect(mocks.testEndpointRequest).toHaveBeenCalledWith(
      expect.objectContaining({
        proxy: {
          enabled: true,
          address: 'proxy.example:1080',
          username: 'proxy-user',
          password: '********',
          password_set: true
        }
      })
    )
  })

  it('includes arbitrary request body text in test requests', async () => {
    const wrapper = mount(EndpointForm)
    const body = '<xml>\n  <probe enabled="true" />\n</xml>'

    await wrapper.get('input[placeholder="https://example.com/admin"]').setValue('https://example.com/api')
    await wrapper.get('select').setValue('POST')
    await wrapper.findAll('input[type="checkbox"]')[0].setValue(true)
    await wrapper.get('textarea[placeholder=\'{"key":"value"}\']').setValue(body)

    const sendButton = wrapper.findAll('button').find((button) => button.text().includes('Send test'))
    await sendButton!.trigger('click')
    await flushPromises()

    expect(mocks.testEndpointRequest).toHaveBeenCalledWith(
      expect.objectContaining({
        http_method: 'POST',
        request_body_enabled: true,
        request_body: body
      })
    )
  })

  it('clears request body before saving when the checkbox is off', async () => {
    const wrapper = mount(EndpointForm, {
      props: {
        endpoint: {
          id: 'endpoint-1',
          name: 'API',
          description: '',
          url: 'https://example.com/api',
          http_method: 'POST',
          headers: [],
          request_body_enabled: true,
          request_body: '{"saved":true}',
          proxy: { enabled: false },
          ping_interval_seconds: 15,
          ping_interval: '15s',
          notify_once: true,
          notify_condition: { type: 'status_code_changed' },
          notification_template: '',
          screenshot_on_match: false,
          tags: [],
          active: true,
          state: 'unknown',
          created_at: new Date().toISOString(),
          updated_at: new Date().toISOString(),
          version: 1
        }
      }
    })

    await wrapper.findAll('input[type="checkbox"]')[0].setValue(false)
    await wrapper.find('form').trigger('submit')

    expect(wrapper.emitted('save')?.[0]?.[0]).toEqual(
      expect.objectContaining({
        request_body_enabled: false,
        request_body: ''
      })
    )
  })

  it('allows screenshot capture only for GET endpoints', async () => {
    const wrapper = mount(EndpointForm)
    const screenshotCheckbox = () =>
      wrapper
        .findAll('label')
        .find((label) => label.text().includes('Screenshot on match'))!
        .find('input')

    expect((screenshotCheckbox().element as HTMLInputElement).disabled).toBe(false)
    expect(wrapper.text()).toContain('Screenshotting is supported only with GET methods')

    await screenshotCheckbox().setValue(true)
    await wrapper.get('select').setValue('POST')

    expect((screenshotCheckbox().element as HTMLInputElement).disabled).toBe(true)
    await wrapper.find('form').trigger('submit')

    expect(wrapper.emitted('save')?.[0]?.[0]).toEqual(expect.objectContaining({ screenshot_on_match: false }))
  })

  it('hides the reuse proxy prompt after five seconds', async () => {
    vi.useFakeTimers()
    mocks.getLastProxy.mockResolvedValueOnce({
      available: true,
      proxy: {
        enabled: true,
        address: 'proxy.example:1080'
      }
    })
    const wrapper = mount(EndpointForm)

    await wrapper.findAll('input[type="checkbox"]')[2].setValue(true)
    await flushPromises()
    expect(wrapper.text()).toContain('Reuse latest socks5 proxy to proxy.example:1080?')

    vi.advanceTimersByTime(5000)
    await wrapper.vm.$nextTick()

    expect(wrapper.text()).not.toContain('Reuse latest socks5 proxy')
  })

  it('closes the reuse proxy prompt with the close button', async () => {
    mocks.getLastProxy.mockResolvedValueOnce({
      available: true,
      proxy: {
        enabled: true,
        address: 'proxy.example:1080'
      }
    })
    const wrapper = mount(EndpointForm)

    await wrapper.findAll('input[type="checkbox"]')[2].setValue(true)
    await flushPromises()
    expect(wrapper.text()).toContain('Reuse latest socks5 proxy to proxy.example:1080?')

    await wrapper.get('button[aria-label="Close reuse proxy prompt"]').trigger('click')

    expect(wrapper.text()).not.toContain('Reuse latest socks5 proxy')
  })

  it('strips non-digits from duration amount fields', async () => {
    const wrapper = mount(EndpointForm)
    const interval = wrapper.get('input[aria-label="Ping interval amount"]')
    const expiry = wrapper.get('input[aria-label="Deactivate after amount"]')

    await interval.setValue('12abc')
    await expiry.setValue('3h')

    expect((interval.element as HTMLInputElement).value).toBe('12')
    expect((expiry.element as HTMLInputElement).value).toBe('3')
  })
})
