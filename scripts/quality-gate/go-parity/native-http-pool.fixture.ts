import fs from 'node:fs'
import { join } from 'node:path'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual native HTTP pool')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_HTTP_POOL_FIXTURE !== '1') throw new Error('Pinned owned pool fixture required')
const absentPaths = absentManagedSettingsFixturePaths()
for (const path of ['/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token']) {
  if (!fs.existsSync(path)) absentPaths.push(path)
}
const caps = await installClientFixtureCapabilities(home, { absentPaths, loopbackFetchOrigin: input.peer })
;(globalThis as any).MACRO = { VERSION: '999.0.0-http-pool-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const secure = await import('../../../src/utils/secureStorage/index')
mock.module('../../../src/utils/secureStorage/index', () => ({ ...secure, getSecureStorage: () => ({ read: () => {
  caps.secureReads.push('owned-empty-credentials')
  try { return JSON.parse(fs.readFileSync(join(home, '.claude', '.credentials.json'), 'utf8')) } catch { return null }
} }) }))
const portable = await import('../../../src/utils/execFileNoThrow')
mock.module('../../../src/utils/execFileNoThrow', () => ({ ...portable, execSyncWithDefaults_DEPRECATED(command: string) {
  if (!command.startsWith('security find-generic-password ')) throw new Error('Unexpected Source OS primitive')
  caps.secureReads.push('owned-empty-legacy'); return null
} }))
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home); state.setOriginalCwd(home); state.setProjectRoot(home); state.setAllowedSettingSources(['userSettings'])
const config = await import('../../../src/utils/config')
config.enableConfigs()
process.env.ANTHROPIC_BASE_URL = input.peer
process.env.ANTHROPIC_API_KEY = 'owned-http-pool-key'
const actualFetch = globalThis.fetch
let headDone!: () => void
const observedHead = new Promise<void>(resolve => { headDone = resolve })
// Observe the actual HEAD promise without changing its identity or adding an
// adoption to the business caller. This fixture's explicit headers-complete
// phase isolates idle reuse; normal public CLI cold timing is tested separately.
globalThis.fetch = ((url, init) => {
  const promise = actualFetch(url, init)
  if (init?.method === 'HEAD') void promise.then(headDone, headDone)
  return promise
}) as typeof fetch
const { preconnectAnthropicApi } = await import('../../../src/utils/apiPreconnect')
preconnectAnthropicApi()
await observedHead
const { getAnthropicClient } = await import('../../../src/services/api/client')
if (input.keepaliveDisabled) {
  const { disableKeepAlive } = await import('../../../src/utils/proxy')
  disableKeepAlive()
}
const makeClient = () => getAnthropicClient({ maxRetries: 0, model: 'claude-sonnet-4-5', source: 'repl_main_thread' })
const values = []
let retries = 0
if (input.staleFirst) {
  const { withRetry } = await import('../../../src/services/api/withRetry')
  const generator = withRetry(makeClient, client => client.messages.create({ model: 'claude-sonnet-4-5', max_tokens: 16, messages: [{ role: 'user', content: 'OWNED_POOL_FIRST' }] }), { model: 'claude-sonnet-4-5', thinkingConfig: { type: 'disabled' }, maxRetries: 1 })
  for (;;) {
    const next = await generator.next()
    if (next.done) { values.push(next.value); break }
    retries++
  }
  const client = await makeClient()
  values.push(await client.messages.create({ model: 'claude-sonnet-4-5', max_tokens: 16, messages: [{ role: 'user', content: 'OWNED_POOL_SECOND' }] }))
} else {
  const client = await makeClient()
  for (const content of ['OWNED_POOL_FIRST', 'OWNED_POOL_SECOND']) {
    values.push(await client.messages.create({ model: 'claude-sonnet-4-5', max_tokens: 16, messages: [{ role: 'user', content }] }))
  }
}
if (caps.blocked.length) throw new Error('Source pool escaped private boundary: ' + JSON.stringify(caps.blocked))
fs.writeSync(1, JSON.stringify({ responses: values.length, retries, blocked: caps.blocked, secureReads: caps.secureReads }))
process.exit(0)
