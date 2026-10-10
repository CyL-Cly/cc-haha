import { assertDisposableFixtureHome } from './fixture-guard'
import { installClientFixtureCapabilities } from './client-options-capabilities'
import type { SDKStreamObservation, SDKStreamScenario } from './sdk-stream-fixtures'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as SDKStreamScenario & { home: string }
const home = assertDisposableFixtureHome(input.home, 'SDK stream')
if (process.env.CC_HAHA_SDK_STREAM_FIXTURE !== '1' || !Array.isArray(input.wireBytes) || input.wireBytes.some(value => !Number.isInteger(value) || value < 0 || value > 255) || input.chunks?.some(value => !Number.isSafeInteger(value) || value < 1)
  || input.status !== undefined && (!Number.isInteger(input.status) || ![200, 204, 205].includes(input.status) && (input.status < 400 || input.status > 599))
  || input.headers !== undefined && (!Array.isArray(input.headers) || input.headers.some(pair => !Array.isArray(pair) || pair.length !== 2 || typeof pair[0] !== 'string' || !/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(pair[0]) || typeof pair[1] !== 'string' || /[\r\n\0]/.test(pair[1])))) throw new Error('SDK stream requires the explicit valid offline wire profile')
const wire = Uint8Array.from(input.wireBytes)
const pairs: Array<[string, string]> = [['content-type', 'text/event-stream'], ['date', 'Thu, 01 Jan 1970 00:00:00 GMT'], ['content-length', String(wire.length)], ['connection', 'keep-alive'], ...input.headers ?? []]
// Pin the HTTP wire itself. A Response(ReadableStream) server may replace an
// explicit length with chunked framing or omit empty headers; those fixture
// server choices must not masquerade as SDK/Go transport behavior.
const connections = new Set<Bun.Socket<{ request: Buffer; responded: boolean }>>()
const server = Bun.listen<{ request: Buffer; responded: boolean }>({ hostname: '127.0.0.1', port: 0, socket: {
  open(socket) { socket.data = { request: Buffer.alloc(0), responded: false }; connections.add(socket) },
  close(socket) { connections.delete(socket) },
  data(socket, data) {
    if (socket.data.responded) return
    socket.data.request = Buffer.concat([socket.data.request, data])
    const end = socket.data.request.indexOf('\r\n\r\n')
    if (end < 0) return
    const header = socket.data.request.subarray(0, end).toString()
    const length = Number(header.match(/\r\ncontent-length:\s*(\d+)/i)?.[1] ?? 0)
    if (socket.data.request.length < end + 4 + length) return
    if (!header.startsWith('POST /v1/messages HTTP/1.1\r\n')) throw new Error('Unexpected offline SDK HTTP path')
    socket.data.responded = true
    socket.write(`HTTP/1.1 ${input.status ?? 200} Fixture\r\n${pairs.map(([name, value]) => `${name}: ${value}`).join('\r\n')}\r\n\r\n`)
    for (let offset = 0, count = 0; offset < wire.length; count++) {
      const length = input.chunks?.[count % input.chunks.length] ?? wire.length
      const end = Math.min(offset + length, wire.length)
      socket.write(wire.slice(offset, end))
      offset = end
    }
    if (pairs.some(([name, value]) => name.toLowerCase() === 'connection' && value.toLowerCase().includes('close'))) socket.end()
  },
} })
const origin = `http://127.0.0.1:${server.port}`
const capabilities = await installClientFixtureCapabilities(home, { loopbackFetchOrigin: origin })
const { default: Anthropic } = await import('@anthropic-ai/sdk')
const { Stream } = await import('@anthropic-ai/sdk/core/streaming')
const { VERSION } = await import('@anthropic-ai/sdk/version')
const diagnostics: SDKStreamObservation['diagnostics'] = []
const client = new Anthropic({ apiKey: 'offline-fixture-key', baseURL: origin, maxRetries: 0, logLevel: 'error', logger: {
  error: (message, ...values) => diagnostics.push({ message, valueSource: JSON.stringify(values[0]) }), warn: () => {}, info: () => {}, debug: () => {},
} })
function numberLeaves(value: unknown) {
  type Path = { key: string | number; parent?: Path }
  const result: Record<string, { value: string; negativeZero: boolean }> = {}
  const pending: Array<{ value: unknown; path?: Path }> = [{ value }]
  while (pending.length) {
    const { value, path } = pending.pop()!
    if (typeof value === 'number') {
      const parts: Array<string | number> = []
      for (let item = path; item; item = item.parent) parts.push(item.key)
      result[JSON.stringify(parts.reverse())] = { value: String(value), negativeZero: Object.is(value, -0) }
    } else if (Array.isArray(value)) value.forEach((child, index) => pending.push({ value: child, path: { key: index, parent: path } }))
    else if (value && typeof value === 'object') for (const [name, child] of Object.entries(value)) pending.push({ value: child, path: { key: name, parent: path } })
  }
  return result
}
const controller = new AbortController()
try {
  const observation: SDKStreamObservation = { yields: [], error: null, diagnostics, headers: [], requestID: null }
  try {
    // Non-2xx fixtures must exercise SDK request/APIError generation, rather
    // than manually applying Stream to a response the client would reject.
    const result = input.status !== undefined
      ? await client.messages.create({ model: 'sdk-stream-fixture', messages: [], max_tokens: 32, stream: true }, { signal: controller.signal }).withResponse()
      : { response: await fetch(origin + '/v1/messages', { method: 'POST', headers: { 'content-type': 'application/json', 'x-api-key': 'offline-fixture-key' }, body: '{"model":"sdk-stream-fixture","messages":[],"max_tokens":32,"stream":true}', signal: controller.signal }), data: null }
    observation.headers = [...result.response.headers]
    observation.requestID = result.response.headers.get('request-id')
    const stream = result.data ?? Stream.fromSSEResponse(result.response, controller, client)
    for await (const value of stream) observation.yields.push({ source: JSON.stringify(value), numberLeaves: numberLeaves(value) })
  } catch (failure) {
    const error = failure as Error & { status?: number; requestID?: string | null; error?: unknown; headers?: Headers }
    const state = (value: unknown) => value === undefined ? { kind: 'undefined' } : { kind: value === null ? 'null' : typeof value, source: JSON.stringify(value) }
    if (!observation.headers.length && error.headers) { observation.headers = [...error.headers]; observation.requestID = error.headers.get('request-id') }
    observation.error = { kind: error.constructor.name === 'SyntaxError' ? 'json_parse' : error.constructor.name === 'AnthropicError' ? 'body_missing' : error.status !== undefined ? 'http_api' : 'sse_api', class: error.constructor.name, name: error.name, message: error.message,
      status: state(error.status), requestID: state(error.requestID), payload: { ...state(error.error), ...(error.error !== undefined ? { numberLeaves: numberLeaves(error.error) } : {}) }, headers: error.headers ? [...error.headers] : null }
  }
  process.stdout.write(JSON.stringify({ observation, capabilities, sdkVersion: VERSION, engineVersion: process.versions.bun }))
} finally { controller.abort(); for (const socket of connections) socket.end(); server.stop(true) }
