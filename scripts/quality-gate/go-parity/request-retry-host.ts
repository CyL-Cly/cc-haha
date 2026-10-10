import { requestRetryErrorBody, requestRetryStart, requestRetryWire, type RequestRetryMode } from './request-retry-fixtures'

export function createRequestRetryHost(mode: RequestRetryMode) {
  const sockets = new Set<Bun.Socket<{ buffer: Buffer; responded: boolean; model: boolean }>>()
  let requests = 0, closedModelConnections = 0
  const wireRequests: Array<{ path: string; headers: string[][]; body: string }> = []
  const waiting: Bun.Socket<{ buffer: Buffer; responded: boolean; model: boolean }>[] = []
  const state = () => ({ requests, closedModelConnections })
  const control = (socket: Bun.Socket<any>, value: unknown) => {
    const body = JSON.stringify(value)
    socket.end(`HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: ${Buffer.byteLength(body)}\r\nConnection: close\r\n\r\n${body}`)
  }
  const server = Bun.listen<{ buffer: Buffer; responded: boolean; model: boolean }>({
    hostname: '127.0.0.1', port: 0,
    socket: {
      open(socket) { socket.data = { buffer: Buffer.alloc(0), responded: false, model: false }; sockets.add(socket) },
      close(socket) { sockets.delete(socket); if (socket.data.model) closedModelConnections++ },
      data(socket, bytes) {
        if (socket.data.responded) return
        socket.data.buffer = Buffer.concat([socket.data.buffer, bytes])
        const end = socket.data.buffer.indexOf('\r\n\r\n')
        if (end < 0) return
        const header = socket.data.buffer.subarray(0, end).toString()
        const length = Number(header.match(/\r\ncontent-length:\s*(\d+)/i)?.[1] ?? 0)
        if (socket.data.buffer.length < end + 4 + length) return
        socket.data.responded = true
        const [method, path] = header.split('\r\n')[0].split(' ')
        if (method === 'GET' && path === '/__fixture/state') return control(socket, state())
        if (method === 'GET' && path === '/__fixture/request-seen') {
          if (requests) return control(socket, state())
          waiting.push(socket)
          return
        }
        if (method !== 'POST' || path !== '/v1/messages?beta=true') throw new Error('Unexpected request retry fixture socket path')
        socket.data.model = true
        requests++
        wireRequests.push({ path, headers: header.split('\r\n').slice(1).map(line => { const colon = line.indexOf(':'); return [line.slice(0, colon), line.slice(colon + 1).trim()] }), body: socket.data.buffer.subarray(end + 4, end + 4 + length).toString() })
        for (const waiter of waiting.splice(0)) control(waiter, state())
        if (mode === 'abort-headers') return
        if (mode === 'stop-stream') {
          socket.write(`HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nRequest-Id: response-1\r\nTransfer-Encoding: chunked\r\nConnection: close\r\n\r\n${Buffer.byteLength(requestRetryStart).toString(16)}\r\n${requestRetryStart}\r\n`)
          return
        }
        const status = mode === 'overflow' ? requests === 1 ? 400 : 200 : requests === 1 ? 500 : requests === 2 ? 429 : 200
        const body = status === 200 ? requestRetryWire : requestRetryErrorBody(status)
        const pairs = [['X-Uncommon', 'first'], ['Content-Type', status === 200 ? 'text/event-stream' : 'application/json'], ['Set-Cookie', 'a=1'], ['Request-Id', `response-${requests}`], ['X-Uncommon', 'last'], ['Set-Cookie', 'b=2'],
          ...status === 429 ? [['Retry-After', '1'], ['retry-after', '2']] : [],
          ['Content-Length', String(Buffer.byteLength(body))], ['Connection', 'close']]
        socket.end(`HTTP/1.1 ${status} Fixture\r\n${pairs.map(([name, value]) => `${name}: ${value}`).join('\r\n')}\r\n\r\n${body}`)
      },
    },
  })
  return {
    origin: `http://127.0.0.1:${server.port}`, state, wireRequests,
    async settled() {
      for (let count = 0; count < 100 && closedModelConnections !== requests; count++) await Bun.sleep(5)
      return state()
    },
    close() { for (const socket of sockets) socket.end(); server.stop(true) },
  }
}
