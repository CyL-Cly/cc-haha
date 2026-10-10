import { assertDisposableFixtureHome } from './fixture-guard'
import { createServer, type Socket } from 'node:net'
import Anthropic from '@anthropic-ai/sdk'
assertDisposableFixtureHome(process.env.HOME, 'Native SDK abort creation')
if (process.env.CC_HAHA_SDK_ABORT_FIXTURE !== '1') throw new Error('Unowned abort fixture')
const cases: any[] = []
for (const phase of ['before-headers', 'headers-before-fetch-handoff', 'after-stream-creation']) {
  process.stderr.write(`phase ${phase} start\n`)
  const controller = new AbortController()
  let accepted!: () => void, closed!: () => void
  const reached = new Promise<void>(resolve => { accepted = resolve })
  const physicalClosed = new Promise<void>(resolve => { closed = resolve })
  const sockets = new Set<Socket>()
  let peerClosedBeforeCleanup = false
  const server = createServer(socket => {
    sockets.add(socket)
    let request = Buffer.alloc(0), dispatched = false
    socket.on('close', () => { sockets.delete(socket); peerClosedBeforeCleanup = true; closed() })
    socket.on('data', bytes => {
      request = Buffer.concat([request, bytes])
      const end = request.indexOf('\r\n\r\n')
      if (end < 0 || dispatched) return
      const length = Number(request.subarray(0, end).toString().match(/content-length:\s*(\d+)/i)?.[1] ?? 0)
      if (request.length < end + 4 + length) return
      dispatched = true
      if (phase !== 'before-headers') socket.write('HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n')
      accepted()
    })
  })
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
  const address = server.address()
  if (!address || typeof address === 'string') throw new Error('private HTTP allocation failed')
  let nativeFailure: any = null
  const actualFetch = globalThis.fetch
  const client = new Anthropic({ baseURL: `http://127.0.0.1:${address.port}`, apiKey: 'private-sdk-placeholder', maxRetries: 0,
    fetch: async (url, init) => {
      if (phase !== 'headers-before-fetch-handoff') return actualFetch(url, init)
      const response = await actualFetch(url, init)
      // Retain the actual SDK fetch handoff until caller cancellation has
      // closed its original response body. This is a physical AbortError,
      // not a manufactured fetch exception or replacement Response.
      const reader = response.body!.getReader()
      const pendingRead = reader.read()
      controller.abort()
      try { await pendingRead }
      catch (error) { nativeFailure = { constructor: (error as Error).constructor.name, name: (error as Error).name, message: (error as Error).message }; throw error }
      reader.releaseLock()
      return response
    },
  })
  const pending = client.beta.messages.create({ model: 'private-fixture-model', max_tokens: 8, messages: [], stream: true }, { signal: controller.signal })
  if (phase === 'before-headers') { await reached; controller.abort() }
  try {
    const stream = await pending
    process.stderr.write(`phase ${phase} created\n`)
    if (phase === 'before-headers') throw new Error('aborted pre-header creation returned a stream')
    controller.abort()
    let events = 0
    for await (const _ of stream) events++
    cases.push({ phase, stream: true, events, nativeFailure })
  } catch (error) {
    process.stderr.write(`phase ${phase} failure ${(error as Error).constructor.name}\n`)
    const value = error as any
    cases.push({ phase, class: value.constructor.name, name: value.name, message: value.message, hasStatus: Object.hasOwn(value, 'status'), status: value.status ?? null, payload: value.error ?? null, cause: value.cause ?? null, nativeFailure })
  }
  // Source Bun may retain an aborted pre-header socket until its own fetch
  // bookkeeping finishes. Do not turn that into a fake immediate-close claim.
  cases[cases.length - 1].peerClosedBeforeCleanup = peerClosedBeforeCleanup
  for (const socket of sockets) socket.destroy()
  await physicalClosed
  await new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve()))
}
process.stdout.write(JSON.stringify({ cases }))
