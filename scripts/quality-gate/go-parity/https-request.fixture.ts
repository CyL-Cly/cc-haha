import * as crypto from 'node:crypto'
import { lstatSync, readFileSync } from 'node:fs'
import { relative, isAbsolute, join } from 'node:path'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import type { HTTPSRequestInput } from './https-request-fixtures'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as HTTPSRequestInput
const home = assertDisposableFixtureHome(input.home, 'HTTPS request')
if (process.env.CC_HAHA_HTTPS_REQUEST_FIXTURE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Request retry requires explicit private production profile')
const allowedEnvironment = new Set(['ANTHROPIC_BASE_URL', 'ANTHROPIC_API_KEY', 'API_TIMEOUT_MS', 'CLAUDE_CODE_MAX_RETRIES', 'CLAUDE_CODE_DISABLE_FAST_MODE', 'CLAUDE_CODE_EXTRA_METADATA', 'NODE_EXTRA_CA_CERTS'])
for (const [name, value] of Object.entries(input.env)) if (!allowedEnvironment.has(name) || typeof value !== 'string') throw new Error('Request retry rejects unported environment')
const base = new URL(input.env.ANTHROPIC_BASE_URL)
if (base.username || base.password || base.pathname !== '/' || base.search || base.hash || !base.port) throw new Error('Request retry requires exact HTTPS loopback origin')
const origin = base.origin
if (new URL(origin).hostname !== '127.0.0.1' || !origin.startsWith('https:') || !input.env.ANTHROPIC_API_KEY || input.env.CLAUDE_CODE_DISABLE_FAST_MODE !== '1') throw new Error('Request retry requires direct original-env-key loopback')
if (!['query', 'creation'].includes(input.layer)) throw new Error('HTTPS request requires declared boundary layer')
if (input.env.NODE_EXTRA_CA_CERTS !== undefined && input.env.NODE_EXTRA_CA_CERTS !== join(home, 'original-ca.pem')) throw new Error('HTTPS request requires private original CA path before imports')
const originalKeys = new Set(['name', 'mode', 'home', 'env', 'historySources', 'systemSources', 'model', 'maxOutputTokens', 'deviceID', 'sessionID', 'runtime', 'sdkBetas', 'layer', 'probe', 'probePath'])
if (Object.keys(input).some(key => !originalKeys.has(key))) throw new Error('HTTPS request rejects prepared/unknown input before imports')
if (input.env.NODE_EXTRA_CA_CERTS) {
  try { const info = lstatSync(input.env.NODE_EXTRA_CA_CERTS); if (info.isSymbolicLink() || !info.isFile()) throw new Error('HTTPS request requires non-symlink private CA file before imports') }
  catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error }
}
const capabilities = await installClientFixtureCapabilities(home, { loopbackFetchOrigin: origin, absentPaths: absentManagedSettingsFixturePaths() })
const probe = (input as HTTPSRequestInput & { probe?: string; probePath?: string }).probe
if (probe === 'native') Bun.spawn(['security'])
if (probe === 'network') await fetch('http://127.0.0.1:2')
if (probe === 'outside-read') readFileSync((input as HTTPSRequestInput & { probePath: string }).probePath)
for (const [name, value] of Object.entries(input.env)) process.env[name] = value
const global = globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }
global.MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.switchSession(input.sessionID as Parameters<typeof bootstrap.switchSession>[0])
bootstrap.setIsInteractive(false)
bootstrap.setSdkBetas(input.sdkBetas ?? [])
bootstrap.setPromptCache1hEligible(false)
bootstrap.setPromptCache1hAllowlist([])
const config = await import('../../../src/utils/config')
config.enableConfigs()
config.saveGlobalConfig(previous => ({ ...previous, userID: input.deviceID }))
const clientModule = await import('../../../src/services/api/client')
const auth = await import('../../../src/utils/auth')
const failures: unknown[] = []
let creationUUIDCalls = 0
function projectFailure(error: any): unknown {
  return { class: error.constructor.name, name: error.name, message: error.message, messageJSON: JSON.stringify(error.message), json: JSON.stringify(error), cause: error.cause === undefined ? null : projectFailure(error.cause) }
}
const caReads: Array<{ path: string; code: string }> = []
const { getFsImplementation } = await import('../../../src/utils/fsOperations')
const files = getFsImplementation(), readOriginal = files.readFileSync.bind(files)
spyOn(files, 'readFileSync').mockImplementation((path, options) => {
  if (path !== input.env.NODE_EXTRA_CA_CERTS) return readOriginal(path, options)
  const row = { path: relative(home, isAbsolute(path) ? path : join(home, path)), code: '' }
  caReads.push(row)
  try { return readOriginal(path, options) } catch (error) { row.code = (error as NodeJS.ErrnoException).code ?? ''; throw error }
})
const getClient = clientModule.getAnthropicClient
const getKey = auth.getAnthropicApiKey
let clients = 0, keyLookups = 0
spyOn(clientModule, 'getAnthropicClient').mockImplementation(async options => {
  clients++
  const client = await getClient(options)
  const create = client.beta.messages.create.bind(client.beta.messages)
  spyOn(client.beta.messages, 'create').mockImplementation(((...args: any[]) => {
    const promise = (create as any)(...args)
    const withResponse = promise.withResponse.bind(promise)
    promise.withResponse = async () => {
      try { const result = await withResponse(); if (input.mode === 'abort-body') originalTimeout(() => controller.abort(), 40); return result } catch (error) {
        failures.push(projectFailure(error))
        creationUUIDCalls = observation.uuidCalls
        throw error
      }
    }
    return promise
  }) as any)
  return client
})
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
if (Bun.version !== '1.3.14' || Bun.revision !== '0d9b296af33f2b851fcbf4df3e9ec89751734ba4') throw new Error('Request retry requires pinned Bun')
const observation = { failures, caReads, clients: 0, keyLookups: 0, requests: [] as unknown[], notices: [] as string[], sleeps: [] as number[], yielded: [] as string[], yieldedAfter: [] as string[], historyBefore: input.historySources, historyAfter: [] as string[], normalizedHistory: [] as unknown[], preparationCalls: 0, uuidCallsOnRequest: [] as number[], uuidCalls: 0, outcome: '', pause: null as unknown }
spyOn(crypto, 'randomUUID').mockImplementation(() => `retry-id-${++observation.uuidCalls}` as ReturnType<typeof crypto.randomUUID>)
spyOn(Math, 'random').mockReturnValue(0)
const RealDate = Date
globalThis.Date = class extends RealDate {
  constructor(...args: unknown[]) { super(...(args.length ? args : [1700000000000]) as [number]) }
  static now() { return 1700000000000 }
} as DateConstructor
let controlTLS: unknown
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
  tools: [], thinkingConfig: { type: 'disabled' }, signal: controller.signal,
  options: {
    model: input.model, querySource: 'sdk', isNonInteractiveSession: true, maxOutputTokensOverride: input.maxOutputTokens,
    agents: [], mcpTools: [], hasAppendSystemPrompt: false, getToolPermissionContext: async () => getEmptyToolPermissionContext(),
    fetchOverride: async (url, init) => {
      const parsed = new URL(String(url))
      if (parsed.origin !== origin || init?.method !== 'POST') throw new Error('Request retry escaped private POST origin')
      controlTLS = (init as any).tls
      observation.uuidCallsOnRequest.push(observation.uuidCalls)
      observation.requests.push({ path: parsed.pathname + parsed.search, method: init.method, headers: [...new Headers(init.headers)], body: String(init.body) })
      const response = fetch(url, init)
      if (input.mode === 'abort-handshake') void (async () => {
        for (let count = 0; count < 300; count++) {
          try { readFileSync(join(home, 'accepted')); controller.abort(); return } catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error }
          await new Promise(resolve => originalTimeout(resolve, 5))
        }
        throw new Error('Actual private TCP accept was not observed')
      })()
      if (input.mode === 'abort-headers') {
        await fetch(origin + '/__fixture/request-seen', { tls: controlTLS } as any)
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
        const server = await (await fetch(origin + '/__fixture/state', { tls: controlTLS } as any)).json()
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
const queryTerminal = input.layer === 'creation' ? { outcome: observation.outcome, yielded: observation.yielded, yieldedAfter: yielded.map(value => JSON.stringify(value)), uuidCalls: observation.uuidCalls } : null
if (input.layer === 'creation') {
  // Preserve the complete real query terminal separately; the Go opener does
  // not implement getAssistantMessageFromError and must not receive its bytes.
  observation.outcome = failures.length ? 'creation_failed' : observation.outcome
  observation.yielded = [...observation.notices]
  observation.uuidCalls = creationUUIDCalls
}
observation.preparationCalls = pairingCalls
observation.normalizedHistory = normalizedHistory
observation.clients = clients
observation.keyLookups = keyLookups
observation.yieldedAfter = input.layer === 'creation' ? [...observation.notices] : yielded.map(value => JSON.stringify(value))
observation.historyAfter = history.map(value => JSON.stringify(value))
process.stdout.write(JSON.stringify({ observation, sourceOnly: { capabilities, pairingEvidence, queryTerminal, sdk: '0.80.0', runtime: { platform: process.platform, arch: process.arch, version: process.version } } }))
