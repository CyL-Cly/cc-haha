import fs from 'node:fs'
import { dirname, join } from 'node:path'
import { PassThrough } from 'node:stream'
import * as http from 'node:http'
import * as net from 'node:net'
import { mock } from 'bun:test'
import { createRequire, syncBuiltinESMExports } from 'node:module'
import { assertDisposableFixtureHome } from './fixture-guard'
import { absentManagedSettingsFixturePaths, installClientFixtureCapabilities } from './client-options-capabilities'

const input = await Bun.stdin.json()
const home = assertDisposableFixtureHome(input.home, 'Actual MCPSettings agent-only preauthentication UI')
if (Bun.version !== '1.3.14' || process.env.CC_HAHA_AGENT_MCP_UI_SOURCE !== '1') throw new Error('Missing pinned original agent MCP UI')
const absentPaths = absentManagedSettingsFixturePaths()
for (const path of [join(dirname(absentPaths[0]!), 'managed-mcp.json'), '/home/claude/.claude/remote/.session_ingress_token', '/home/claude/.claude/remote/.api_key', '/home/claude/.claude/remote/.oauth_token']) if (!fs.existsSync(path)) absentPaths.push(path)
for (const spelling of new Set([home, fs.realpathSync(home)])) for (let parent = spelling; ; parent = dirname(parent)) {
  const candidate = join(parent, '.git')
  if (!fs.existsSync(candidate)) absentPaths.push(candidate)
  if (dirname(parent) === parent) break
}
await import('yoga-layout')
const originalCreateServer = http.createServer
const originalSocketConnect = net.Socket.prototype.connect
const originalServe = Bun.serve.bind(Bun)
const browser = join(home, 'private-browser')
const caps = await installClientFixtureCapabilities(home, {
  absentPaths, privateExecutableFiles: [browser], loopbackFetchOrigin: new URL(input.url).origin,
  approvedSpawn: ({ command, args }) => command === browser && args.length === 1 && new URL(args[0]!).origin === new URL(input.url).origin
})
Object.assign(createRequire(import.meta.url)('node:http'), { createServer: (...args: any[]) => {
  const server = (originalCreateServer as any)(...args)
  const listen = server.listen
  server.listen = function (port: number, host: string, ...rest: any[]) {
    if (port !== input.port || host !== '127.0.0.1') throw new Error('Unowned original agent callback listener')
    return listen.call(this, port, host, ...rest)
  }
  return server
} })
Bun.serve = ((options: any) => {
  if (options.port !== input.port || options.hostname !== '127.0.0.1') throw new Error('Unowned original agent Bun callback listener')
  return originalServe(options)
}) as typeof Bun.serve
syncBuiltinESMExports()
const { plainTextStorage } = await import('../../../src/utils/secureStorage/plainTextStorage')
const { createFallbackStorage } = await import('../../../src/utils/secureStorage/fallbackStorage')
const fallback = createFallbackStorage({ name: 'owned-unavailable-keychain', read: () => null, readAsync: async () => null, update: () => ({ success: false }), delete: () => false }, plainTextStorage)
mock.module('../../../src/utils/secureStorage/index.ts', () => ({ getSecureStorage: () => fallback }))
;(globalThis as any).MACRO = { VERSION: '999.0.0-agent-mcp-source', BUILD_TIME: '', PACKAGE_URL: 'https://fixture.invalid/package' }
await import('../../../preload')
const state = await import('../../../src/bootstrap/state')
state.setCwdState(fs.realpathSync(home)); state.setOriginalCwd(fs.realpathSync(home)); state.setProjectRoot(fs.realpathSync(home)); state.setAllowedSettingSources(['userSettings']); state.setIsInteractive(true)
const config = await import('../../../src/utils/config'); config.enableConfigs()
const React = await import('react')
const { render } = await import('../../../src/ink')
const { AppStoreContext, getDefaultAppState } = await import('../../../src/state/AppState')
const { createStore } = await import('../../../src/state/store')
const { MCPSettings } = await import('../../../src/components/mcp/MCPSettings')
const { KeybindingProvider } = await import('../../../src/keybindings/KeybindingContext')
const { parseBindings } = await import('../../../src/keybindings/parser')
const { DEFAULT_BINDINGS } = await import('../../../src/keybindings/defaultBindings')
const initial = getDefaultAppState()
initial.agentDefinitions = { allAgents: input.agents, activeAgents: input.agents }
const store = createStore(initial)
const active = new Set(['Confirmation'])
const provider = { bindings: parseBindings(DEFAULT_BINDINGS), handlerRegistryRef: { current: new Map() }, pendingChordRef: { current: null }, pendingChord: null, setPendingChord: () => {}, activeContexts: active, registerActiveContext: (value: any) => active.add(value), unregisterActiveContext: (value: any) => active.delete(value) }
const completions: any[] = []
const stdin = new PassThrough() as any; stdin.isTTY = true; stdin.setRawMode = () => {}; stdin.ref = () => stdin; stdin.unref = () => stdin
const stdout = new PassThrough() as any; stdout.columns = 150; stdout.isTTY = false
let output = ''; stdout.on('data', (chunk: any) => output += chunk.toString())
const view = React.createElement(AppStoreContext.Provider, { value: store }, React.createElement(KeybindingProvider, provider as any, React.createElement(MCPSettings, { onComplete: (message: any) => completions.push(message) })))
const app = await render(view, { stdin, stdout, stderr: stdout, exitOnCtrlC: false, patchConsole: false })
const deadline = Date.now() + 14000
const frames: any[] = []
async function until(predicate: () => boolean, label: string) {
  while (!predicate()) { if (Date.now() > deadline) throw new Error(label + ': ' + JSON.stringify({ blocked: caps.blocked, completions }) + output.slice(-16000)); await new Promise(resolve => setTimeout(resolve, 5)) }
  for (let i = 0; i < 6; i++) await new Promise(resolve => setImmediate(resolve))
}
async function key(value: string) { output = ''; stdin.write(value); for (let i = 0; i < 6; i++) await new Promise(resolve => setImmediate(resolve)) }
async function closed() {
  return new Promise<boolean>(resolve => { const socket = new net.Socket(); socket.once('error', () => { socket.destroy(); resolve(true) }); socket.once('connect', () => { socket.destroy(); resolve(false) }); originalSocketConnect.call(socket, input.port, '127.0.0.1') })
}
try {
  await until(() => output.includes('agentauth') && output.includes('Agent MCPs'), 'actual MCPSettings agent-only list')
  frames.push({ stage: 'list', output })
  await key('\r'); await until(() => output.includes('Agentauth MCP Server') && output.includes('Used by:'), 'actual agent menu')
  frames.push({ stage: 'menu', output })
  await key('\r'); await until(() => fs.existsSync(join(home, 'browser-url')) && output.includes('copy this URL'), 'original agent browser flow')
  frames.push({ stage: 'auth', output })
  if (output.includes('address bar') || output.includes('URL >')) throw new Error('Original agent acquired ordinary manual callback input')
  await key('\x1b'); await until(() => output.includes('Agentauth MCP Server'), 'actual Esc pre-auth cancel')
  while (!(await closed())) { if (Date.now() > deadline) throw new Error('Esc left original callback listener alive'); await new Promise(resolve => setTimeout(resolve, 5)) }
  fs.rmSync(join(home, 'browser-url'))
  await key('\r'); await until(() => fs.existsSync(join(home, 'browser-url')) && output.includes('copy this URL'), 'actual agent retry browser')
  fs.writeFileSync(join(home, 'callback-ready'), fs.readFileSync(join(home, 'browser-url')))
  await until(() => completions.includes('Authentication successful for agentauth. The server will connect when the agent runs.'), 'original browser callback token exchange')
  frames.push({ stage: 'complete', output, completions: [...completions], clients: store.getState().mcp.clients.map((client: any) => client.name) })
  const exited = app.waitUntilExit()
  app.unmount()
  await exited
  const listenerClosed = await closed()
  if (caps.blocked.length || !listenerClosed) throw new Error('Original agent fixture leaked capability/listener: ' + JSON.stringify(caps.blocked))
  await new Promise<void>((resolve, reject) => process.stdout.write(JSON.stringify({ frames, listenerClosed, blocked: caps.blocked }), error => error ? reject(error) : resolve()))
} finally { app.unmount() }
process.exit(0)
