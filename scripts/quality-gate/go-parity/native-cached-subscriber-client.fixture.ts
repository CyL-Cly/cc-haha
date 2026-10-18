import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { mock } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await new Response(Bun.stdin.stream()).json()
const home = assertDisposableFixtureHome(input.home, 'Actual cached subscriber client')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_CACHED_CLIENT_FIXTURE !== '1') throw new Error('Pinned owned client required')
const absentPaths = absentManagedSettingsFixturePaths()
for (const path of ['/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token']) {
  if (!fs.existsSync(path)) absentPaths.push(path)
}
if (input.clearBetween || input.retryStatus) for (let parent = dirname(home); ; parent = dirname(parent)) {
  const candidate = join(parent, '.git')
  if (!fs.existsSync(candidate)) absentPaths.push(candidate)
  if (dirname(parent) === parent) break
}
const caps = await installClientFixtureCapabilities(home, { absentPaths, loopbackFetchOrigin: input.peer })
;(globalThis as any).MACRO = { VERSION: '999.0.0-cached-client-fixture', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const secure = await import('../../../src/utils/secureStorage/index')
mock.module('../../../src/utils/secureStorage/index', () => ({ ...secure, getSecureStorage: () => ({ read: () => {
  caps.secureReads.push('owned-credentials')
  try { return JSON.parse(fs.readFileSync(join(home, '.claude', '.credentials.json'), 'utf8')) } catch { return null }
} }) }))
const portable = await import('../../../src/utils/execFileNoThrow')
mock.module('../../../src/utils/execFileNoThrow', () => ({ ...portable, execSyncWithDefaults_DEPRECATED(command: string) {
  if (!command.startsWith('security find-generic-password ')) throw new Error('Unexpected Source OS primitive')
  caps.secureReads.push('owned-empty-legacy'); return null
} }))
const state = await import('../../../src/bootstrap/state')
state.setCwdState(home); state.setOriginalCwd(home); state.setProjectRoot(home); state.setAllowedSettingSources(['userSettings']); state.setIsInteractive(true)
const config = await import('../../../src/utils/config')
config.enableConfigs()
const auth = await import('../../../src/utils/auth')
const { getAnthropicClient } = await import('../../../src/services/api/client')
const initialSession = state.getSessionId()
const clearCurrent = async () => {
  const clear = (await import('../../../src/commands/clear/index')).default
  const original = await clear.load()
  const { createFileStateCacheWithSizeLimit } = await import('../../../src/utils/fileStateCache')
  await original.call('', { setMessages: () => {}, readFileState: createFileStateCacheWithSizeLimit(100) })
}
const requests: any[] = []
const makeClient = () => getAnthropicClient({ maxRetries: 0, model: 'claude-sonnet-4-5', source: 'repl_main_thread', fetchOverride: async (url, init) => {
  requests.push({ url: String(url), method: init?.method, headers: Object.fromEntries(new Headers(init?.headers)), body: typeof init?.body === 'string' ? JSON.parse(init.body) : null })
  const response = await fetch(url, init)
  if (input.retryStatus && requests.length === 1) await clearCurrent()
  return response
} })
let response: unknown
if (input.retryStatus) {
  const { withRetry } = await import('../../../src/services/api/withRetry')
  const { getAPIMetadata } = await import('../../../src/services/api/claude')
  const generator = withRetry(makeClient, client => client.messages.create({ model: 'claude-sonnet-4-5', max_tokens: 16, messages: [{ role: 'user', content: 'OWNED_CACHED_SUBSCRIBER_CLIENT' }], metadata: getAPIMetadata() }), { model: 'claude-sonnet-4-5', thinkingConfig: { type: 'disabled' }, maxRetries: 1 })
  for (;;) {
    const next = await generator.next()
    if (next.done) { response = next.value; break }
  }
} else {
  const client = await makeClient()
  response = await client.messages.create({ model: 'claude-sonnet-4-5', max_tokens: 16, messages: [{ role: 'user', content: 'OWNED_CACHED_SUBSCRIBER_CLIENT' }] })
}
if (input.clearBetween) {
  await clearCurrent()
  const nextClient = await makeClient()
  await nextClient.messages.create({ model: 'claude-sonnet-4-5', max_tokens: 16, messages: [{ role: 'user', content: 'OWNED_CACHED_SUBSCRIBER_AFTER_CLEAR' }] })
}
if (caps.blocked.length) throw new Error('Source client escaped private boundary: ' + JSON.stringify(caps.blocked))
await new Promise<void>(resolve => process.stdout.write(JSON.stringify({ subscriber: auth.isClaudeAISubscriber(), requests, response, initialSession, finalSession: state.getSessionId(), parentSession: state.getParentSessionId(), caps: { blocked: caps.blocked, secureReads: caps.secureReads } }), () => resolve()))
process.exit(0)
