import * as crypto from 'node:crypto'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import { requestContextEnvironmentNames, requestContextWire, type RequestContextCase } from './request-context-fixtures'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as RequestContextCase & { homeDir: string }
assertDisposableFixtureHome(input.homeDir, 'Native query logging')
if (process.env.CC_HAHA_NATIVE_QUERY_LOGGING_FIXTURE !== '1' || process.env.NODE_ENV !== 'production' || process.env.CLAUDE_CODE_SIMPLE !== '1') throw new Error('Native query logging requires isolated production profile')
const allowed = new Set(requestContextEnvironmentNames)
for (const tier of ['FABLE', 'OPUS', 'SONNET', 'HAIKU']) {
  allowed.add(`ANTHROPIC_DEFAULT_${tier}_MODEL`)
  allowed.add(`ANTHROPIC_DEFAULT_${tier}_MODEL_SUPPORTED_CAPABILITIES`)
}
for (const [name, value] of Object.entries(input.env ?? {})) if (!allowed.has(name) || typeof value !== 'string') throw new Error('Native query logging cannot override sandbox, credentials or provider origin')
const server = Bun.serve({ hostname: '127.0.0.1', port: 0, fetch() {
  return new Response(requestContextWire, { headers: { 'content-type': 'text/event-stream', 'request-id': 'response-request', connection: 'keep-alive' } })
} })
const capabilities = await installClientFixtureCapabilities(input.homeDir, { loopbackFetchOrigin: server.url.origin, absentPaths: absentManagedSettingsFixturePaths() })
try {
  for (const [name, value] of Object.entries(input.env ?? {})) process.env[name] = value
  process.env.ANTHROPIC_BASE_URL = server.url.origin
  ;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
  const state = await import('../../../src/bootstrap/state')
  state.setCwdState(input.homeDir)
  state.setOriginalCwd(input.homeDir)
  state.setProjectRoot(input.homeDir)
  state.switchSession('00000000-0000-4000-8000-000000000000' as Parameters<typeof state.switchSession>[0])
  state.setIsInteractive(false)
  state.setSdkBetas(input.sdkBetas ?? [])
  const config = await import('../../../src/utils/config')
  config.enableConfigs()
  config.saveGlobalConfig(previous => ({ ...previous, userID: 'fixture-device' }))
  const analytics = await import('../../../src/services/analytics')
  const logs: unknown[] = []
  analytics.attachAnalyticsSink({
    logEvent(name, metadata) {
      if (name === 'tengu_api_query') logs.push({ eventName: name, metadata: { ...metadata, baseUrl: '<loopback>' }, keys: Object.keys(metadata), async: false })
    },
    async logEventAsync() {},
  })
  const trace: string[] = []
  let uuidCalls = 0, newDateCalls = 0, dateNowCalls = 0
  spyOn(crypto, 'randomUUID').mockImplementation(() => { trace.push('uuid'); return `logging-id-${++uuidCalls}` as ReturnType<typeof crypto.randomUUID> })
  const RealDate = Date
  globalThis.Date = class extends RealDate {
    constructor(...args: ConstructorParameters<typeof Date>) {
      if (args.length) super(...args)
      else { super(1700000000000); newDateCalls++ }
    }
    static now() { dateNowCalls++; return 1700000000000 }
  } as typeof Date
  const client = await import('../../../src/services/api/client')
  const getClient = client.getAnthropicClient
  let beforeFirstClient: unknown
  spyOn(client, 'getAnthropicClient').mockImplementation((...args) => {
    trace.push('client')
    beforeFirstClient ??= { uuidCalls, newDateCalls, dateNowCalls }
    return getClient(...args)
  })
  const { queryModelWithStreaming } = await import('../../../src/services/api/claude')
  const { asSystemPrompt } = await import('../../../src/utils/systemPromptType')
  const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
  const physical: unknown[] = []
  for await (const _ of queryModelWithStreaming({
    messages: input.historySources.map(source => JSON.parse(source)),
    systemPrompt: asSystemPrompt([]), thinkingConfig: (input.thinking ?? { type: 'disabled' }) as any,
    tools: [], signal: new AbortController().signal,
    options: {
      model: 'fixture-model', querySource: 'sdk', isNonInteractiveSession: true, agents: [], mcpTools: [], hasAppendSystemPrompt: false,
      ...input.options,
      getToolPermissionContext: async () => { trace.push('permission'); return getEmptyToolPermissionContext() },
      fetchOverride: async (url, init) => {
        if (new URL(String(url)).origin !== server.url.origin || init?.method !== 'POST' || typeof init.body !== 'string') throw new Error('Native query logging escaped loopback POST')
        trace.push('fetch')
        const body = JSON.parse(init.body)
        const policy: Record<string, unknown> = {}
        for (const name of ['model', 'thinking', 'temperature', 'output_config']) if (Object.hasOwn(body, name)) policy[name] = body[name]
        physical.push({ policy, messagesLength: body.messages.length, betas: new Headers(init.headers).get('anthropic-beta') })
        return fetch(url, init)
      },
    },
  })) {}
  if (logs.length !== 1 || physical.length !== 1 || capabilities.blocked.length !== 0) throw new Error('Native query logging expected one Source log and one guarded physical request')
  process.stdout.write(JSON.stringify({ observation: { log: logs[0], physical: physical[0] }, sourceOnly: { capabilities, beforeFirstClient, trace, consumer: 'src/services/api/claude.ts1945-1967 and logging.ts171-242', transport: 'guarded loopback', runtime: process.version } }))
} finally {
  server.stop(true)
}
