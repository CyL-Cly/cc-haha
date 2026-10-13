import { readFileSync, writeFileSync } from 'node:fs'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from '../../../scripts/quality-gate/go-parity/fixture-guard'
import { installClientFixtureCapabilities, absentManagedSettingsFixturePaths } from './client-options-capabilities'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { home: string; origin: string; guard?: string; ca?: string; mode?: string }
const home = assertDisposableFixtureHome(input.home, 'Ignored direct HTTPS Source probe')
if (process.env.CC_HAHA_DIRECT_HTTPS_PROBE !== '1' || process.env.NODE_ENV !== 'production') throw new Error('Explicit Source-only HTTPS profile required')
const origin = new URL(input.origin)
if (origin.protocol !== 'https:' || origin.hostname !== '127.0.0.1' || !origin.port || origin.origin !== input.origin) throw new Error('Private allocated HTTPS origin required')
if (input.mode && !['abort-before', 'abort-handshake', 'timeout-handshake', 'late-sse', 'abort-body', 'cached-ca-file-change', 'cleared-ca-file-change'].includes(input.mode)) throw new Error('Unrepresented lifecycle control')
const capabilities = await installClientFixtureCapabilities(home, { loopbackFetchOrigin: input.origin, absentPaths: [...absentManagedSettingsFixturePaths(), '/home/claude/.claude/remote/.oauth_token'] })
if (input.guard === 'outside') readFileSync('/etc/hosts')
if (input.guard === 'network') await fetch('https://127.0.0.1:2')
if (input.guard === 'native') Bun.spawn(['security'])
if (Bun.version !== '1.3.14' || Bun.revision !== '0d9b296af33f2b851fcbf4df3e9ec89751734ba4') throw new Error('Pinned Bun required')
process.env.ANTHROPIC_BASE_URL = input.origin
process.env.ANTHROPIC_API_KEY = 'fake-direct-https-key'
process.env.API_TIMEOUT_MS = '1000'
process.env.CLAUDE_CODE_DISABLE_FAST_MODE = '1'
if (input.ca !== undefined) process.env.NODE_EXTRA_CA_CERTS = input.ca
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(home)
bootstrap.setOriginalCwd(home)
bootstrap.setProjectRoot(home)
bootstrap.switchSession('00000000-0000-4000-8000-000000000001' as Parameters<typeof bootstrap.switchSession>[0])
bootstrap.setIsInteractive(false)
const config = await import('../../../src/utils/config')
config.enableConfigs()
config.saveGlobalConfig(previous => ({ ...previous, userID: 'fake-https-device' }))
const { getAnthropicClient } = await import('../../../src/services/api/client')
const { getCACertificates, clearCACertsCache } = await import('../../../src/utils/caCerts')
let caReads = 0
const { getFsImplementation } = await import('../../../src/utils/fsOperations')
const fileSystem = getFsImplementation()
const readOriginal = fileSystem.readFileSync.bind(fileSystem)
spyOn(fileSystem, 'readFileSync').mockImplementation(((path: any, ...args: any[]) => {
  if (path === input.ca) caReads++
  return (readOriginal as any)(path, ...args)
}) as typeof fileSystem.readFileSync)
let cacheIdentity: boolean | null = null
if (input.mode === 'cached-ca-file-change' || input.mode === 'cleared-ca-file-change') {
  if (!input.ca) throw new Error('Original CA path required for cache profile')
  const first = getCACertificates()
  writeFileSync(input.ca, readFileSync(home + '/other.pem'))
  if (input.mode === 'cleared-ca-file-change') clearCACertsCache()
  cacheIdentity = first === getCACertificates()
}
const trace: unknown[] = []
const controller = new AbortController()
if (input.mode === 'abort-before') controller.abort()
const client = await getAnthropicClient({ maxRetries: 0, model: 'deepseek-flash', source: 'sdk', fetchOverride: (url, init) => {
  if (new URL(String(url)).origin !== input.origin || init?.method !== 'POST') throw new Error('Request escaped private original origin')
  const options = init as RequestInit & { tls?: { ca?: string[] } }
  trace.push({ url: String(url), method: options.method, tlsCA: options.tls?.ca?.length ?? null, body: String(options.body) })
  const response = fetch(url, init)
  if (input.mode === 'abort-handshake') void (async () => {
    for (let count = 0; count < 300; count++) {
      try { readFileSync(home + '/accepted'); controller.abort(); return } catch (error) {
        if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error
      }
      await new Promise(resolve => setTimeout(resolve, 5))
    }
    throw new Error('Private accepted marker was not observed')
  })()
  if (input.mode === 'abort-body') void response.then(() => { setTimeout(() => controller.abort(), 30) }, () => {})
  return response
} })
function project(error: any, depth = 0): unknown {
  if (depth > 4 || error === undefined) return null
  return { constructor: error?.constructor?.name, name: error?.name, message: error?.message, code: error?.code, errno: error?.errno,
    ownNames: Object.getOwnPropertyNames(error), keys: Object.keys(error), json: JSON.stringify(error),
    descriptorShape: Object.fromEntries(Object.getOwnPropertyNames(error).map(key => { const d = Object.getOwnPropertyDescriptor(error, key)!; return [key, { enumerable: d.enumerable, configurable: d.configurable, writable: d.writable }] })),
    cause: error?.cause === undefined ? null : project(error.cause, depth + 1) }
}
let observation: unknown
try {
  if (input.mode === 'late-sse' || input.mode === 'abort-body') {
    const stream = await client.messages.create({ model: 'deepseek-flash', max_tokens: 16, stream: true, messages: [{ role: 'user', content: 'private transport probe' }] }, { signal: controller.signal })
    const chunks: unknown[] = []
    for await (const chunk of stream) chunks.push(chunk)
    observation = { outcome: controller.signal.aborted ? 'caller-aborted-stream' : 'success', chunks }
  } else {
    const value = await client.messages.create({ model: 'deepseek-flash', max_tokens: 16, messages: [{ role: 'user', content: 'private transport probe' }] }, { signal: controller.signal })
    observation = { outcome: 'success', value }
  }
} catch (error) { observation = { outcome: 'error', error: project(error) } }
process.stdout.write(JSON.stringify({ observation, trace, caCount: getCACertificates()?.length ?? null, caReads, cacheIdentity, capabilities,
  scope: 'actual Source getAnthropicClient + SDK single physical HTTPS creation; not query/retry/Go/default trust proof' }))
