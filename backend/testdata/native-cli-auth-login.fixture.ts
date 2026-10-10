import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import fs from 'node:fs'
import { resolve } from 'node:path'
const home = assertDisposableFixtureHome(process.env.HOME, 'CLI auth login')
if (process.env.CC_HAHA_CLI_LOGIN_FIXTURE !== '1') throw new Error('Unowned CLI auth login')
const peer = new URL(process.env.CC_HAHA_CLI_LOGIN_PEER ?? '')
if (peer.protocol !== 'https:' || peer.hostname !== '127.0.0.1') throw new Error('CLI login fixture requires actual private TLS')
const originalFetch = globalThis.fetch
const guardedFetch = ((input: RequestInfo | URL, options?: RequestInit) => {
  const target = new URL(input instanceof Request ? input.url : String(input))
  if (target.origin === 'https://auth.openai.com') return originalFetch(peer.origin + target.pathname + target.search, options)
  if (!['127.0.0.1', 'localhost', '[::1]'].includes(target.hostname)) throw new Error('Unowned Source OAuth outbound fetch')
  return originalFetch(input, options)
}) as typeof fetch
Object.assign(guardedFetch, originalFetch)
globalThis.fetch = guardedFetch
const securePath = resolve(home, 'secure-fixture.json')
const { mock } = await import('bun:test')
await import('../../preload')
const oauth = await import('../../src/constants/oauth')
const actualConfig = oauth.getOauthConfig()
mock.module(new URL('../../src/constants/oauth.ts', import.meta.url).pathname, () => ({ ...oauth, getOauthConfig: () => ({ ...actualConfig, BASE_API_URL: peer.origin, TOKEN_URL: peer.origin + '/v1/oauth/token', API_KEY_URL: peer.origin + '/api/oauth/claude_cli/create_api_key', ROLES_URL: peer.origin + '/api/oauth/claude_cli/roles' }) }))
mock.module(new URL('../../src/utils/secureStorage/index.ts', import.meta.url).pathname, () => ({ getSecureStorage: () => ({
  name: 'owned-private-opaque-secure-store',
  read: () => { try { return fs.existsSync(securePath) ? JSON.parse(fs.readFileSync(securePath, 'utf8')) : null } catch { return null } },
  readAsync: async () => { try { return fs.existsSync(securePath) ? JSON.parse(fs.readFileSync(securePath, 'utf8')) : null } catch { return null } },
  update: (data: any) => { try { fs.writeFileSync(securePath, JSON.stringify(data, null, 2) + '\n', { mode: 0o600 }); return { success: true } } catch (error) { fs.writeFileSync(resolve(home,'secure-write-failure.json'), JSON.stringify({ code:(error as any).code })); return { success:false, warning:'owned filesystem secure write failed' } } },
  delete: () => { try { fs.rmSync(securePath, { force: true }); return true } catch { return false } },
}) }))
const execaOriginal = await import('execa')
const ownedExecaSpawn = execaOriginal.execa
mock.module('execa', () => ({ ...execaOriginal, execa: async (command: string, args: string[], options: any = {}) => {
  if (command !== 'security' || args.join(' ') !== '-i' || typeof options.input !== 'string') throw new Error('Unowned external auth process')
  fs.writeFileSync(resolve(home, 'legacy-save-fixture.txt'), options.input, { mode: 0o600 })
  if (process.env.CC_HAHA_CLI_LOGIN_SECURITY_MODE === 'nonzero') return ownedExecaSpawn('/usr/bin/false', [], { ...options, input: undefined })
  if (process.env.CC_HAHA_CLI_LOGIN_SECURITY_MODE === 'missing') return ownedExecaSpawn(resolve(home,'owned-missing-security'), [], { ...options, input: undefined })
  return { exitCode: 0, stdout: '', stderr: '' }
} }))
const portable = await import('../../src/utils/authPortable')
mock.module(new URL('../../src/utils/authPortable.ts', import.meta.url).pathname, () => ({ ...portable, maybeRemoveApiKeyFromMacOSKeychainThrows: async () => { fs.appendFileSync(resolve(home, 'legacy-delete.txt'), 'owned-external-delete\n') } }))
// External telemetry/feature-refresh boundaries have no owned producer in this
// command fixture. Actual logout/install/files/clients remain original.
mock.module(new URL('../../src/utils/telemetry/instrumentation.ts', import.meta.url).pathname, () => ({ flushTelemetry: async () => {} }))
const growthbook = await import('../../src/services/analytics/growthbook')
mock.module(new URL('../../src/services/analytics/growthbook.ts', import.meta.url).pathname, () => ({ ...growthbook, refreshGrowthBookAfterAuthChange: () => {} }))
const emptyPolicy = { settings: JSON.parse(process.env.CC_HAHA_CLI_LOGIN_POLICY ?? '{}'), errors: [] }
mock.module(new URL('../../src/utils/settings/mdm/settings.ts', import.meta.url).pathname, () => ({ startMdmSettingsLoad() {}, ensureMdmSettingsLoaded: async () => {}, getMdmSettings: () => emptyPolicy, getHkcuSettings: () => emptyPolicy, clearMdmSettingsCache() {}, setMdmSettingsCache() {}, refreshMdmSettings: async () => ({ mdm: emptyPolicy, hkcu: emptyPolicy }) }))
const mutableFS = require('node:fs')
for (const name of ['readFileSync', 'readdirSync', 'statSync', 'lstatSync', 'existsSync']) {
  const original = mutableFS[name]
  mutableFS[name] = (...args: any[]) => {
    if (typeof args[0] === 'string' && args[0].startsWith('/Library/Application Support/ClaudeCode')) {
      if (name === 'existsSync') return false
      throw Object.assign(new Error('owned absent managed policy'), { code: 'ENOENT' })
    }
    return original(...args)
  }
}

