import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { mock, spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual ordinary command sinks')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_COMMAND_SINKS_SOURCE !== '1') throw new Error('Pinned ordinary command sinks fixture required')
if (typeof input.origin !== 'string' || !/^http:\/\/127\.0\.0\.1:[1-9][0-9]*$/.test(input.origin)) throw new Error('Explicit private HEAD origin required')
const config = join(home, '.claude')
fs.mkdirSync(config, { recursive: true })
const settings = JSON.stringify({ env: { ANTHROPIC_BASE_URL: input.origin + '/safe' }, futurePrivate: { keep: true } })
fs.writeFileSync(join(config, 'settings.json'), settings)
const absent = [...absentManagedSettingsFixturePaths(), '/Library/Application Support/ClaudeCode/managed-mcp.json', '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token']
for (const spelling of new Set([home, fs.realpathSync(home)])) {
  for (let path = spelling; ; path = dirname(path)) {
    for (const suffix of ['.git', '.claude/skills', '.claude/commands', '.agents/skills', '.agents/commands']) {
      const candidate = join(path, suffix)
      if (!fs.existsSync(candidate)) absent.push(candidate)
    }
    if (dirname(path) === path) break
  }
}
const caps = await installClientFixtureCapabilities(home, { absentPaths: absent, loopbackFetchOrigin: input.origin })
// Only absent external OS stores are substituted. No sinks, logger, handler,
// import, Promise or queueMicrotask implementation is replaced.
mock.module('../../../src/utils/settings/mdm/rawRead', () => ({ startMdmRawRead: () => {}, getMdmRawReadPromise: () => Promise.resolve({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }), fireRawRead: async () => ({ plistStdouts: [], hklmStdout: null, hkcuStdout: null }) }))
mock.module('../../../src/utils/secureStorage/keychainPrefetch', () => ({ getLegacyApiKeyPrefetchResult: () => ({ stdout: '' }), getLegacyApiKeyPrefetchPromise: () => undefined, startKeychainPrefetch: () => {}, clearLegacyApiKeyPrefetch: () => {}, ensureKeychainPrefetchCompleted: async () => {} }))
;(globalThis as any).MACRO = { VERSION: '999.0.0-command-sinks', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
process.argv = [process.execPath, fileURLToPath(new URL('../../../src/entrypoints/cli.tsx', import.meta.url)), 'plugin', 'marketplace', 'list', '--json']
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home)
state.setOriginalCwd(home)
state.setProjectRoot(home)
const { main } = await import('../../../src/main')
const errors = await import('../../../src/utils/log')
const analytics = await import('../../../src/services/analytics/index')
const firstParty = await import('../../../src/services/analytics/firstPartyEventLogger')
const events: Array<Record<string, unknown>> = []
const beforeError = new Error('private queued command error')
const syncMetadata = { value: 1 }
const asyncMetadata = { value: 2 }
const attachErrors = errors.attachErrorLogSink
const attachAnalytics = analytics.attachAnalyticsSink
const originalFirstParty = firstParty.logEventTo1P
spyOn(errors, 'attachErrorLogSink').mockImplementation(sink => {
  events.push({ phase: 'attach:error:begin' })
  const original = sink.logError
  sink.logError = error => {
    events.push({ phase: 'error:delivered', sameError: error === beforeError, message: error.message })
    return original(error)
  }
  const returned = attachErrors(sink)
  events.push({ phase: 'attach:error:end' })
  return returned
})
spyOn(analytics, 'attachAnalyticsSink').mockImplementation(sink => {
  events.push({ phase: 'attach:analytics:begin' })
  const sync = sink.logEvent, asynchronous = sink.logEventAsync
  sink.logEvent = (name, metadata) => {
    if (name.startsWith('private_command_')) events.push({ phase: 'analytics:sync', name, sameMetadata: metadata === syncMetadata, value: metadata.value })
    return sync(name, metadata)
  }
  sink.logEventAsync = (name, metadata) => {
    const pending = asynchronous(name, metadata)
    if (name.startsWith('private_command_')) events.push({ phase: 'analytics:async', name, sameMetadata: metadata === asyncMetadata, value: metadata.value, originalPromise: Promise.resolve(pending) === pending })
    return pending // retain the callee's actual Promise; no async observer wrapper
  }
  const returned = attachAnalytics(sink)
  events.push({ phase: 'attach:analytics:end' })
  return returned
})
spyOn(firstParty, 'logEventTo1P').mockImplementation((name, metadata) => {
  if (name.startsWith('private_command_')) events.push({ phase: 'analytics:actual-1p-route', name, value: metadata?.value })
  return originalFirstParty(name, metadata)
})
errors.logError(beforeError)
analytics.logEvent('private_command_sync', syncMetadata)
const queuedAsync = analytics.logEventAsync('private_command_async', asyncMetadata)
syncMetadata.value = 41
asyncMetadata.value = 42
events.push({ phase: 'before:actual-main', errorMemory: errors.getInMemoryErrors().some(row => row.error.includes(beforeError.message)), queuedPublicPromise: Promise.resolve(queuedAsync) === queuedAsync })
process.on('exit', code => {
  fs.writeFileSync(join(home, 'ordinary-command-sinks-source-receipt.json'), JSON.stringify({ code, events, caps, protectedUnchanged: fs.readFileSync(join(config, 'settings.json'), 'utf8') === settings,
    scope: 'Whole original main/Commander preAction calls actual initSinks and actual handlers. Synchronous observers preserve callee values/Promises; business queue proof only, not cold HEAD timing authority.' }))
})
await main()
