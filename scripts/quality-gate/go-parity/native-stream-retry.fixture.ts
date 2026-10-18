import * as crypto from 'node:crypto'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
assertDisposableFixtureHome(input.home, 'Native stream retry')
if (process.env.CC_HAHA_NATIVE_STREAM_RETRY_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Unowned stream retry fixture')
const origin = new URL(input.origin)
if (origin.protocol !== 'http:' || origin.hostname !== '127.0.0.1' || !origin.port || origin.username || origin.password || origin.pathname !== '/' || origin.search || origin.hash) throw new Error('Stream retry requires exact loopback origin')
const capabilities = await installClientFixtureCapabilities(input.home, { loopbackFetchOrigin: origin.origin, absentPaths: absentManagedSettingsFixturePaths() })
Object.assign(process.env, { ANTHROPIC_BASE_URL: origin.origin, ANTHROPIC_API_KEY: 'original-key', CLAUDE_CODE_MAX_RETRIES: input.maxRetries ?? '0', CLAUDE_CODE_DISABLE_FAST_MODE: '1', CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK: '1', CLAUDE_ENABLE_STREAM_WATCHDOG: '0', CLAUDE_STREAM_TRANSIENT_RETRY_MAX: input.retries })
if (input.fallback === true) process.env.CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK = '0'
for (const name of ['CLAUDE_ENABLE_STREAM_WATCHDOG', 'CLAUDE_STREAM_IDLE_TIMEOUT_MS', 'CLAUDE_STREAM_FIRST_TOKEN_TIMEOUT_MS', 'CLAUDE_STREAM_MAX_DURATION_MS', 'CLAUDE_STREAM_TOOL_INPUT_MAX_DURATION_MS']) {
  if (typeof input.watchdog?.[name] === 'string') process.env[name] = input.watchdog[name]
}
;(globalThis as any).MACRO = { BUILD_TIME: '' }
const state = await import('../../../src/bootstrap/state')
state.setCwdState(input.home)
state.setOriginalCwd(input.home)
state.setProjectRoot(input.home)
state.switchSession('00000000-0000-4000-8000-000000000000' as any)
state.setIsInteractive(false)
state.setSdkBetas([])
const config = await import('../../../src/utils/config')
config.enableConfigs()
config.saveGlobalConfig(previous => ({ ...previous, userID: 'fixture-device' }))
const { queryModelWithStreaming } = await import('../../../src/services/api/claude')
const { asSystemPrompt } = await import('../../../src/utils/systemPromptType')
const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
const RealDate = Date
globalThis.Date = class extends RealDate {
  constructor(...args: unknown[]) { super(...(args.length ? args : [1700000000000]) as [number]) }
  static now() { return 1700000000000 }
} as DateConstructor
spyOn(crypto, 'randomUUID').mockImplementation(() => 'private-go-id' as ReturnType<typeof crypto.randomUUID>)
spyOn(Math, 'random').mockReturnValue(0)
const sleeps: number[] = []
const sleepModule = await import('../../../src/utils/sleep')
spyOn(sleepModule, 'sleep').mockImplementation(async (ms: number) => { sleeps.push(ms) })
let posts = 0
const budget = typeof input.budget === 'number' ? { remaining: input.budget } : undefined
const physicalFetch = globalThis.fetch
spyOn(globalThis, 'fetch').mockImplementation(async (url: any, init: any) => {
  if (new URL(String(url)).origin === origin.origin && init?.method === 'POST') posts++
  return physicalFetch(url, init)
})
const yielded: unknown[] = []
const queryParams: Parameters<typeof queryModelWithStreaming>[0] = {
  messages: [{ type: 'user', uuid: 'original-user', message: { role: 'user', content: 'original' } }] as any,
  systemPrompt: asSystemPrompt([]), tools: [], thinkingConfig: { type: 'disabled' }, signal: new AbortController().signal,
  options: {
    model: 'fixture-model', querySource: input.compact ? 'compact' : 'sdk', isNonInteractiveSession: true, maxOutputTokensOverride: 32,
    ...(budget ? {apiAttemptBudget:budget} : {}),
    agents: [], mcpTools: [], hasAppendSystemPrompt: false, getToolPermissionContext: async () => getEmptyToolPermissionContext(),
    fetchOverride: async (url, init) => {
      if (new URL(String(url)).origin !== origin.origin || init?.method !== 'POST') throw new Error('Stream retry escaped the exact fixture POST')
      const pending = fetch(url, init)
      if (input.poolHeadersReceipt === true && input.poolCutAfterMessageStart !== true) {
        // The private fault producer closes only after actual fetch headers
        // are fulfilled. This observes that Promise without awaiting the
        // observer, replacing its identity, or adding a transport request.
        void pending.then(response => {
          if (response.headers.get('x-owned-stream-cutpoint') === 'headers') process.stderr.write('owned-stream-headers-ready\n')
        }, () => {})
      }
      return pending
    },
  },
}
for await (const message of queryModelWithStreaming(queryParams)) {
  // Frozen UUID/Date primitives make complete emitted fields comparable. Their
  // allocation and owner scheduling are covered by the separate demand tests.
  const record = JSON.parse(JSON.stringify(message))
  yielded.push(record)
  if (input.poolHeadersReceipt === true && input.poolCutAfterMessageStart === true && record.type === 'stream_event' && record.event.type === 'message_start') process.stderr.write('owned-stream-message-start-ready\n')
  if (input.stopRecovery && record.type === 'system' && record.subtype === 'streaming_fallback') break
  if (input.stopAssistant && record.type === 'assistant') break
}
if (input.poolFollowup) {
  const count = input.poolFollowup === true ? 1 : input.poolFollowup
  if (!Number.isInteger(count) || count < 1 || count > 2) throw new Error('Owned pool followup must be one or two actual queries')
  for (let index = 0; index < count; index++) {
    for await (const _message of queryModelWithStreaming(queryParams)) { /* actual following query consumes the same process fetch policy */ }
  }
}
if (capabilities.blocked.length) throw new Error('Stream retry left the isolated Source capabilities')
const { getProxyFetchOptions } = await import('../../../src/utils/proxy')
process.stdout.write(JSON.stringify({ posts, yielded, sleeps, keepaliveDisabled: getProxyFetchOptions().keepalive === false, totals: { input: state.getTotalInputTokens(), output: state.getTotalOutputTokens() }, capabilities }))