let browserReceipt: Promise<boolean> | undefined
const actualExit = process.exit.bind(process)
// The fixture joins only its owned external browser transport receipt before
// terminating. Auth/client/install conclusions remain the original functions.
process.exit = ((code?: number) => {
  if (browserReceipt) { void browserReceipt.then(() => actualExit(code), () => actualExit(code)); return undefined }
  return actualExit(code)
}) as typeof process.exit
mock.module(new URL('../../src/utils/browser.ts', import.meta.url).pathname, () => ({ openBrowser: (address: string) => {
  const task = (async () => {
  const url = new URL(address)
  const callback = new URL(url.searchParams.get('redirect_uri')!)
  if (callback.hostname !== 'localhost') throw new Error('Unowned CLI callback address')
  callback.searchParams.set('code', 'fixture-authorization-code')
  callback.searchParams.set('state', process.env.CC_HAHA_CLI_LOGIN_WRONG_STATE === '1' ? 'wrong-fixture-state' : url.searchParams.get('state')!)
  fs.writeFileSync(resolve(home, 'browser-fixture.json'), JSON.stringify({ url: address, callback: callback.toString() }) + '\n')
  const response = await fetch(callback, { redirect: 'manual' })
  fs.writeFileSync(resolve(home, 'callback-fixture.json'), JSON.stringify({ status: response.status, location: response.headers.get('location'), body: await response.text() }) + '\n')
  return true
  })()
  browserReceipt = task
  return task
} }))
process.env.CLAUDE_CODE_ENTRYPOINT = 'cli'
const { enableConfigs } = await import('../../src/utils/config')
enableConfigs()
if (process.env.CC_HAHA_CLI_LOGIN_LISTENER_ONLY === '1') {
  const { AuthCodeListener } = await import('../../src/services/oauth/auth-code-listener')
  const listener = new AuthCodeListener('/callback')
  const port = await listener.start()
  let receipt: Promise<any> | undefined
  const mode = process.env.CC_HAHA_CLI_LOGIN_LISTENER_MODE
  let outcome: any
  try {
    outcome = { code: await listener.waitForAuthorization('fixture-state', async () => {
      if (mode === 'wrongpath') {
        const r = await fetch(`http://localhost:${port}/other`)
        fs.writeFileSync(resolve(home, 'wrong-path.json'), JSON.stringify({ status: r.status, body: await r.text() }))
      }
      const callback = new URL(`http://localhost:${port}/callback`)
      if (mode !== 'no-code') callback.searchParams.set('code', 'fixture-code')
      callback.searchParams.set('state', mode === 'bad-state' ? 'wrong' : 'fixture-state')
      receipt = fetch(callback, { redirect: 'manual' }).then(async r => ({ status: r.status, location: r.headers.get('location'), body: await r.text() }))
      await receipt
    }) }
  } catch (error) { outcome = { error: (error as Error).message } }
  listener.close()
  console.log(JSON.stringify({ ...outcome, response: await receipt, wrongPath: fs.existsSync(resolve(home, 'wrong-path.json')) ? JSON.parse(fs.readFileSync(resolve(home, 'wrong-path.json'), 'utf8')) : null }))
} else if (process.env.CC_HAHA_CLI_LOGIN_CLIENT_ONLY === '1') {
  Date.now = () => 1700000000000
  const { refreshOAuthToken } = await import('../../src/services/oauth/client')
  try { console.log(JSON.stringify({ tokens: await refreshOAuthToken('fixture-refresh', { scopes: ['user:profile', 'user:inference'] }) })) }
  catch (error) { const { getSSLErrorHint } = await import('../../src/services/api/errorUtils'); console.log(JSON.stringify({ error: (error as Error).message, sslHint: getSSLErrorHint(error) })) }
} else {
  const { authLogin } = await import('../../src/cli/handlers/auth')
  await authLogin(JSON.parse(process.env.CC_HAHA_CLI_LOGIN_OPTIONS ?? '{}'))
}
