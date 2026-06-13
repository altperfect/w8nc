import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api, configureSessionExpiredHandler } from './client'

describe('api client', () => {
  let expiredCount = 0

  beforeEach(() => {
    expiredCount = 0
    configureSessionExpiredHandler(() => {
      expiredCount += 1
    })
    vi.stubGlobal('fetch', vi.fn())
  })

  afterEach(() => {
    configureSessionExpiredHandler()
    vi.unstubAllGlobals()
  })

  it('refreshes the app when a protected request reports an expired session', async () => {
    vi.mocked(fetch).mockResolvedValue(
      new Response(JSON.stringify({ error: 'authentication required' }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' }
      })
    )

    await expect(api.listEndpoints(new URLSearchParams())).rejects.toThrow('authentication required')

    expect(expiredCount).toBe(1)
  })

  it('does not refresh the app for normal login failures', async () => {
    vi.mocked(fetch).mockResolvedValue(
      new Response(JSON.stringify({ error: 'invalid password' }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' }
      })
    )

    await expect(api.login('wrong-password')).rejects.toThrow('invalid password')

    expect(expiredCount).toBe(0)
  })

  it('only refreshes once for repeated expired session responses', async () => {
    vi.mocked(fetch).mockImplementation(() =>
      Promise.resolve(
        new Response(JSON.stringify({ error: 'authentication required' }), {
          status: 401,
          headers: { 'Content-Type': 'application/json' }
        })
      )
    )

    await expect(api.listEndpoints(new URLSearchParams())).rejects.toThrow('authentication required')
    await expect(api.getNotificationSettings()).rejects.toThrow('authentication required')

    expect(expiredCount).toBe(1)
  })
})
