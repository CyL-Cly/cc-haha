import { beforeEach, describe, expect, it, vi } from 'vitest'

const h5 = vi.hoisted(() => ({ get: vi.fn(), enable: vi.fn(), disable: vi.fn(), regenerate: vi.fn(), update: vi.fn() }))
vi.mock('../api/h5Access', () => ({ h5AccessApi: h5 }))

const oldSettings = {
  enabled: true,
  token: 'h5_fixture_stable',
  tokenPreview: 'h5_fixture',
  allowedOrigins: ['https://existing.example'],
  publicBaseUrl: 'http://192.168.1.27:52820',
  fixedPort: null,
  disconnectGraceSeconds: 80,
}

beforeEach(() => {
  vi.resetModules()
  vi.resetAllMocks()
  h5.get.mockResolvedValue({ settings: oldSettings })
  h5.enable.mockResolvedValue({ settings: oldSettings, token: oldSettings.token })
  h5.regenerate.mockResolvedValue({ settings: oldSettings, token: oldSettings.token })
})

describe('H5 launch origin registration', () => {
  it('migrates an old empty or incomplete origin list when explicitly enabling and preserves the stable token', async () => {
    const allowedOrigins = [...oldSettings.allowedOrigins, oldSettings.publicBaseUrl]
    h5.update.mockResolvedValue({ settings: { ...oldSettings, allowedOrigins } })
    const { useSettingsStore } = await import('./settingsStore')
    await expect(useSettingsStore.getState().enableH5Access()).resolves.toBe(oldSettings.token)
    expect(h5.update).toHaveBeenCalledWith({ allowedOrigins })
    expect(useSettingsStore.getState().h5Access).toEqual({ ...oldSettings, allowedOrigins })
  })

  it('uses the exact origin of an approved proxy path and does not add paths or credentials to the allowlist', async () => {
    const settings = { ...oldSettings, publicBaseUrl: 'https://PHONE.example:443/app/' }
    const allowedOrigins = [...settings.allowedOrigins, 'https://phone.example']
    h5.enable.mockResolvedValue({ settings, token: settings.token })
    h5.update.mockResolvedValue({ settings: { ...settings, allowedOrigins } })
    const { useSettingsStore } = await import('./settingsStore')
    await useSettingsStore.getState().enableH5Access()
    expect(h5.update).toHaveBeenCalledWith({ allowedOrigins })
  })

  it('does not rewrite an already registered origin on re-enable or token rotation', async () => {
    const settings = { ...oldSettings, allowedOrigins: [...oldSettings.allowedOrigins, oldSettings.publicBaseUrl] }
    h5.enable.mockResolvedValue({ settings, token: settings.token })
    h5.regenerate.mockResolvedValue({ settings, token: settings.token })
    const { useSettingsStore } = await import('./settingsStore')
    await useSettingsStore.getState().enableH5Access()
    await useSettingsStore.getState().regenerateH5AccessToken()
    expect(h5.update).not.toHaveBeenCalled()
  })

  it('registers the effective server port returned after editing the LAN address', async () => {
    const settings = { ...oldSettings, publicBaseUrl: 'http://192.168.1.42:52820' }
    const allowedOrigins = [...settings.allowedOrigins, settings.publicBaseUrl]
    h5.update.mockResolvedValueOnce({ settings }).mockResolvedValueOnce({ settings: { ...settings, allowedOrigins } })
    const { useSettingsStore } = await import('./settingsStore')
    await useSettingsStore.getState().updateH5AccessSettings({ publicBaseUrl: 'http://192.168.1.42' })
    expect(h5.update.mock.calls).toEqual([[{ publicBaseUrl: 'http://192.168.1.42' }], [{ allowedOrigins }]])
  })

  it.each(['file:///tmp/app', 'https://user:pass@example.test', 'invalid', null])('never admits an invalid or non-HTTP launch origin: %s', async publicBaseUrl => {
    h5.enable.mockResolvedValue({ settings: { ...oldSettings, publicBaseUrl }, token: oldSettings.token })
    const { useSettingsStore } = await import('./settingsStore')
    await useSettingsStore.getState().enableH5Access()
    expect(h5.update).not.toHaveBeenCalled()
  })

  it('surfaces failed origin persistence and retains the actual enabled server state', async () => {
    h5.update.mockRejectedValue(new Error('Cannot persist origin'))
    const { useSettingsStore } = await import('./settingsStore')
    await expect(useSettingsStore.getState().enableH5Access()).rejects.toThrow('Cannot persist origin')
    expect(useSettingsStore.getState().h5Access.enabled).toBe(true)
    expect(useSettingsStore.getState().h5AccessError).toBe('Cannot persist origin')
  })
})
