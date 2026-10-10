import fs from 'node:fs'
import { mock } from 'bun:test'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../scripts/quality-gate/go-parity/client-options-capabilities'
const input = JSON.parse(await Bun.stdin.text())
const home = assertDisposableFixtureHome(input.home, 'model fast status')
if (process.env.CC_HAHA_MODEL_FAST_STATUS_FIXTURE !== '1' || Bun.version !== '1.3.14') throw new Error('Unowned model fast status Source')
const peer = new URL(input.origin)
if (peer.protocol !== 'https:' || peer.hostname !== '127.0.0.1') throw new Error('Actual owned fast TLS required')
// Preserve the original Node HTTP adapter primitives before the general
// fixture guard denies them. Restore only this owned loopback transport;
// Axios request/headers/codec/error and Source auth/refresh remain stock.
const http = require('node:http'), https = require('node:https'), net = require('node:net'), tls = require('node:tls')
const nodePrimitives = { httpRequest: http.request, httpGet: http.get, httpsRequest: https.request, httpsGet: https.get, socketConnect: net.Socket.prototype.connect, tlsConnect: tls.connect }
const capabilities = await installClientFixtureCapabilities(home, { absentPaths: [...absentManagedSettingsFixturePaths(), '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token'], loopbackFetchOrigin: input.origin })
const allowOptions = (value: any) => {
  const target = value instanceof URL ? value : typeof value === 'string' ? new URL(value) : new URL((value.protocol ?? 'https:') + '//' + (value.hostname ?? value.host) + ':' + value.port + (value.path ?? '/'))
  if (target.origin !== peer.origin || target.username || target.password) throw new Error('Unowned fast Axios transport')
}
for (const [module, name, original] of [[http, 'request', nodePrimitives.httpRequest], [http, 'get', nodePrimitives.httpGet], [https, 'request', nodePrimitives.httpsRequest], [https, 'get', nodePrimitives.httpsGet]] as const) module[name] = (...args: any[]) => { allowOptions(args[0]); return original(...args) }
net.Socket.prototype.connect = function (...args: any[]) {
  const value = Array.isArray(args[0]) ? args[0][0] : args[0]
  const host = typeof value === 'object' ? value.host ?? value.hostname : args[1]
  const port = typeof value === 'object' ? value.port : value
  if (host !== '127.0.0.1' || Number(port) !== Number(peer.port)) throw new Error('Unowned fast socket')
  return nodePrimitives.socketConnect.apply(this, args)
}
tls.connect = (...args: any[]) => { const options = args[0]; if (options.host !== '127.0.0.1' || Number(options.port) !== Number(peer.port)) throw new Error('Unowned fast TLS socket'); return nodePrimitives.tlsConnect(...args) }
fs.writeFileSync(join(home, 'root.pem'), input.certificate)
process.env.NODE_EXTRA_CA_CERTS = join(home, 'root.pem')
let secure = input.secure ?? null
mock.module(new URL('../../src/utils/secureStorage/index.ts', import.meta.url).pathname, () => ({ getSecureStorage: () => ({ name: 'owned-fake-model-fast-store', read: () => secure, readAsync: async () => secure, update: (value: any) => { secure = JSON.parse(JSON.stringify(value)); return { success: true } }, delete: () => { throw new Error('No fast status credential delete') } }) }))
mock.module('../../src/utils/secureStorage/keychainPrefetch', () => ({ getLegacyApiKeyPrefetchResult: () => ({ stdout: null }), clearLegacyApiKeyPrefetch: () => {}, startKeychainPrefetch: () => { throw new Error('No keychain process') }, ensureKeychainPrefetchCompleted: async () => {} }))
await import('../../preload')
const oauth = await import('../../src/constants/oauth')
const originalOauthConfig = oauth.getOauthConfig()
mock.module(new URL('../../src/constants/oauth.ts', import.meta.url).pathname, () => ({ ...oauth, getOauthConfig: () => ({ ...originalOauthConfig, BASE_API_URL: input.origin, TOKEN_URL: input.origin + '/v1/oauth/token' }) }))
const bootstrap = await import('../../src/bootstrap/state')
bootstrap.setCwdState(home); bootstrap.setOriginalCwd(home); bootstrap.setProjectRoot(home); bootstrap.setAllowedSettingSources(['userSettings']); bootstrap.setIsInteractive(true)
const config = await import('../../src/utils/config'); config.enableConfigs()
const { normalizeApiKeyForConfig } = await import('../../src/utils/authPortable')
config.saveGlobalConfig(current => ({ ...current, ...input.global, customApiKeyResponses: { approved: [normalizeApiKeyForConfig(process.env.ANTHROPIC_API_KEY ?? '')], rejected: [] } }))
fs.mkdirSync(join(home, '.claude'), { recursive: true }); fs.writeFileSync(join(home, '.claude/settings.json'), JSON.stringify(input.settings))
const fast = await import('../../src/utils/fastMode')
const before = { available: fast.isFastModeAvailable(), initial: fast.getInitialFastModeSetting('opus') }
const signals: boolean[] = []; const unsubscribe = fast.onOrgFastModeChanged(enabled => signals.push(enabled))
await Promise.all([fast.prefetchFastModeStatus(), fast.prefetchFastModeStatus()])
unsubscribe()
if (capabilities.blocked.length) throw new Error('Source fast capability blocked ' + capabilities.blocked.join(', '))
process.stdout.write(JSON.stringify({ before, available: fast.isFastModeAvailable(), reason: fast.getFastModeUnavailableReason(), initial: fast.getInitialFastModeSetting('opus'), signals, global: config.getGlobalConfig(), settings: JSON.parse(fs.readFileSync(join(home, '.claude/settings.json'), 'utf8')), secure }) + '\n')
