import * as crypto from 'node:crypto'
import { readFileSync } from 'node:fs'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import type { RequestRetryInput } from './request-retry-fixtures'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as RequestRetryInput
const home = assertDisposableFixtureHome(input.home, 'Request retry')
if (process.env.CC_HAHA_REQUEST_RETRY_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Request retry requires explicit private production profile')
const inputNames = new Set(['name', 'mode', 'home', 'env', 'historySources', 'systemSources', 'model', 'maxOutputTokens', 'deviceID', 'sessionID', 'runtime', 'sdkBetas', 'nonInteractive', 'querySource', 'toolDefinitions', 'extraToolSchemaSources', 'toolChoiceSource', 'auth', 'probe', 'probePath'])
if (Object.keys(input).some(name => !inputNames.has(name))) throw new Error('Request retry rejects unknown original field/prepared shortcut')
if (input.nonInteractive !== undefined && typeof input.nonInteractive !== 'boolean' || input.env.CLAUDE_CODE_SIMPLE !== undefined && input.env.CLAUDE_CODE_SIMPLE !== '1' || input.nonInteractive === false && input.env.CLAUDE_CODE_SIMPLE !== '1') throw new Error('Request retry rejects unported bare/interactive auth profile')
if (input.auth && (typeof input.auth !== 'object' || Array.isArray(input.auth) || Object.keys(input.auth).some(name => !['flagSettingsPath', 'flagSettingsInline'].includes(name)) || input.auth.flagSettingsPath !== undefined && typeof input.auth.flagSettingsPath !== 'string' || input.auth.flagSettingsPath || input.auth.flagSettingsInline !== undefined && input.auth.flagSettingsInline !== null)) throw new Error('Request retry rejects original flag settings/helper lifecycle')
// Original processed image/document history is now admitted by the native
// opener. Source normalization and pairing still own its handling, including
// removing orphaned results; unknown media remains outside this finite caller.
for (const source of input.historySources) for (const block of JSON.parse(source).message?.content ?? []) {
  if (block?.type === 'tool_result' && Array.isArray(block.content) && block.content.some((part: { type?: string }) => !['text', 'tool_reference', 'image', 'document'].includes(part?.type ?? ''))) throw new Error('Request retry rejects unported tool result media')
}
const allowedEnvironment = new Set(['ANTHROPIC_BASE_URL', 'ANTHROPIC_API_KEY', 'API_TIMEOUT_MS', 'CLAUDE_CODE_MAX_RETRIES', 'CLAUDE_CODE_DISABLE_FAST_MODE', 'CLAUDE_CODE_EXTRA_METADATA', 'CLAUDE_CODE_SIMPLE'])
for (const [name, value] of Object.entries(input.env)) if (!allowedEnvironment.has(name) || typeof value !== 'string') throw new Error('Request retry rejects unported environment')
const base = new URL(input.env.ANTHROPIC_BASE_URL)
if (base.username || base.password || base.pathname !== '/' || base.search || base.hash || !base.port) throw new Error('Request retry requires exact HTTP loopback origin')
const origin = base.origin
if (new URL(origin).hostname !== '127.0.0.1' || !origin.startsWith('http:') || !input.env.ANTHROPIC_API_KEY || input.env.CLAUDE_CODE_DISABLE_FAST_MODE !== '1') throw new Error('Request retry requires direct original-env-key loopback')
const capabilities = await installClientFixtureCapabilities(home, { loopbackFetchOrigin: origin, absentPaths: absentManagedSettingsFixturePaths() })
const probe = (input as RequestRetryInput & { probe?: string; probePath?: string }).probe
if (probe === 'native') Bun.spawn(['security'])
if (probe === 'network') await fetch('http://127.0.0.1:2')
if (probe === 'outside-read') readFileSync((input as RequestRetryInput & { probePath: string }).probePath)
for (const [name, value] of Object.entries(input.env)) process.env[name] = value
const global = globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }
global.MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.switchSession(input.sessionID as Parameters<typeof bootstrap.switchSession>[0])
bootstrap.setIsInteractive(input.nonInteractive === false)
if (input.toolDefinitions) bootstrap.setInitialMainLoopModel(input.model)
bootstrap.setSdkBetas(input.sdkBetas ?? [])
bootstrap.setPromptCache1hEligible(false)
bootstrap.setPromptCache1hAllowlist([])
const config = await import('../../../src/utils/config')
config.enableConfigs()
config.saveGlobalConfig(previous => ({ ...previous, userID: input.deviceID }))
const clientModule = await import('../../../src/services/api/client')
const auth = await import('../../../src/utils/auth')
const getClient = clientModule.getAnthropicClient
const getKey = auth.getAnthropicApiKey
let clients = 0, keyLookups = 0
spyOn(clientModule, 'getAnthropicClient').mockImplementation(async options => { clients++; return getClient(options) })
spyOn(auth, 'getAnthropicApiKey').mockImplementation(() => { keyLookups++; return getKey() })
const messageModule = await import('../../../src/utils/messages')
const actualPairing = messageModule.ensureToolResultPairing
let pairingCalls = 0
let normalizedHistory: unknown[] = []
const pairingEvidence: unknown[] = []
spyOn(messageModule, 'ensureToolResultPairing').mockImplementation(history => {
  pairingCalls++
  const result = actualPairing(history)
  normalizedHistory = result.map(message => ({ type: message.type, uuid: message.uuid ?? null, messageSource: JSON.stringify(message.message) }))
  pairingEvidence.push({ input: history.map(message => JSON.stringify(message)), output: result.map(message => JSON.stringify(message)) })
  return result
})
const { queryModelWithStreaming } = await import('../../../src/services/api/claude')
const { asSystemPrompt } = await import('../../../src/utils/systemPromptType')
const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
const promptCalls: Record<string, number> = {}
const tools = input.toolDefinitions ? await (await import('./shared-opener-tools')).instantiateSharedOpenerTools(input.toolDefinitions, promptCalls) : []
if (Bun.version !== '1.3.14' || Bun.revision !== '0d9b296af33f2b851fcbf4df3e9ec89751734ba4') throw new Error('Request retry requires pinned Bun')
const observation = { clients: 0, keyLookups: 0, requests: [] as unknown[], notices: [] as string[], sleeps: [] as number[], yielded: [] as string[], yieldedAfter: [] as string[], historyBefore: input.historySources, historyAfter: [] as string[], normalizedHistory: [] as unknown[], preparationCalls: 0, uuidCallsOnRequest: [] as number[], uuidCalls: 0, outcome: '', pause: null as unknown }
spyOn(crypto, 'randomUUID').mockImplementation(() => `retry-id-${++observation.uuidCalls}` as ReturnType<typeof crypto.randomUUID>)
spyOn(Math, 'random').mockReturnValue(0)
const RealDate = Date
globalThis.Date = class extends RealDate {
  constructor(...args: unknown[]) { super(...(args.length ? args : [1700000000000]) as [number]) }
  static now() { return 1700000000000 }
} as DateConstructor
const controller = new AbortController()
if (input.mode === 'abort-before') controller.abort()
const originalTimeout = globalThis.setTimeout
globalThis.setTimeout = ((callback: (...args: unknown[]) => void, ms: number, ...args: unknown[]) => {
  if (args.length !== 3 || typeof args[2] !== 'function') return originalTimeout(callback, ms, ...args)
  observation.sleeps.push(ms)
  queueMicrotask(() => input.mode === 'abort-sleep' ? controller.abort() : callback(...args))
  return 0
}) as typeof setTimeout
const history = input.historySources.map(source => JSON.parse(source))
const yielded: unknown[] = []
const generator = queryModelWithStreaming({
  messages: history, systemPrompt: asSystemPrompt(input.systemSources.map(source => JSON.parse(source))),
  tools, thinkingConfig: { type: 'disabled' }, signal: controller.signal,
  options: {
    model: input.model, querySource: input.querySource ?? 'sdk', isNonInteractiveSession: input.nonInteractive ?? true, maxOutputTokensOverride: input.maxOutputTokens,
    extraToolSchemas: input.extraToolSchemaSources?.map(source => JSON.parse(source)),
    toolChoice: input.toolChoiceSource ? JSON.parse(input.toolChoiceSource) : undefined,
    agents: [], mcpTools: [], hasAppendSystemPrompt: false, getToolPermissionContext: async () => getEmptyToolPermissionContext(),
    fetchOverride: async (url, init) => {
      const parsed = new URL(String(url))
      if (parsed.origin !== origin || init?.method !== 'POST') throw new Error('Request retry escaped private POST origin')
      observation.uuidCallsOnRequest.push(observation.uuidCalls)
      observation.requests.push({ path: parsed.pathname + parsed.search, method: init.method, headers: [...new Headers(init.headers)], body: String(init.body) })
      const response = fetch(url, init)
      if (input.mode === 'abort-headers') {
        await fetch(origin + '/__fixture/request-seen')
        controller.abort()
      }
      return response
    },
  },
})
try {
  for (;;) {
    const next = await generator.next()
    if (next.done) { observation.outcome = controller.signal.aborted ? 'caller_aborted' : 'success'; break }
    yielded.push(next.value)
    observation.yielded.push(JSON.stringify(next.value))
    if (next.value.type === 'system' && next.value.subtype === 'api_error') {
      observation.notices.push(JSON.stringify(next.value))
      if (input.mode === 'pause' && observation.notices.length === 1) {
        await new Promise<void>(resolve => originalTimeout(resolve, 15))
        const server = await (await fetch(origin + '/__fixture/state')).json()
        observation.pause = { sleeps: [...observation.sleeps], requests: observation.requests.length, serverRequests: server.requests }
      }
      if (input.mode === 'stop-notice') { await generator.return(undefined); observation.outcome = 'consumer_stopped'; break }
    } else if (input.mode === 'stop-stream') {
      await generator.return(undefined)
      observation.outcome = 'consumer_stopped'
      break
    }
  }
} finally {
  globalThis.setTimeout = originalTimeout
  controller.abort()
}
observation.preparationCalls = pairingCalls
observation.normalizedHistory = normalizedHistory
observation.clients = clients
observation.keyLookups = keyLookups
observation.yieldedAfter = yielded.map(value => JSON.stringify(value))
observation.historyAfter = history.map(value => JSON.stringify(value))
process.stdout.write(JSON.stringify({ observation: { ...observation, ...(input.toolDefinitions ? { promptCalls } : {}) }, sourceOnly: { capabilities, pairingEvidence, sdk: '0.128.0', runtime: { platform: process.platform, arch: process.arch, version: process.version } } }))
