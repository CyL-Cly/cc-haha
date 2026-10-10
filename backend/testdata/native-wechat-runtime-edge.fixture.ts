import { mock } from 'bun:test'
import { readFileSync, mkdirSync, writeFileSync, readdirSync, existsSync } from 'node:fs'
import { join } from 'node:path'
import { assertDisposableFixtureHome } from '../../scripts/quality-gate/go-parity/fixture-guard'

assertDisposableFixtureHome(process.env.HOME, 'Wechat consumer edge Source')
if (process.env.CC_HAHA_WECHAT_RUNTIME_EDGE_FIXTURE !== '1') throw new Error('Unowned Wechat consumer edge fixture')
const input = JSON.parse(await new Response(Bun.stdin.stream()).text())
console.log = console.warn = console.error = () => {}
Date.now = () => input.now
mkdirSync(process.env.CLAUDE_CONFIG_DIR!, { recursive: true })
const configPath = join(process.env.CLAUDE_CONFIG_DIR!, 'adapters.json')
writeFileSync(configPath, JSON.stringify(input.config))
// Append accessors only: routing and pollLoop execute the unchanged original body.
Bun.plugin({ name: 'actual-wechat-consumer-edge-exports', setup(build) {
  build.onLoad({ filter: /[\\/]adapters[\\/]wechat[\\/]index\.ts$/ }, args => ({
    contents: readFileSync(args.path, 'utf8') + '\nexport { pollLoop as edgePollLoop }; export const edgeStop = () => { stopped = true }; export const edgeCursor = () => getUpdatesBuf; export const edgeContexts = () => Array.from(contextTokens.entries()); export const edgeDedup = () => Array.from((dedup as any).store.keys());\n', loader: 'ts',
  }))
} })
const notices: any[] = [], packets: any[] = [], polls: any[] = [], retries: number[] = []
const sessions = new Map<string, string>()
mock.module('../../adapters/common/ws-bridge', () => ({ WsBridge: class {
  getSessionId(id: string) { return sessions.get(id) }
  hasSession(id: string) { return sessions.has(id) }
  isSessionOpen(id: string) { return sessions.has(id) }
  resetSession(id: string) { sessions.delete(id) }
  connectSession(id: string, sid: string) { sessions.set(id, sid) }
  onServerMessage() {}
  async waitForOpen() { return true }
  sendUserMessage(id: string, text: string, attachments?: any) { packets.push({ type: 'user_message', content: text, ...(attachments ? { attachments } : {}) }); return sessions.has(id) }
  sendPermissionResponse() { return true }
  sendStopGeneration() { return true }
  destroy() {}
} }))
mock.module('../../adapters/common/adapter-client', () => ({ createAdapterClient: () => ({ defaultWorkDir: input.work, httpClient: {
  sessionExists: async () => true, createSession: async () => 'session-edge', listRecentProjects: async () => [],
  getGitInfo: async () => ({ repoName: 'repo', workDir: input.work, branch: 'main' }), getTasksForSession: async () => [],
} }) }))
const actualProtocol = await import('../../adapters/wechat/protocol')
let runtime: any
let pollIndex = 0
mock.module('../../adapters/wechat/protocol', () => ({ ...actualProtocol,
  sendWechatText: async ({ to, text, contextToken }: any) => { notices.push({ id: to, text, ...(contextToken === undefined ? {} : { context: contextToken }) }) },
  getWechatConfig: async () => ({ ret: 0 }), sendWechatTyping: async () => {},
  getWechatUpdates: async ({ getUpdatesBuf }: any) => {
    if (input.mode === 'cancel') return new Promise(() => {})
    polls.push(JSON.parse(JSON.stringify(getUpdatesBuf)))
    if (pollIndex >= input.responses.length) { runtime.edgeStop(); return { msgs: [] } }
    return JSON.parse(input.responses[pollIndex++])
  },
}))
if (input.mode === 'cancel') process.argv.push('--wechat')
runtime = await import('../../adapters/wechat/index')
const { enqueue } = await import('../../adapters/common/chat-queue')
const originalTimeout = globalThis.setTimeout
if (input.mode === 'poll') {
  globalThis.setTimeout = ((fn: (...args: any[]) => void, ms: number, ...args: any[]) => {
    if (ms === 3000) { retries.push(ms); queueMicrotask(() => fn(...args)); return 0 as any }
    return originalTimeout(fn, ms, ...args)
  }) as any
}
function failure(err: unknown) {
  const detail = err instanceof Error ? err.message : String(err)
  if (detail.includes('slice')) return 'slice'
  if (detail.includes('trim')) return 'trim'
  if (detail.includes('iterable')) return 'iterable'
  if (detail.includes('null')) return 'null'
  return 'other'
}
function snapshot() {
  return { dedup: runtime.edgeDedup(), contexts: runtime.edgeContexts().sort((a: any, b: any) => JSON.stringify(a[0]).localeCompare(JSON.stringify(b[0]))), notices: [...notices], packets: [...packets] }
}
let result: any
if (input.mode === 'route') {
  const rows: any[] = []
  for (const action of input.actions) {
    if (action.config) writeFileSync(configPath, JSON.stringify(action.config))
    let error = ''
    try { await runtime.routeUserMessage(JSON.parse(action.raw)) } catch (err) { error = failure(err) }
    for (const id of input.users ?? []) await enqueue(id, async () => {})
    rows.push({ error, ...snapshot() })
  }
  result = { rows, config: JSON.parse(readFileSync(configPath, 'utf8')) }
} else if (input.mode === 'poll') {
  await runtime.edgePollLoop()
  for (const id of input.users ?? []) await enqueue(id, async () => {})
  result = { polls, retries, cursor: runtime.edgeCursor(), ...snapshot() }
} else if (input.mode === 'fifo' || input.mode === 'cancel') {
  const control = async (label: string) => (await fetch(`${input.peer}/control/${label}`)).json()
  const files = () => {
    const root = join(process.env.HOME!, '.claude', 'im-downloads')
    const values: any[] = []
    const walk = (dir: string, prefix: string) => {
      if (!existsSync(dir)) return
      for (const entry of readdirSync(dir, { withFileTypes: true })) {
        const name = prefix + entry.name
        if (entry.isDirectory()) walk(join(dir, entry.name), name + '/')
        else values.push({ name, bytes: readFileSync(join(dir, entry.name)).toString('base64') })
      }
    }
    walk(root, '')
    return values.sort((a, b) => a.name.localeCompare(b.name))
  }
  await runtime.routeUserMessage(JSON.parse(input.unauthorized))
  const unauthorized = { ...(await control('snapshot')), packets: [...packets], files: files() }
  writeFileSync(configPath, JSON.stringify(input.allowedConfig))
  runtime.sessionStore.set('U1', 'session-edge', input.work)
  runtime.bridge.connectSession('U1', 'session-edge')
  await runtime.routeUserMessage(JSON.parse(input.messages[0]))
  await control('started')
  // Admission resolves while the first physical download is still held. The
  // next two messages must retain their original authorization and FIFO order.
  await runtime.routeUserMessage(JSON.parse(input.messages[1]))
  await runtime.routeUserMessage(JSON.parse(input.messages[2]))
  writeFileSync(configPath, JSON.stringify(input.config))
  const during = { ...(await control('snapshot')), packets: [...packets], files: files() }
  if (input.mode === 'cancel') {
    process.stdout.write(JSON.stringify({ unauthorized, during }))
    // Execute the unchanged original signal handler, including process.exit.
    process.emit('SIGINT')
    throw new Error('Original Wechat SIGINT must terminate')
  }
  await control('release')
  await enqueue('U1', async () => {})
  result = { unauthorized, during, after: { ...(await control('snapshot')), packets: [...packets], files: files() } }
} else {
  throw new Error('Unknown Wechat consumer edge mode')
}
runtime.typingController.destroy()
runtime.dedup.destroy()
runtime.bridge.destroy()
globalThis.setTimeout = originalTimeout
process.stdout.write(JSON.stringify(result))
