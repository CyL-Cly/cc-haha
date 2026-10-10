import * as crypto from 'node:crypto'
import { spyOn } from 'bun:test'
import type { RequestContextCase } from './request-context-fixtures'
import { requestContextEnvironmentNames, requestContextWire } from './request-context-fixtures'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as RequestContextCase & { homeDir: string; probe?: string }
assertDisposableFixtureHome(input.homeDir, 'Request context')
if (process.env.CC_HAHA_REQUEST_CONTEXT_FIXTURE !== '1' || process.env.NODE_ENV !== 'production' || process.env.CLAUDE_CODE_SIMPLE !== '1') throw new Error('Request context fixture requires isolated production profile')
for (const [name, value] of Object.entries(input.env ?? {})) if (!requestContextEnvironmentNames.has(name) || typeof value !== 'string') throw new Error('Request context fixture cannot override sandbox or auth/provider environment')
const wireRequests: unknown[] = []
const server = Bun.serve({ hostname: '127.0.0.1', port: 0, async fetch(request) {
  wireRequests.push({ path: new URL(request.url).pathname + new URL(request.url).search, method: request.method, headers: [...request.headers], body: await request.text() })
  return new Response(requestContextWire, { headers: { 'content-type': 'text/event-stream', 'request-id': 'response-request', connection: 'keep-alive' } })
} })
const capabilities = await installClientFixtureCapabilities(input.homeDir, { loopbackFetchOrigin: server.url.origin, absentPaths: absentManagedSettingsFixturePaths() })
try {
  if (input.probe === 'network') await fetch('https://fixture.invalid')
  if (input.probe === 'native') Bun.spawn(['security'])
  for (const [name, value] of Object.entries(input.env ?? {})) process.env[name] = value
  process.env.ANTHROPIC_BASE_URL = server.url.origin + (input.proxy ? '/proxy/providers/fixture' : '')
  ;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
  const bootstrap = await import('../../../src/bootstrap/state')
  bootstrap.setCwdState(input.homeDir)
  bootstrap.setOriginalCwd(input.homeDir)
  bootstrap.setProjectRoot(input.homeDir)
  bootstrap.switchSession('00000000-0000-4000-8000-000000000000' as Parameters<typeof bootstrap.switchSession>[0])
  bootstrap.setIsInteractive(input.options?.isNonInteractiveSession === false)
  bootstrap.setSdkBetas(input.sdkBetas)
  if (input.ttlEligible !== undefined) bootstrap.setPromptCache1hEligible(input.ttlEligible)
  if (input.ttlAllowlist) bootstrap.setPromptCache1hAllowlist(input.ttlAllowlist)
  const config = await import('../../../src/utils/config')
  config.enableConfigs()
  config.saveGlobalConfig(previous => ({ ...previous, userID: 'fixture-device' }))
  const messages = await import('../../../src/utils/messages')
  let normalizedHistory: unknown[] = []
  const pairing = messages.ensureToolResultPairing
  spyOn(messages, 'ensureToolResultPairing').mockImplementation(history => {
    const result = pairing(history)
    normalizedHistory = result.map(message => ({ type: message.type, uuid: message.uuid ?? null, messageSource: JSON.stringify(message.message) }))
    return result
  })
  const logging = await import('../../../src/services/api/logging')
  const logQuery = logging.logAPIQuery
  let previousRequestID: string | null = null
  spyOn(logging, 'logAPIQuery').mockImplementation(options => { previousRequestID = options.previousRequestId ?? null; return logQuery(options) })
  const { queryModelWithStreaming } = await import('../../../src/services/api/claude')
  const { asSystemPrompt } = await import('../../../src/utils/systemPromptType')
  const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
  const { runWithWorkload } = await import('../../../src/utils/workloadContext')
  let uuidCalls = 0
  spyOn(crypto, 'randomUUID').mockImplementation(() => `context-id-${++uuidCalls}` as ReturnType<typeof crypto.randomUUID>)
  const RealDate = Date
  const FixedDate = class extends RealDate { constructor(...args: ConstructorParameters<typeof Date>) { if (args.length) super(...args); else super(1700000000000) } static now() { return 1700000000000 } }
  globalThis.Date = FixedDate as typeof Date
  const history = input.historySources.map(source => JSON.parse(source))
  const before = history.map(message => JSON.stringify(message))
  const fetches: unknown[] = []
  const yields: string[] = []
  await runWithWorkload(input.workload, async () => {
    for await (const event of queryModelWithStreaming({
      messages: history, systemPrompt: asSystemPrompt((input.systemSources ?? []).map(source => JSON.parse(source))), thinkingConfig: (input.thinking ?? { type: 'disabled' }) as any,
      tools: [], signal: new AbortController().signal, options: {
        model: 'fixture-model', querySource: 'sdk', isNonInteractiveSession: true, agents: [], mcpTools: [], hasAppendSystemPrompt: false,
        ...input.options, getToolPermissionContext: async () => getEmptyToolPermissionContext(),
        fetchOverride: async (url, init) => {
          const parsed = new URL(String(url))
          if (parsed.origin !== server.url.origin || init?.method !== 'POST') throw new Error('Request context escaped loopback POST')
          fetches.push({ path: parsed.pathname + parsed.search, method: init.method, headers: [...new Headers(init.headers)], body: init.body })
          return fetch(url, init)
        },
      },
    })) yields.push(JSON.stringify(event))
  })
  process.stdout.write(JSON.stringify({ observation: { fetches, normalizedHistory, historyBefore: before, historyAfter: history.map(message => JSON.stringify(message)), uuidCalls, previousRequestID }, sourceOnly: { wireRequests, yields, capabilities, lastMainRequestID: bootstrap.getLastMainRequestId(), runtime: { platform: process.platform, arch: process.arch, version: process.version }, sdk: '0.80.0', profile: 'direct-relay-no-experiments' } }))
} finally { server.stop(true) }
