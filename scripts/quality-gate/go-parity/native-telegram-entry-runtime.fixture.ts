import { assertDisposableFixtureHome } from './fixture-guard'
import { mkdirSync, writeFileSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
const home = assertDisposableFixtureHome(process.env.HOME, 'Native Telegram entry runtime')
if (process.env.CC_HAHA_TELEGRAM_ENTRY_RUNTIME_FIXTURE !== '1') throw new Error('Unowned Telegram entry runtime fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
let now = input.now
Date.now = () => now
console.log = (...values) => process.stderr.write(values.join(' ') + '\n')
Bun.plugin({ name: 'actual-private-telegram-entry-handlers', setup(build) {
  build.onLoad({ filter: /\/adapters\/telegram\/index\.ts$/ }, async args => ({ contents: `${await Bun.file(args.path).text()}\nexport { handleServerMessage, getRuntimeState, pendingPermissions, accumulatedThinkingText }`, loader: 'ts' }))
} })
const calls: any[] = []
const http: any[] = []
const packets: any[] = []
let nextSession = 0
let nextMessage = 0
let active = 'private-provider'
let currentModel = 'private-model'
const connections = new Map<string, any>()
const botInfo = { id: 999, is_bot: true, username: 'private_test_bot', first_name: 'Private' }
const reply = (result: any) => Response.json({ ok: true, result })
const peer = Bun.serve<{ id: string }>({ hostname: '127.0.0.1', port: 0, async fetch(request, server) {
  const url = new URL(request.url)
  if (url.pathname.startsWith('/botprivate-token/')) {
    const method = url.pathname.split('/').pop()!
    const payload = await request.json()
    calls.push({ method, payload })
    return reply(method === 'sendMessage' ? { message_id: ++nextMessage } : method === 'getMe' ? botInfo : true)
  }
  if (url.pathname.startsWith('/ws/')) {
    http.push({ method: 'WS', path: url.pathname, token: url.searchParams.get('token') })
    if (server.upgrade(request, { data: { id: url.pathname.slice(4) } })) return
    return new Response('', { status: 400 })
  }
  const body = request.method === 'POST' || request.method === 'PUT' ? await request.text() : ''
  http.push({ method: request.method, path: url.pathname + url.search, authorization: request.headers.get('authorization'), ...(body ? { body: JSON.parse(body) } : {}) })
  if (url.pathname === '/api/sessions' && request.method === 'POST') return Response.json({ sessionId: `private-session-${++nextSession}` })
  if (url.pathname === '/api/sessions/recent-projects') return Response.json({ projects: input.projects })
  if (url.pathname === '/api/sessions') return Response.json({ sessions: input.sessions, total: input.sessions.length })
  if (url.pathname.endsWith('/git-info')) return Response.json({ repoName: 'Private Repo', branch: 'fixture', workDir: input.work })
  if (url.pathname.startsWith('/api/tasks/lists/')) return Response.json({ tasks: [{ id: '1', subject: 'test', status: 'completed' }] })
  if (url.pathname.startsWith('/api/sessions/')) return Response.json({ workDir: input.work, permissionMode: 'default' })
  if (url.pathname === '/api/providers') return Response.json({ providers: [{ id: 'private-provider', name: 'Private', models: { main: 'private-model' } }], activeId: active })
  if (url.pathname === '/api/providers/official') { active = null as any; return Response.json({ ok: true }) }
  if (url.pathname.endsWith('/activate')) { active = url.pathname.split('/')[3]!; return Response.json({ ok: true }) }
  if (url.pathname === '/api/models') return Response.json({ provider: { id: active, name: 'Private' }, models: [{ id: 'private-model', name: 'Private Model', description: 'fixture', context: 'test' }, { id: 'second-model', name: 'Second', description: '', context: '' }] })
  if (url.pathname === '/api/models/current') { if (request.method === 'PUT') currentModel = JSON.parse(body).modelId; return Response.json({ model: { id: currentModel } }) }
  if (url.pathname === '/api/skills') return Response.json({ skills: [{ name: 'private-skill', displayName: 'Private Skill', source: 'project', description: 'fixture', userInvocable: true }, { name: 'hidden', source: 'user', description: '', userInvocable: false }] })
  return new Response('', { status: 404 })
}, websocket: { open(ws) { connections.set(ws.data.id, ws) }, message(ws, message) { packets.push({ id: ws.data.id, message: JSON.parse(String(message)) }) }, close(ws) { connections.delete(ws.data.id) } } })
process.env.ADAPTER_SERVER_URL = `ws://127.0.0.1:${peer.port}`
process.env.CC_HAHA_LOCAL_ACCESS_TOKEN = 'private-server-token'
process.env.TELEGRAM_BOT_TOKEN = 'private-token'
mkdirSync(process.env.CLAUDE_CONFIG_DIR!, { recursive: true })
writeFileSync(join(process.env.CLAUDE_CONFIG_DIR!, 'adapters.json'), JSON.stringify({ defaultProjectDir: input.work, allowedProjectRoots: [input.work], telegram: { botToken: 'private-token', allowedUsers: [42] }, pairing: { code: 'PAIRME', expiresAt: Date.now() + 600_000 } }))
const { Bot } = await import(Bun.resolveSync('grammy', new URL('../../../adapters/telegram/', import.meta.url).pathname))
const privateApi = new Bot('private-token', { client: { apiRoot: `http://127.0.0.1:${peer.port}` } }).api
const entry: any = await import('../../../adapters/telegram/index')
entry.bot.botInfo = botInfo
entry.bot.api.config.use(async (_prev: any, method: any, payload: any, signal: any) => ({ ok: true, result: await privateApi.raw[method](payload, signal) }))
const rows = []
try {
  for (const action of input.actions) {
    if (action.now !== undefined) now = action.now
    if (action.update) await entry.bot.handleUpdate(action.update)
    if (action.event) await entry.handleServerMessage('42', action.event)
    await new Promise(resolve => setTimeout(resolve, 15))
    const sessionFile = join(process.env.CLAUDE_CONFIG_DIR!, 'adapter-sessions.json')
    let stored = null
    try { stored = JSON.parse(readFileSync(sessionFile, 'utf8')) } catch {}
    rows.push({ calls: calls.splice(0), http: http.splice(0), packets: packets.splice(0), state: { ...entry.getRuntimeState('42') }, stored })
  }
  process.stdout.write(JSON.stringify(rows))
} finally { entry.stopTelegramAdapter(); peer.stop(true) }
