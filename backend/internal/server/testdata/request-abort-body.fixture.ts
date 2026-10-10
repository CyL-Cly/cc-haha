import { spyOn } from 'bun:test'
import { assertDisposableFixtureHome } from '../../../../scripts/quality-gate/go-parity/fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from '../../../../scripts/quality-gate/go-parity/client-options-capabilities'

const input = JSON.parse(await Bun.stdin.text())
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_REQUEST_ABORT_BODY_FIXTURE !== '1') throw new Error('actual Source delayed request body fixture only')
assertDisposableFixtureHome(input.home, 'request abort body')
const physicalServe = Bun.serve.bind(Bun)
const physicalConnect = Bun.connect.bind(Bun)
const capabilities = await installClientFixtureCapabilities(input.home, { absentPaths: absentManagedSettingsFixturePaths() })
await import('../../../../preload')
spyOn(console, 'log').mockImplementation(() => {})
const { sessionService } = await import('../../../../src/server/services/sessionService')
const { handleConversationsApi, getSessionChatState } = await import('../../../../src/server/api/conversations')
const { settleResponseOnRequestAbort } = await import('../../../../src/server/requestLifecycle')
const bounded = <T>(promise: Promise<T>, label: string): Promise<T> => new Promise((resolve, reject) => {
  const timer = setTimeout(() => reject(new Error('missing physical event: ' + label)), 5000)
  promise.then(value => { clearTimeout(timer); resolve(value) }, error => { clearTimeout(timer); reject(error) })
})
const rows = []
for (const mode of ['full', 'partial']) {
  let enter!: () => void, release!: () => void, respond!: () => void, complete!: () => void
  const entered = new Promise<void>(resolve => { enter = resolve })
  const held = new Promise<void>(resolve => { release = resolve })
  const responded = new Promise<void>(resolve => { respond = resolve })
  const completed = new Promise<void>(resolve => { complete = resolve })
  const id = 'delayed-' + mode
  spyOn(sessionService, 'getSessionSummary').mockImplementation(async actual => { if (actual !== id) throw new Error('wrong actual Source id'); enter(); await held; return { id } as any })
  let responseStatus: number | undefined, operationStatus: number | undefined, operationBody: unknown
  const server = physicalServe({ hostname: '127.0.0.1', port: 0, async fetch(req) {
    const operation = handleConversationsApi(req, new URL(req.url), ['api', 'conversations', id])
    operation.then(async response => { operationStatus = response.status; operationBody = await response.json(); complete() })
    const response = await settleResponseOnRequestAbort(req, operation)
    responseStatus = response.status; respond(); return response
  } })
  const body = JSON.stringify({ content: 'complete request survives delayed Exists' })
  const bytes = Buffer.from(body)
  const sent = mode === 'full' ? bytes : bytes.subarray(0, bytes.length - 7)
  const socket = await physicalConnect({ hostname: '127.0.0.1', port: server.port, socket: {
    open(socket) { socket.write(`POST /api/conversations/${id} HTTP/1.1\r\nHost: 127.0.0.1:${server.port}\r\nContent-Type: application/json\r\nContent-Length: ${bytes.length}\r\nConnection: close\r\n\r\n`); socket.write(sent) },
    data() {}, close() {}, error(_socket, error) { throw error },
  } })
  await bounded(entered, 'Exists entered')
  socket.terminate()
  await bounded(responded, 'response499')
  const pendingAtResponse = operationStatus === undefined
  release()
  await bounded(completed, 'delayed JSON read')
  rows.push({ mode, responseStatus, operationStatus, operationBody, state: getSessionChatState(id), pendingAtResponse })
  server.stop(true)
}
const sizeBoundaries = []
for (const declared of [128 * 1024 * 1024, 128 * 1024 * 1024 + 1]) {
  let delivered = false, finish!: () => void
  const finished = new Promise<void>(resolve => { finish = resolve })
  let wire = ''
  const server = physicalServe({ hostname: '127.0.0.1', port: 0, fetch() { delivered = true; return new Response(null, { status: 204 }) } })
  const socket = await physicalConnect({ hostname: '127.0.0.1', port: server.port, socket: {
    open(socket) { socket.write(`POST /api/size HTTP/1.1\r\nHost: 127.0.0.1:${server.port}\r\nContent-Length: ${declared}\r\nConnection: close\r\n\r\n`) },
    data(socket, data) { wire += Buffer.from(data).toString(); if (wire.includes('\r\n')) { finish(); socket.terminate() } }, close() { finish() }, error() { finish() },
  } })
  await bounded(finished, 'actual default Bun body cap')
  socket.terminate(); server.stop(true)
  sizeBoundaries.push({ declared, delivered, status: Number(wire.split(' ')[1]), headers: wire.split('\r\n\r\n')[0], body: wire.split('\r\n\r\n').slice(1).join('\r\n\r\n') })
}
const earlyResponsePooling = []
for (const mode of ['full', 'partial']) {
let poolingFinish!: () => void
const poolingFinished = new Promise<void>(resolve => { poolingFinish = resolve })
let poolingWire = '', poolingSentNext = false, poolingCalls = 0
const poolingServer = physicalServe({ hostname: '127.0.0.1', port: 0, async fetch(req) {
  poolingCalls++
  return handleConversationsApi(req, new URL(req.url), ['api', 'conversations', 'pooling', ...(req.url.endsWith('/status') ? ['status'] : [])])
} })
const poolingSocket = await physicalConnect({ hostname: '127.0.0.1', port: poolingServer.port, socket: {
  open(socket) { socket.write(`GET /api/conversations/pooling HTTP/1.1\r\nHost: 127.0.0.1:${poolingServer.port}\r\nContent-Length: 3\r\n\r\n${mode === 'full' ? 'abc' : 'a'}`) },
  data(socket, data) {
    poolingWire += Buffer.from(data).toString()
    if (!poolingSentNext && poolingWire.includes('\r\n\r\n')) {
      poolingSentNext = true
      socket.write(`${mode === 'full' ? '' : 'bc'}GET /api/conversations/pooling/status HTTP/1.1\r\nHost: 127.0.0.1:${poolingServer.port}\r\nConnection: close\r\n\r\n`)
    }
    if ((poolingWire.match(/HTTP\/1\.1 /g) ?? []).length === 2) poolingFinish()
  }, close() { poolingFinish() }, error() { poolingFinish() },
} })
await bounded(poolingFinished, 'original unread body response pooling')
poolingSocket.terminate(); poolingServer.stop(true)
earlyResponsePooling.push({ mode, calls: poolingCalls, statuses: [...poolingWire.matchAll(/HTTP\/1\.1 (\d+)/g)].map(match => Number(match[1])), firstHeaders: poolingWire.split('\r\n\r\n')[0] })
}
process.stdout.write(JSON.stringify({ rows, sizeBoundaries, earlyResponsePooling, capabilities, consumer: 'actual conversations sendMessage + requestLifecycle on physical Bun HTTP; only delayed session-summary seam' }))
