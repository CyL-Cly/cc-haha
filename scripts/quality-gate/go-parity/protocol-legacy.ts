import { readFile } from 'node:fs/promises'
import { lstatSync, realpathSync } from 'node:fs'
import { dirname } from 'node:path'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

// Fresh child only: never share the real API graph with mock.module tests and
// never load actual HOME, provider settings, or fixture-recording defaults.
const home = assertDisposableFixtureHome(process.env.HOME, 'Protocol')
const fixturePath = process.env.CC_HAHA_PROTOCOL_FIXTURE
if (!fixturePath) throw new Error('Protocol fixture path is required')
if (dirname(fixturePath) !== home || lstatSync(fixturePath).isSymbolicLink() || !lstatSync(fixturePath).isFile() || dirname(realpathSync(fixturePath)) !== realpathSync(home)) {
  throw new Error('Protocol fixture file requires a regular file inside disposable HOME')
}
const fixture = JSON.parse(await readFile(fixturePath, 'utf8')) as { wire?: string; wireBytes?: number[]; probeCapability?: string; redirectTarget?: string }
if ((typeof fixture.wire === 'string') === Array.isArray(fixture.wireBytes) || (fixture.wireBytes && fixture.wireBytes.some(byte => !Number.isInteger(byte) || byte < 0 || byte > 255))) {
  throw new Error('Protocol fixture requires exactly one valid wire or wireBytes input')
}
const wireBytes = fixture.wireBytes ? new Uint8Array(fixture.wireBytes) : new TextEncoder().encode(fixture.wire)
if (fixture.probeCapability === 'redirect') {
  const target = new URL(fixture.redirectTarget ?? '')
  if (target.protocol !== 'http:' || target.hostname !== '127.0.0.1' || !target.port || target.username || target.password) throw new Error('Protocol redirect probe requires an explicit HTTP loopback target')
}
const httpTrace: unknown[] = []
const server = Bun.serve({
  hostname: '127.0.0.1', port: 0,
  fetch(request) {
    if (fixture.probeCapability === 'redirect' && new URL(request.url).pathname === '/fixture-redirect') return new Response(null, { status: 302, headers: { location: fixture.redirectTarget! } })
    if (new URL(request.url).pathname !== '/v1/messages') return new Response('unexpected fixture provider path', { status: 404 })
    if (process.env.CC_HAHA_PROTOCOL_HTTP_OBSERVATION === '1') {
      const url = new URL(request.url)
      httpTrace.push({ method: request.method, path: url.pathname + url.search, apiKey: request.headers.get('x-api-key'), authorization: request.headers.get('authorization'), version: request.headers.get('anthropic-version') })
    }
    let offset = 0
    const bytes = wireBytes
    const stream = new ReadableStream({
      pull(controller) {
        if (offset === bytes.length) return controller.close()
        controller.enqueue(bytes.slice(offset, offset + 1))
        offset++
      },
    })
    return new Response(stream, { headers: { 'content-type': 'text/event-stream' } })
  },
})
const origin = server.url.origin
process.env.ANTHROPIC_BASE_URL = origin
const capabilities = await installClientFixtureCapabilities(home, { loopbackFetchOrigin: origin, absentPaths: absentManagedSettingsFixturePaths() })
if (fixture.probeCapability) {
  try {
    if (fixture.probeCapability === 'subprocess') require('node:child_process').execFileSync('security', ['find-generic-password'])
    else if (fixture.probeCapability === 'native') Bun.spawn(['security'])
    else if (fixture.probeCapability === 'network') await fetch('https://fixture.invalid')
    else if (fixture.probeCapability === 'proxy-origin') await fetch(origin.replace('127.0.0.1', 'localhost') + '/v1/messages')
    else if (fixture.probeCapability === 'wrong-port') await fetch(`http://127.0.0.1:${server.port === 65535 ? 1 : server.port + 1}/v1/messages`)
    else if (fixture.probeCapability === 'credentials') await fetch(origin.replace('http://', 'http://fixture@') + '/v1/messages')
    else if (fixture.probeCapability === 'redirect') {
      try { await fetch(origin + '/fixture-redirect') }
      catch (error) { throw new Error(`Protocol fixture refused redirect: ${(error as Error).message}`) }
    }
    else throw new Error('Unknown protocol capability probe')
    throw new Error('Protocol capability probe unexpectedly succeeded')
  } finally { server.stop(true) }
}
;(globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }).MACRO = { BUILD_TIME: '' }
const bootstrap = await import('../../../src/bootstrap/state')
bootstrap.setCwdState(process.cwd())
bootstrap.setOriginalCwd(process.cwd())
bootstrap.setProjectRoot(process.cwd())
const { enableConfigs } = await import('../../../src/utils/config')
enableConfigs()
const { queryModelWithStreaming } = await import('../../../src/services/api/claude')
const { createUserMessage } = await import('../../../src/utils/messages')
const { asSystemPrompt } = await import('../../../src/utils/systemPromptType')
const { getEmptyToolPermissionContext } = await import('../../../src/Tool')
const controller = new AbortController()
const content: unknown[] = []
let messageID = ''
let stopReason = ''
let usage: Record<string, unknown> = {}
let failed = false
const messages: Array<{ content: unknown[]; usage: Record<string, unknown> }> = []
try {
  for await (const part of queryModelWithStreaming({
    messages: [createUserMessage({ content: 'Protocol fixture' })], systemPrompt: asSystemPrompt([]),
    thinkingConfig: { type: 'disabled' }, tools: [], signal: controller.signal,
    options: { model: 'fixture-model', querySource: 'sdk', isNonInteractiveSession: true, maxOutputTokensOverride: 32_000, getToolPermissionContext: async () => getEmptyToolPermissionContext() },
  })) {
    if (part.type === 'stream_event') {
      if (part.event.type === 'message_start') messageID = part.event.message.id
      if (part.event.type === 'message_delta' && part.event.delta.stop_reason != null) stopReason = part.event.delta.stop_reason
    }
    if (part.type === 'assistant') {
      if (part.isApiErrorMessage) failed = true
      else messages.push(part.message as unknown as { content: unknown[]; usage: Record<string, unknown> })
    }
  }
} catch {
  failed = true
} finally {
  controller.abort()
  server.stop(true)
}
for (const message of messages) {
  content.push(...message.content)
  usage = message.usage
}
// An empty end_turn has no assistant block to carry usage. The codec has the
// provider envelope, so compare counters only when a block was actually
// committed by the reference API. This does not normalize content or errors.
const result = {
  messageID, stopReason, content, contentBytes: JSON.stringify(content), failed,
  usage: content.length ? { input_tokens: usage.input_tokens ?? 0, output_tokens: usage.output_tokens ?? 0, cache_creation_input_tokens: usage.cache_creation_input_tokens ?? 0, cache_read_input_tokens: usage.cache_read_input_tokens ?? 0 } : null,
  ...(process.env.CC_HAHA_PROTOCOL_HTTP_OBSERVATION === '1' ? { http: httpTrace } : {}),
}
process.stdout.write(`TS_PROTOCOL_RESULT:${JSON.stringify(result)}\n`)
// This Source profile observes empty external credentials and absent OS-level
// managed policy. Their calls are isolation evidence, separate from the
// provider/codec observations compared with Go.
process.stdout.write(`TS_PROTOCOL_CAPABILITIES:${JSON.stringify(capabilities)}\n`)
