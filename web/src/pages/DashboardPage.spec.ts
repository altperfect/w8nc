import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import DashboardPage from './DashboardPage.vue'

const mocks = vi.hoisted(() => ({
  listEndpoints: vi.fn(),
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
    mocks.listChecks.mockResolvedValue({ items: [], page: 1, page_size: 20, total: 0 })
  })

  it('renders endpoints returned by the API', async () => {
    const wrapper = mount(DashboardPage)
    await flushPromises()
    expect(wrapper.text()).toContain('Admin panel')
    expect(wrapper.text()).toContain('https://example.com/admin')
    expect(wrapper.find('.url-origin').text()).toBe('https://example.com')
    expect(wrapper.find('.url-path').text()).toBe('/admin')
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
