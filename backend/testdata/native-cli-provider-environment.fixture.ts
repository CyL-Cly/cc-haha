import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import fs from 'node:fs'
const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.homeDir, 'ordinary CLI provider environment')
if (process.env.CC_HAHA_CLI_PROVIDER_FIXTURE !== '1') throw new Error('Unowned provider environment fixture')
await import('../../preload')
const { mock } = await import('bun:test')
const absent = { settings: {}, errors: [] }
mock.module(new URL('../../src/utils/settings/mdm/settings.ts', import.meta.url).pathname, () => ({ startMdmSettingsLoad() {}, ensureMdmSettingsLoaded: async () => {}, getMdmSettings: () => absent, getHkcuSettings: () => absent, clearMdmSettingsCache() {}, setMdmSettingsCache() {}, refreshMdmSettings: async () => ({ mdm: absent, hkcu: absent }) }))
mock.module(new URL('../../src/utils/secureStorage/index.ts', import.meta.url).pathname, () => ({ getSecureStorage: () => ({ name: 'owned-fake-provider-fixture', read: () => null, readAsync: async () => null, update: () => { throw new Error('Unowned credential write') }, delete: () => { throw new Error('Unowned credential delete') } }) }))
const mutableFS = require('node:fs')
for (const name of ['readFileSync', 'readdirSync', 'statSync', 'lstatSync', 'existsSync']) {
  const original = mutableFS[name]
  mutableFS[name] = (...args: any[]) => {
    if (typeof args[0] === 'string' && args[0].startsWith('/Library/Application Support/ClaudeCode')) {
      if (name === 'existsSync') return false
      throw Object.assign(new Error('Owned absent managed policy'), { code: 'ENOENT' })
    }
    return original(...args)
  }
}
process.env.CLAUDE_CODE_ENTRYPOINT = 'cli'
const state = await import('../../src/bootstrap/state')
state.setOriginalCwd(process.cwd())
const { enableConfigs } = await import('../../src/utils/config')
enableConfigs()
const { applySafeConfigEnvironmentVariables } = await import('../../src/utils/managedEnv')
const { isEnvTruthy } = await import('../../src/utils/envUtils')
const { activeProviderNeedsProxy, getManagedEnvKeys } = await import('../../src/server/services/providerRuntimeEnv')
const { stopStandaloneProviderProxyForTests } = await import('../../src/server/proxy/standaloneProviderProxy')
try {
  applySafeConfigEnvironmentVariables()
  const environment: Record<string, string> = {}
  for (const name of [...getManagedEnvKeys(), 'OWNED_LEGACY', 'OWNED_TRUSTED']) {
    if (process.env[name] !== undefined) environment[name] = process.env[name]!
  }
  const needsProxy = !isEnvTruthy(process.env.CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST) && activeProviderNeedsProxy(process.env.CLAUDE_CONFIG_DIR!)
  let health: unknown = null
  let probe: unknown = null
  if (needsProxy) {
    const address = new URL(process.env.ANTHROPIC_BASE_URL!)
    if (address.hostname !== '127.0.0.1' || address.pathname !== '/proxy' || !address.port) throw new Error('Unowned Source proxy')
    health = await (await fetch(address.origin + '/health')).json()
    if (input.probe) {
      const response = await fetch(address.origin + '/proxy/v1/messages', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'x-api-key': 'proxy-managed' },
        body: JSON.stringify(input.probe),
      })
      probe = { status: response.status, body: await response.json() }
    }
    environment.ANTHROPIC_BASE_URL = 'http://127.0.0.1:<PORT>/proxy'
  }
  process.stdout.write(JSON.stringify({ environment, health, probe }) + '\n')
} finally {
  stopStandaloneProviderProxyForTests()
}
