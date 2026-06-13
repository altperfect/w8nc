import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import DashboardPage from './DashboardPage.vue'

const mocks = vi.hoisted(() => ({
  listEndpoints: vi.fn(),
  listTags: vi.fn(),
  deleteEndpoint: vi.fn(),
  activateEndpoint: vi.fn(),
  deactivateEndpoint: vi.fn(),
  pingNow: vi.fn(),
  listChecks: vi.fn(),
  retryScreenshotAttempt: vi.fn()
}))

vi.mock('../api/client', () => ({
  api: {
    listEndpoints: mocks.listEndpoints,
    listTags: mocks.listTags,
    deleteEndpoint: mocks.deleteEndpoint,
    activateEndpoint: mocks.activateEndpoint,
    deactivateEndpoint: mocks.deactivateEndpoint,
    pingNow: mocks.pingNow,
    listChecks: mocks.listChecks,
    retryScreenshotAttempt: mocks.retryScreenshotAttempt,
    screenshotImageURL: (id: string) => `/api/screenshot-attempts/${id}/image`
  }
}))

describe('DashboardPage', () => {
  beforeEach(() => {
    mocks.listEndpoints.mockReset()
    mocks.listTags.mockReset()
    mocks.deleteEndpoint.mockReset()
    mocks.activateEndpoint.mockReset()
    mocks.deactivateEndpoint.mockReset()
    mocks.pingNow.mockReset()
    mocks.listChecks.mockReset()
    mocks.retryScreenshotAttempt.mockReset()
    mocks.listEndpoints.mockResolvedValue({
      items: [
        {
          id: 'endpoint-1',
          name: 'Admin panel',
          description: 'Watch this endpoint after deploys',
          url: 'https://example.com/admin',
          http_method: 'GET',
          headers: [],
          request_body_enabled: false,
          request_body: '',
          ping_interval_seconds: 15,
          ping_interval: '15s',
          notify_condition: { type: 'status_code_changed' },
          notify_once: true,
          notification_template: '',
          screenshot_on_match: false,
          tags: [
            { id: 'tag-1', name: 'prod', color: 'teal' },
            { id: 'tag-2', name: 'auth', color: 'blue' },
            { id: 'tag-3', name: 'external', color: 'gray' },
            { id: 'tag-4', name: 'slow', color: 'amber' }
          ],
          active: true,
          state: 'unknown',
          created_at: '2026-06-12T00:00:00Z',
          updated_at: '2026-06-12T00:00:00Z',
          version: 1
        }
      ],
      page: 1,
      page_size: 20,
      total: 1
    })
    mocks.listTags.mockResolvedValue({
      items: [{ id: 'tag-1', name: 'prod', color: 'teal', endpoint_count: 1 }],
      colors: ['slate', 'blue', 'teal', 'green', 'amber', 'rose', 'violet', 'gray']
    })
    mocks.listChecks.mockResolvedValue({ items: [], page: 1, page_size: 20, total: 0 })
  })

  it('renders endpoints returned by the API', async () => {
    const wrapper = mount(DashboardPage)
    await flushPromises()
    expect(wrapper.text()).toContain('Admin panel')
    expect(wrapper.text()).toContain('https://example.com/admin')
    expect(wrapper.find('.url-origin').text()).toBe('https://example.com')
    expect(wrapper.find('.url-path').text()).toBe('/admin')
    expect(wrapper.find('.tag-chip').text()).toContain('prod')
    expect(wrapper.find('.note-info').exists()).toBe(true)
    expect(wrapper.text()).toContain('Watch this endpoint after deploys')
    expect(wrapper.find('.tag-overflow').text()).toContain('+1')
    expect(wrapper.find('.tag-overflow-tooltip').text()).toContain('slow')
    expect(wrapper.find('th:nth-child(7)').text()).not.toBe('Monitoring')
    expect(wrapper.find('.monitoring-action.monitoring-on').exists()).toBe(true)
  })

  it('filters endpoints by tag', async () => {
    const wrapper = mount(DashboardPage)
    await flushPromises()

    await wrapper.findAll('select').find((select) => select.text().includes('Any tag'))?.setValue('prod')
    await flushPromises()

    const calls = mocks.listEndpoints.mock.calls
    const params = calls[calls.length - 1][0] as URLSearchParams
    expect(params.get('tag')).toBe('prod')
  })

  it('applies metric tile filters', async () => {
    const wrapper = mount(DashboardPage)
    await flushPromises()

    const metricButtons = wrapper.findAll('.metric-button')

    await metricButtons[0].trigger('click')
    await flushPromises()
    let params = mocks.listEndpoints.mock.calls.at(-1)?.[0] as URLSearchParams
    expect(params.get('active')).toBe('true')
    expect(params.get('state')).toBeNull()

    await metricButtons[1].trigger('click')
    await flushPromises()
    params = mocks.listEndpoints.mock.calls.at(-1)?.[0] as URLSearchParams
    expect(params.get('active')).toBe('false')
    expect(params.get('state')).toBeNull()

    await metricButtons[2].trigger('click')
    await flushPromises()
    params = mocks.listEndpoints.mock.calls.at(-1)?.[0] as URLSearchParams
    expect(params.get('active')).toBeNull()
    expect(params.get('state')).toBe('needs_attention')

    await wrapper.get('input[placeholder="Search by URL or name"]').setValue('api')
    await wrapper.findAll('select').find((select) => select.text().includes('Any method'))?.setValue('POST')
    await wrapper.findAll('select').find((select) => select.text().includes('Newest created'))?.setValue('updated_desc')
    await metricButtons[2].trigger('click')
    await flushPromises()
    params = mocks.listEndpoints.mock.calls.at(-1)?.[0] as URLSearchParams
    expect(params.get('active')).toBeNull()
    expect(params.get('state')).toBeNull()
    expect(params.get('method')).toBeNull()
    expect(params.get('search')).toBeNull()
    expect(params.get('sort')).toBe('created_desc')
  })

  it('renders not checked without a state dot', async () => {
    const wrapper = mount(DashboardPage)
    await flushPromises()

    expect(wrapper.text()).toContain('Not checked')
    expect(wrapper.find('.state-dot').exists()).toBe(false)
  })

  it('keeps rendering when the API returns null items', async () => {
    mocks.listEndpoints.mockResolvedValueOnce({
      items: null,
      page: 1,
      page_size: 20,
      total: 0
    })
    const wrapper = mount(DashboardPage)
    await flushPromises()
    expect(wrapper.text()).toContain('No endpoints yet')
    expect(wrapper.text()).toContain('Add endpoint')
  })

  it('confirms endpoint deletion in an app modal', async () => {
    const wrapper = mount(DashboardPage)
    await flushPromises()

    await wrapper.find('button[title="Delete endpoint"]').trigger('click')

    expect(wrapper.text()).toContain('This endpoint and its check history will be removed.')
    expect(mocks.deleteEndpoint).not.toHaveBeenCalled()

    await wrapper
      .findAll('button')
      .find((button) => button.text() === 'Delete endpoint')
      ?.trigger('click')
    await flushPromises()

    expect(mocks.deleteEndpoint).toHaveBeenCalledWith('endpoint-1')
  })

  it('shows failed screenshot attempts and retries them', async () => {
    mocks.listChecks.mockResolvedValueOnce({
      items: [
        {
          id: 'check-1',
          endpoint_id: 'endpoint-1',
          started_at: '2026-06-12T00:00:00Z',
          finished_at: '2026-06-12T00:00:01Z',
          status_code: 200,
          response_length: 123,
          duration_ms: 100,
          truncated: false,
          condition_matched: true,
          created_at: '2026-06-12T00:00:01Z',
          screenshot_attempts: [
            {
              id: 'shot-1',
              endpoint_id: 'endpoint-1',
              endpoint_check_id: 'check-1',
              status: 'failed',
              error: 'chromium failed',
              image_available: false,
              created_at: '2026-06-12T00:00:01Z',
              updated_at: '2026-06-12T00:00:01Z'
            }
          ]
        }
      ],
      page: 1,
      page_size: 20,
      total: 1
    })
    mocks.retryScreenshotAttempt.mockResolvedValueOnce({
      id: 'shot-1',
      endpoint_id: 'endpoint-1',
      endpoint_check_id: 'check-1',
      status: 'pending',
      image_available: false,
      created_at: '2026-06-12T00:00:01Z',
      updated_at: '2026-06-12T00:00:02Z'
    })
    const wrapper = mount(DashboardPage)
    await flushPromises()

    await wrapper.find('button[title="View check history"]').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('Screenshot')
    expect(wrapper.text()).toContain('Failed')
    expect(wrapper.text()).toContain('chromium failed')

    await wrapper.findAll('button').find((button) => button.text() === 'Retry')?.trigger('click')
    await flushPromises()

    expect(mocks.retryScreenshotAttempt).toHaveBeenCalledWith('shot-1')
    wrapper.unmount()
  })
})
