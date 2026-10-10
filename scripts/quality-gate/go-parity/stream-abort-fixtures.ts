import * as tls from 'node:tls'
import type { Socket } from 'node:net'
import { requestRetryProfile, requestRetryStart } from './request-retry-fixtures'

export type StreamAbortMode = 'completed-text' | 'unfinished-text' | 'pending-local-tool' | 'server-boundary'
export type StreamAbortInput = typeof requestRetryProfile & {
  name: StreamAbortMode
  mode: 'caller-abort'
  layer: 'query'
  home: string
  env: Record<string, string>
  runtime: { platform: string; arch: string; version: string }
  abortType: string
  abortIndex: number
}
const event = (part: unknown) => `event: ${(part as { type: string }).type}\ndata: ${JSON.stringify(part)}\n\n`
export const streamAbortCases = (['completed-text', 'unfinished-text', 'pending-local-tool', 'server-boundary'] as const).map(name => {
  const parts: unknown[] = [
    { type: 'content_block_start', index: 0, content_block: { type: 'text', text: 'ignored' } },
    { type: 'content_block_delta', index: 0, delta: { type: 'text_delta', text: 'visible' } },
  ]
  let abortType = 'content_block_delta', abortIndex = 0
  if (name !== 'unfinished-text') {
    parts.push({ type: 'content_block_stop', index: 0 })
    abortType = 'content_block_stop'
  }
  if (name === 'pending-local-tool') {
    parts.push({ type: 'content_block_start', index: 1, content_block: { type: 'tool_use', name: 'unsafe', id: 'local', input: {} } }, { type: 'content_block_delta', index: 1, delta: { type: 'input_json_delta', partial_json: '{}' } }, { type: 'content_block_stop', index: 1 })
    abortIndex = 1
  }
  if (name === 'server-boundary') {
    parts.push({ type: 'content_block_start', index: 1, content_block: { type: 'server_tool_use', name: 'search', id: 'server', input: {} } }, { type: 'content_block_delta', index: 1, delta: { type: 'input_json_delta', partial_json: '{}' } }, { type: 'content_block_stop', index: 1 }, { type: 'content_block_start', index: 2, content_block: { type: 'text', text: '' } }, { type: 'content_block_delta', index: 2, delta: { type: 'text_delta', text: 'later visible' } })
    abortType = 'content_block_delta'
    abortIndex = 2
  }
  return { name, abortType, abortIndex, prefix: requestRetryStart + parts.map(event).join('') }
})

// Hold the actual response socket open after the selected original events. Its
// only completion is the child's real cancellation; no synthetic EOF is sent.
export async function createStreamAbortHost(prefix: string, cert: string, key: string) {
  const connections = new Set<Socket>()
  let acceptedTCP = 0, closedTCP = 0, requests = 0, closedModelConnections = 0
  const wireRequests: Array<{ path: string; headers: string[][]; body: string }> = []
  const state = () => ({ acceptedTCP, closedTCP, requests, closedModelConnections })
  const server = tls.createServer({ cert, key, minVersion: 'TLSv1.2', ALPNProtocols: ['http/1.1'] }, socket => {
    let buffer = Buffer.alloc(0), responded = false, model = false
    socket.on('error', () => {})
    socket.on('close', () => { if (model) closedModelConnections++ })
    socket.on('data', data => {
      if (responded) return
      buffer = Buffer.concat([buffer, data])
      const end = buffer.indexOf('\r\n\r\n')
      if (end < 0) return
      const header = buffer.subarray(0, end).toString()
      const length = Number(header.match(/\r\ncontent-length:\s*(\d+)/i)?.[1] ?? 0)
      if (buffer.length < end + 4 + length) return
      responded = true
      const [method, path] = header.split('\r\n')[0].split(' ')
      if (method !== 'POST' || path !== '/v1/messages?beta=true') { socket.destroy(); return }
      model = true
      requests++
      wireRequests.push({ path, headers: header.split('\r\n').slice(1).map(line => { const colon = line.indexOf(':'); return [line.slice(0, colon), line.slice(colon + 1).trim()] }), body: buffer.subarray(end + 4, end + 4 + length).toString() })
      socket.write(`HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nRequest-Id: abort-physical\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n${Buffer.byteLength(prefix).toString(16)}\r\n${prefix}\r\n`)
    })
  })
  server.on('connection', socket => { acceptedTCP++; connections.add(socket); socket.on('error', () => {}); socket.on('close', () => { connections.delete(socket); closedTCP++ }) })
  server.on('tlsClientError', () => {})
  await new Promise<void>((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve) })
  const address = server.address()
  if (!address || typeof address === 'string') throw new Error('Stream abort host requires allocated private port')
  return {
    origin: `https://127.0.0.1:${address.port}`, wireRequests,
    async settled() { for (let count = 0; count < 100 && closedTCP !== acceptedTCP; count++) await Bun.sleep(5); return state() },
    close() { for (const socket of connections) socket.destroy(); server.close() },
  }
}
