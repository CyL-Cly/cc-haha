import * as crypto from 'node:crypto'
import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'
import type { HTTPMetadataCase, HeaderPair } from './http-metadata-fixtures'

const input = JSON.parse(await new Response(Bun.stdin.stream()).text()) as { home: string; cases: HTTPMetadataCase[] }
const home = assertDisposableFixtureHome(input.home, 'HTTP metadata')
if (process.env.CC_HAHA_HTTP_METADATA_FIXTURE !== '1' || !Array.isArray(input.cases) || input.cases.some(value =>
  !['constructed', 'http', 'sse', 'completion'].includes(value.kind)
  || !Array.isArray(value.pairs)
  || value.pairs.some(pair => !Array.isArray(pair) || pair.length !== 2 || pair.some(part => typeof part !== 'string') || /[\r\n\0]/.test(pair[0]) || /[\r\n\0]/.test(pair[1]))
)) throw new Error('HTTP metadata requires explicit valid offline original inputs')

const payload = '{"message":"metadata upstream","future":9007199254740993,"text":"<>&\\ud800"}'
const bodyFor = (item: HTTPMetadataCase) => item.kind === 'http' ? payload
  : item.kind === 'sse' ? `event: error\ndata: ${payload}\n\n`
    : 'event: completion\ndata: {"completion":"metadata fixture"}\n\n'
const wirePairsFor = (item: HTTPMetadataCase) => [
  ...item.pairs,
  ...item.chunked ? [['transfer-encoding', 'chunked']] : item.eof ? [] : [['content-length', String(Buffer.byteLength(bodyFor(item)))]],
  ...item.omitConnection ? [] : [['connection', item.connection ?? 'close']],
] as HeaderPair[]

const sockets = new Set<Bun.Socket<{ request: Buffer; responded: boolean }>>()
let active: HTTPMetadataCase | undefined, requestCount = 0
const server = Bun.listen<{ request: Buffer; responded: boolean }>({
  hostname: '127.0.0.1', port: 0,
  socket: {
    open(socket) {
      socket.data = { request: Buffer.alloc(0), responded: false }
      sockets.add(socket)
    },
    close(socket) { sockets.delete(socket) },
    data(socket, data) {
      if (socket.data.responded) return
      socket.data.request = Buffer.concat([socket.data.request, data])
      const end = socket.data.request.indexOf('\r\n\r\n')
      if (end < 0) return
      const header = socket.data.request.subarray(0, end).toString()
      const length = Number(header.match(/\r\ncontent-length:\s*(\d+)/i)?.[1] ?? 0)
      if (socket.data.request.length < end + 4 + length) return
      if (!active || !header.startsWith('POST /v1/messages HTTP/1.1\r\n')) throw new Error('Unexpected offline metadata request')
      requestCount++
      socket.data.responded = true
      const body = bodyFor(active)
      const wireBody = active.chunked ? `${Buffer.byteLength(body).toString(16)}\r\n${body}\r\n0\r\n\r\n` : body
      const response = `${active.version ?? 'HTTP/1.1'} ${active.kind === 'http' ? 500 : 200} Fixture\r\n${wirePairsFor(active).map(([name, value]) => `${name}: ${value}`).join('\r\n')}\r\n\r\n${wireBody}`
      socket.end(Buffer.from(response, active.latin1HeaderBytes ? 'latin1' : 'utf8'))
    },
  },
})

