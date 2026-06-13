import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import SettingsPage from './SettingsPage.vue'

const mocks = vi.hoisted(() => ({
  getNotificationSettings: vi.fn(),
  updateNotificationSettings: vi.fn(),
  testNotification: vi.fn(),
  changePassword: vi.fn()
}))

vi.mock('../api/client', () => ({
  api: {
    getNotificationSettings: mocks.getNotificationSettings,
    updateNotificationSettings: mocks.updateNotificationSettings,
    testNotification: mocks.testNotification,
    changePassword: mocks.changePassword
  }
}))

describe('SettingsPage', () => {
  beforeEach(() => {
    mocks.getNotificationSettings.mockReset()
    mocks.updateNotificationSettings.mockReset()
    mocks.testNotification.mockReset()
    mocks.changePassword.mockReset()
    mocks.getNotificationSettings.mockResolvedValue({
      telegram_enabled: true,
      telegram_api_key_set: true,
      telegram_chat_id: '12345',
      telegram_parse_mode: 'Markdown',
      timezone: 'Asia/Yekaterinburg',
      timezone_options: ['UTC', 'Europe/Moscow', 'Asia/Yekaterinburg'],
      proxy: {
        enabled: false,
        address: '',
        username: '',
        password: '',
        password_set: false
      }
    })
  })

  it('does not display saved Telegram tokens', async () => {
    const wrapper = mount(SettingsPage)
    await flushPromises()
    const tokenInput = wrapper.find('input[type="password"]').element as HTMLInputElement
    expect(tokenInput.value).toBe('')
    expect(tokenInput.placeholder).toContain('Token saved')
    expect(wrapper.text()).not.toContain('BOT_TOKEN')
  })

  it('saves notification SOCKS5 proxy settings', async () => {
    mocks.updateNotificationSettings.mockResolvedValueOnce({
      telegram_enabled: true,
      telegram_api_key_set: true,
      telegram_chat_id: '12345',
      telegram_parse_mode: 'Markdown',
      timezone: 'Asia/Yekaterinburg',
      timezone_options: ['UTC', 'Europe/Moscow', 'Asia/Yekaterinburg'],
      proxy: {
        enabled: true,
        address: '127.0.0.1:9050',
        username: 'proxy-user',
        password: '********',
        password_set: true
      }
    })
    const wrapper = mount(SettingsPage)
    await flushPromises()

    await wrapper.findAll('input[type="checkbox"]')[1].setValue(true)
    await wrapper.get('input[placeholder="127.0.0.1"]').setValue('127.0.0.1')
    await wrapper.get('input[placeholder="9050"]').setValue('9050')
    await wrapper.get('input[autocomplete="off"]').setValue('proxy-user')
    await wrapper.get('input[placeholder="Optional with username"]').setValue('proxy-pass')
    await wrapper.findAll('form')[0].trigger('submit')
    await flushPromises()

    expect(mocks.updateNotificationSettings).toHaveBeenCalledWith(
      expect.objectContaining({
        timezone: 'Asia/Yekaterinburg',
        proxy: {
          enabled: true,
          address: '127.0.0.1:9050',
          username: 'proxy-user',
          password: 'proxy-pass',
          password_set: false
        }
      })
    )
  })

  it('shows UTC offsets next to notification timezone options', async () => {
    const wrapper = mount(SettingsPage)
    await flushPromises()

    const labels = wrapper
      .findAll('select[aria-label="Notification timezone"] option')
      .map((option) => option.text())

    expect(labels).toContain('Asia/Yekaterinburg (UTC+5)')
    expect(labels).toContain('Europe/Moscow (UTC+3)')
    expect(labels).toContain('UTC (UTC+0)')
  })

  it('saves the selected notification timezone', async () => {
    mocks.updateNotificationSettings.mockResolvedValueOnce({
      telegram_enabled: true,
      telegram_api_key_set: true,
      telegram_chat_id: '12345',
      telegram_parse_mode: 'Markdown',
      timezone: 'UTC',
      timezone_options: ['UTC', 'Asia/Yekaterinburg'],
      proxy: {
        enabled: false,
        address: '',
        username: '',
        password: '',
        password_set: false
      }
    })
    const wrapper = mount(SettingsPage)
    await flushPromises()

    await wrapper.get('select[aria-label="Notification timezone"]').setValue('UTC')
    await wrapper.findAll('form')[0].trigger('submit')
    await flushPromises()

    expect(mocks.updateNotificationSettings).toHaveBeenCalledWith(expect.objectContaining({ timezone: 'UTC' }))
  })

  it('requires matching new passwords before changing the password', async () => {
    const wrapper = mount(SettingsPage)
    await flushPromises()

    const inputs = wrapper.findAll('input[type="password"]')
    await inputs[1].setValue('old-password')
    await inputs[2].setValue('new-password-1')
    await inputs[3].setValue('new-password-2')
    await wrapper.findAll('form')[1].trigger('submit')

    expect(wrapper.text()).toContain('New passwords do not match')
    expect(mocks.changePassword).not.toHaveBeenCalled()
  })

  it('sends current and new password when changing the password', async () => {
    mocks.changePassword.mockResolvedValueOnce({ ok: true })
    const wrapper = mount(SettingsPage)
    await flushPromises()

    const inputs = wrapper.findAll('input[type="password"]')
    await inputs[1].setValue('old-password')
    await inputs[2].setValue('new-password-1')
    await inputs[3].setValue('new-password-1')
    await wrapper.findAll('form')[1].trigger('submit')
    await flushPromises()

    expect(mocks.changePassword).toHaveBeenCalledWith({
      current_password: 'old-password',
      new_password: 'new-password-1'
    })
    expect(wrapper.text()).toContain('Password changed')
  })
})
