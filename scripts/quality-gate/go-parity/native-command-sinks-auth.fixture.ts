import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { mock, spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Whole original command sinks auth')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_COMMAND_SINKS_AUTH_SOURCE !== '1') throw new Error('Pinned private sinks auth fixture required')
if (typeof input.origin !== 'string' || !/^http:\/\/127\.0\.0\.1:[1-9][0-9]*$/.test(input.origin)) throw new Error('Explicit owned HTTP origin required')
const kinds = ['ordinary-env', 'ordinary-helper-cold', 'ordinary-subscriber', 'ordinary-subscriber-no-profile', 'ordinary-subscriber-expired', 'ordinary-pro-default', 'ordinary-pro-custom']
if (!kinds.includes(input.kind)) throw new Error('Unknown concrete auth profile')
const cwd = join(home, 'workspace'), config = join(home, '.claude')
for (const path of [cwd, config, join(cwd, '.claude')]) fs.mkdirSync(path, { recursive: true })
const key = 'fake-owned-command-1p-env-key'
const settings = { env: { ANTHROPIC_BASE_URL: input.origin + '/safe' }, futurePrivate: { keep: true }, ...(input.kind === 'ordinary-pro-custom' ? { model: 'haiku' } : {}) }
fs.writeFileSync(join(config, 'settings.json'), JSON.stringify(settings))
const helper = join(home, 'owned-helper.sh')
fs.writeFileSync(helper, `#!/bin/sh\nprintf '%s' fake-owned-helper-key\nprintf '%s' actual > '${join(home, 'helper-executed')}'\n`, { mode: 0o700 })
if (input.kind === 'ordinary-helper-cold') fs.writeFileSync(join(cwd, '.claude', 'settings.json'), JSON.stringify({ apiKeyHelper: helper }))
const subscriber = input.kind.startsWith('ordinary-subscriber') || input.kind.startsWith('ordinary-pro-')
const global: Record<string, unknown> = { userID: 'owned-command-1p-device', futureGlobal: { keep: true } }
if (input.kind === 'ordinary-env') global.customApiKeyResponses = { approved: [key.slice(-20)], rejected: [] }
if (subscriber) {
  global.oauthAccount = { accountUuid: 'owned-account', organizationUuid: 'owned-organization', emailAddress: 'owned@fixture.invalid', billingType: 'stripe_subscription', accountCreatedAt: '2020-01-01T00:00:00.000Z', subscriptionCreatedAt: '2020-01-01T00:00:00.000Z', hasExtraUsageEnabled: false }
  fs.writeFileSync(join(config, '.credentials.json'), JSON.stringify({ claudeAiOauth: { accessToken: 'fake-owned-command-cached-token', scopes: input.kind === 'ordinary-subscriber-no-profile' ? ['user:inference'] : ['user:inference', 'user:profile'], expiresAt: input.kind === 'ordinary-subscriber-expired' ? 1 : Date.now() + 365 * 86400000, subscriptionType: input.kind.startsWith('ordinary-pro-') ? 'pro' : 'max', rateLimitTier: input.kind.startsWith('ordinary-pro-') ? 'default_claude_pro' : 'default_claude_max_5x' } }))
}
fs.writeFileSync(join(config, '.claude.json'), JSON.stringify(global))
if (input.kind === 'ordinary-env') process.env.ANTHROPIC_API_KEY = key
else delete process.env.ANTHROPIC_API_KEY
const absent = [...absentManagedSettingsFixturePaths(), '/Library/Application Support/ClaudeCode/managed-mcp.json', '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token']
for (const path of ['/sys/hypervisor/uuid', '/.dockerenv']) if (!fs.existsSync(path)) absent.push(path)
for (const spelling of new Set([home, fs.realpathSync(home)])) for (let path = spelling; ; path = dirname(path)) {
  for (const suffix of ['.git', '.claude/skills', '.claude/commands', '.agents/skills', '.agents/commands']) if (!fs.existsSync(join(path, suffix))) absent.push(join(path, suffix))
  if (dirname(path) === path) break
}
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent, loopbackFetchOrigin: input.origin })
const deniedRequests: unknown[] = []
const https = require('node:https')
const deniedHTTPSRequest = https.request
https.request = (...args: any[]) => {
  const request = args[0]
  deniedRequests.push({ target: request instanceof URL ? request.href : typeof request === 'string' ? request : { protocol: request?.protocol, hostname: request?.hostname, path: request?.path }, stack: new Error('actual source HTTPS admission').stack })
  return deniedHTTPSRequest(...args)
}
mock.module('../../../src/utils/settings/mdm/rawRead', () => ({ startMdmRawRead: () => {}, getMdmRawReadPromise: () => Promise.resolve({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }), fireRawRead: async () => ({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }) }))
mock.module('../../../src/utils/secureStorage/keychainPrefetch', () => ({ getLegacyApiKeyPrefetchResult: () => ({ stdout: '' }), getLegacyApiKeyPrefetchPromise: () => undefined, startKeychainPrefetch: () => {}, clearLegacyApiKeyPrefetch: () => {}, ensureKeychainPrefetchCompleted: async () => {} }))
// Only the external secure-store primitive reads this private fake credential
// file. Token/auth/scope/expiry selection remains the original implementation.
mock.module('../../../src/utils/secureStorage/index', () => ({ getSecureStorage: () => ({
  read: () => { caps.secureReads.push('private-fake-credential-store'); try { return JSON.parse(fs.readFileSync(join(config, '.credentials.json'), 'utf8')) } catch { return null } },
  readAsync: async () => { caps.secureReads.push('private-fake-credential-store-async'); try { return JSON.parse(fs.readFileSync(join(config, '.credentials.json'), 'utf8')) } catch { return null } },
}) }))
;(globalThis as any).MACRO = { VERSION: '999.0.0-command-sinks-auth', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
process.argv = [process.execPath, fileURLToPath(new URL('../../../src/entrypoints/cli.tsx', import.meta.url)), 'plugin', 'list', '--json']
const state = await import('../../../src/bootstrap/state')
state.setCwdState(cwd); state.setOriginalCwd(cwd); state.setProjectRoot(cwd)
const configModule = await import('../../../src/utils/config')
const auth = await import('../../../src/utils/auth')
const metadata = await import('../../../src/services/analytics/metadata')
const logger = await import('../../../src/services/analytics/firstPartyEventLogger')
const exporter = await import('../../../src/services/analytics/firstPartyEventLoggingExporter')
const { main } = await import('../../../src/main')
const axios = (await import('axios')).default
const originalPost = axios.post.bind(axios)
const prepares: unknown[] = [], emitted: unknown[] = [], initializations: unknown[] = []
// The external HTTP primitive maps only the exact real 1P endpoint to a real
// loopback socket. No headers, payload, status or exporter logic is fabricated.
spyOn(axios, 'post').mockImplementation((target: any, body: any, options: any) => {
  if (target !== 'https://api.anthropic.com/api/event_logging/batch') throw new Error('Unowned Source telemetry endpoint: ' + target)
  prepares.push({ target, headers: options.headers, timeout: options.timeout, body })
  return originalPost(input.origin + '/events', body, { ...options, adapter: 'fetch' })
})
const metadataJobs: Promise<unknown>[] = []
const getMetadata = metadata.getEventMetadata
spyOn(metadata, 'getEventMetadata').mockImplementation((...args) => {
  const pending = getMetadata(...args)
  metadataJobs.push(pending)
  return pending // same callee Promise; no observer async-return adoption
})
const initialize = logger.initialize1PEventLogging
spyOn(logger, 'initialize1PEventLogging').mockImplementation(() => {
  const result = initialize()
  initializations.push({ phase: 'actual-init-1p', nonInteractive: state.getIsNonInteractiveSession(), trust: configModule.checkHasTrustDialogAccepted() })
  return result
})
const originalExport = exporter.FirstPartyEventLoggingExporter.prototype.export
spyOn(exporter.FirstPartyEventLoggingExporter.prototype, 'export').mockImplementation(function (records, callback) {
  emitted.push(records.map(record => ({ body: record.body, attributes: record.attributes })))
  return originalExport.call(this, records, callback)
})
const exits: number[] = []
// This fixture replaces only the terminal OS exit primitive, then exercises
// the original graceful 1P shutdown consumer. It is not normal-exit timing or
// cold CLI/HEAD authority; the original main/handler/sinks all execute.
const terminalExit = new Error('owned external terminal exit')
process.exit = ((code?: number) => { exits.push(code ?? 0); throw terminalExit }) as never
try { await main() } catch (failure) { if (failure !== terminalExit) throw failure }
await Promise.all(metadataJobs)
await logger.shutdown1PEventLogging()
const receipt = { kind: input.kind, initializations, prepares, emitted, exits, deniedRequests, nonInteractive: state.getIsNonInteractiveSession(), trust: configModule.checkHasTrustDialogAccepted(), subscriber: auth.isClaudeAISubscriber(), helperCached: auth.getApiKeyFromApiKeyHelperCached(), helperExecuted: fs.existsSync(join(home, 'helper-executed')), userSettingsUnchanged: fs.readFileSync(join(config, 'settings.json'), 'utf8') === JSON.stringify(settings), globalAfter: JSON.parse(fs.readFileSync(join(config, '.claude.json'), 'utf8')), caps, scope: 'whole original main preAction init -> actual initSinks -> real plugin handler/public analytics -> real metadata/logger/exporter -> original 1P shutdown; external exit/HTTP/secure primitives isolated; not ordinary-exit or cold-time authority' }
fs.writeFileSync(join(home, 'command-sinks-auth-source.json'), JSON.stringify(receipt))