const capabilities = await installClientFixtureCapabilities(home, {
  loopbackFetchOrigin: `http://127.0.0.1:${server.port}`, absentPaths: absentManagedSettingsFixturePaths(),
})
const global = globalThis as typeof globalThis & { MACRO?: { BUILD_TIME: string } }
global.MACRO = { BUILD_TIME: '' }
const { default: Anthropic, APIError } = await import('@anthropic-ai/sdk')
const { VERSION } = await import('@anthropic-ai/sdk/version')
const { createSystemAPIErrorMessage } = await import('../../../src/utils/messages')
const { getRetryDelay } = await import('../../../src/services/api/withRetry')
const pinnedRevision = '0d9b296af33f2b851fcbf4df3e9ec89751734ba4'
if (VERSION !== '0.128.0' || Bun.version !== '1.3.14' || Bun.revision !== pinnedRevision) throw new Error('HTTP metadata requires pinned MAIN SDK/runtime')
spyOn(Math, 'random').mockReturnValue(0)
const RealDate = Date
globalThis.Date = class extends RealDate {
  constructor(...args: unknown[]) { super(...(args.length ? args : [1700000000000]) as [number]) }
  static now() { return 1700000000000 }
} as DateConstructor
let ids = 0
spyOn(crypto, 'randomUUID').mockImplementation(() => `metadata-notice-${++ids}` as ReturnType<typeof crypto.randomUUID>)
const client = new Anthropic({
  apiKey: 'metadata-offline-key', baseURL: `http://127.0.0.1:${server.port}`, maxRetries: 0,
  logger: { error() {}, warn() {}, info() {}, debug() {} },
})
const observeHeaders = (headers: Headers, names: string[]) => ({
  get: [...new Set(names.map(name => name.toLowerCase()))].map(name => [name, headers.get(name)]),
  iterator: [...headers], jsonSource: JSON.stringify(headers.toJSON()), stringifySource: JSON.stringify(headers),
  jsonKeys: Object.keys(headers.toJSON()), setCookies: headers.getSetCookie(),
})
const observations: unknown[] = []
try {
  for (const item of input.cases) {
    let headers: Headers, initial: unknown = null, clone: unknown = null, failure: APIError | null = null
    const values: string[] = []
    if (item.kind === 'constructed') {
      headers = new Headers(item.record ? Object.fromEntries(item.pairs) : item.pairs)
      initial = observeHeaders(headers, item.pairs.map(([name]) => name))
      for (const operation of item.operations ?? []) {
        if (operation.kind === 'delete') headers.delete(operation.name)
        else headers[operation.kind](operation.name, operation.value!)
      }
      clone = observeHeaders(new Headers(headers), item.pairs.map(([name]) => name))
      failure = APIError.generate(500, JSON.parse(payload), undefined, headers)
    } else {
      active = item
      headers = new Headers()
      try {
        const result = await client.messages.create({ model: 'metadata-fixture', messages: [], max_tokens: 32, stream: true }).withResponse()
        headers = result.response.headers
        for await (const value of result.data) values.push(JSON.stringify(value))
      } catch (error) {
        if (!(error instanceof APIError)) throw error
        failure = error
        headers = error.headers!
      }
    }
    for (const operation of item.afterErrorOperations ?? []) {
      if (operation.kind === 'delete') headers.delete(operation.name)
      else headers[operation.kind](operation.name, operation.value!)
    }
    const state = (value: unknown) => value === undefined ? { kind: 'undefined' }
      : { kind: value === null ? 'null' : typeof value, source: JSON.stringify(value) }
    const error = failure ? {
      class: failure.constructor.name, name: failure.name, messageSource: JSON.stringify(failure.message),
      status: state(failure.status), requestID: state(failure.requestID), payload: state(failure.error),
    } : null
    const noticeSource = failure ? JSON.stringify(createSystemAPIErrorMessage(failure, 500, 1, 4)) : null
    const names = (item.kind === 'constructed' ? item.pairs : wirePairsFor(item)).map(([name]) => name)
    observations.push({
      name: item.name, headers: observeHeaders(headers, names), initial, clone, error, noticeSource, values,
      retryDelaySource: failure ? JSON.stringify(getRetryDelay(1, failure.headers?.get('retry-after'), 32000)) : null,
    })
  }
  process.stdout.write(JSON.stringify({
    observation: { observations, requestCount, ids },
    sourceOnly: { capabilities, sdkVersion: VERSION, runtimeVersion: Bun.version, runtimeRevision: Bun.revision },
  }))
} finally {
  for (const socket of sockets) socket.end()
  server.stop(true)
}
