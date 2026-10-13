import * as crypto from 'node:crypto'
import { join } from 'node:path'
import { lstatSync } from 'node:fs'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import { streamAbortCases, type StreamAbortInput } from './stream-abort-fixtures'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as StreamAbortInput
const home = assertDisposableFixtureHome(input.home, 'Stream abort')
const keys = new Set(['name', 'mode', 'layer', 'home', 'env', 'historySources', 'systemSources', 'model', 'maxOutputTokens', 'deviceID', 'sessionID', 'runtime', 'abortType', 'abortIndex'])
if (Object.keys(input).some(key => !keys.has(key)) || process.env.CC_HAHA_STREAM_ABORT_FIXTURE !== '1' || process.env.NODE_ENV !== 'production' || Bun.version !== '1.3.14' || Bun.revision !== '0d9b296af33f2b851fcbf4df3e9ec89751734ba4' || input.mode !== 'caller-abort' || input.layer !== 'query') throw new Error('Stream abort requires original private profile before imports')
const scenario = streamAbortCases.find(item => item.name === input.name)
if (!scenario || input.abortType !== scenario.abortType || input.abortIndex !== scenario.abortIndex || input.runtime.platform !== process.platform || input.runtime.arch !== process.arch || input.runtime.version !== process.version) throw new Error('Stream abort requires original control/runtime')
const envNames = new Set(['ANTHROPIC_BASE_URL', 'ANTHROPIC_API_KEY', 'CLAUDE_CODE_SIMPLE', 'CLAUDE_CODE_MAX_RETRIES', 'CLAUDE_CODE_DISABLE_FAST_MODE', 'NODE_EXTRA_CA_CERTS'])
if (Object.entries(input.env).some(([key, value]) => !envNames.has(key) || typeof value !== 'string') || input.env.CLAUDE_CODE_SIMPLE !== '1' || input.env.CLAUDE_CODE_DISABLE_FAST_MODE !== '1' || input.env.CLAUDE_CODE_MAX_RETRIES !== '0' || !input.env.ANTHROPIC_API_KEY) throw new Error('Stream abort requires original direct-key environment')
const base = new URL(input.env.ANTHROPIC_BASE_URL)
if (base.protocol !== 'https:' || base.hostname !== '127.0.0.1' || !base.port || base.username || base.password || base.pathname !== '/' || base.search || base.hash || input.env.NODE_EXTRA_CA_CERTS !== join(home, 'original-ca.pem')) throw new Error('Stream abort requires private HTTPS/CA before imports')
const ca = lstatSync(input.env.NODE_EXTRA_CA_CERTS)
if (!ca.isFile() || ca.isSymbolicLink()) throw new Error('Stream abort requires private CA file')
const capabilities = await installClientFixtureCapabilities(home, { loopbackFetchOrigin: base.origin, absentPaths: absentManagedSettingsFixturePaths() })
for (const [name, value] of Object.entries(input.env)) process.env[name] = value
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.switchSession(input.sessionID as Parameters<typeof bootstrap.switchSession>[0])
bootstrap.setIsInteractive(false)
bootstrap.setSdkBetas([])
bootstrap.setPromptCache1hEligible(false)
bootstrap.setPromptCache1hAllowlist([])
const config = await import('../../../src/utils/config')
config.enableConfigs()
config.saveGlobalConfig(previous => ({ ...previous, userID: input.deviceID }))
const messages = await import('../../../src/utils/messages')
const originalPair = messages.ensureToolResultPairing
const { queryModelWithStreaming } = await import('../../../src/services/api/claude')
const { asSystemPrompt } = await import('../../../src/utils/systemPromptType')
const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
const observation = { yielded: [] as string[], yieldedAfter: [] as string[], requests: [] as unknown[], uuidCalls: 0, dateCalls: 0, preparationCalls: 0, historyBefore: input.historySources, historyAfter: [] as string[], returned: false, aborted: false }
spyOn(messages, 'ensureToolResultPairing').mockImplementation(history => { observation.preparationCalls++; return originalPair(history) })
spyOn(crypto, 'randomUUID').mockImplementation(() => `abort-id-${++observation.uuidCalls}` as ReturnType<typeof crypto.randomUUID>)
const RealDate = Date
globalThis.Date = class extends RealDate {
  constructor(...args: unknown[]) { super(...(args.length ? args : [1700000000000]) as [number]); if (!args.length) observation.dateCalls++ }
  static now() { return 1700000000000 }
} as DateConstructor
const controller = new AbortController()
const history = input.historySources.map(source => JSON.parse(source))
const values: unknown[] = []
const generator = queryModelWithStreaming({ messages: history, systemPrompt: asSystemPrompt(input.systemSources.map(source => JSON.parse(source))), tools: [], thinkingConfig: { type: 'disabled' }, signal: controller.signal, options: {
  model: input.model, querySource: 'sdk', isNonInteractiveSession: true, maxOutputTokensOverride: input.maxOutputTokens, agents: [], mcpTools: [], hasAppendSystemPrompt: false, getToolPermissionContext: async () => getEmptyToolPermissionContext(),
  fetchOverride: async (url, init) => { const parsed = new URL(String(url)); if (parsed.origin !== base.origin || init?.method !== 'POST') throw new Error('Stream abort escaped private POST'); observation.requests.push({ path: parsed.pathname + parsed.search, method: init.method, headers: [...new Headers(init.headers)], body: String(init.body) }); return fetch(url, init) },
} })
for (;;) {
  const next = await generator.next()
  if (next.done) { observation.returned = true; break }
  values.push(next.value)
  observation.yielded.push(JSON.stringify(next.value))
  if (next.value.type === 'stream_event' && next.value.event.type === input.abortType && 'index' in next.value.event && next.value.event.index === input.abortIndex) controller.abort('user')
}
observation.aborted = controller.signal.aborted
observation.yieldedAfter = values.map(value => JSON.stringify(value))
observation.historyAfter = history.map(value => JSON.stringify(value))
const { VERSION: sdkVersion } = await import('@anthropic-ai/sdk/version')
process.stdout.write(JSON.stringify({ observation, sourceOnly: { capabilities, sdk: sdkVersion, boundary: 'actual single-model generator; no outer-query/hooks/SDK writer claim' } }))
