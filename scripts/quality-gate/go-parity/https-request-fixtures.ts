import { requestRetryProfile, type RequestRetryInput, type RequestRetryMode } from './request-retry-fixtures'
export type HTTPSRequestMode = RequestRetryMode | 'abort-handshake' | 'timeout-handshake' | 'late-sse' | 'abort-body'
export type HTTPSRequestInput = Omit<RequestRetryInput, 'mode'> & { mode: HTTPSRequestMode; layer: 'query' | 'creation' }
export type HTTPSRequestCase = { name: string; mode: HTTPSRequestMode; trust: 'valid' | 'malformed' | 'malformed-large' | 'wrong' | 'missing' | 'default'; layer: 'query' | 'creation'; historySources?: string[]; apiTimeout?: string }
export const httpsRequestProfile = requestRetryProfile
export const httpsRequestCases: HTTPSRequestCase[] = [
  ...(['sequence', 'pause', 'stop-notice', 'abort-sleep', 'abort-before', 'stop-stream'] as const).map(mode => ({ name: 'verified-' + mode, mode, trust: 'valid' as const, layer: 'query' as const })),
  ...(['abort-handshake', 'late-sse', 'abort-body'] as const).map(mode => ({ name: 'verified-' + mode, mode, trust: 'valid' as const, layer: 'query' as const })),
  { name: 'creation-handshake-timeout', mode: 'timeout-handshake', trust: 'valid', layer: 'creation' },
  { name: 'creation-malformed-timeout-zero', mode: 'sequence', trust: 'malformed', layer: 'creation', apiTimeout: '0' },
  { name: 'creation-malformed-timeout-one', mode: 'sequence', trust: 'malformed', layer: 'creation', apiTimeout: '1' },
  { name: 'creation-large-malformed-timeout-one', mode: 'sequence', trust: 'malformed-large', layer: 'creation', apiTimeout: '1' },
  ...(['malformed', 'wrong', 'missing', 'default'] as const).map(trust => ({ name: 'creation-' + trust, mode: 'sequence' as const, trust, layer: 'creation' as const })),
]

import * as tls from 'node:tls'
import { createServer as createTCPServer, type Socket } from 'node:net'
import { writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { requestRetryErrorBody, requestRetryStart, requestRetryWire } from './request-retry-fixtures'

// The parent owns this actual TLS/raw HTTP host. Child capability guards grant
// only its separately allocated origin; no Source transport is replaced.
export async function createHTTPSRequestHost(mode: HTTPSRequestMode, cert: string, key: string, home?: string) {
  const connections = new Set<Socket>()
  let acceptedTCP = 0, closedTCP = 0, requests = 0, closedModelConnections = 0
  const wireRequests: Array<{ path: string; headers: string[][]; body: string }> = []
  const tlsMetadata: Array<{ version: string | null; alpn: string | false; cipher: string | undefined }> = []
  const state = () => ({ acceptedTCP, closedTCP, requests, closedModelConnections })
  const waiting: tls.TLSSocket[] = []
  const control = (socket: tls.TLSSocket, value: unknown) => {
    const body = JSON.stringify(value)
    socket.end(`HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: ${Buffer.byteLength(body)}\r\nConnection: close\r\n\r\n${body}`)
  }
  const stalled = ['abort-handshake', 'timeout-handshake'].includes(mode)
  const tlsServer = tls.createServer({ cert, key, minVersion: 'TLSv1.2', ALPNProtocols: ['http/1.1'] }, socket => {
    tlsMetadata.push({ version: socket.getProtocol(), alpn: socket.alpnProtocol, cipher: socket.getCipher()?.name })
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
      if (method === 'GET' && path === '/__fixture/state') return control(socket, state())
      if (method === 'GET' && path === '/__fixture/request-seen') {
        if (requests) return control(socket, state())
        waiting.push(socket)
        return
      }
      if (method !== 'POST' || path !== '/v1/messages?beta=true') throw new Error('Unexpected private HTTPS fixture socket path')
      model = true
      requests++
      wireRequests.push({ path, headers: header.split('\r\n').slice(1).map(line => { const colon = line.indexOf(':'); return [line.slice(0, colon), line.slice(colon + 1).trim()] }), body: buffer.subarray(end + 4, end + 4 + length).toString() })
      for (const waiter of waiting.splice(0)) control(waiter, state())
      if (mode === 'abort-headers') return
      if (mode === 'stop-stream') {
        socket.write(`HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nRequest-Id: response-1\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n${Buffer.byteLength(requestRetryStart).toString(16)}\r\n${requestRetryStart}\r\n`)
        return
      }
      const status = requests === 1 ? 500 : requests === 2 ? 429 : 200
      const body = status === 200 ? requestRetryWire : requestRetryErrorBody(status)
      const pairs = [['X-Uncommon', 'first'], ['Content-Type', status === 200 ? 'text/event-stream' : 'application/json'], ['Set-Cookie', 'a=1'], ['Request-Id', `response-${requests}`], ['X-Uncommon', 'last'], ['Set-Cookie', 'b=2'],
        ...status === 429 ? [['Retry-After', '1'], ['retry-after', '2']] : [], ['Content-Length', String(Buffer.byteLength(body))], ['Connection', 'close']]
      if (status === 200 && ['late-sse', 'abort-body'].includes(mode)) {
        socket.write(`HTTP/1.1 ${status} Fixture\r\n${pairs.map(([name, value]) => `${name}: ${value}`).join('\r\n')}\r\n\r\n`)
        setTimeout(() => socket.end(body), 1250)
        return
      }
      socket.end(`HTTP/1.1 ${status} Fixture\r\n${pairs.map(([name, value]) => `${name}: ${value}`).join('\r\n')}\r\n\r\n${body}`)
    })
  })
  const server = stalled ? createTCPServer(socket => socket.on('data', () => {})) : tlsServer
  server.on('connection', socket => { acceptedTCP++; if (home) writeFileSync(join(home, 'accepted'), 'private TCP accepted'); connections.add(socket); socket.on('error', () => {}); socket.on('close', () => { connections.delete(socket); closedTCP++ }) })
  server.on('tlsClientError', () => {})
  await new Promise<void>((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve) })
  const address = server.address()
  if (!address || typeof address === 'string') throw new Error('Private TLS host requires allocated port')
  return {
    origin: `https://127.0.0.1:${address.port}`, state, wireRequests, tlsMetadata,
    async settled() {
      for (let count = 0; count < 100 && (closedTCP !== acceptedTCP || closedModelConnections !== requests); count++) await Bun.sleep(5)
      return state()
    },
    close() { for (const socket of connections) socket.destroy(); server.close() },
  }
}
