import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import EndpointEntry from './EndpointEntry.vue'

const listEndpoints = vi.hoisted(() => vi.fn())
vi.mock('../api/client', () => ({ api: { listEndpoints } }))
const endpoint = (id: string, name: string) => ({ id, name, url: `https://example.com/${id}`, http_method: 'GET' })

beforeEach(() => listEndpoints.mockReset())

describe('endpoint entry', () => {
  it('loads all pages and searches endpoints by name', async () => {
    listEndpoints.mockResolvedValueOnce({ items: [endpoint('1', 'First')], total: 2 })
      .mockResolvedValueOnce({ items: [endpoint('2', 'Second')], total: 2 })
    const wrapper = mount(EndpointEntry)
    await wrapper.findAll('.entry-options button')[2].trigger('click')
    await flushPromises()
    expect(listEndpoints).toHaveBeenCalledTimes(2)
    expect(listEndpoints.mock.calls[1][0].get('page')).toBe('2')
    await wrapper.get('input').setValue('second')
    expect(wrapper.findAll('.duplicate-list button')).toHaveLength(1)
    await wrapper.get('.duplicate-list button').trigger('click')
    expect(wrapper.emitted('select')?.[0]).toEqual([expect.objectContaining({ name: 'Second - copy' }), '2'])
  })
  it('allows retry after a list error and handles empty lists', async () => {
    listEndpoints.mockRejectedValueOnce(new Error('Unable to load endpoints'))
      .mockResolvedValueOnce({ items: null, total: 0 })
    const wrapper = mount(EndpointEntry)
    await wrapper.findAll('.entry-options button')[2].trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('Unable to load endpoints')
    await wrapper.findAll('button').find(button => button.text() === 'Retry')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('No endpoints to duplicate yet.')
  })
  it('keeps invalid curl commands available for correction', async () => {
    const wrapper = mount(EndpointEntry)
    await wrapper.findAll('.entry-options button')[1].trigger('click')
    await wrapper.get('textarea').setValue('curl --unknown https://example.com')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.get('[role="alert"]').text()).toContain('Unsupported curl option')
    expect(wrapper.emitted('select')).toBeUndefined()
  })
})
