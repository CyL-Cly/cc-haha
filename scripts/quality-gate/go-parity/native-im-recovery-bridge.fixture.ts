import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
const home = assertDisposableFixtureHome(process.env.HOME, 'Native IM recovery bridge')
if (process.env.CC_HAHA_IM_RECOVERY_FIXTURE !== '1') throw new Error('Unowned IM recovery fixture')
console.log = (...values) => process.stderr.write(values.join(' ') + '\n')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
const { WsBridge } = await import('../../../adapters/common/ws-bridge')
const { SessionStore } = await import('../../../adapters/common/session-store')
const { AdapterHttpClient } = await import('../../../adapters/common/http-client')
const { restoreStoredSessionBinding } = await import('../../../adapters/common/session-recovery')
let responseStatus = 200
let permissionMode = 'default'
let rejectSocket = false
const requests: unknown[] = []
const messages: unknown[] = []
const sockets = new Map<string, any>()
const host = Bun.serve<{ id: string }>({ hostname: '127.0.0.1', port: 0, fetch(request, server) {
  const url = new URL(request.url)
  if (url.pathname.startsWith('/ws/')) {
    requests.push({ path: url.pathname, token: url.searchParams.get('token') })
    if (rejectSocket) return new Response('', { status: 403 })
    if (server.upgrade(request, { data: { id: url.pathname.slice(4) } })) return
    return new Response('', { status: 400 })
  }
  requests.push({ path: url.pathname, authorization: request.headers.get('authorization') })
  return Response.json({ workDir: input.work, permissionMode, message: 'private unavailable' }, { status: responseStatus })
}, websocket: { open(ws) { sockets.set(ws.data.id, ws) }, message(ws, bytes) { messages.push({ id: ws.data.id, message: JSON.parse(String(bytes)) }) }, close(ws) { sockets.delete(ws.data.id) } } })
const file = join(process.env.CLAUDE_CONFIG_DIR!, 'adapter-sessions.json')
mkdirSync(process.env.CLAUDE_CONFIG_DIR!, { recursive: true })
writeFileSync(file, JSON.stringify(input.seed))
const store = new SessionStore()
const bridge = new WsBridge(`ws://127.0.0.1:${host.port}`, 'tg', 'private-local-token')
const http = new AdapterHttpClient(`ws://127.0.0.1:${host.port}`, { allowedProjectRoots: [home], localAccessToken: 'private-local-token' })
const rows = []
let clears = 0
try {
  for (const action of input.actions) {
    if (action.seed !== undefined) writeFileSync(file, JSON.stringify(action.seed))
    if (action.status !== undefined) responseStatus = action.status
    if (action.permission !== undefined) permissionMode = action.permission
    if (action.rejectSocket !== undefined) rejectSocket = action.rejectSocket
    if (action.reset) bridge.resetSession(action.chat)
    if (action.restore) {
      const result = await restoreStoredSessionBinding({ chatId: action.chat, bridge, sessionStore: store, httpClient: http, onServerMessage: () => {}, logPrefix: '[Private]', clearTransientState: () => { clears++ } })
      rows.push({ result, clears, stored: store.get(action.chat) })
    }
    if (action.send) {
      const sent = bridge.sendUserMessage(action.chat, '<actual incoming>')
      await new Promise(resolve => setTimeout(resolve, 0))
      rows.push({ sent })
    }
    if (action.allow) {
      const sent = bridge.sendPermissionResponse(action.chat, 'private-request', true, 'always')
      await new Promise(resolve => setTimeout(resolve, 0))
      rows.push({ sent })
    }
    if (action.stop) {
      const sent = bridge.sendStopGeneration(action.chat)
      await new Promise(resolve => setTimeout(resolve, 0))
      rows.push({ sent })
    }
  }
  await new Promise(resolve => setTimeout(resolve, 0))
  process.stdout.write(JSON.stringify({ rows, requests, messages }))
} finally { bridge.destroy(); host.stop(true) }
