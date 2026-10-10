import { spyOn } from 'bun:test'
import * as crypto from 'node:crypto'
import type { ToolUseContext } from '../../../src/Tool'
import type { QueryRequestInput, QueryRequestObservation } from './query-request-fixtures'
import { requestContextEnvironmentNames } from './request-context-fixtures'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as QueryRequestInput
assertDisposableFixtureHome(input.home, 'Query request')
const allowed = new Set(['name', 'nonInteractive', 'historySources', 'systemSources', 'maxOutputTokens', 'env', 'wire', 'home', 'origin', 'runtime', 'deviceID', 'sessionID'])
if (Object.keys(input).some(key => !allowed.has(key)) || process.env.CC_HAHA_QUERY_REQUEST_FIXTURE !== '1' || process.env.NODE_ENV !== 'production' || typeof input.origin !== 'string' || !input.origin || input.origin !== process.env.ANTHROPIC_BASE_URL || typeof input.nonInteractive !== 'boolean' || !Array.isArray(input.historySources) || !Array.isArray(input.systemSources) || typeof input.wire !== 'string' || input.maxOutputTokens !== 32000 || input.deviceID !== 'fixture-query-device' || input.sessionID !== '00000000-0000-4000-8000-000000000000') throw new Error('Query request requires explicit tool-free offline profile and original inputs')
for (const [name, value] of Object.entries(input.env)) if (!requestContextEnvironmentNames.has(name) || typeof value !== 'string') throw new Error('Query request forbids ambient configuration and provider overrides')
if (input.runtime.platform !== process.platform || input.runtime.arch !== process.arch || input.runtime.version !== process.version) throw new Error('Query request requires the pinned Source runtime identity')
const capabilities = await installClientFixtureCapabilities(input.home, { absentPaths: absentManagedSettingsFixturePaths(), loopbackFetchOrigin: input.origin })
for (const [name, value] of Object.entries(input.env)) process.env[name] = value
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(input.home)
bootstrap.setOriginalCwd(input.home)
bootstrap.setProjectRoot(input.home)
bootstrap.switchSession(input.sessionID as Parameters<typeof bootstrap.switchSession>[0])
bootstrap.setIsInteractive(!input.nonInteractive)
bootstrap.setSdkBetas([])
bootstrap.setPromptCache1hEligible(false)
bootstrap.setPromptCache1hAllowlist([])
const { enableConfigs } = await import('../../../src/utils/config')
enableConfigs()
const { query } = await import('../../../src/query')
const { productionDeps } = await import('../../../src/query/deps')
const { buildQueryConfig } = await import('../../../src/query/config')
const { getDefaultAppState } = await import('../../../src/state/AppStateStore')
const { asSystemPrompt } = await import('../../../src/utils/systemPromptType')
if (buildQueryConfig().gates.streamingToolExecution) throw new Error('Query request oracle requires the observed nonstreaming-tool snapshot')
const observation: QueryRequestObservation = { modelHistory: [], yielded: [], yieldedAfter: [], history: [], allocations: [], requests: [], terminal: null, error: null }
let uuidCount = 0
spyOn(crypto, 'randomUUID').mockImplementation(() => {
  const value = `query-request-id-${++uuidCount}`
  observation.allocations.push({ kind: 'uuid', value })
  return value as ReturnType<typeof crypto.randomUUID>
})
const OriginalDate = Date
globalThis.Date = class extends OriginalDate {
  constructor(...args: unknown[]) {
    super(...(args.length ? args : [1_700_000_000_000]) as [number])
    if (!args.length) observation.allocations.push({ kind: 'time', value: this.toISOString() })
  }
  static now() { return 1_700_000_000_000 }
} as DateConstructor
let state = getDefaultAppState()
const context = {
  options: { commands: [], debug: false, mainLoopModel: 'fixture-model', tools: [], verbose: false, thinkingConfig: { type: 'disabled' }, mcpClients: [], mcpResources: {}, isNonInteractiveSession: input.nonInteractive, agentDefinitions: { activeAgents: [], allAgents: [] } },
  abortController: new AbortController(), readFileState: new Map(),
  getAppState: () => state, setAppState: (update: (value: typeof state) => typeof state) => { state = update(state) },
  setInProgressToolUseIDs: () => { throw new Error('Tool-free profile attempted tools') },
  setResponseLength: () => {}, updateFileHistoryState: () => {}, updateAttributionState: () => {}, messages: [],
} as unknown as ToolUseContext
const deps = productionDeps()
const originalModel = deps.callModel
let resolvedHistory: unknown[] = []
const assistants: unknown[] = []
deps.callModel = async function* (request) {
  resolvedHistory = request.messages
  observation.modelHistory.push(request.messages.map(message => JSON.stringify(message)))
  // Observe the final client fetch boundary, then use the real loopback socket.
  // All normalization, body construction, signing and SDK headers stay real.
  for await (const message of originalModel({ ...request, options: { ...request.options, fetchOverride: async (url, init) => {
    const parsed = new URL(String(url))
    if (parsed.origin !== input.origin || init?.method !== 'POST') throw new Error('Query request escaped exact loopback POST')
    observation.requests.push({ path: parsed.pathname + parsed.search, method: init.method, headers: [...new Headers(init.headers)], body: String(init.body) })
    return fetch(url, init)
  } } })) {
    if (message.type === 'assistant') assistants.push(message)
    yield message
  }
}
const yielded: unknown[] = []
try {
  const generator = query({ messages: input.historySources.map(source => JSON.parse(source)), systemPrompt: asSystemPrompt(input.systemSources.map(source => JSON.parse(source))), userContext: {}, systemContext: {},
    canUseTool: async () => { throw new Error('Tool-free profile requested permission') }, toolUseContext: context,
    querySource: input.nonInteractive ? 'sdk' : 'repl_main_thread', maxTurns: 1, maxOutputTokensOverride: input.maxOutputTokens, deps,
  })
  while (true) {
    const next = await generator.next()
    if (next.done) { observation.terminal = next.value; break }
    yielded.push(next.value)
    observation.yielded.push(JSON.stringify(next.value))
  }
} catch (failure) {
  if (!(failure instanceof Error)) throw failure
  observation.error = { name: failure.name, message: failure.message }
} finally { context.abortController.abort() }
observation.yieldedAfter = yielded.map(message => JSON.stringify(message))
// Observe the original envelopes delegated to query(), rather than flattening
// blocks or using a constructed expectation. The fixed no-retry/no-hook profile
// has exactly one original model-history input and one successful response.
observation.history = [...resolvedHistory, ...assistants].map(message => JSON.stringify(message))
process.stdout.write(JSON.stringify({ observationSource: JSON.stringify(observation), sourceOnly: { capabilities, runtime: { platform: process.platform, arch: process.arch, version: process.version } } }))
